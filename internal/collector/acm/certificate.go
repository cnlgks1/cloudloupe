// Package acm은 ACM 리소스를 조회해 도메인 모델로 바꾼다.
//
// ACM은 다른 수집기와 같은 "목록 조회 + 항목별 상세 조회"(N+1) 형태다. ListCertificates는
// 인증서 ARN과 도메인만 주고 만료일·상태·사용처는 DescribeCertificate로 다시 물어야 한다.
// 그래서 [collect.FanOut]으로 상한 있는 팬아웃을 쓴다.
package acm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// certificateAPI는 인증서 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListCertificates는 인증서 ARN 목록을, DescribeCertificate는 ARN 하나의 상세를 준다.
// 클라이언트 전체가 아니라 이 둘만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type certificateAPI interface {
	ListCertificates(context.Context, *awsacm.ListCertificatesInput, ...func(*awsacm.Options)) (*awsacm.ListCertificatesOutput, error)
	DescribeCertificate(context.Context, *awsacm.DescribeCertificateInput, ...func(*awsacm.Options)) (*awsacm.DescribeCertificateOutput, error)
}

// certificateCollector는 ACM 인증서를 조회한다.
type certificateCollector struct {
	api certificateAPI
	// limit은 DescribeCertificate 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewCertificate는 ACM 인증서 수집기를 만든다.
func NewCertificate(api certificateAPI) collect.Collector {
	return certificateCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c certificateCollector) Type() string { return model.TypeACMCertificate }

// Collect는 리전의 ACM 인증서를 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListCertificates로 인증서 ARN 목록을 받는다(페이지네이션).
//  2. ARN마다 DescribeCertificate를 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 ARN으로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c certificateCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	arns, listErr := c.certificateARNs(ctx)
	if len(arns) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, arns,
		func(ctx context.Context, arn string) (*acmtypes.CertificateDetail, error) {
			out, err := c.api.DescribeCertificate(ctx, &awsacm.DescribeCertificateInput{
				CertificateArn: aws.String(arn),
			})
			if err != nil {
				return nil, fmt.Errorf("describe certificate (%s): %w", arn, err)
			}

			return out.Certificate, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, cert := range described {
		if cert == nil {
			continue
		}

		out = append(out, certificateToResource(req.Scope, *cert))
	}

	return out, errors.Join(listErr, describeErr)
}

// certificateARNs는 리전의 인증서 ARN 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c certificateCollector) certificateARNs(ctx context.Context) ([]string, error) {
	paginator := awsacm.NewListCertificatesPaginator(c.api, &awsacm.ListCertificatesInput{})

	var arns []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return arns, fmt.Errorf("list certificates: %w", err)
		}

		for _, summary := range page.CertificateSummaryList {
			arns = append(arns, aws.ToString(summary.CertificateArn))
		}
	}

	return arns, nil
}

// certificateToResource는 SDK 인증서 상세를 도메인 리소스로 변환한다.
//
// ID·이름은 DomainName(주 도메인), ARN은 CertificateArn을 그대로 쓴다. InUseBy는 이 인증서를
// 쓰는 리소스 ARN 목록인데, ELB·CloudFront·API Gateway 등 종류가 섞여 하나의 대상 타입으로
// 묶을 수 없으므로 관계 대신 개수와 목록을 필드로 보여준다. 역방향 연결은 graph가 ARN
// 색인으로 잡는다.
func certificateToResource(scope collect.Scope, cert acmtypes.CertificateDetail) model.Resource {
	return model.Resource{
		Type:      model.TypeACMCertificate,
		ID:        aws.ToString(cert.DomainName),
		Name:      aws.ToString(cert.DomainName),
		ARN:       aws.ToString(cert.CertificateArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(cert.Status),
		CreatedAt: cert.IssuedAt,
		Fields: []model.Field{
			{Key: "Status", Value: orDash(string(cert.Status))},
			{Key: "Type", Value: orDash(string(cert.Type))},
			{Key: "KeyAlgorithm", Value: orDash(string(cert.KeyAlgorithm))},
			{Key: "SubjectAlternativeNames", Value: orDash(strings.Join(cert.SubjectAlternativeNames, ", "))},
			{Key: "NotBefore", Value: dateValue(cert.NotBefore)},
			{Key: "NotAfter", Value: dateValue(cert.NotAfter)},
			{Key: "RenewalEligibility", Value: orDash(string(cert.RenewalEligibility))},
			{Key: "Issuer", Value: orDash(aws.ToString(cert.Issuer))},
			{Key: "InUseBy", Value: inUseBy(cert.InUseBy)},
		},
	}
}

// inUseBy는 인증서를 쓰는 리소스 목록을 API 값 그대로 표시한다.
//
// 비어 있으면 "-"로 두어 어디에도 안 붙은 미사용 인증서를 눈에 띄게 한다.
func inUseBy(arns []string) string {
	if len(arns) == 0 {
		return "-"
	}

	return strconv.Itoa(len(arns)) + ": " + strings.Join(arns, ", ")
}

// dateValue는 선택적인 시각을 RFC 3339로 표시한다. nil이면 "-"다.
func dateValue(t *time.Time) string {
	if t == nil {
		return "-"
	}

	return t.UTC().Format(time.RFC3339)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
