package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ebcollector "github.com/cnlgks1/cloudloupe/internal/collector/eventbridge"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func eventbridgeDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awseb.Client
		clientOnce sync.Once
	)
	clientFor := func() *awseb.Client {
		clientOnce.Do(func() {
			client = awseb.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeEventBridgeEventBus,
			Label:          "Event buses",
			Scope:          Regional,
			Columns:        []string{"Description"},
			SummaryColumns: []string{"Description"},
			newCollector: func() collect.Collector {
				return ebcollector.NewEventBus(clientFor())
			},
		},
		{
			Type:           model.TypeEventBridgeRule,
			Label:          "Rules",
			Scope:          Regional,
			Columns:        []string{"State", "EventBusName", "ScheduleExpression", "EventPattern", "Description", "ManagedBy", "RoleArn"},
			SummaryColumns: []string{"State", "EventBusName", "ScheduleExpression"},
			newCollector: func() collect.Collector {
				return ebcollector.NewRule(clientFor())
			},
		},
	}
}
