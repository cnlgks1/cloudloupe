package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	autoscalingcollector "github.com/cnlgks1/cloudloupe/internal/collector/autoscaling"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// autoscalingDefinitions는 EC2 Auto Scaling 그룹의 리소스 타입을 조립한다.
func autoscalingDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsautoscaling.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsautoscaling.Client {
		clientOnce.Do(func() {
			client = awsautoscaling.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeAutoScalingGroup,
			Label:          "Auto Scaling groups",
			Scope:          Regional,
			Columns:        []string{"MinSize", "MaxSize", "DesiredCapacity", "AvailabilityZones", "HealthCheckType", "LaunchTemplate", "Instances"},
			SummaryColumns: []string{"MinSize", "MaxSize", "DesiredCapacity"},
			newCollector: func() collect.Collector {
				return autoscalingcollector.NewGroup(clientFor())
			},
		},
	}
}
