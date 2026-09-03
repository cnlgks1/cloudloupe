package eks_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ekscollector "github.com/cnlgks1/cloudloupe/internal/collector/eks"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeNodegroupAPI는 노드그룹 수집기가 쓰는 세 메서드를 대신한다.
//
// clusters는 ListClusters가 줄 클러스터 이름들, listByCluster는 클러스터별 ListNodegroups의
// 노드그룹 이름들, describe는 (클러스터/노드그룹) 키로 상세다. listErr는 특정 클러스터의
// ListNodegroups만 실패시킨다.
type fakeNodegroupAPI struct {
	clusters      []string
	listByCluster map[string][]string
	listErr       map[string]error
	describe      map[string]ekstypes.Nodegroup
}

func (f *fakeNodegroupAPI) ListClusters(
	_ context.Context,
	_ *awseks.ListClustersInput,
	_ ...func(*awseks.Options),
) (*awseks.ListClustersOutput, error) {
	return &awseks.ListClustersOutput{Clusters: f.clusters}, nil
}

func (f *fakeNodegroupAPI) ListNodegroups(
	_ context.Context,
	in *awseks.ListNodegroupsInput,
	_ ...func(*awseks.Options),
) (*awseks.ListNodegroupsOutput, error) {
	cluster := aws.ToString(in.ClusterName)
	if err, ok := f.listErr[cluster]; ok {
		return nil, err
	}

	return &awseks.ListNodegroupsOutput{Nodegroups: f.listByCluster[cluster]}, nil
}

func (f *fakeNodegroupAPI) DescribeNodegroup(
	_ context.Context,
	in *awseks.DescribeNodegroupInput,
	_ ...func(*awseks.Options),
) (*awseks.DescribeNodegroupOutput, error) {
	key := aws.ToString(in.ClusterName) + "/" + aws.ToString(in.NodegroupName)
	ng, ok := f.describe[key]
	if !ok {
		return &awseks.DescribeNodegroupOutput{}, nil
	}

	return &awseks.DescribeNodegroupOutput{Nodegroup: &ng}, nil
}

// TestNodegroupCollectorType은 타입 ID를 확인한다.
func TestNodegroupCollectorType(t *testing.T) {
	t.Parallel()

	if got := ekscollector.NewNodegroup(&fakeNodegroupAPI{}).Type(); got != model.TypeEKSNodegroup {
		t.Errorf("Type() = %q, want %q", got, model.TypeEKSNodegroup)
	}
}

// TestNodegroupCollectBuildsFieldsAndRelations는 노드그룹이 클러스터·노드역할·서브넷·
// Auto Scaling Group으로 이어지는 관계를 만들고 스케일링 값을 그대로 담는지 확인한다.
func TestNodegroupCollectBuildsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	nodeRole := "arn:aws:iam::123456789012:role/eksNodeRole"
	api := &fakeNodegroupAPI{
		clusters:      []string{"app"},
		listByCluster: map[string][]string{"app": {"ng-1"}},
		describe: map[string]ekstypes.Nodegroup{
			"app/ng-1": {
				NodegroupName: aws.String("ng-1"),
				NodegroupArn:  aws.String("arn:aws:eks:ap-northeast-2:123456789012:nodegroup/app/ng-1/abc"),
				ClusterName:   aws.String("app"),
				Status:        ekstypes.NodegroupStatusActive,
				InstanceTypes: []string{"t3.medium"},
				AmiType:       ekstypes.AMITypesAl2X8664,
				CapacityType:  ekstypes.CapacityTypesOnDemand,
				NodeRole:      aws.String(nodeRole),
				Subnets:       []string{"subnet-1", "subnet-2"},
				Version:       aws.String("1.29"),
				DiskSize:      aws.Int32(20),
				ScalingConfig: &ekstypes.NodegroupScalingConfig{
					DesiredSize: aws.Int32(2),
					MinSize:     aws.Int32(1),
					MaxSize:     aws.Int32(4),
				},
				Resources: &ekstypes.NodegroupResources{
					AutoScalingGroups: []ekstypes.AutoScalingGroup{{Name: aws.String("eks-ng-1-asg")}},
				},
			},
		},
	}

	got, err := ekscollector.NewNodegroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("노드그룹 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "ng-1" {
		t.Errorf("ID = %q, want ng-1", res.ID)
	}
	// 값은 AWS가 준 그대로. AL2_x86_64/ON_DEMAND를 번역하지 않는다.
	if got, want := res.FieldValue("AmiType"), "AL2_x86_64"; got != want {
		t.Errorf("AmiType = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("CapacityType"), "ON_DEMAND"; got != want {
		t.Errorf("CapacityType = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("DesiredSize"), "2"; got != want {
		t.Errorf("DesiredSize = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("DiskSize"), "20"; got != want {
		t.Errorf("DiskSize = %q, want %q", got, want)
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
		{"ClusterName", model.TypeEKSCluster, "app"},
		{"NodeRole", model.TypeIAMRole, nodeRole},
		{"Subnets", model.TypeEC2Subnet, "subnet-1"},
		{"Subnets", model.TypeEC2Subnet, "subnet-2"},
		{"Resources.AutoScalingGroups.Name", model.TypeAutoScalingGroup, "eks-ng-1-asg"},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 =\n  %+v\nwant\n  %+v", gotRels, want)
	}
}

// TestNodegroupCollectDistinguishesMissingScaling은 스케일링 설정이 없으면 크기를 "-"로
// 두는지 확인한다. 0과 미설정은 다르다.
func TestNodegroupCollectDistinguishesMissingScaling(t *testing.T) {
	t.Parallel()

	api := &fakeNodegroupAPI{
		clusters:      []string{"app"},
		listByCluster: map[string][]string{"app": {"ng-1"}},
		describe: map[string]ekstypes.Nodegroup{
			"app/ng-1": {NodegroupName: aws.String("ng-1"), ClusterName: aws.String("app")},
		},
	}

	got, err := ekscollector.NewNodegroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if v := got[0].FieldValue("DesiredSize"); v != "-" {
		t.Errorf("스케일링 없는 DesiredSize = %q, want -", v)
	}
	if v := got[0].FieldValue("DiskSize"); v != "-" {
		t.Errorf("DiskSize 없음 = %q, want -", v)
	}
}

// TestNodegroupCollectKeepsOtherClustersOnListFailure는 한 클러스터의 ListNodegroups가
// 실패해도 다른 클러스터의 노드그룹은 살리는지 확인한다.
func TestNodegroupCollectKeepsOtherClustersOnListFailure(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeNodegroupAPI{
		clusters:      []string{"good", "bad"},
		listByCluster: map[string][]string{"good": {"ng-ok"}},
		listErr:       map[string]error{"bad": denied},
		describe: map[string]ekstypes.Nodegroup{
			"good/ng-ok": {NodegroupName: aws.String("ng-ok"), ClusterName: aws.String("good")},
		},
	}

	got, err := ekscollector.NewNodegroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "ng-ok" {
		t.Errorf("수집 결과 = %+v, want ng-ok 하나", got)
	}
}
