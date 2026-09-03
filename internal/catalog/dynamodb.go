package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ddbcollector "github.com/cnlgks1/cloudloupe/internal/collector/dynamodb"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func dynamodbDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsddb.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsddb.Client {
		clientOnce.Do(func() {
			client = awsddb.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeDynamoDBTable,
			Label:   "Tables",
			Scope:   Regional,
			Columns: []string{"TableStatus", "KeySchema", "BillingMode", "ItemCount", "TableSizeBytes", "ReadCapacityUnits", "WriteCapacityUnits", "GlobalSecondaryIndexes", "SSEType"},
			newCollector: func() collect.Collector {
				return ddbcollector.NewTable(clientFor())
			},
		},
	}
}
