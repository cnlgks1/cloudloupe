package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	smcollector "github.com/cnlgks1/cloudloupe/internal/collector/secretsmanager"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func secretsmanagerDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awssm.Client
		clientOnce sync.Once
	)
	clientFor := func() *awssm.Client {
		clientOnce.Do(func() {
			client = awssm.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeSecretsManagerSecret,
			Label:   "Secrets",
			Scope:   Regional,
			Columns: []string{"Description", "RotationEnabled", "KmsKeyId", "RotationLambdaARN", "LastChangedDate", "LastAccessedDate"},
			newCollector: func() collect.Collector {
				return smcollector.NewSecret(clientFor())
			},
		},
	}
}
