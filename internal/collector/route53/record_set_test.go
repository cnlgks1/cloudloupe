package route53_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsroute53 "github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/route53"
	"github.com/cnlgks1/cloudloupe/internal/graph"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeRoute53API는 route53API(메서드 2개)를 만족하는 테스트 대역이다.
type fakeRoute53API struct {
	zonePages   []*awsroute53.ListHostedZonesOutput
	zoneCalls   int
	records     map[string]*awsroute53.ListResourceRecordSetsOutput // zoneID -> 레코드 응답
	zoneErr     error
	recordErr   error
	recordCalls int
}

func (f *fakeRoute53API) ListHostedZones(_ context.Context, _ *awsroute53.ListHostedZonesInput, _ ...func(*awsroute53.Options)) (*awsroute53.ListHostedZonesOutput, error) {
	if f.zoneErr != nil {
		return nil, f.zoneErr
	}

	page := f.zonePages[f.zoneCalls]
	f.zoneCalls++

	return page, nil
}

func (f *fakeRoute53API) ListResourceRecordSets(_ context.Context, in *awsroute53.ListResourceRecordSetsInput, _ ...func(*awsroute53.Options)) (*awsroute53.ListResourceRecordSetsOutput, error) {
	f.recordCalls++

	if f.recordErr != nil {
		return nil, f.recordErr
	}

	if out, ok := f.records[aws.ToString(in.HostedZoneId)]; ok {
		return out, nil
	}

	return &awsroute53.ListResourceRecordSetsOutput{}, nil
}

func TestRoute53RecordSetCollectorConvertsRecords(t *testing.T) {
	t.Parallel()

	api := &fakeRoute53API{
		zonePages: []*awsroute53.ListHostedZonesOutput{{
			HostedZones: []route53types.HostedZone{
				{Id: aws.String("/hostedzone/Z1"), Name: aws.String("example.com.")},
			},
		}},
		records: map[string]*awsroute53.ListResourceRecordSetsOutput{
			"/hostedzone/Z1": {ResourceRecordSets: []route53types.ResourceRecordSet{
				{
					Name:            aws.String("www.example.com."),
					Type:            route53types.RRTypeA,
					TTL:             aws.Int64(300),
					ResourceRecords: []route53types.ResourceRecord{{Value: aws.String("192.0.2.1")}},
				},
			}},
		},
	}

	c := route53.NewRecordSet(api)

	got, err := c.Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	r := got[0]

	if r.Type != model.TypeRoute53RecordSet {
		t.Errorf("Type = %q, want %q", r.Type, model.TypeRoute53RecordSet)
	}

	if r.ID != "www.example.com.|A" {
		t.Errorf("ID = %q, want 이름|타입", r.ID)
	}
	if r.Namespace != "Z1" || r.FieldValue("HostedZoneId") != "Z1" {
		t.Errorf("호스팅 영역 namespace = %q, field = %q", r.Namespace, r.FieldValue("HostedZoneId"))
	}

	if r.Region != "global" {
		t.Errorf("Region = %q, want global (Route53는 글로벌)", r.Region)
	}

	if got := r.FieldValue("ResourceRecords"); got != "192.0.2.1" {
		t.Errorf("값 = %q", got)
	}

	if got := r.FieldValue("HostedZoneName"); got != "example.com." {
		t.Errorf("호스팅 영역 = %q", got)
	}
}

func TestRoute53RecordSetCollectorAliasResolvesTo(t *testing.T) {
	t.Parallel()

	api := &fakeRoute53API{
		zonePages: []*awsroute53.ListHostedZonesOutput{{
			HostedZones: []route53types.HostedZone{{Id: aws.String("/hostedzone/Z1"), Name: aws.String("example.com.")}},
		}},
		records: map[string]*awsroute53.ListResourceRecordSetsOutput{
			"/hostedzone/Z1": {ResourceRecordSets: []route53types.ResourceRecordSet{
				{
					Name:        aws.String("app.example.com."),
					Type:        route53types.RRTypeA,
					AliasTarget: &route53types.AliasTarget{DNSName: aws.String("dualstack.web-alb-123.ap-northeast-2.elb.amazonaws.com.")},
				},
			}},
		},
	}

	c := route53.NewRecordSet(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := got[0].FieldValue("AliasTarget"); got == "" {
		t.Error("별칭 대상 필드가 비었다")
	}

	res := got[0].RelatedBy("AliasTarget.DNSName")
	if len(res) != 1 {
		t.Fatalf("resolves-to 관계 1개여야 한다: %+v", got[0].Related)
	}

	// 후행 점(.)은 제거되어야 한다.
	if res[0].ID != "dualstack.web-alb-123.ap-northeast-2.elb.amazonaws.com" {
		t.Errorf("resolves-to 대상 = %q (후행 점 제거 확인)", res[0].ID)
	}
	if res[0].IdentifierKind != model.IdentifierDNS {
		t.Errorf("resolves-to 식별자 종류 = %q, want %q", res[0].IdentifierKind, model.IdentifierDNS)
	}
}

func TestRoute53RecordSetCollectorFollowsTruncation(t *testing.T) {
	t.Parallel()

	// ListResourceRecordSets는 페이지네이터가 없다. IsTruncated로 손수 페이지를 넘긴다.
	api := &fakeRoute53API{
		zonePages: []*awsroute53.ListHostedZonesOutput{{
			HostedZones: []route53types.HostedZone{{Id: aws.String("Z1"), Name: aws.String("example.com.")}},
		}},
	}
	// 첫 응답은 truncated, 두 번째로 끝. 같은 zoneID를 두 번 호출하므로 순서 기반으로 답한다.
	pages := []*awsroute53.ListResourceRecordSetsOutput{
		{
			ResourceRecordSets: []route53types.ResourceRecordSet{{Name: aws.String("a.example.com."), Type: route53types.RRTypeA}},
			IsTruncated:        true,
			NextRecordName:     aws.String("b.example.com."),
			NextRecordType:     route53types.RRTypeA,
		},
		{
			ResourceRecordSets: []route53types.ResourceRecordSet{{Name: aws.String("b.example.com."), Type: route53types.RRTypeA}},
		},
	}
	call := 0
	api.records = nil
	// records map 대신 직접 함수로 바꿔치기할 수 없으니, 별도 fake를 쓴다.
	seq := &seqRecordsAPI{zonePages: api.zonePages, recordPages: pages, recordAt: &call}

	c := route53.NewRecordSet(seq)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("truncation을 따라 레코드 2개가 나와야 한다, got %d", len(got))
	}
}

