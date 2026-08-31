package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	elbv2collector "github.com/cnlgks1/cloudloupe/internal/collector/elbv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func elbv2Definitions(cfg aws.Config) []Definition {
	var (
		client     *awselbv2.Client
		clientOnce sync.Once
	)
	clientFor := func() *awselbv2.Client {
		clientOnce.Do(func() {
			client = awselbv2.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeELBv2LoadBalancer,
			Label:          "로드 밸런서",
			Scope:          Regional,
			Columns:        []string{"종류", "스킴", "DNS 이름", "VPC"},
			SummaryColumns: []string{"종류", "스킴", "DNS 이름"},
			newCollector: func() collect.Collector {
				return elbv2collector.NewLoadBalancer(clientFor())
			},
		},
		{
			Type:           model.TypeELBv2TargetGroup,
			Label:          "타깃 그룹",
			Scope:          Regional,
			Columns:        []string{"프로토콜", "포트", "타깃 종류", "타깃 수"},
			SummaryColumns: []string{"프로토콜", "포트", "타깃 수"},
			newCollector: func() collect.Collector {
				return elbv2collector.NewTargetGroup(clientFor())
			},
		},
	}
}
