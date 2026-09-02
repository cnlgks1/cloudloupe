package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	rdscollector "github.com/cnlgks1/cloudloupe/internal/collector/rds"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// rdsDefinitions는 RDS 그룹의 리소스 타입을 조립한다.
//
// DB 클러스터와 DB 인스턴스는 같은 리전별 SDK 클라이언트를 지연 생성해 공유한다.
func rdsDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsrds.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsrds.Client {
		clientOnce.Do(func() {
			client = awsrds.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeRDSDBCluster,
			Label:          "DB clusters",
			Scope:          Regional,
			Columns:        []string{"Engine", "EngineVersion", "Endpoint", "Port", "MultiAZ", "StorageEncrypted"},
			SummaryColumns: []string{"Engine", "Endpoint", "MultiAZ"},
			newCollector: func() collect.Collector {
				return rdscollector.NewDBCluster(clientFor())
			},
		},
		{
			Type:           model.TypeRDSDBInstance,
			Label:          "DB instances",
			Scope:          Regional,
			Columns:        []string{"DBInstanceClass", "Engine", "AvailabilityZone", "MultiAZ", "Endpoint", "StorageType", "StorageEncrypted"},
			SummaryColumns: []string{"DBInstanceClass", "Engine", "AvailabilityZone"},
			newCollector: func() collect.Collector {
				return rdscollector.NewDBInstance(clientFor())
			},
		},
	}
}
