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

// fakeCacheCluster는 캐시 클러스터 수집기가 쓰는 DescribeCacheClusters를 대신한다.
type fakeCacheCluster struct {
	pages   [][]ectypes.CacheCluster
	pageErr error

	calls int
}

func (f *fakeCacheCluster) DescribeCacheClusters(
	_ context.Context,
	_ *awsec.DescribeCacheClustersInput,
	_ ...func(*awsec.Options),
) (*awsec.DescribeCacheClustersOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awsec.DescribeCacheClustersOutput{}, nil
	}

	out := &awsec.DescribeCacheClustersOutput{CacheClusters: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		out.Marker = aws.String("next")
	}

	return out, nil
}

func TestCacheClusterCollectorType(t *testing.T) {
	t.Parallel()

	if got := eccollector.NewCacheCluster(&fakeCacheCluster{}).Type(); got != model.TypeElastiCacheCacheCluster {
		t.Errorf("Type() = %q, want %q", got, model.TypeElastiCacheCacheCluster)
	}
}

// TestCacheClusterCollectConvertsFieldsAndRelations는 값을 그대로 담고 복제 그룹·보안 그룹
// 관계를 만드는지 확인한다.
func TestCacheClusterCollectConvertsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:elasticache:ap-northeast-2:123456789012:cluster:cache-001"
	api := &fakeCacheCluster{
		pages: [][]ectypes.CacheCluster{{
			{
				CacheClusterId:          aws.String("cache-001"),
				ARN:                     aws.String(arn),
				CacheClusterStatus:      aws.String("available"),
				Engine:                  aws.String("redis"),
				EngineVersion:           aws.String("7.1"),
				CacheNodeType:           aws.String("cache.r6g.large"),
				NumCacheNodes:           aws.Int32(1),
				ReplicationGroupId:      aws.String("cache"),
				CacheSubnetGroupName:    aws.String("default"),
				AtRestEncryptionEnabled: aws.Bool(true),
				SecurityGroups: []ectypes.SecurityGroupMembership{
					{SecurityGroupId: aws.String("sg-1"), Status: aws.String("active")},
					{SecurityGroupId: aws.String("sg-2"), Status: aws.String("active")},
				},
			},
		}},
	}

	got, err := eccollector.NewCacheCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("캐시 클러스터 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "cache-001" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if got, want := res.FieldValue("EngineVersion"), "7.1"; got != want {
		t.Errorf("EngineVersion = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("NumCacheNodes"), "1"; got != want {
		t.Errorf("NumCacheNodes = %q, want %q", got, want)
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
		{"ReplicationGroupId", model.TypeElastiCacheReplicationGroup, "cache"},
		{"SecurityGroups.SecurityGroupId", model.TypeEC2SecurityGroup, "sg-1"},
		{"SecurityGroups.SecurityGroupId", model.TypeEC2SecurityGroup, "sg-2"},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 =\n  %+v\nwant\n  %+v", gotRels, want)
	}
}

// TestCacheClusterCollectStandaloneHasNoReplicationGroup은 복제 그룹에 안 속한 클러스터가
// 그 관계를 만들지 않는지 확인한다.
func TestCacheClusterCollectStandaloneHasNoReplicationGroup(t *testing.T) {
	t.Parallel()

	api := &fakeCacheCluster{
		pages: [][]ectypes.CacheCluster{{
			{CacheClusterId: aws.String("memcached-1"), Engine: aws.String("memcached")},
		}},
	}

	got, err := eccollector.NewCacheCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	for _, r := range got[0].Related {
		if r.Relation == "ReplicationGroupId" {
			t.Errorf("독립 클러스터에 복제 그룹 관계가 생겼다: %+v", r)
		}
	}
	if v := got[0].FieldValue("NumCacheNodes"); v != "-" {
		t.Errorf("NumCacheNodes 없음 = %q, want -", v)
	}
}

// TestCacheClusterCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestCacheClusterCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeCacheCluster{
		pages:   [][]ectypes.CacheCluster{{{CacheClusterId: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := eccollector.NewCacheCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
