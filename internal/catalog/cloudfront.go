package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscf "github.com/aws/aws-sdk-go-v2/service/cloudfront"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	cfcollector "github.com/cnlgks1/cloudloupe/internal/collector/cloudfront"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func cloudfrontDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awscf.Client
		clientOnce sync.Once
	)
	clientFor := func() *awscf.Client {
		clientOnce.Do(func() {
			client = awscf.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeCloudFrontDistribution,
			Label:   "Distributions",
			Scope:   Global,
			Columns: []string{"Status", "Enabled", "DomainName", "Aliases", "Origins", "PriceClass", "HttpVersion", "WebACLId", "Comment"},
			newCollector: func() collect.Collector {
				return cfcollector.NewDistribution(clientFor())
			},
		},
	}
}
