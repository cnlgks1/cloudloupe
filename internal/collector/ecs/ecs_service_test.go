package ecs_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ecscollector "github.com/cnlgks1/cloudloupe/internal/collector/ecs"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeServiceAPI는 서비스 수집기가 쓰는 세 메서드를 대신한다.
//
// clusters는 ListClusters가 줄 클러스터 ARN들, listByCluster는 클러스터별 ListServices의
// 서비스 ARN들, describe는 서비스 ARN별 상세다. listErr는 특정 클러스터의 ListServices만
// 실패시킨다.
type fakeServiceAPI struct {
	clusters      []string
	listByCluster map[string][]string
	listErr       map[string]error
	describe      map[string]ecstypes.Service

	describeBatches [][]string
}

func (f *fakeServiceAPI) ListClusters(
	_ context.Context,
	_ *awsecs.ListClustersInput,
	_ ...func(*awsecs.Options),
) (*awsecs.ListClustersOutput, error) {
	return &awsecs.ListClustersOutput{ClusterArns: f.clusters}, nil
}

func (f *fakeServiceAPI) ListServices(
	_ context.Context,
	in *awsecs.ListServicesInput,
	_ ...func(*awsecs.Options),
) (*awsecs.ListServicesOutput, error) {
	cluster := aws.ToString(in.Cluster)
	if err, ok := f.listErr[cluster]; ok {
		return nil, err
	}

	return &awsecs.ListServicesOutput{ServiceArns: f.listByCluster[cluster]}, nil
}

func (f *fakeServiceAPI) DescribeServices(
	_ context.Context,
	in *awsecs.DescribeServicesInput,
	_ ...func(*awsecs.Options),
) (*awsecs.DescribeServicesOutput, error) {
	// 배치 크기를 기록해 10개 상한이 지켜지는지 검증한다.
	f.describeBatches = append(f.describeBatches, append([]string(nil), in.Services...))

	out := make([]ecstypes.Service, 0, len(in.Services))
	for _, arn := range in.Services {
		if svc, ok := f.describe[arn]; ok {
			out = append(out, svc)
		}
	}

	return &awsecs.DescribeServicesOutput{Services: out}, nil
}

// TestServiceCollectorType은 타입 ID를 확인한다.
func TestServiceCollectorType(t *testing.T) {
	t.Parallel()

	if got := ecscollector.NewService(&fakeServiceAPI{}).Type(); got != model.TypeECSService {
		t.Errorf("Type() = %q, want %q", got, model.TypeECSService)
	}
}

// TestServiceCollectBuildsRelations는 서비스가 클러스터·태스크 정의·서브넷·보안그룹·타깃
// 그룹으로 이어지는 관계를 SDK 응답 필드 경로 이름으로 만드는지 확인한다.
func TestServiceCollectBuildsRelations(t *testing.T) {
	t.Parallel()

	clusterARN := "arn:aws:ecs:ap-northeast-2:123456789012:cluster/app"
	svcARN := "arn:aws:ecs:ap-northeast-2:123456789012:service/app/web"
	tdARN := "arn:aws:ecs:ap-northeast-2:123456789012:task-definition/web:7"
	tgARN := "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web/abc"

	api := &fakeServiceAPI{
		clusters:      []string{clusterARN},
		listByCluster: map[string][]string{clusterARN: {svcARN}},
		describe: map[string]ecstypes.Service{
			svcARN: {
				ServiceName:    aws.String("web"),
				ServiceArn:     aws.String(svcARN),
				ClusterArn:     aws.String(clusterARN),
				Status:         aws.String("ACTIVE"),
				LaunchType:     ecstypes.LaunchTypeFargate,
				DesiredCount:   2,
				RunningCount:   2,
				PendingCount:   0,
				TaskDefinition: aws.String(tdARN),
				NetworkConfiguration: &ecstypes.NetworkConfiguration{
					AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
						Subnets:        []string{"subnet-1", "subnet-2"},
						SecurityGroups: []string{"sg-1"},
					},
				},
				LoadBalancers: []ecstypes.LoadBalancer{{TargetGroupArn: aws.String(tgARN)}},
			},
		},
	}

	got, err := ecscollector.NewService(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("서비스 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "web" || res.ARN != svcARN {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if got, want := res.FieldValue("LaunchType"), "FARGATE"; got != want {
		t.Errorf("LaunchType = %q, want %q", got, want)
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
		{"ClusterArn", model.TypeECSCluster, clusterARN},
		{"TaskDefinition", model.TypeECSTaskDefinition, tdARN},
		{"NetworkConfiguration.AwsvpcConfiguration.Subnets", model.TypeEC2Subnet, "subnet-1"},
		{"NetworkConfiguration.AwsvpcConfiguration.Subnets", model.TypeEC2Subnet, "subnet-2"},
		{"NetworkConfiguration.AwsvpcConfiguration.SecurityGroups", model.TypeEC2SecurityGroup, "sg-1"},
		{"LoadBalancers.TargetGroupArn", model.TypeELBv2TargetGroup, tgARN},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 = %+v\nwant %+v", gotRels, want)
	}
}

// TestServiceCollectKeepsOtherClustersOnListFailure는 한 클러스터의 ListServices가 실패해도
// 다른 클러스터의 서비스는 살리는지 확인한다.
func TestServiceCollectKeepsOtherClustersOnListFailure(t *testing.T) {
	t.Parallel()

	good := "arn/cluster-good"
	bad := "arn/cluster-bad"
	svcARN := "arn/service-ok"
	denied := errors.New("access denied")

	api := &fakeServiceAPI{
		clusters:      []string{good, bad},
		listByCluster: map[string][]string{good: {svcARN}},
		listErr:       map[string]error{bad: denied},
		describe: map[string]ecstypes.Service{
			svcARN: {ServiceName: aws.String("ok"), ServiceArn: aws.String(svcARN)},
		},
	}

	got, err := ecscollector.NewService(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "ok" {
		t.Errorf("수집 결과 = %+v, want ok 하나", got)
	}
}

// TestServiceCollectBatchesDescribeByTen은 DescribeServices가 최대 10개씩 잘려 불리는지
// 확인한다. ECS API 상한을 넘기면 조회 자체가 실패한다.
func TestServiceCollectBatchesDescribeByTen(t *testing.T) {
	t.Parallel()

	clusterARN := "arn/cluster"
	arns := make([]string, 0, 23)
	describe := make(map[string]ecstypes.Service, 23)
	for i := range 23 {
		arn := "arn/service-" + string(rune('a'+i))
		arns = append(arns, arn)
		describe[arn] = ecstypes.Service{ServiceName: aws.String(arn), ServiceArn: aws.String(arn)}
	}

	api := &fakeServiceAPI{
		clusters:      []string{clusterARN},
		listByCluster: map[string][]string{clusterARN: arns},
		describe:      describe,
	}

	got, err := ecscollector.NewService(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 23 {
		t.Errorf("서비스 %d개 수집, want 23", len(got))
	}
	if len(api.describeBatches) != 3 {
		t.Fatalf("DescribeServices 호출 = %d회, want 3", len(api.describeBatches))
	}
	for i, batch := range api.describeBatches {
		if len(batch) > 10 {
			t.Errorf("배치 %d 크기 = %d, want <= 10", i, len(batch))
		}
	}
}
