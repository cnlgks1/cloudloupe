// Package ecs는 ECS 리소스를 조회해 도메인 모델로 바꾼다.
//
// ECS는 KMS와 같은 "목록 조회 + 항목별 상세 조회"(N+1) 형태다. ListClusters는 클러스터
// ARN만 주고 상태·태스크 수·용량 공급자는 DescribeClusters로 다시 물어야 한다. 서비스와
// 태스크 정의도 마찬가지라 세 수집기 모두 [collect.FanOut]으로 상한 있는 팬아웃을 쓴다.
// 이 파일의 구조를 본떠 서비스·태스크 정의 수집기를 만들었다.
package ecs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// clusterAPI는 클러스터 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListClusters는 클러스터 ARN 목록을, DescribeClusters는 ARN 묶음의 상세를 준다.
// 클라이언트 전체가 아니라 이 둘만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type clusterAPI interface {
	ListClusters(context.Context, *awsecs.ListClustersInput, ...func(*awsecs.Options)) (*awsecs.ListClustersOutput, error)
	DescribeClusters(context.Context, *awsecs.DescribeClustersInput, ...func(*awsecs.Options)) (*awsecs.DescribeClustersOutput, error)
}

// clusterCollector는 ECS 클러스터를 조회한다.
type clusterCollector struct {
	api clusterAPI
	// limit은 DescribeClusters 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	// 테스트에서 상한을 고정하려고 필드로 둔다.
	limit int
}

// NewCluster는 ECS 클러스터 수집기를 만든다.
func NewCluster(api clusterAPI) collect.Collector {
	return clusterCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c clusterCollector) Type() string { return model.TypeECSCluster }

// Collect는 리전의 ECS 클러스터를 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListClusters로 클러스터 ARN 목록을 받는다(페이지네이션).
//  2. ARN마다 DescribeClusters를 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 ARN으로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 모든 부분 실패는 수집한 리소스와 함께 반환되어 화면의 오류
// 목록에 남는다.
func (c clusterCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	arns, listErr := c.clusterARNs(ctx)
	if len(arns) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, arns,
		func(ctx context.Context, arn string) (*ecstypes.Cluster, error) {
			out, err := c.api.DescribeClusters(ctx, &awsecs.DescribeClustersInput{
				Clusters: []string{arn},
			})
			if err != nil {
				return nil, fmt.Errorf("describe clusters (%s): %w", arn, err)
			}
			if len(out.Clusters) == 0 {
				return nil, nil
			}

			return &out.Clusters[0], nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, cluster := range described {
		if cluster == nil {
			// DescribeClusters가 성공했는데 클러스터가 비는 경우(권한/삭제 경합)를 대비해
			// nil을 건너뛴다. 변환 함수에 nil을 넘기면 터진다.
			continue
		}

		out = append(out, clusterToResource(req.Scope, *cluster))
	}

	// errors.Join은 nil을 무시하므로 두 단계의 부분 실패를 그대로 묶어 올린다.
	return out, errors.Join(listErr, describeErr)
}

// clusterARNs는 리전의 클러스터 ARN 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다. 절반의 클러스터라도 보여주는 편이 낫다.
func (c clusterCollector) clusterARNs(ctx context.Context) ([]string, error) {
	paginator := awsecs.NewListClustersPaginator(c.api, &awsecs.ListClustersInput{})

	var arns []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return arns, fmt.Errorf("list clusters: %w", err)
		}

		arns = append(arns, page.ClusterArns...)
	}

	return arns, nil
}

// clusterToResource는 SDK 클러스터를 도메인 리소스로 변환한다.
//
// ID·이름은 ClusterName, ARN은 ClusterArn, 상태는 Status를 그대로 쓴다. 클러스터는 하위
// 서비스나 태스크를 여기서 나열하지 않으므로 관계를 만들지 않는다. 서비스→클러스터 방향은
// 서비스 수집기가 기록하고, 역방향은 graph가 만든다.
func clusterToResource(scope collect.Scope, cluster ecstypes.Cluster) model.Resource {
	return model.Resource{
		Type:      model.TypeECSCluster,
		ID:        aws.ToString(cluster.ClusterName),
		Name:      aws.ToString(cluster.ClusterName),
		ARN:       aws.ToString(cluster.ClusterArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(cluster.Status),
		Fields: []model.Field{
			{Key: "Status", Value: orDash(aws.ToString(cluster.Status))},
			{Key: "RunningTasksCount", Value: strconv.Itoa(int(cluster.RunningTasksCount))},
			{Key: "ActiveServicesCount", Value: strconv.Itoa(int(cluster.ActiveServicesCount))},
			{Key: "RegisteredContainerInstancesCount", Value: strconv.Itoa(int(cluster.RegisteredContainerInstancesCount))},
			{Key: "CapacityProviders", Value: orDash(strings.Join(cluster.CapacityProviders, ", "))},
		},
	}
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// appendARNRef는 비어 있지 않은 ARN 관계를 추가한다.
//
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다. 상세 화면이
// 그대로 보여주므로 aws CLI 출력에서 같은 경로를 찾아 대조할 수 있다.
func appendARNRef(refs []model.Ref, typeID, relation, arn string) []model.Ref {
	if arn == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:           typeID,
		ID:             arn,
		IdentifierKind: model.IdentifierARN,
		Relation:       relation,
	})
}

// appendIDRef는 비어 있지 않은 리소스 ID 관계를 추가한다.
func appendIDRef(refs []model.Ref, typeID, relation, id string) []model.Ref {
	if id == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:     typeID,
		ID:       id,
		Relation: relation,
	})
}
