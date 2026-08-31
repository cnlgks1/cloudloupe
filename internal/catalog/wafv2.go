package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awswafv2 "github.com/aws/aws-sdk-go-v2/service/wafv2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	wafv2collector "github.com/cnlgks1/cloudloupe/internal/collector/wafv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func wafv2Definitions(cfg aws.Config) []Definition {
	var (
		client     *awswafv2.Client
		clientOnce sync.Once
	)
	clientFor := func() *awswafv2.Client {
		clientOnce.Do(func() {
			client = awswafv2.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:    model.TypeWAFv2WebACL,
			Label:   "WAF Web ACL",
			Scope:   Regional,
			Columns: []string{"설명", "규칙 수"},
			newCollector: func() collect.Collector {
				return wafv2collector.NewWebACL(clientFor())
			},
		},
	}
}
