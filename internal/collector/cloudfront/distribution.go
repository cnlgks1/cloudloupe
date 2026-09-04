// Package cloudfront는 CloudFront 리소스를 조회해 도메인 모델로 바꾼다.
//
// CloudFront는 리전 개념이 없는 글로벌 서비스다. 그래서 이 패키지가 만드는 리소스의 Region은
// [model.RegionGlobal]로 고정하고, 카탈로그에서도 Global 범위로 등록해 선택한 리전 수와
// 무관하게 한 번만 조회한다. ListDistributions가 상세까지 주므로 항목별 팬아웃은 필요 없다.
package cloudfront

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscf "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// distributionAPI는 배포 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type distributionAPI interface {
	ListDistributions(context.Context, *awscf.ListDistributionsInput, ...func(*awscf.Options)) (*awscf.ListDistributionsOutput, error)
}

// distributionCollector는 CloudFront 배포를 조회한다.
type distributionCollector struct {
	api distributionAPI
}

// NewDistribution은 CloudFront 배포 수집기를 만든다.
func NewDistribution(api distributionAPI) collect.Collector {
	return distributionCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c distributionCollector) Type() string { return model.TypeCloudFrontDistribution }

// Collect는 계정의 CloudFront 배포를 모두 조회해 도메인 리소스로 변환한다.
//
// ListDistributions 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한 리소스를
// 오류와 함께 반환해 부분 결과를 살린다.
func (c distributionCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awscf.NewListDistributionsPaginator(c.api, &awscf.ListDistributionsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list distributions: %w", err)
		}

		if page.DistributionList == nil {
			continue
		}

		for i := range page.DistributionList.Items {
			out = append(out, distributionToResource(req.Scope, page.DistributionList.Items[i]))
		}
	}

	return out, nil
}

// distributionToResource는 SDK 배포 요약을 도메인 리소스로 변환한다.
//
// ID는 Id, 이름은 DomainName(cloudfront.net 도메인), ARN은 ARN을 그대로 쓴다. 글로벌
// 서비스이므로 Region은 [model.RegionGlobal]로 고정한다. 뷰어 인증서(ACM)와 WebACL로
// 이어진다. 관계 이름에는 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func distributionToResource(scope collect.Scope, dist cftypes.DistributionSummary) model.Resource {
	var refs []model.Ref

	if dist.ViewerCertificate != nil {
		refs = appendARNRef(refs, model.TypeACMCertificate, "ViewerCertificate.ACMCertificateArn", aws.ToString(dist.ViewerCertificate.ACMCertificateArn))
	}

	// CloudFront의 WebACLId는 WAFv2 웹 ACL의 ARN이다(WAF Classic이면 ID). 값이 ARN이면
	// graph가 ARN으로 색인한다.
	refs = appendARNRef(refs, model.TypeWAFv2WebACL, "WebACLId", aws.ToString(dist.WebACLId))

	return model.Resource{
		Type:      model.TypeCloudFrontDistribution,
		ID:        aws.ToString(dist.Id),
		Name:      aws.ToString(dist.DomainName),
		ARN:       aws.ToString(dist.ARN),
		Region:    model.RegionGlobal,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(dist.Status),
		Fields: []model.Field{
			{Key: "Status", Value: orDash(aws.ToString(dist.Status))},
			{Key: "Enabled", Value: boolPtrValue(dist.Enabled)},
			{Key: "DomainName", Value: orDash(aws.ToString(dist.DomainName))},
			{Key: "Aliases", Value: orDash(aliases(dist.Aliases))},
			{Key: "Origins", Value: orDash(origins(dist.Origins))},
			{Key: "PriceClass", Value: orDash(string(dist.PriceClass))},
			{Key: "HttpVersion", Value: orDash(string(dist.HttpVersion))},
			{Key: "WebACLId", Value: orDash(aws.ToString(dist.WebACLId))},
			{Key: "Comment", Value: orDash(aws.ToString(dist.Comment))},
		},
		Related: refs,
	}
}

// aliases는 배포의 대체 도메인 이름(CNAME) 목록을 API 값 그대로 콤마로 잇는다.
func aliases(a *cftypes.Aliases) string {
	if a == nil {
		return ""
	}

	return strings.Join(a.Items, ", ")
}

// origins는 오리진 도메인 이름 목록을 API 값 그대로 콤마로 잇는다.
//
// 오리진은 S3 버킷·ALB·커스텀 서버 등 종류가 섞여 하나의 대상 타입으로 묶을 수 없으므로
// 관계 대신 도메인 목록을 필드로 보여준다. 역방향 연결은 graph가 DNS 색인으로 잡는다.
func origins(o *cftypes.Origins) string {
	if o == nil {
		return ""
	}

	names := make([]string, 0, len(o.Items))
	for _, origin := range o.Items {
		names = append(names, aws.ToString(origin.DomainName))
	}

	return strings.Join(names, ", ")
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// boolPtrValue는 선택적인 불리언 값을 API 값 그대로 표시한다. nil이면 "-"다.
func boolPtrValue(value *bool) string {
	if value == nil {
		return "-"
	}

	if *value {
		return "true"
	}

	return "false"
}

// appendARNRef는 비어 있지 않은 ARN 관계를 추가한다.
//
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다.
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
