package iam

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listGroupsAPI는 그룹 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type listGroupsAPI interface {
	ListGroups(context.Context, *awsiam.ListGroupsInput, ...func(*awsiam.Options)) (*awsiam.ListGroupsOutput, error)
}

// groupCollector는 IAM 그룹을 조회한다.
type groupCollector struct {
	api listGroupsAPI
}

// NewGroup은 IAM 그룹 수집기를 만든다.
func NewGroup(api listGroupsAPI) collect.Collector {
	return groupCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c groupCollector) Type() string { return model.TypeIAMGroup }

// Collect는 계정의 IAM 그룹을 모두 조회해 도메인 리소스로 변환한다.
//
// 페이지 조회가 중간에 실패하면 그때까지 모은 그룹과 오류를 함께 반환한다.
func (c groupCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsiam.NewListGroupsPaginator(c.api, &awsiam.ListGroupsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list groups: %w", err)
		}

		for i := range page.Groups {
			out = append(out, groupToResource(req.Scope, page.Groups[i]))
		}
	}

	return out, nil
}

// groupToResource는 SDK 그룹을 도메인 리소스로 변환한다.
//
// ID로 그룹 이름을 쓴다. 그룹 멤버십과 연결 정책은 그룹 하나씩 GetGroup·ListAttachedGroupPolicies를
// 부르는 N+1 조회가 필요해 지금은 넣지 않는다. ListGroups가 주는 기본 메타데이터만 담는다.
func groupToResource(scope collect.Scope, group iamtypes.Group) model.Resource {
	r := model.Resource{
		Type:      model.TypeIAMGroup,
		ID:        aws.ToString(group.GroupName),
		Name:      aws.ToString(group.GroupName),
		ARN:       aws.ToString(group.Arn),
		Region:    model.RegionGlobal,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Path", Value: orDash(aws.ToString(group.Path))},
			{Key: "GroupId", Value: orDash(aws.ToString(group.GroupId))},
		},
	}

	if group.CreateDate != nil {
		createdAt := group.CreateDate.UTC()
		r.CreatedAt = &createdAt
	}

	return r
}
