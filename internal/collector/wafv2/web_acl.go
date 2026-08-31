package wafv2

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awswafv2 "github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// wafv2API는 Web ACL 수집기가 필요로 하는 SDK 메서드를 담은 인터페이스다.
//
// 목록(ListWebACLs)은 요약만 주므로, 규칙 수 같은 상세는 각 ACL마다 GetWebACL로 따로
// 가져온다. List/Get 모두 조회 계열이라 조회 전용 가드를 통과한다.
type wafv2API interface {
	ListWebACLs(context.Context, *awswafv2.ListWebACLsInput, ...func(*awswafv2.Options)) (*awswafv2.ListWebACLsOutput, error)
	GetWebACL(context.Context, *awswafv2.GetWebACLInput, ...func(*awswafv2.Options)) (*awswafv2.GetWebACLOutput, error)
}

// webACLCollector는 WAFv2 Web ACL(REGIONAL 스코프)을 조회한다.
type webACLCollector struct {
	api wafv2API
}

// NewWebACL은 Web ACL 수집기를 만든다.
func NewWebACL(api wafv2API) collect.Collector {
	return webACLCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c webACLCollector) Type() string { return model.TypeWAFv2WebACL }

// Collect는 REGIONAL 스코프의 Web ACL을 모두 조회한다.
//
// CLOUDFRONT 스코프는 us-east-1에서만 조회 가능한 별개 축이라 여기서는 다루지 않는다.
// ALB/API Gateway 같은 리전 리소스를 보호하는 REGIONAL ACL만 대상으로 한다.
//
// ListWebACLs에는 페이지네이터가 없다. NextMarker가 있는 동안 손으로 페이지를 넘긴다.
// 규칙 수는 요약에 없으므로 각 ACL마다 GetWebACL로 가져오되, 실패해도 그 ACL만 규칙 수를
// 비운 채 살린다(부분 실패는 전체 실패가 아니다).
func (c webACLCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	var (
		out         []model.Resource
		marker      *string
		partialErrs []error
	)

	for {
		page, err := c.api.ListWebACLs(ctx, &awswafv2.ListWebACLsInput{
			Scope:      wafv2types.ScopeRegional,
			NextMarker: marker,
		})
		if err != nil {
			return nil, fmt.Errorf("list web acls: %w", err)
		}

		for i := range page.WebACLs {
			resource, err := c.webACLToResource(ctx, req.Scope, page.WebACLs[i])
			out = append(out, resource)

			if err != nil {
				partialErrs = append(partialErrs, err)
			}
		}

		if page.NextMarker == nil {
			break
		}

		marker = page.NextMarker
	}

	return out, errors.Join(partialErrs...)
}

// webACLToResource는 Web ACL 요약을 도메인 리소스로 변환하고, 규칙 수를 상세 조회로 채운다.
func (c webACLCollector) webACLToResource(ctx context.Context, scope collect.Scope, acl wafv2types.WebACLSummary) (model.Resource, error) {
	name := aws.ToString(acl.Name)

	r := model.Resource{
		Type:      model.TypeWAFv2WebACL,
		ID:        name,
		Name:      name,
		ARN:       aws.ToString(acl.ARN),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
	}

	ruleCount := "-"

	detail, err := c.api.GetWebACL(ctx, &awswafv2.GetWebACLInput{
		Name:  acl.Name,
		Id:    acl.Id,
		Scope: wafv2types.ScopeRegional,
	})
	if err == nil && detail.WebACL != nil {
		ruleCount = displayInt32(int32(len(detail.WebACL.Rules)))
	}

	r.Fields = []model.Field{
		{Key: "설명", Value: displayString(aws.ToString(acl.Description))},
		{Key: "규칙 수", Value: ruleCount},
		{Key: "ID", Value: aws.ToString(acl.Id)},
	}

	if err != nil {
		return r, fmt.Errorf("get web acl (%s): %w", name, err)
	}

	return r, nil
}

// displayString은 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func displayString(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// displayInt32는 int32를 표시용 문자열로 바꾼다.
func displayInt32(n int32) string {
	return strconv.Itoa(int(n))
}
