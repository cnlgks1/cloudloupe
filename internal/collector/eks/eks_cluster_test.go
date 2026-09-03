package eks_test

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
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ekscollector "github.com/cnlgks1/cloudloupe/internal/collector/eks"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeClusterAPI는 클러스터 수집기가 쓰는 ListClusters·DescribeCluster를 대신한다.
//
// listPages는 ListClusters의 페이지들, listErr는 마지막 페이지 뒤에 낼 오류다. describe는
// 이름으로 상세를, describeErr는 특정 이름만 실패시킨다.
type fakeClusterAPI struct {
	listPages   [][]string
	listErr     error
	describe    map[string]ekstypes.Cluster
	describeErr map[string]error

	mu            sync.Mutex
	listCalls     int
	describeCalls atomic.Int32
	running       int32
	peakRunning   int32
}

func (f *fakeClusterAPI) ListClusters(
	_ context.Context,
	_ *awseks.ListClustersInput,
	_ ...func(*awseks.Options),
) (*awseks.ListClustersOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if i >= len(f.listPages) {
		if f.listErr != nil {
			return nil, f.listErr
		}

		return &awseks.ListClustersOutput{}, nil
	}

	out := &awseks.ListClustersOutput{Clusters: f.listPages[i]}
	if i+1 < len(f.listPages) || f.listErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func (f *fakeClusterAPI) DescribeCluster(
	_ context.Context,
	in *awseks.DescribeClusterInput,
	_ ...func(*awseks.Options),
) (*awseks.DescribeClusterOutput, error) {
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

	name := aws.ToString(in.Name)
	if err, ok := f.describeErr[name]; ok {
		return nil, err
	}

	cluster, ok := f.describe[name]
	if !ok {
		return &awseks.DescribeClusterOutput{}, nil
	}

	return &awseks.DescribeClusterOutput{Cluster: &cluster}, nil
}

// testScope는 eks 패키지 테스트가 공유하는 범위다.
func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestClusterCollectorType(t *testing.T) {
	t.Parallel()

	if got := ekscollector.NewCluster(&fakeClusterAPI{}).Type(); got != model.TypeEKSCluster {
		t.Errorf("Type() = %q, want %q", got, model.TypeEKSCluster)
	}
}

// TestClusterCollectBuildsFieldsAndRelations는 SDK 값을 그대로 담고 IAM·서브넷·보안그룹·KMS
// 관계를 SDK 응답 필드 경로 이름으로 만드는지 확인한다.
func TestClusterCollectBuildsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	arn := "arn:aws:eks:ap-northeast-2:123456789012:cluster/app"
	role := "arn:aws:iam::123456789012:role/eksClusterRole"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeClusterAPI{
		listPages: [][]string{{"app"}},
		describe: map[string]ekstypes.Cluster{
			"app": {
				Name:            aws.String("app"),
				Arn:             aws.String(arn),
				Status:          ekstypes.ClusterStatusActive,
				Version:         aws.String("1.29"),
				PlatformVersion: aws.String("eks.5"),
				Endpoint:        aws.String("https://abc.gr7.ap-northeast-2.eks.amazonaws.com"),
				RoleArn:         aws.String(role),
				CreatedAt:       &created,
				ResourcesVpcConfig: &ekstypes.VpcConfigResponse{
					VpcId:                  aws.String("vpc-1"),
					SubnetIds:              []string{"subnet-1", "subnet-2"},
					SecurityGroupIds:       []string{"sg-1"},
					ClusterSecurityGroupId: aws.String("sg-cluster"),
					EndpointPublicAccess:   true,
					EndpointPrivateAccess:  false,
				},
				EncryptionConfig: []ekstypes.EncryptionConfig{
					{Provider: &ekstypes.Provider{KeyArn: aws.String(kmsKey)}},
				},
			},
		},
	}

	got, err := ekscollector.NewCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("클러스터 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "app" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", res.Status)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	if got, want := res.FieldValue("Version"), "1.29"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("EndpointPublicAccess"), "true"; got != want {
		t.Errorf("EndpointPublicAccess = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("EndpointPrivateAccess"), "false"; got != want {
		t.Errorf("EndpointPrivateAccess = %q, want %q", got, want)
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
		{"RoleArn", model.TypeIAMRole, role},
		{"ResourcesVpcConfig.SubnetIds", model.TypeEC2Subnet, "subnet-1"},
		{"ResourcesVpcConfig.SubnetIds", model.TypeEC2Subnet, "subnet-2"},
		{"ResourcesVpcConfig.SecurityGroupIds", model.TypeEC2SecurityGroup, "sg-1"},
		{"ResourcesVpcConfig.ClusterSecurityGroupId", model.TypeEC2SecurityGroup, "sg-cluster"},
		{"EncryptionConfig.Provider.KeyArn", model.TypeKMSKey, kmsKey},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 =\n  %+v\nwant\n  %+v", gotRels, want)
	}
}

// TestClusterCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를 살리는지
// 확인한다.
func TestClusterCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeClusterAPI{
		listPages: [][]string{{"a", "b", "c"}},
		describe: map[string]ekstypes.Cluster{
			"a": {Name: aws.String("a"), Arn: aws.String("arn/a")},
			"c": {Name: aws.String("c"), Arn: aws.String("arn/c")},
		},
		describeErr: map[string]error{"b": denied},
	}

	got, err := ekscollector.NewCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), "b") {
		t.Errorf("오류에 실패한 이름이 없다: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a", "c"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}

// TestClusterCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestClusterCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeClusterAPI{
		listPages: [][]string{{"c1"}, {"c2"}},
		describe: map[string]ekstypes.Cluster{
			"c1": {Name: aws.String("c1")},
			"c2": {Name: aws.String("c2")},
		},
	}

	got, err := ekscollector.NewCluster(api).Collect(context.Background(), collect.Request{Scope: testScope()})
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
// 확인한다.
func TestClusterCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	names := make([]string, 0, 24)
	describe := make(map[string]ekstypes.Cluster, 24)
	for i := range 24 {
		name := "cluster-" + string(rune('a'+i%26))
		names = append(names, name)
		describe[name] = ekstypes.Cluster{Name: aws.String(name)}
	}

	api := &fakeClusterAPI{listPages: [][]string{names}, describe: describe}

	if _, err := ekscollector.NewCluster(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("DescribeCluster 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
}

// TestClusterCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestClusterCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeClusterAPI{listErr: context.Canceled}
	if _, err := ekscollector.NewCluster(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