// seqRecordsAPI는 ListResourceRecordSets를 순서대로 돌려주는 대역이다. truncation 테스트용.
type seqRecordsAPI struct {
	zonePages   []*awsroute53.ListHostedZonesOutput
	zoneCalls   int
	recordPages []*awsroute53.ListResourceRecordSetsOutput
	recordAt    *int
}

func (f *seqRecordsAPI) ListHostedZones(_ context.Context, _ *awsroute53.ListHostedZonesInput, _ ...func(*awsroute53.Options)) (*awsroute53.ListHostedZonesOutput, error) {
	page := f.zonePages[f.zoneCalls]
	f.zoneCalls++

	return page, nil
}

func (f *seqRecordsAPI) ListResourceRecordSets(_ context.Context, _ *awsroute53.ListResourceRecordSetsInput, _ ...func(*awsroute53.Options)) (*awsroute53.ListResourceRecordSetsOutput, error) {
	page := f.recordPages[*f.recordAt]
	*f.recordAt++

	return page, nil
}

func TestRoute53RecordSetCollectorWrapsZoneError(t *testing.T) {
	t.Parallel()

	api := &fakeRoute53API{zoneErr: errors.New("AccessDenied")}
	c := route53.NewRecordSet(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{}})
	if err == nil {
		t.Fatal("영역 조회 실패는 에러여야 한다")
	}

	if got := err.Error(); got == "AccessDenied" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestRoute53RecordSetCollectorType(t *testing.T) {
	t.Parallel()

	c := route53.NewRecordSet(&fakeRoute53API{})
	if c.Type() != model.TypeRoute53RecordSet {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeRoute53RecordSet)
	}
}

func TestRoute53PolicyRecordsHaveStableDistinctIdentity(t *testing.T) {
	t.Parallel()

	api := &fakeRoute53API{
		zonePages: []*awsroute53.ListHostedZonesOutput{{
			HostedZones: []route53types.HostedZone{{
				Id: aws.String("/hostedzone/Z1"), Name: aws.String("example.com."),
			}},
		}},
		records: map[string]*awsroute53.ListResourceRecordSetsOutput{
			"/hostedzone/Z1": {ResourceRecordSets: []route53types.ResourceRecordSet{
				{
					Name: aws.String("app.example.com."), Type: route53types.RRTypeA,
					SetIdentifier: aws.String("green"), Weight: aws.Int64(50),
				},
				{
					Name: aws.String("app.example.com."), Type: route53types.RRTypeA,
					SetIdentifier: aws.String("blue"), Weight: aws.Int64(50),
				},
			}},
		},
	}

	resources, err := route53.NewRecordSet(api).Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("Resources = %+v, want 2", resources)
	}
	if resources[0].Key() == resources[1].Key() {
		t.Fatalf("정책 기반 레코드 키가 충돌함: %q", resources[0].Key())
	}
	for _, resource := range resources {
		if resource.FieldValue("SetIdentifier") == "" {
			t.Errorf("세트 식별자 필드가 비어 있음: %+v", resource)
		}
	}
	if _, err := graph.Build(resources); err != nil {
		t.Fatalf("정상 정책 레코드로 graph.Build 실패: %v", err)
	}

	reversed := append([]model.Resource(nil), resources...)
	slices.Reverse(reversed)
	first := model.NewSnapshot(time.Unix(0, 0), model.ToolInfo{}, model.Scope{}, resources, nil)
	second := model.NewSnapshot(time.Unix(0, 0), model.ToolInfo{}, model.Scope{}, reversed, nil)
	firstIDs := []string{first.Resources[0].ID, first.Resources[1].ID}
	secondIDs := []string{second.Resources[0].ID, second.Resources[1].ID}
	if !slices.Equal(firstIDs, secondIDs) {
		t.Errorf("입력 순서에 따라 snapshot 순서가 바뀜: %v != %v", firstIDs, secondIDs)
	}
}
