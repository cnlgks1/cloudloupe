package s3_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	s3collector "github.com/cnlgks1/cloudloupe/internal/collector/s3"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeListBuckets는 ListBuckets를 대신한다. 테스트는 실제 AWS를 때리지 않는다.
//
// regions에는 요청이 넘긴 BucketRegion을 기록한다. 리전 필터를 실제로 거는지가 이 수집기의
// 핵심 계약이므로 값으로 확인한다.
type fakeListBuckets struct {
	pages   []*awss3.ListBucketsOutput
	errs    []error
	calls   int
	regions []string
	tokens  []string
}

func (f *fakeListBuckets) ListBuckets(
	_ context.Context,
	in *awss3.ListBucketsInput,
	_ ...func(*awss3.Options),
) (*awss3.ListBucketsOutput, error) {
	i := f.calls
	f.calls++
	f.regions = append(f.regions, aws.ToString(in.BucketRegion))
	f.tokens = append(f.tokens, aws.ToString(in.ContinuationToken))

	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i >= len(f.pages) {
		return &awss3.ListBucketsOutput{}, nil
	}

	return f.pages[i], nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestBucketCollectorType(t *testing.T) {
	t.Parallel()

	if got := s3collector.NewBucket(&fakeListBuckets{}).Type(); got != model.TypeS3Bucket {
		t.Errorf("Type() = %q, want %q", got, model.TypeS3Bucket)
	}
}

// TestBucketCollectFiltersByScopeRegion은 조회에 범위 리전 필터를 거는지 확인한다.
// 필터가 없으면 리전을 여러 개 고른 조회에서 같은 버킷이 리전마다 중복 수집된다.
func TestBucketCollectFiltersByScopeRegion(t *testing.T) {
	t.Parallel()

	created := time.Date(2024, 7, 8, 9, 10, 11, 0, time.UTC)
	api := &fakeListBuckets{pages: []*awss3.ListBucketsOutput{{Buckets: []s3types.Bucket{{
		Name:         aws.String("app-core-logs"),
		BucketRegion: aws.String("ap-northeast-2"),
		CreationDate: &created,
	}}}}}

	got, err := s3collector.NewBucket(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(api.regions) == 0 || api.regions[0] != "ap-northeast-2" {
		t.Errorf("BucketRegion = %v, want ap-northeast-2", api.regions)
	}
	if len(got) != 1 {
		t.Fatalf("버킷 %d개 수집, want 1", len(got))
	}

	bucket := got[0]
	if bucket.ID != "app-core-logs" || bucket.Name != "app-core-logs" {
		t.Errorf("ID/Name = %q/%q, want app-core-logs", bucket.ID, bucket.Name)
	}
	if bucket.Region != "ap-northeast-2" {
		t.Errorf("Region = %q, want ap-northeast-2", bucket.Region)
	}
	if bucket.CreatedAt == nil || !bucket.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", bucket.CreatedAt, created)
	}
	if got, want := bucket.FieldValue("CreationDate"), "2024-07-08T09:10:11Z"; got != want {
		t.Errorf("생성일 = %q, want %q", got, want)
	}
	// 일반 버킷은 응답에 ARN이 없다. 이름으로 합성하지 않는다.
	if bucket.ARN != "" {
		t.Errorf("ARN = %q, want 빈 문자열", bucket.ARN)
	}
}

// TestBucketCollectFallsBackToScopeRegion은 응답에 리전이 없을 때 범위 리전을 쓰는지 확인한다.
func TestBucketCollectFallsBackToScopeRegion(t *testing.T) {
	t.Parallel()

	api := &fakeListBuckets{pages: []*awss3.ListBucketsOutput{{Buckets: []s3types.Bucket{{
		Name: aws.String("no-region"),
	}}}}}

	got, err := s3collector.NewBucket(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if got[0].Region != "ap-northeast-2" {
		t.Errorf("Region = %q, want 범위 리전", got[0].Region)
	}
	if got, want := got[0].FieldValue("CreationDate"), "-"; got != want {
		t.Errorf("생성일 = %q, want %q", got, want)
	}
}

// TestBucketCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestBucketCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeListBuckets{pages: []*awss3.ListBucketsOutput{
		{
			Buckets:           []s3types.Bucket{{Name: aws.String("first")}},
			ContinuationToken: aws.String("page-2"),
		},
		{
			Buckets: []s3types.Bucket{{Name: aws.String("second")}},
		},
	}}

	got, err := s3collector.NewBucket(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, bucket := range got {
		names = append(names, bucket.ID)
	}
	if want := []string{"first", "second"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
	if api.calls != 2 {
		t.Errorf("ListBuckets 호출 = %d회, want 2", api.calls)
	}
	if len(api.tokens) < 2 || api.tokens[1] != "page-2" {
		t.Errorf("두 번째 요청 토큰 = %v, want page-2", api.tokens)
	}
}

// TestBucketCollectKeepsPartialResults는 뒤 페이지가 실패해도 앞 페이지를 버리지 않는지
// 확인한다. 부분 실패는 전체 실패가 아니다.
func TestBucketCollectKeepsPartialResults(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeListBuckets{
		pages: []*awss3.ListBucketsOutput{
			{
				Buckets:           []s3types.Bucket{{Name: aws.String("first")}},
				ContinuationToken: aws.String("page-2"),
			},
			nil,
		},
		errs: []error{nil, denied},
	}

	got, err := s3collector.NewBucket(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "first" {
		t.Errorf("실패 전 수집분이 남아야 한다: %+v", got)
	}
	// 오류 문맥에 리전이 있어야 어느 리전이 막혔는지 화면에서 구분된다.
	if !strings.Contains(err.Error(), "ap-northeast-2") {
		t.Errorf("오류에 리전 문맥이 없다: %v", err)
	}
}

// TestBucketCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestBucketCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeListBuckets{errs: []error{context.Canceled}}
	if _, err := s3collector.NewBucket(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
