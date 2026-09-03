package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	sqscollector "github.com/cnlgks1/cloudloupe/internal/collector/sqs"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func sqsDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awssqs.Client
		clientOnce sync.Once
	)
	clientFor := func() *awssqs.Client {
		clientOnce.Do(func() {
			client = awssqs.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeSQSQueue,
			Label:   "Queues",
			Scope:   Regional,
			Columns: []string{"QueueArn", "FifoQueue", "ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible", "VisibilityTimeout", "MessageRetentionPeriod", "DelaySeconds", "KmsMasterKeyId", "RedrivePolicy"},
			newCollector: func() collect.Collector {
				return sqscollector.NewQueue(clientFor())
			},
		},
	}
}
