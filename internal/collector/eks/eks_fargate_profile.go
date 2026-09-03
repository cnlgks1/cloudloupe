package eks

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fargateProfileAPI는 파게이트 프로파일 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 파게이트 프로파일은 클러스터 하위에 있어 먼저 ListClusters로 클러스터를 찾고, 클러스터마다
// ListFargateProfiles로 이름을, DescribeFargateProfile로 상세를 받는다.
type fargateProfileAPI interface {
	ListClusters(context.Context, *awseks.ListClustersInput, ...func(*awseks.Options)) (*awseks.ListClustersOutput, error)
	ListFargateProfiles(context.Context, *awseks.ListFargateProfilesInput, ...func(*awseks.Options)) (*awseks.ListFargateProfilesOutput, error)
	DescribeFargateProfile(context.Context, *awseks.DescribeFargateProfileInput, ...func(*awseks.Options)) (*awseks.DescribeFargateProfileOutput, error)
}

// fargateProfileCollector는 EKS 파게이트 프로파일을 조회한다.
type fargateProfileCollector struct {
	api fargateProfileAPI
	// limit은 클러스터별 조회 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewFargateProfile은 EKS 파게이트 프로파일 수집기를 만든다.
func NewFargateProfile(api fargateProfileAPI) collect.Collector {
	return fargateProfileCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c fargateProfileCollector) Type() string { return model.TypeEKSFargateProfile }

// Collect는 리전의 EKS 파게이트 프로파일을 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 노드그룹과 같다. ListClusters로 클러스터를 찾고, 클러스터마다 파게이트 프로파일을
// 나열한 뒤 상세를 받는다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c fargateProfileCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	clusterNames, listErr := c.clusterNames(ctx)
	if len(clusterNames) == 0 {
		return nil, listErr
	}

	perCluster, fanErr := collect.FanOut(ctx, c.limit, clusterNames, c.profilesForCluster)

	out := make([]model.Resource, 0)
	for _, profiles := range perCluster {
		for i := range profiles {
			out = append(out, fargateProfileToResource(req.Scope, profiles[i]))
		}
	}

	return out, errors.Join(listErr, fanErr)
}

// clusterNames는 리전의 클러스터 이름 목록을 모두 받는다.
func (c fargateProfileCollector) clusterNames(ctx context.Context) ([]string, error) {
	paginator := awseks.NewListClustersPaginator(c.api, &awseks.ListClustersInput{})

	var names []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return names, fmt.Errorf("list clusters: %w", err)
		}

		names = append(names, page.Clusters...)
	}

	return names, nil
}

// profilesForCluster는 클러스터 하나의 파게이트 프로파일 상세를 모두 받는다.
func (c fargateProfileCollector) profilesForCluster(ctx context.Context, clusterName string) ([]ekstypes.FargateProfile, error) {
	paginator := awseks.NewListFargateProfilesPaginator(c.api, &awseks.ListFargateProfilesInput{
		ClusterName: aws.String(clusterName),
	})

	var names []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list fargate profiles (%s): %w", clusterName, err)
		}

		names = append(names, page.FargateProfileNames...)
	}

	var (
		profiles []ekstypes.FargateProfile
		errs     []error
	)

	for _, name := range names {
		out, err := c.api.DescribeFargateProfile(ctx, &awseks.DescribeFargateProfileInput{
			ClusterName:        aws.String(clusterName),
			FargateProfileName: aws.String(name),
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("describe fargate profile (%s/%s): %w", clusterName, name, err))

			continue
		}
		if out.FargateProfile == nil {
			continue
		}

		profiles = append(profiles, *out.FargateProfile)
	}

	return profiles, errors.Join(errs...)
}

// fargateProfileToResource는 SDK 파게이트 프로파일을 도메인 리소스로 변환한다.
//
// ID·이름은 FargateProfileName, ARN은 FargateProfileArn을 그대로 쓴다. 관계 이름에는 값을
// 꺼낸 SDK 응답 필드 경로를 넣는다.
func fargateProfileToResource(scope collect.Scope, fp ekstypes.FargateProfile) model.Resource {
	var refs []model.Ref

	refs = appendIDRef(refs, model.TypeEKSCluster, "ClusterName", aws.ToString(fp.ClusterName))
	refs = appendARNRef(refs, model.TypeIAMRole, "PodExecutionRoleArn", aws.ToString(fp.PodExecutionRoleArn))

	for _, subnet := range fp.Subnets {
		refs = appendIDRef(refs, model.TypeEC2Subnet, "Subnets", subnet)
	}

	return model.Resource{
		Type:      model.TypeEKSFargateProfile,
		ID:        aws.ToString(fp.FargateProfileName),
		Name:      aws.ToString(fp.FargateProfileName),
		ARN:       aws.ToString(fp.FargateProfileArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(fp.Status),
		CreatedAt: fp.CreatedAt,
		Fields: []model.Field{
			{Key: "ClusterName", Value: orDash(aws.ToString(fp.ClusterName))},
			{Key: "Status", Value: orDash(string(fp.Status))},
			{Key: "PodExecutionRoleArn", Value: orDash(aws.ToString(fp.PodExecutionRoleArn))},
			{Key: "Subnets", Value: orDash(joinValues(fp.Subnets))},
			{Key: "Selectors.Namespace", Value: orDash(selectorNamespaces(fp.Selectors))},
		},
		Related: refs,
	}
}

// selectorNamespaces는 셀렉터가 고른 쿠버네티스 네임스페이스를 API 값 그대로 콤마로 잇는다.
//
// 파게이트 프로파일이 어느 네임스페이스의 파드를 파게이트로 띄우는지가 이 프로파일의
// 핵심이라 목록에서 바로 보여준다. 라벨 셀렉터는 상세가 깊어 여기서는 생략한다.
func selectorNamespaces(selectors []ekstypes.FargateProfileSelector) string {
	namespaces := make([]string, 0, len(selectors))
	for _, s := range selectors {
		namespaces = append(namespaces, aws.ToString(s.Namespace))
	}

	return joinValues(namespaces)
}
