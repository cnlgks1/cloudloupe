package iam

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listPoliciesAPI는 정책 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type listPoliciesAPI interface {
	ListPolicies(context.Context, *awsiam.ListPoliciesInput, ...func(*awsiam.Options)) (*awsiam.ListPoliciesOutput, error)
}

// policyCollector는 IAM 고객 관리형 정책을 조회한다.
type policyCollector struct {
	api listPoliciesAPI
}

// NewPolicy는 IAM 정책 수집기를 만든다.
func NewPolicy(api listPoliciesAPI) collect.Collector {
	return policyCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c policyCollector) Type() string { return model.TypeIAMPolicy }

// Collect는 계정의 고객 관리형 IAM 정책을 모두 조회해 도메인 리소스로 변환한다.
//
// Scope를 Local로 고정해 고객이 만든 정책만 받는다. AWS 관리형 정책은 수백 개라 목록을
// 도배하고 조사 가치가 낮아 제외한다. 페이지 조회가 중간에 실패하면 그때까지 모은 정책과
// 오류를 함께 반환한다.
func (c policyCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsiam.NewListPoliciesPaginator(c.api, &awsiam.ListPoliciesInput{
		Scope: iamtypes.PolicyScopeTypeLocal,
	})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list policies: %w", err)
		}

		for i := range page.Policies {
			out = append(out, policyToResource(req.Scope, page.Policies[i]))
		}
	}

	return out, nil
}

// policyToResource는 SDK 정책을 도메인 리소스로 변환한다.
//
// ID로 정책 이름을 쓴다. AttachmentCount가 0이면 아무 데도 안 붙은 정책이라 나중에 미사용
// 후보 판정의 근거가 된다. 정책 문서(권한 내용)는 GetPolicyVersion을 또 부르는 N+1 조회가
// 필요해 넣지 않는다. ListPolicies가 주는 메타데이터만 담는다.
func policyToResource(scope collect.Scope, policy iamtypes.Policy) model.Resource {
	r := model.Resource{
		Type:      model.TypeIAMPolicy,
		ID:        aws.ToString(policy.PolicyName),
		Name:      aws.ToString(policy.PolicyName),
		ARN:       aws.ToString(policy.Arn),
		Region:    model.RegionGlobal,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Path", Value: orDash(aws.ToString(policy.Path))},
			{Key: "Description", Value: orDash(aws.ToString(policy.Description))},
			{Key: "AttachmentCount", Value: int32Value(policy.AttachmentCount)},
			{Key: "IsAttachable", Value: strconv.FormatBool(policy.IsAttachable)},
			{Key: "DefaultVersionId", Value: orDash(aws.ToString(policy.DefaultVersionId))},
			{Key: "PolicyId", Value: orDash(aws.ToString(policy.PolicyId))},
		},
		Tags: iamTags(policy.Tags),
	}

	if policy.CreateDate != nil {
		createdAt := policy.CreateDate.UTC()
		r.CreatedAt = &createdAt
	}

	return r
}

// int32Value는 선택적인 정수 값을 API가 준 그대로 표시한다. nil이면 "-"다.
func int32Value(value *int32) string {
	if value == nil {
		return "-"
	}

	return strconv.Itoa(int(*value))
}
