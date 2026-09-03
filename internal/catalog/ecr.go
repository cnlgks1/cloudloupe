package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ecrcollector "github.com/cnlgks1/cloudloupe/internal/collector/ecr"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func ecrDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsecr.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsecr.Client {
		clientOnce.Do(func() {
			client = awsecr.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeECRRepository,
			Label:   "Repositories",
			Scope:   Regional,
			Columns: []string{"RepositoryUri", "ImageTagMutability", "EncryptionType", "KmsKey"},
			newCollector: func() collect.Collector {
				return ecrcollector.NewRepository(clientFor())
			},
		},
	}
}
