package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsroute53 "github.com/aws/aws-sdk-go-v2/service/route53"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	route53collector "github.com/cnlgks1/cloudloupe/internal/collector/route53"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func route53Definitions(cfg aws.Config) []Definition {
	var (
		client     *awsroute53.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsroute53.Client {
		clientOnce.Do(func() {
			client = awsroute53.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeRoute53RecordSet,
			Label:          "레코드",
			Scope:          Global,
			Columns:        []string{"타입", "세트 식별자", "호스팅 영역", "TTL", "값", "별칭 대상"},
			SummaryColumns: []string{"타입", "세트 식별자", "호스팅 영역", "TTL", "값"},
			newCollector: func() collect.Collector {
				return route53collector.NewRecordSet(clientFor())
			},
		},
	}
}
