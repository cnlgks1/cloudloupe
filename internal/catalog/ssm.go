package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ssmcollector "github.com/cnlgks1/cloudloupe/internal/collector/ssm"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func ssmDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsssm.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsssm.Client {
		clientOnce.Do(func() {
			client = awsssm.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeSSMParameter,
			Label:   "Parameters",
			Scope:   Regional,
			Columns: []string{"Type", "Tier", "DataType", "Version", "Description", "KeyId", "LastModifiedDate"},
			newCollector: func() collect.Collector {
				return ssmcollector.NewParameter(clientFor())
			},
		},
	}
}
