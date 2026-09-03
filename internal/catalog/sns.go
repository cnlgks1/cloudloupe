package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	snscollector "github.com/cnlgks1/cloudloupe/internal/collector/sns"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func snsDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awssns.Client
		clientOnce sync.Once
	)
	clientFor := func() *awssns.Client {
		clientOnce.Do(func() {
			client = awssns.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeSNSTopic,
			Label:   "Topics",
			Scope:   Regional,
			Columns: []string{"DisplayName", "Owner", "SubscriptionsConfirmed", "SubscriptionsPending", "SubscriptionsDeleted", "FifoTopic", "KmsMasterKeyId"},
			newCollector: func() collect.Collector {
				return snscollector.NewTopic(clientFor())
			},
		},
	}
}
