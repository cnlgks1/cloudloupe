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
			Label:          "Load balancers",
			Scope:          Regional,
			Columns:        []string{"Type", "Scheme", "DNSName", "VpcId"},
			SummaryColumns: []string{"Type", "Scheme", "DNSName"},
			newCollector: func() collect.Collector {
				return elbv2collector.NewLoadBalancer(clientFor())
			},
		},
		{
			Type:           model.TypeELBv2Listener,
			Label:          "Listeners",
			Scope:          Regional,
			Columns:        []string{"Protocol", "Port", "SslPolicy", "DefaultActions", "Rules"},
			SummaryColumns: []string{"Protocol", "Port", "Rules"},
			newCollector: func() collect.Collector {
				return elbv2collector.NewListener(clientFor())
			},
		},
		{
			Type:           model.TypeELBv2TargetGroup,
			Label:          "Target groups",
			Scope:          Regional,
			Columns:        []string{"Protocol", "Port", "TargetType", "Targets"},
			SummaryColumns: []string{"Protocol", "Port", "Targets"},
			newCollector: func() collect.Collector {
				return elbv2collector.NewTargetGroup(clientFor())
			},
		},
	}
}
