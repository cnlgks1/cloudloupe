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

// fakeFargateProfileAPI는 파게이트 프로파일 수집기가 쓰는 세 메서드를 대신한다.
type fakeFargateProfileAPI struct {
	clusters      []string
	listByCluster map[string][]string
	listErr       map[string]error
	describe      map[string]ekstypes.FargateProfile
}

func (f *fakeFargateProfileAPI) ListClusters(
	_ context.Context,
	_ *awseks.ListClustersInput,
	_ ...func(*awseks.Options),
) (*awseks.ListClustersOutput, error) {
	return &awseks.ListClustersOutput{Clusters: f.clusters}, nil
}

func (f *fakeFargateProfileAPI) ListFargateProfiles(
	_ context.Context,
	in *awseks.ListFargateProfilesInput,
	_ ...func(*awseks.Options),
) (*awseks.ListFargateProfilesOutput, error) {
	cluster := aws.ToString(in.ClusterName)
	if err, ok := f.listErr[cluster]; ok {
		return nil, err
	}

	return &awseks.ListFargateProfilesOutput{FargateProfileNames: f.listByCluster[cluster]}, nil
}

func (f *fakeFargateProfileAPI) DescribeFargateProfile(
	_ context.Context,
	in *awseks.DescribeFargateProfileInput,
	_ ...func(*awseks.Options),
) (*awseks.DescribeFargateProfileOutput, error) {
	key := aws.ToString(in.ClusterName) + "/" + aws.ToString(in.FargateProfileName)
	fp, ok := f.describe[key]
	if !ok {
		return &awseks.DescribeFargateProfileOutput{}, nil
	}

	return &awseks.DescribeFargateProfileOutput{FargateProfile: &fp}, nil
}

// TestFargateProfileCollectorType은 타입 ID를 확인한다.
func TestFargateProfileCollectorType(t *testing.T) {
	t.Parallel()

	if got := ekscollector.NewFargateProfile(&fakeFargateProfileAPI{}).Type(); got != model.TypeEKSFargateProfile {
		t.Errorf("Type() = %q, want %q", got, model.TypeEKSFargateProfile)
	}
}

// TestFargateProfileCollectBuildsFieldsAndRelations는 프로파일이 클러스터·파드실행역할·
// 서브넷으로 이어지는 관계를 만들고 셀렉터 네임스페이스를 그대로 담는지 확인한다.
func TestFargateProfileCollectBuildsFieldsAndRelations(t *testing.T) {
	t.Parallel()

	podRole := "arn:aws:iam::123456789012:role/eksFargatePodRole"
	api := &fakeFargateProfileAPI{
		clusters:      []string{"app"},
		listByCluster: map[string][]string{"app": {"fp-1"}},
		describe: map[string]ekstypes.FargateProfile{
			"app/fp-1": {
				FargateProfileName:  aws.String("fp-1"),
				FargateProfileArn:   aws.String("arn:aws:eks:ap-northeast-2:123456789012:fargateprofile/app/fp-1/abc"),
				ClusterName:         aws.String("app"),
				Status:              ekstypes.FargateProfileStatusActive,
				PodExecutionRoleArn: aws.String(podRole),
				Subnets:             []string{"subnet-1"},
				Selectors: []ekstypes.FargateProfileSelector{
					{Namespace: aws.String("default")},
					{Namespace: aws.String("kube-system")},
				},
			},
		},
	}

	got, err := ekscollector.NewFargateProfile(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("프로파일 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "fp-1" {
		t.Errorf("ID = %q, want fp-1", res.ID)
	}
	if got, want := res.FieldValue("Selectors.Namespace"), "default, kube-system"; got != want {
		t.Errorf("Selectors.Namespace = %q, want %q", got, want)
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
		{"PodExecutionRoleArn", model.TypeIAMRole, podRole},
		{"Subnets", model.TypeEC2Subnet, "subnet-1"},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 =\n  %+v\nwant\n  %+v", gotRels, want)
	}
}

// TestFargateProfileCollectKeepsOtherClustersOnListFailure는 한 클러스터 조회가 실패해도
// 다른 클러스터의 프로파일은 살리는지 확인한다.
func TestFargateProfileCollectKeepsOtherClustersOnListFailure(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeFargateProfileAPI{
		clusters:      []string{"good", "bad"},
		listByCluster: map[string][]string{"good": {"fp-ok"}},
		listErr:       map[string]error{"bad": denied},
		describe: map[string]ekstypes.FargateProfile{
			"good/fp-ok": {FargateProfileName: aws.String("fp-ok"), ClusterName: aws.String("good")},
		},
	}

	got, err := ekscollector.NewFargateProfile(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "fp-ok" {
		t.Errorf("수집 결과 = %+v, want fp-ok 하나", got)
	}
}
