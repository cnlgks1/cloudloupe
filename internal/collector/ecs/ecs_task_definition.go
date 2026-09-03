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

// taskDefinitionAPI는 태스크 정의 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListTaskDefinitions는 태스크 정의 ARN 목록을, DescribeTaskDefinition은 ARN 하나의
// 상세를 준다.
type taskDefinitionAPI interface {
	ListTaskDefinitions(context.Context, *awsecs.ListTaskDefinitionsInput, ...func(*awsecs.Options)) (*awsecs.ListTaskDefinitionsOutput, error)
	DescribeTaskDefinition(context.Context, *awsecs.DescribeTaskDefinitionInput, ...func(*awsecs.Options)) (*awsecs.DescribeTaskDefinitionOutput, error)
}

// taskDefinitionCollector는 ECS 태스크 정의를 조회한다.
type taskDefinitionCollector struct {
	api taskDefinitionAPI
	// limit은 DescribeTaskDefinition 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewTaskDefinition은 ECS 태스크 정의 수집기를 만든다.
func NewTaskDefinition(api taskDefinitionAPI) collect.Collector {
	return taskDefinitionCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c taskDefinitionCollector) Type() string { return model.TypeECSTaskDefinition }

// Collect는 리전의 활성 ECS 태스크 정의를 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListTaskDefinitions로 활성(ACTIVE) 태스크 정의 ARN 목록을 최신 리비전 순으로 받는다.
//  2. ARN마다 DescribeTaskDefinition을 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 ARN으로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c taskDefinitionCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	arns, listErr := c.taskDefinitionARNs(ctx)
	if len(arns) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, arns,
		func(ctx context.Context, arn string) (*ecstypes.TaskDefinition, error) {
			out, err := c.api.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
				TaskDefinition: aws.String(arn),
			})
			if err != nil {
				return nil, fmt.Errorf("describe task definition (%s): %w", arn, err)
			}

			return out.TaskDefinition, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, td := range described {
		if td == nil {
			continue
		}

		out = append(out, taskDefinitionToResource(req.Scope, *td))
	}

	return out, errors.Join(listErr, describeErr)
}

// taskDefinitionARNs는 리전의 활성 태스크 정의 ARN 목록을 모두 받는다.
//
// 상태는 ACTIVE, 정렬은 최신 리비전이 먼저 오도록 DESC로 고정한다. 페이지 하나가 실패해도
// 앞서 받은 목록은 살린다.
func (c taskDefinitionCollector) taskDefinitionARNs(ctx context.Context) ([]string, error) {
	paginator := awsecs.NewListTaskDefinitionsPaginator(c.api, &awsecs.ListTaskDefinitionsInput{
		Status: ecstypes.TaskDefinitionStatusActive,
		Sort:   ecstypes.SortOrderDesc,
	})

	var arns []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return arns, fmt.Errorf("list task definitions: %w", err)
		}

		arns = append(arns, page.TaskDefinitionArns...)
	}

	return arns, nil
}

// taskDefinitionToResource는 SDK 태스크 정의를 도메인 리소스로 변환한다.
//
// ID·이름은 "Family:Revision" 형태로 만든다. 콘솔·CLI에서 태스크 정의를 부르는 이름과
// 같아 대조하기 쉽다. ARN은 TaskDefinitionArn을 그대로 쓴다. 관계 이름에는 값을 꺼낸
// SDK 응답 필드 경로를 넣는다.
func taskDefinitionToResource(scope collect.Scope, td ecstypes.TaskDefinition) model.Resource {
	var refs []model.Ref

	refs = appendARNRef(refs, model.TypeIAMRole, "ExecutionRoleArn", aws.ToString(td.ExecutionRoleArn))
	refs = appendARNRef(refs, model.TypeIAMRole, "TaskRoleArn", aws.ToString(td.TaskRoleArn))

	name := taskDefinitionName(td)

	return model.Resource{
		Type:      model.TypeECSTaskDefinition,
		ID:        name,
		Name:      name,
		ARN:       aws.ToString(td.TaskDefinitionArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(td.Status),
		Fields: []model.Field{
			{Key: "Family", Value: orDash(aws.ToString(td.Family))},
			{Key: "Revision", Value: strconv.Itoa(int(td.Revision))},
			{Key: "Status", Value: orDash(string(td.Status))},
			{Key: "Cpu", Value: orDash(aws.ToString(td.Cpu))},
			{Key: "Memory", Value: orDash(aws.ToString(td.Memory))},
			{Key: "NetworkMode", Value: orDash(string(td.NetworkMode))},
			{Key: "RequiresCompatibilities", Value: orDash(compatibilities(td.RequiresCompatibilities))},
			{Key: "ExecutionRoleArn", Value: orDash(aws.ToString(td.ExecutionRoleArn))},
			{Key: "TaskRoleArn", Value: orDash(aws.ToString(td.TaskRoleArn))},
		},
		Related: refs,
	}
}

// taskDefinitionName은 "Family:Revision" 이름을 만든다.
//
// Family가 비면 TaskDefinitionArn의 이름 부분으로 대체한다. 콘솔이 태스크 정의를 부르는
// 방식과 같게 맞춘다.
func taskDefinitionName(td ecstypes.TaskDefinition) string {
	family := aws.ToString(td.Family)
	if family == "" {
		arn := aws.ToString(td.TaskDefinitionArn)
		if idx := strings.LastIndex(arn, "/"); idx >= 0 {
			return arn[idx+1:]
		}

		return arn
	}

	return family + ":" + strconv.Itoa(int(td.Revision))
}

// compatibilities는 요구 호환성 목록을 API 값 그대로 콤마로 잇는다.
func compatibilities(values []ecstypes.Compatibility) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, string(v))
	}

	return strings.Join(parts, ", ")
}
