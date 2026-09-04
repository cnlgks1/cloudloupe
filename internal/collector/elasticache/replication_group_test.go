package elasticache_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	eccollector "github.com/cnlgks1/cloudloupe/internal/collector/elasticache"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeReplicationGroup은 복제 그룹 수집기가 쓰는 DescribeReplicationGroups를 대신한다.
//
// pages는 응답 페이지들, pageErr는 마지막 페이지 뒤에 낼 오류다.
type fakeReplicationGroup struct {
	pages   [][]ectypes.ReplicationGroup
	pageErr error

	calls int
}

func (f *fakeReplicationGroup) DescribeReplicationGroups(
	_ context.Context,
	_ *awsec.DescribeReplicationGroupsInput,
	_ ...func(*awsec.Options),
) (*awsec.DescribeReplicationGroupsOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awsec.DescribeReplicationGroupsOutput{}, nil
	}

	out := &awsec.DescribeReplicationGroupsOutput{ReplicationGroups: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		out.Marker = aws.String("next")
	}

	return out, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestReplicationGroupCollectorType(t *testing.T) {
	t.Parallel()

	if got := eccollector.NewReplicationGroup(&fakeReplicationGroup{}).Type(); got != model.TypeElastiCacheReplicationGroup {
		t.Errorf("Type() = %q, want %q", got, model.TypeElastiCacheReplicationGroup)
	}
}

// TestReplicationGroupCollectConvertsFieldsAndRelations는 값을 그대로 담고 KMS 키·멤버 클러스터
// 관계를 만드는지 확인한다.
func TestReplicationGroupCollectConvertsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:elasticache:ap-northeast-2:123456789012:replicationgroup:cache"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeReplicationGroup{
		pages: [][]ectypes.ReplicationGroup{{
			{
				ReplicationGroupId:       aws.String("cache"),
				ARN:                      aws.String(arn),
				Status:                   aws.String("available"),
				Description:              aws.String("세션 캐시"),
				Engine:                   aws.String("redis"),
				CacheNodeType:            aws.String("cache.r6g.large"),
				MemberClusters:           []string{"cache-001", "cache-002"},
				MultiAZ:                  ectypes.MultiAZStatusEnabled,
				AutomaticFailover:        ectypes.AutomaticFailoverStatusEnabled,
				AtRestEncryptionEnabled:  aws.Bool(true),
				TransitEncryptionEnabled: aws.Bool(true),
				KmsKeyId:                 aws.String(kmsKey),
			},
		}},
	}

	got, err := eccollector.NewReplicationGroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("복제 그룹 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "cache" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Status != "available" {
		t.Errorf("Status = %q, want available", res.Status)
	}
	// 값은 AWS가 준 그대로. redis/enabled를 번역하지 않는다.
	if got, want := res.FieldValue("Engine"), "redis"; got != want {
		t.Errorf("Engine = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("MemberClusters"), "cache-001, cache-002"; got != want {
		t.Errorf("MemberClusters = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("AtRestEncryptionEnabled"), "true"; got != want {
		t.Errorf("AtRestEncryptionEnabled = %q, want %q", got, want)
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
		{"KmsKeyId", model.TypeKMSKey, kmsKey},
		{"MemberClusters", model.TypeElastiCacheCacheCluster, "cache-001"},
		{"MemberClusters", model.TypeElastiCacheCacheCluster, "cache-002"},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 =\n  %+v\nwant\n  %+v", gotRels, want)
	}
}

// TestReplicationGroupCollectFollowsPages는 페이지네이션을 이어 받는지 확인한다.
func TestReplicationGroupCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeReplicationGroup{
		pages: [][]ectypes.ReplicationGroup{
			{{ReplicationGroupId: aws.String("a")}},
			{{ReplicationGroupId: aws.String("b")}},
		},
	}

	got, err := eccollector.NewReplicationGroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
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
		t.Errorf("DescribeReplicationGroups 호출 = %d회, want 2", api.calls)
	}
}

// TestReplicationGroupCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를
// 살리는지 확인한다.
func TestReplicationGroupCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeReplicationGroup{
		pages:   [][]ectypes.ReplicationGroup{{{ReplicationGroupId: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := eccollector.NewReplicationGroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
