// Package eks는 EKS 리소스를 조회해 도메인 모델로 바꾼다.
//
// EKS는 ECS와 같은 "목록 조회 + 항목별 상세 조회"(N+1) 형태다. ListClusters는 이름만 주고
// 상세는 DescribeCluster로 다시 물어야 한다. 노드그룹·파게이트 프로파일은 클러스터마다
// 다시 나열해야 하므로, 세 수집기 모두 [collect.FanOut]으로 상한 있는 팬아웃을 쓴다.
//
// 클러스터 안의 파드·디플로이먼트 같은 워크로드는 AWS API가 아니라 쿠버네티스 API 서버가
// 가지고 있어 여기서는 보이지 않는다. cloudloupe는 AWS 조회 전용이므로 클러스터·노드그룹·
// 파게이트 프로파일 같은 AWS 쪽 메타데이터까지만 다룬다.
package eks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// clusterAPI는 클러스터 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListClusters는 클러스터 이름 목록을, DescribeCluster는 이름 하나의 상세를 준다. 클라이언트
// 전체가 아니라 이 둘만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type clusterAPI interface {
	ListClusters(context.Context, *awseks.ListClustersInput, ...func(*awseks.Options)) (*awseks.ListClustersOutput, error)
	DescribeCluster(context.Context, *awseks.DescribeClusterInput, ...func(*awseks.Options)) (*awseks.DescribeClusterOutput, error)
}

// clusterCollector는 EKS 클러스터를 조회한다.
type clusterCollector struct {
	api clusterAPI
	// limit은 DescribeCluster 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewCluster는 EKS 클러스터 수집기를 만든다.
func NewCluster(api clusterAPI) collect.Collector {
	return clusterCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c clusterCollector) Type() string { return model.TypeEKSCluster }

// Collect는 리전의 EKS 클러스터를 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListClusters로 클러스터 이름 목록을 받는다(페이지네이션).
//  2. 이름마다 DescribeCluster를 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 이름으로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c clusterCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	names, listErr := c.clusterNames(ctx)
	if len(names) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, names,
		func(ctx context.Context, name string) (*ekstypes.Cluster, error) {
			out, err := c.api.DescribeCluster(ctx, &awseks.DescribeClusterInput{
				Name: aws.String(name),
			})
			if err != nil {
				return nil, fmt.Errorf("describe cluster (%s): %w", name, err)
			}

			return out.Cluster, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, cluster := range described {
		if cluster == nil {
			continue
		}

		out = append(out, clusterToResource(req.Scope, *cluster))
	}

	return out, errors.Join(listErr, describeErr)
}

// clusterNames는 리전의 클러스터 이름 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c clusterCollector) clusterNames(ctx context.Context) ([]string, error) {
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

// clusterToResource는 SDK 클러스터를 도메인 리소스로 변환한다.
//
// ID·이름은 Name, ARN은 Arn을 그대로 쓴다. 관계 이름에는 값을 꺼낸 SDK 응답 필드 경로를
// 넣는다.
func clusterToResource(scope collect.Scope, cluster ekstypes.Cluster) model.Resource {
	var refs []model.Ref

	refs = appendARNRef(refs, model.TypeIAMRole, "RoleArn", aws.ToString(cluster.RoleArn))

	var vpcID string
	if cluster.ResourcesVpcConfig != nil {
		vpc := cluster.ResourcesVpcConfig
		vpcID = aws.ToString(vpc.VpcId)

		for _, subnet := range vpc.SubnetIds {
			refs = appendIDRef(refs, model.TypeEC2Subnet, "ResourcesVpcConfig.SubnetIds", subnet)
		}

		for _, sg := range vpc.SecurityGroupIds {
			refs = appendIDRef(refs, model.TypeEC2SecurityGroup, "ResourcesVpcConfig.SecurityGroupIds", sg)
		}

		refs = appendIDRef(refs, model.TypeEC2SecurityGroup, "ResourcesVpcConfig.ClusterSecurityGroupId", aws.ToString(vpc.ClusterSecurityGroupId))
	}

	for _, enc := range cluster.EncryptionConfig {
		if enc.Provider != nil {
			refs = appendARNRef(refs, model.TypeKMSKey, "EncryptionConfig.Provider.KeyArn", aws.ToString(enc.Provider.KeyArn))
		}
	}

	return model.Resource{
		Type:      model.TypeEKSCluster,
		ID:        aws.ToString(cluster.Name),
		Name:      aws.ToString(cluster.Name),
		ARN:       aws.ToString(cluster.Arn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(cluster.Status),
		CreatedAt: cluster.CreatedAt,
		Fields: []model.Field{
			{Key: "Status", Value: orDash(string(cluster.Status))},
			{Key: "Version", Value: orDash(aws.ToString(cluster.Version))},
			{Key: "PlatformVersion", Value: orDash(aws.ToString(cluster.PlatformVersion))},
			{Key: "Endpoint", Value: orDash(aws.ToString(cluster.Endpoint))},
			{Key: "RoleArn", Value: orDash(aws.ToString(cluster.RoleArn))},
			{Key: "VpcId", Value: orDash(vpcID)},
			{Key: "EndpointPublicAccess", Value: vpcEndpointPublicAccess(cluster.ResourcesVpcConfig)},
			{Key: "EndpointPrivateAccess", Value: vpcEndpointPrivateAccess(cluster.ResourcesVpcConfig)},
		},
		Related: refs,
	}
}

// vpcEndpointPublicAccess는 API 서버 퍼블릭 접근 여부를 API 불리언 그대로 표시한다.
func vpcEndpointPublicAccess(vpc *ekstypes.VpcConfigResponse) string {
	if vpc == nil {
		return "-"
	}

	return strconv.FormatBool(vpc.EndpointPublicAccess)
}

// vpcEndpointPrivateAccess는 API 서버 프라이빗 접근 여부를 API 불리언 그대로 표시한다.
func vpcEndpointPrivateAccess(vpc *ekstypes.VpcConfigResponse) string {
	if vpc == nil {
		return "-"
	}

	return strconv.FormatBool(vpc.EndpointPrivateAccess)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// appendARNRef는 비어 있지 않은 ARN 관계를 추가한다.
//
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다.
func appendARNRef(refs []model.Ref, typeID, relation, arn string) []model.Ref {
	if arn == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:           typeID,
		ID:             arn,
		IdentifierKind: model.IdentifierARN,
		Relation:       relation,
	})
}

// appendIDRef는 비어 있지 않은 리소스 ID 관계를 추가한다.
func appendIDRef(refs []model.Ref, typeID, relation, id string) []model.Ref {
	if id == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:     typeID,
		ID:       id,
		Relation: relation,
	})
}

// joinValues는 문자열 목록을 API 값 그대로 콤마로 잇는다.
func joinValues(values []string) string {
	return strings.Join(values, ", ")
}
