package elbv2

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// targetGroupAPI는 타깃 그룹 수집기가 필요로 하는 SDK 메서드를 담은 인터페이스다.
//
// 대부분의 수집기는 메서드 하나만 받지만, 타깃 그룹은 예외적으로 둘을 받는다. 타깃 그룹
// 목록(DescribeTargetGroups)과 각 그룹의 타깃 헬스 상태(DescribeTargetHealth)가 별도
// API이기 때문이다. 두 메서드 모두 Describe로 시작하므로 조회 전용 가드를 통과한다.
type targetGroupAPI interface {
	DescribeTargetGroups(context.Context, *awselbv2.DescribeTargetGroupsInput, ...func(*awselbv2.Options)) (*awselbv2.DescribeTargetGroupsOutput, error)
	DescribeTargetHealth(context.Context, *awselbv2.DescribeTargetHealthInput, ...func(*awselbv2.Options)) (*awselbv2.DescribeTargetHealthOutput, error)
}

// targetGroupCollector는 타깃 그룹과 그 타깃의 헬스 상태를 조회한다.
type targetGroupCollector struct {
	api targetGroupAPI
}

// NewTargetGroup은 타깃 그룹 수집기를 만든다.
func NewTargetGroup(api targetGroupAPI) collect.Collector {
	return targetGroupCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c targetGroupCollector) Type() string { return model.TypeELBv2TargetGroup }

// Collect는 범위 안의 타깃 그룹을 모두 조회하고, 각 그룹의 타깃 헬스를 이어서 조회한다.
//
// 타깃 헬스 조회가 그룹 하나에서 실패해도 전체를 멈추지 않는다. 그룹 정보는 이미 얻었으므로,
// 헬스만 비운 채로 리소스를 만든다. 부분 실패는 전체 실패가 아니다(원칙 5).
func (c targetGroupCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awselbv2.NewDescribeTargetGroupsPaginator(c.api, &awselbv2.DescribeTargetGroupsInput{})

	var (
		out         []model.Resource
		partialErrs []error
	)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe target groups: %w", err)
		}

		for i := range page.TargetGroups {
			tg := page.TargetGroups[i]

			health, err := c.targetHealth(ctx, aws.ToString(tg.TargetGroupArn))
			if err != nil {
				// 헬스 조회 실패는 이 그룹의 타깃 관계만 비우고 오류는 결과에 남긴다.
				partialErrs = append(partialErrs, err)
				health = nil
			}

			out = append(out, targetGroupToResource(req.Scope, tg, health))
		}
	}

	return out, errors.Join(partialErrs...)
}

// targetHealth는 한 타깃 그룹의 타깃 헬스 목록을 조회한다.
//
// DescribeTargetHealth에는 페이지네이터가 없다. 한 그룹의 타깃은 한 번의 응답에 담긴다.
func (c targetGroupCollector) targetHealth(ctx context.Context, arn string) ([]elbv2types.TargetHealthDescription, error) {
	out, err := c.api.DescribeTargetHealth(ctx, &awselbv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(arn),
	})
	if err != nil {
		return nil, fmt.Errorf("describe target health (%s): %w", arn, err)
	}

	return out.TargetHealthDescriptions, nil
}

// targetGroupToResource는 SDK 타깃 그룹을 도메인 리소스로 변환한다.
func targetGroupToResource(scope collect.Scope, tg elbv2types.TargetGroup, health []elbv2types.TargetHealthDescription) model.Resource {
	r := model.Resource{
		Type:      model.TypeELBv2TargetGroup,
		ID:        aws.ToString(tg.TargetGroupName),
		Name:      aws.ToString(tg.TargetGroupName),
		ARN:       aws.ToString(tg.TargetGroupArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
	}

	r.Fields = []model.Field{
		{Key: "프로토콜", Value: string(tg.Protocol)},
		{Key: "포트", Value: displayInt32(aws.ToInt32(tg.Port))},
		{Key: "타깃 종류", Value: string(tg.TargetType)},
		{Key: "VPC", Value: displayString(aws.ToString(tg.VpcId))},
		{Key: "타깃 수", Value: displayInt32(int32(len(health)))},
	}

	r.Related = targetGroupRelations(tg, health)

	return r
}

// targetGroupRelations는 타깃 그룹의 관계를 만든다.
//
//   - 타깃 그룹 → 로드밸런서: forwards-to. 어떤 LB가 이 그룹으로 트래픽을 보내는지.
//     LoadBalancerArns가 ARN이므로 마지막 경로 조각에서 이름을 뽑아 ID로 쓴다.
//   - 타깃 그룹 → 타깃(인스턴스 등): targets. 헬스 상태를 Via에 담아, 그래프에서
//     정상/비정상 타깃을 구분할 수 있게 한다.
func targetGroupRelations(tg elbv2types.TargetGroup, health []elbv2types.TargetHealthDescription) []model.Ref {
	var refs []model.Ref

	for _, arn := range tg.LoadBalancerArns {
		if name := lbNameFromARN(arn); name != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeELBv2LoadBalancer,
				ID:       name,
				Relation: model.RelationForwardsTo,
			})
		}
	}

	for _, h := range health {
		if h.Target == nil {
			continue
		}

		id := aws.ToString(h.Target.Id)
		if id == "" {
			continue
		}

		state := ""
		if h.TargetHealth != nil {
			state = string(h.TargetHealth.State)
		}

		refs = append(refs, model.Ref{
			Type:     model.TypeEC2Instance,
			ID:       id,
			Relation: model.RelationTargets,
			Via:      state,
		})
	}

	return refs
}

// lbNameFromARN은 로드밸런서 ARN에서 이름을 뽑는다.
//
// ELBv2 ARN 형식: .../loadbalancer/app/<name>/<id>. 로드밸런서 수집기가 이름을 ID로
// 쓰므로, 관계도 같은 이름을 가리켜야 그래프에서 노드가 이어진다.
func lbNameFromARN(arn string) string {
	parts := strings.Split(arn, "/")
	// [..., "loadbalancer", "app"|"net", "<name>", "<id>"]
	if len(parts) >= 4 {
		return parts[len(parts)-2]
	}

	return ""
}
