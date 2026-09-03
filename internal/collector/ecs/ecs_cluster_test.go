package ecs_test

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
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ecscollector "github.com/cnlgks1/cloudloupe/internal/collector/ecs"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeClusterAPI는 클러스터 수집기가 쓰는 ListClusters·DescribeClusters를 대신한다.
//
// listPages는 ListClusters의 페이지들, listErr는 마지막 페이지 뒤에 낼 오류다.
// describe는 ARN으로 상세를, describeErr는 특정 ARN만 실패시킨다. 클러스터마다 상세를 다시
// 묻는 N+1 구조에서 "일부만 권한 없음"을 재현하려면 이 구분이 필요하다.
type fakeClusterAPI struct {
	listPages   [][]string
	listErr     error
	describe    map[string]ecstypes.Cluster
	describeErr map[string]error

	mu            sync.Mutex
	listCalls     int
	describeCalls atomic.Int32
	running       int32
	peakRunning   int32
}

func (f *fakeClusterAPI) ListClusters(
	_ context.Context,
	_ *awsecs.ListClustersInput,
	_ ...func(*awsecs.Options),
) (*awsecs.ListClustersOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if i >= len(f.listPages) {
		if f.listErr != nil {
			return nil, f.listErr
		}

		return &awsecs.ListClustersOutput{}, nil
	}

	out := &awsecs.ListClustersOutput{ClusterArns: f.listPages[i]}
	if i+1 < len(f.listPages) || f.listErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func (f *fakeClusterAPI) DescribeClusters(
	_ context.Context,
	in *awsecs.DescribeClustersInput,
	_ ...func(*awsecs.Options),
) (*awsecs.DescribeClustersOutput, error) {
	f.describeCalls.Add(1)

	running := atomic.AddInt32(&f.running, 1)
	for {
		peak := atomic.LoadInt32(&f.peakRunning)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peakRunning, peak, running) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&f.running, -1)

	arn := in.Clusters[0]
	if err, ok := f.describeErr[arn]; ok {
		return nil, err
	}

	cluster, ok := f.describe[arn]
	if !ok {
		return &awsecs.DescribeClustersOutput{}, nil
	}

	return &awsecs.DescribeClustersOutput{Clusters: []ecstypes.Cluster{cluster}}, nil
}

// testScope는 ecs 패키지 테스트가 공유하는 범위다.
func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestClusterCollectorType(t *testing.T) {
	t.Parallel()

	if got := ecscollector.NewCluster(&fakeClusterAPI{}).Type(); got != model.TypeECSCluster {
		t.Errorf("Type() = %q, want %q", got, model.TypeECSCluster)
	}
}

// TestClusterCollectConvertsFields는 SDK 값을 가공 없이 그대로 필드에 담는지 확인한다.
func TestClusterCollectConvertsFields(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:ecs:ap-northeast-2:123456789012:cluster/app"
	api := &fakeClusterAPI{
		listPages: [][]string{{arn}},
		describe: map[string]ecstypes.Cluster{
			arn: {
				ClusterName:                       aws.String("app"),
				ClusterArn:                        aws.String(arn),
				Status:                            aws.String("ACTIVE"),
				RunningTasksCount:                 3,
				ActiveServicesCount:               2,
				RegisteredContainerInstancesCount: 0,
				CapacityProviders:                 []string{"FARGATE", "FARGATE_SPOT"},
			},
		},
	}

	got, err := ecscollector.NewCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("클러스터 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "app" || res.Name != "app" {
		t.Errorf("ID/Name = %q/%q, want app/app", res.ID, res.Name)
	}
	if res.ARN != arn {
		t.Errorf("ARN = %q, want %q", res.ARN, arn)
	}
	if res.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", res.Status)
	}
	// 값은 AWS가 준 그대로. FARGATE를 번역하지 않는다.
	if got, want := res.FieldValue("CapacityProviders"), "FARGATE, FARGATE_SPOT"; got != want {
		t.Errorf("CapacityProviders = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("RunningTasksCount"), "3"; got != want {
		t.Errorf("RunningTasksCount = %q, want %q", got, want)
	}
	// 클러스터는 하위를 나열하지 않으므로 관계가 없어야 한다.
	if len(res.Related) != 0 {
		t.Errorf("Related = %+v, want 없음", res.Related)
	}
}

// TestClusterCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를 살리는지
// 확인한다.
func TestClusterCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	a, b, c := "arn/cluster-a", "arn/cluster-b", "arn/cluster-c"
	api := &fakeClusterAPI{
		listPages: [][]string{{a, b, c}},
		describe: map[string]ecstypes.Cluster{
			a: {ClusterName: aws.String("cluster-a"), ClusterArn: aws.String(a)},
			c: {ClusterName: aws.String("cluster-c"), ClusterArn: aws.String(c)},
		},
		describeErr: map[string]error{b: denied},
	}

	got, err := ecscollector.NewCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), "cluster-b") {
		t.Errorf("오류에 실패한 ARN이 없다: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"cluster-a", "cluster-c"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}

// TestClusterCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestClusterCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeClusterAPI{
		listPages: [][]string{{"arn/c1"}, {"arn/c2"}},
		describe: map[string]ecstypes.Cluster{
			"arn/c1": {ClusterName: aws.String("c1"), ClusterArn: aws.String("arn/c1")},
			"arn/c2": {ClusterName: aws.String("c2"), ClusterArn: aws.String("arn/c2")},
		},
	}

	got, err := ecscollector.NewCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("클러스터 %d개 수집, want 2", len(got))
	}
	if api.listCalls != 2 {
		t.Errorf("ListClusters 호출 = %d회, want 2", api.listCalls)
	}
}

// TestClusterCollectLimitsConcurrentDescribes는 상세 조회가 무제한으로 퍼지지 않는지
// 확인한다. 클러스터가 많은 계정에서 스로틀링을 피하려면 상한이 실제로 걸려야 한다.
func TestClusterCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	arns := make([]string, 0, 24)
	describe := make(map[string]ecstypes.Cluster, 24)
	for i := range 24 {
		arn := "arn/cluster-" + string(rune('a'+i%26))
		arns = append(arns, arn)
		describe[arn] = ecstypes.Cluster{ClusterName: aws.String(arn), ClusterArn: aws.String(arn)}
	}

	api := &fakeClusterAPI{listPages: [][]string{arns}, describe: describe}

	if _, err := ecscollector.NewCluster(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("DescribeClusters 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
}

// TestClusterCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestClusterCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeClusterAPI{listErr: context.Canceled}
	if _, err := ecscollector.NewCluster(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
