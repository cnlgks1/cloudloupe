package cloudfront_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscf "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	cfcollector "github.com/cnlgks1/cloudloupe/internal/collector/cloudfront"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeCloudFront는 배포 수집기가 쓰는 ListDistributions를 대신한다.
//
// pages는 응답 페이지들(각 페이지의 DistributionList.Items), pageErr는 마지막 페이지 뒤에
// 낼 오류다.
type fakeCloudFront struct {
	pages   [][]cftypes.DistributionSummary
	pageErr error

	calls int
}

func (f *fakeCloudFront) ListDistributions(
	_ context.Context,
	_ *awscf.ListDistributionsInput,
	_ ...func(*awscf.Options),
) (*awscf.ListDistributionsOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awscf.ListDistributionsOutput{DistributionList: &cftypes.DistributionList{}}, nil
	}

	list := &cftypes.DistributionList{Items: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		list.IsTruncated = aws.Bool(true)
		list.NextMarker = aws.String("next")
	}

	return &awscf.ListDistributionsOutput{DistributionList: list}, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "us-east-1", AccountID: "123456789012"}
}

func TestDistributionCollectorType(t *testing.T) {
	t.Parallel()

	if got := cfcollector.NewDistribution(&fakeCloudFront{}).Type(); got != model.TypeCloudFrontDistribution {
		t.Errorf("Type() = %q, want %q", got, model.TypeCloudFrontDistribution)
	}
}

// TestDistributionCollectConvertsFieldsAndRelations는 값을 그대로 담고 글로벌 리전으로
// 고정하며 ACM 인증서·WebACL 관계를 만드는지 확인한다.
func TestDistributionCollectConvertsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:cloudfront::123456789012:distribution/E123"
	acmARN := "arn:aws:acm:us-east-1:123456789012:certificate/abc"
	wafARN := "arn:aws:wafv2:us-east-1:123456789012:global/webacl/site/abc"

	api := &fakeCloudFront{
		pages: [][]cftypes.DistributionSummary{{
			{
				Id:          aws.String("E123"),
				ARN:         aws.String(arn),
				DomainName:  aws.String("d111.cloudfront.net"),
				Status:      aws.String("Deployed"),
				Enabled:     aws.Bool(true),
				Comment:     aws.String("사이트 배포"),
				PriceClass:  cftypes.PriceClassPriceClassAll,
				HttpVersion: cftypes.HttpVersionHttp2,
				WebACLId:    aws.String(wafARN),
				Aliases:     &cftypes.Aliases{Items: []string{"www.example.com", "example.com"}},
				Origins: &cftypes.Origins{Items: []cftypes.Origin{
					{Id: aws.String("s3"), DomainName: aws.String("assets.s3.amazonaws.com")},
				}},
				ViewerCertificate: &cftypes.ViewerCertificate{ACMCertificateArn: aws.String(acmARN)},
			},
		}},
	}

	got, err := cfcollector.NewDistribution(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("배포 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "E123" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Name != "d111.cloudfront.net" {
		t.Errorf("Name = %q, want d111.cloudfront.net", res.Name)
	}
	// 글로벌 서비스이므로 선택한 리전이 아니라 global로 고정된다.
	if res.Region != model.RegionGlobal {
		t.Errorf("Region = %q, want %q", res.Region, model.RegionGlobal)
	}
	// 값은 AWS가 준 그대로. PriceClass_All/http2를 번역하지 않는다.
	if got, want := res.FieldValue("PriceClass"), "PriceClass_All"; got != want {
		t.Errorf("PriceClass = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("Aliases"), "www.example.com, example.com"; got != want {
		t.Errorf("Aliases = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("Origins"), "assets.s3.amazonaws.com"; got != want {
		t.Errorf("Origins = %q, want %q", got, want)
	}

	type rel struct {
		relation string
		typ      string
		id       string
	}
	gotRels := make([]rel, 0, len(res.Related))
	for _, r := range res.Related {
		gotRels = append(gotRels, rel{r.Relation, r.Type, r.ID})
	}
	want := []rel{
		{"ViewerCertificate.ACMCertificateArn", model.TypeACMCertificate, acmARN},
		{"WebACLId", model.TypeWAFv2WebACL, wafARN},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 = %+v, want %+v", gotRels, want)
	}
}

// TestDistributionCollectWithDefaultCertHasNoACMRelation은 CloudFront 기본 인증서면 ACM
// 관계를 만들지 않는지 확인한다.
func TestDistributionCollectWithDefaultCertHasNoACMRelation(t *testing.T) {
	t.Parallel()

	api := &fakeCloudFront{
		pages: [][]cftypes.DistributionSummary{{
			{
				Id:                aws.String("E1"),
				DomainName:        aws.String("d1.cloudfront.net"),
				ViewerCertificate: &cftypes.ViewerCertificate{CloudFrontDefaultCertificate: aws.Bool(true)},
			},
		}},
	}

	got, err := cfcollector.NewDistribution(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got[0].Related) != 0 {
		t.Errorf("기본 인증서면 관계 없음, got %+v", got[0].Related)
	}
}

// TestDistributionCollectFollowsPages는 페이지네이션을 이어 받는지 확인한다.
func TestDistributionCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeCloudFront{
		pages: [][]cftypes.DistributionSummary{
			{{Id: aws.String("a")}},
			{{Id: aws.String("b")}},
		},
	}

	got, err := cfcollector.NewDistribution(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, res := range got {
		ids = append(ids, res.ID)
	}
	if want := []string{"a", "b"}; !slices.Equal(ids, want) {
		t.Errorf("수집 결과 = %v, want %v", ids, want)
	}
	if api.calls != 2 {
		t.Errorf("ListDistributions 호출 = %d회, want 2", api.calls)
	}
}

// TestDistributionCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를
// 살리는지 확인한다.
func TestDistributionCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeCloudFront{
		pages:   [][]cftypes.DistributionSummary{{{Id: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := cfcollector.NewDistribution(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
