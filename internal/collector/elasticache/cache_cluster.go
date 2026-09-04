package elasticache

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// cacheClusterAPI는 캐시 클러스터 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type cacheClusterAPI interface {
	DescribeCacheClusters(context.Context, *awsec.DescribeCacheClustersInput, ...func(*awsec.Options)) (*awsec.DescribeCacheClustersOutput, error)
}

// cacheClusterCollector는 ElastiCache 캐시 클러스터를 조회한다.
type cacheClusterCollector struct {
	api cacheClusterAPI
}

// NewCacheCluster는 ElastiCache 캐시 클러스터 수집기를 만든다.
func NewCacheCluster(api cacheClusterAPI) collect.Collector {
	return cacheClusterCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c cacheClusterCollector) Type() string { return model.TypeElastiCacheCacheCluster }

// Collect는 리전의 캐시 클러스터를 모두 조회해 도메인 리소스로 변환한다.
//
// DescribeCacheClusters 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한 리소스를
// 오류와 함께 반환해 부분 결과를 살린다.
func (c cacheClusterCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec.NewDescribeCacheClustersPaginator(c.api, &awsec.DescribeCacheClustersInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe cache clusters: %w", err)
		}

		for i := range page.CacheClusters {
			out = append(out, cacheClusterToResource(req.Scope, page.CacheClusters[i]))
		}
	}

	return out, nil
}

// cacheClusterToResource는 SDK 캐시 클러스터를 도메인 리소스로 변환한다.
//
// ID·이름은 CacheClusterId, ARN은 ARN을 그대로 쓴다. 복제 그룹에 속하면 ReplicationGroupId로,
// VPC 보안 그룹은 SecurityGroups.SecurityGroupId로 이어진다. 관계 이름에는 값을 꺼낸 SDK
// 응답 필드 경로를 넣는다.
func cacheClusterToResource(scope collect.Scope, cluster ectypes.CacheCluster) model.Resource {
	var refs []model.Ref

	refs = appendIDRef(refs, model.TypeElastiCacheReplicationGroup, "ReplicationGroupId", aws.ToString(cluster.ReplicationGroupId))

	for _, sg := range cluster.SecurityGroups {
		refs = appendIDRef(refs, model.TypeEC2SecurityGroup, "SecurityGroups.SecurityGroupId", aws.ToString(sg.SecurityGroupId))
	}

	return model.Resource{
		Type:      model.TypeElastiCacheCacheCluster,
		ID:        aws.ToString(cluster.CacheClusterId),
		Name:      aws.ToString(cluster.CacheClusterId),
		ARN:       aws.ToString(cluster.ARN),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(cluster.CacheClusterStatus),
		Fields: []model.Field{
			{Key: "CacheClusterStatus", Value: orDash(aws.ToString(cluster.CacheClusterStatus))},
			{Key: "Engine", Value: orDash(aws.ToString(cluster.Engine))},
			{Key: "EngineVersion", Value: orDash(aws.ToString(cluster.EngineVersion))},
			{Key: "CacheNodeType", Value: orDash(aws.ToString(cluster.CacheNodeType))},
			{Key: "NumCacheNodes", Value: int32PtrValue(cluster.NumCacheNodes)},
			{Key: "ReplicationGroupId", Value: orDash(aws.ToString(cluster.ReplicationGroupId))},
			{Key: "CacheSubnetGroupName", Value: orDash(aws.ToString(cluster.CacheSubnetGroupName))},
			{Key: "AtRestEncryptionEnabled", Value: boolPtrValue(cluster.AtRestEncryptionEnabled)},
		},
		Related: refs,
	}
}

// int32PtrValue는 선택적인 정수 값을 API가 준 단위 그대로 표시한다. nil이면 "-"다.
func int32PtrValue(value *int32) string {
	if value == nil {
		return "-"
	}

	return fmt.Sprintf("%d", *value)
}
