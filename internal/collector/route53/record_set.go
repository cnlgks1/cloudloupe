package route53

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsroute53 "github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// route53API는 레코드 수집기가 필요로 하는 SDK 메서드를 담은 인터페이스다.
//
// 호스팅 영역 목록(ListHostedZones)과 각 영역의 레코드(ListResourceRecordSets)가
// 별도 API라 메서드 둘을 받는다. 둘 다 List로 시작하므로 조회 전용 가드를 통과한다.
type route53API interface {
	ListHostedZones(context.Context, *awsroute53.ListHostedZonesInput, ...func(*awsroute53.Options)) (*awsroute53.ListHostedZonesOutput, error)
	ListResourceRecordSets(context.Context, *awsroute53.ListResourceRecordSetsInput, ...func(*awsroute53.Options)) (*awsroute53.ListResourceRecordSetsOutput, error)
}

// recordSetCollector는 Route 53 호스팅 영역의 레코드를 조회한다.
type recordSetCollector struct {
	api route53API
}

// NewRecordSet은 Route 53 레코드 수집기를 만든다.
func NewRecordSet(api route53API) collect.Collector {
	return recordSetCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c recordSetCollector) Type() string { return model.TypeRoute53RecordSet }

// Collect는 모든 호스팅 영역을 돌며 그 안의 레코드를 조회한다.
//
// Route 53는 글로벌 서비스라 리전 개념이 없다. 그래서 여기서 만드는 리소스의 Region은
// "global"로 고정한다. 한 영역의 레코드 조회가 실패해도 다른 영역은 계속 진행한다.
func (c recordSetCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	zonePaginator := awsroute53.NewListHostedZonesPaginator(c.api, &awsroute53.ListHostedZonesInput{})

	var (
		out         []model.Resource
		partialErrs []error
	)

	for zonePaginator.HasMorePages() {
		zonePage, err := zonePaginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list hosted zones: %w", err)
		}

		for i := range zonePage.HostedZones {
			zone := zonePage.HostedZones[i]

			records, err := c.recordSets(ctx, aws.ToString(zone.Id))
			if err != nil {
				// 이 영역만 건너뛰되 실패는 성공한 다른 영역 데이터와 함께 보고한다.
				partialErrs = append(partialErrs, err)

				continue
			}

			zoneName := aws.ToString(zone.Name)
			for j := range records {
				out = append(out, recordSetToResource(req.Scope, zoneName, records[j]))
			}
		}
	}

	return out, errors.Join(partialErrs...)
}

// recordSets는 한 호스팅 영역의 모든 레코드를 조회한다.
//
// ListResourceRecordSets에는 페이지네이터가 없다. IsTruncated가 true인 동안 다음
// 시작 이름/타입을 넘겨 손으로 페이지를 넘긴다.
func (c recordSetCollector) recordSets(ctx context.Context, zoneID string) ([]route53types.ResourceRecordSet, error) {
	var (
		all       []route53types.ResourceRecordSet
		startName *string
		startType route53types.RRType
	)

	for {
		in := &awsroute53.ListResourceRecordSetsInput{
			HostedZoneId:    aws.String(zoneID),
			StartRecordName: startName,
		}
		if startType != "" {
			in.StartRecordType = startType
		}

		page, err := c.api.ListResourceRecordSets(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("list record sets (%s): %w", zoneID, err)
		}

		all = append(all, page.ResourceRecordSets...)

		if !page.IsTruncated {
			break
		}

		startName = page.NextRecordName
		startType = page.NextRecordType
	}

	return all, nil
}

// recordSetToResource는 SDK 레코드를 도메인 리소스로 변환한다.
//
// 레코드 이름은 한 영역 안에서 (이름, 타입) 조합으로 유일하다. 그래서 ID를 "이름|타입"으로
// 만들어 같은 이름의 A/AAAA 레코드가 충돌하지 않게 한다.
func recordSetToResource(scope collect.Scope, zoneName string, rec route53types.ResourceRecordSet) model.Resource {
	name := aws.ToString(rec.Name)
	recType := string(rec.Type)

	r := model.Resource{
		Type:      model.TypeRoute53RecordSet,
		ID:        name + "|" + recType,
		Name:      name,
		Region:    "global",
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    recType,
	}

	fields := []model.Field{
		{Key: "타입", Value: recType},
		{Key: "호스팅 영역", Value: zoneName},
	}

	if rec.TTL != nil {
		fields = append(fields, model.Field{Key: "TTL", Value: fmt.Sprintf("%d", aws.ToInt64(rec.TTL))})
	}

	// 별칭(Alias) 레코드는 값 대신 다른 AWS 리소스를 가리킨다. 일반 레코드는 값 목록을
	// 가진다. 둘을 구분해 표시하고, 별칭이면 그 대상으로의 resolves-to 관계를 남긴다.
	if rec.AliasTarget != nil {
		target := aws.ToString(rec.AliasTarget.DNSName)
		fields = append(fields, model.Field{Key: "별칭 대상", Value: target})
		r.Fields = fields
		r.Related = []model.Ref{{
			Type:     model.TypeELBv2LoadBalancer,
			ID:       strings.TrimSuffix(target, "."),
			Relation: model.RelationResolvesTo,
		}}

		return r
	}

	values := make([]string, 0, len(rec.ResourceRecords))
	for _, rr := range rec.ResourceRecords {
		values = append(values, aws.ToString(rr.Value))
	}

	fields = append(fields, model.Field{Key: "값", Value: orDash(strings.Join(values, ", "))})
	r.Fields = fields

	return r
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
