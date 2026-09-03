package acm_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsacm "github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	acmcollector "github.com/cnlgks1/cloudloupe/internal/collector/acm"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeACM은 인증서 수집기가 쓰는 ListCertificates·DescribeCertificate를 대신한다.
//
// listPages는 ListCertificates의 페이지들, listErr는 마지막 페이지 뒤에 낼 오류다. describe는
// ARN으로 상세를, describeErr는 특정 ARN만 실패시킨다.
type fakeACM struct {
	listPages   [][]string
	listErr     error
	describe    map[string]acmtypes.CertificateDetail
	describeErr map[string]error

	mu          sync.Mutex
	listCalls   int
	running     int32
	peakRunning int32
}

func (f *fakeACM) ListCertificates(
	_ context.Context,
	_ *awsacm.ListCertificatesInput,
	_ ...func(*awsacm.Options),
) (*awsacm.ListCertificatesOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if i >= len(f.listPages) {
		if f.listErr != nil {
			return nil, f.listErr
		}

		return &awsacm.ListCertificatesOutput{}, nil
	}

	summaries := make([]acmtypes.CertificateSummary, 0, len(f.listPages[i]))
	for _, arn := range f.listPages[i] {
		summaries = append(summaries, acmtypes.CertificateSummary{CertificateArn: aws.String(arn)})
	}

	out := &awsacm.ListCertificatesOutput{CertificateSummaryList: summaries}
	if i+1 < len(f.listPages) || f.listErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func (f *fakeACM) DescribeCertificate(
	_ context.Context,
	in *awsacm.DescribeCertificateInput,
	_ ...func(*awsacm.Options),
) (*awsacm.DescribeCertificateOutput, error) {
	running := atomic.AddInt32(&f.running, 1)
	for {
		peak := atomic.LoadInt32(&f.peakRunning)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peakRunning, peak, running) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&f.running, -1)

	arn := aws.ToString(in.CertificateArn)
	if err, ok := f.describeErr[arn]; ok {
		return nil, err
	}

	cert, ok := f.describe[arn]
	if !ok {
		return &awsacm.DescribeCertificateOutput{}, nil
	}

	return &awsacm.DescribeCertificateOutput{Certificate: &cert}, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestCertificateCollectorType(t *testing.T) {
	t.Parallel()

	if got := acmcollector.NewCertificate(&fakeACM{}).Type(); got != model.TypeACMCertificate {
		t.Errorf("Type() = %q, want %q", got, model.TypeACMCertificate)
	}
}

// TestCertificateCollectConvertsFields는 SDK 값을 그대로 담고 만료일·사용처를 표기하는지
// 확인한다.
func TestCertificateCollectConvertsFields(t *testing.T) {
	t.Parallel()

	issued := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	notAfter := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	arn := "arn:aws:acm:ap-northeast-2:123456789012:certificate/abc"
	lbARN := "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:loadbalancer/app/web/abc"

	api := &fakeACM{
		listPages: [][]string{{arn}},
		describe: map[string]acmtypes.CertificateDetail{
			arn: {
				CertificateArn:          aws.String(arn),
				DomainName:              aws.String("example.com"),
				Status:                  acmtypes.CertificateStatusIssued,
				Type:                    acmtypes.CertificateTypeAmazonIssued,
				KeyAlgorithm:            acmtypes.KeyAlgorithmRsa2048,
				SubjectAlternativeNames: []string{"example.com", "www.example.com"},
				IssuedAt:                &issued,
				NotAfter:                &notAfter,
				RenewalEligibility:      acmtypes.RenewalEligibilityEligible,
				Issuer:                  aws.String("Amazon"),
				InUseBy:                 []string{lbARN},
			},
		},
	}

	got, err := acmcollector.NewCertificate(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("인증서 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "example.com" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Status != "ISSUED" {
		t.Errorf("Status = %q, want ISSUED", res.Status)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(issued) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, issued)
	}
	if got, want := res.FieldValue("NotAfter"), "2026-01-02T03:04:05Z"; got != want {
		t.Errorf("NotAfter = %q, want %q", got, want)
	}
	// 값은 AWS가 준 그대로. AMAZON_ISSUED/RSA_2048을 번역하지 않는다.
	if got, want := res.FieldValue("Type"), "AMAZON_ISSUED"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("SubjectAlternativeNames"), "example.com, www.example.com"; got != want {
		t.Errorf("SAN = %q, want %q", got, want)
	}
	if got := res.FieldValue("InUseBy"); !strings.Contains(got, lbARN) || !strings.HasPrefix(got, "1:") {
		t.Errorf("InUseBy = %q, want 개수와 ARN 포함", got)
	}
	// InUseBy는 종류가 섞여 관계로 만들지 않는다.
	if len(res.Related) != 0 {
		t.Errorf("Related = %+v, want 없음", res.Related)
	}
}

// TestCertificateCollectMarksUnused는 어디에도 안 붙은 인증서의 InUseBy가 "-"인지 확인한다.
// 미사용 인증서를 눈에 띄게 하려는 것이다.
func TestCertificateCollectMarksUnused(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:acm:x:1:certificate/unused"
	api := &fakeACM{
		listPages: [][]string{{arn}},
		describe: map[string]acmtypes.CertificateDetail{
			arn: {CertificateArn: aws.String(arn), DomainName: aws.String("old.example.com")},
		},
	}

	got, err := acmcollector.NewCertificate(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if v := got[0].FieldValue("InUseBy"); v != "-" {
		t.Errorf("미사용 InUseBy = %q, want -", v)
	}
	if v := got[0].FieldValue("NotAfter"); v != "-" {
		t.Errorf("NotAfter 없음 = %q, want -", v)
	}
}

// TestCertificateCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를 살리는지
// 확인한다.
func TestCertificateCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	a, b, c := "arn:x:1:a", "arn:x:1:b", "arn:x:1:c"
	api := &fakeACM{
		listPages: [][]string{{a, b, c}},
		describe: map[string]acmtypes.CertificateDetail{
			a: {CertificateArn: aws.String(a), DomainName: aws.String("a.com")},
			c: {CertificateArn: aws.String(c), DomainName: aws.String("c.com")},
		},
		describeErr: map[string]error{b: denied},
	}

	got, err := acmcollector.NewCertificate(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), ":b") {
		t.Errorf("오류에 실패한 ARN이 없다: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a.com", "c.com"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}

// TestCertificateCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestCertificateCollectFollowsPages(t *testing.T) {
	t.Parallel()

	a, b := "arn:x:1:c1", "arn:x:1:c2"
	api := &fakeACM{
		listPages: [][]string{{a}, {b}},
		describe: map[string]acmtypes.CertificateDetail{
			a: {CertificateArn: aws.String(a), DomainName: aws.String("a.com")},
			b: {CertificateArn: aws.String(b), DomainName: aws.String("b.com")},
		},
	}

	got, err := acmcollector.NewCertificate(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("인증서 %d개 수집, want 2", len(got))
	}
	if api.listCalls != 2 {
		t.Errorf("ListCertificates 호출 = %d회, want 2", api.listCalls)
	}
}

// TestCertificateCollectLimitsConcurrentDescribes는 상세 조회가 무제한으로 퍼지지 않는지
// 확인한다.
func TestCertificateCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	arns := make([]string, 0, 24)
	describe := make(map[string]acmtypes.CertificateDetail, 24)
	for i := range 24 {
		arn := "arn:x:1:cert-" + string(rune('a'+i%26))
		arns = append(arns, arn)
		describe[arn] = acmtypes.CertificateDetail{CertificateArn: aws.String(arn), DomainName: aws.String(arn)}
	}

	api := &fakeACM{listPages: [][]string{arns}, describe: describe}

	if _, err := acmcollector.NewCertificate(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("DescribeCertificate 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
}

// TestCertificateCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestCertificateCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeACM{listErr: context.Canceled}
	if _, err := acmcollector.NewCertificate(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
