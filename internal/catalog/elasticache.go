package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec "github.com/aws/aws-sdk-go-v2/service/elasticache"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	eccollector "github.com/cnlgks1/cloudloupe/internal/collector/elasticache"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func elasticacheDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsec.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsec.Client {
		clientOnce.Do(func() {
			client = awsec.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeElastiCacheReplicationGroup,
			Label:          "Replication groups",
			Scope:          Regional,
			Columns:        []string{"Status", "Description", "Engine", "CacheNodeType", "MemberClusters", "MultiAZ", "AutomaticFailover", "AtRestEncryptionEnabled", "TransitEncryptionEnabled", "KmsKeyId"},
			SummaryColumns: []string{"Status", "Engine", "CacheNodeType"},
			newCollector: func() collect.Collector {
				return eccollector.NewReplicationGroup(clientFor())
			},
		},
		{
			Type:           model.TypeElastiCacheCacheCluster,
			Label:          "Cache clusters",
			Scope:          Regional,
			Columns:        []string{"CacheClusterStatus", "Engine", "EngineVersion", "CacheNodeType", "NumCacheNodes", "ReplicationGroupId", "CacheSubnetGroupName", "AtRestEncryptionEnabled"},
			SummaryColumns: []string{"CacheClusterStatus", "Engine", "CacheNodeType"},
			newCollector: func() collect.Collector {
				return eccollector.NewCacheCluster(clientFor())
			},
		},
	}
}
