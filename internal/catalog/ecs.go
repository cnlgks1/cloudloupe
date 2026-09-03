package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ecscollector "github.com/cnlgks1/cloudloupe/internal/collector/ecs"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func ecsDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsecs.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsecs.Client {
		clientOnce.Do(func() {
			client = awsecs.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeECSCluster,
			Label:          "Clusters",
			Scope:          Regional,
			Columns:        []string{"Status", "RunningTasksCount", "ActiveServicesCount", "RegisteredContainerInstancesCount", "CapacityProviders"},
			SummaryColumns: []string{"Status", "RunningTasksCount", "ActiveServicesCount"},
			newCollector: func() collect.Collector {
				return ecscollector.NewCluster(clientFor())
			},
		},
		{
			Type:           model.TypeECSService,
			Label:          "Services",
			Scope:          Regional,
			Columns:        []string{"Status", "LaunchType", "DesiredCount", "RunningCount", "PendingCount", "TaskDefinition", "PlatformVersion", "SchedulingStrategy"},
			SummaryColumns: []string{"Status", "LaunchType", "RunningCount"},
			newCollector: func() collect.Collector {
				return ecscollector.NewService(clientFor())
			},
		},
		{
			Type:           model.TypeECSTaskDefinition,
			Label:          "Task definitions",
			Scope:          Regional,
			Columns:        []string{"Family", "Revision", "Status", "Cpu", "Memory", "NetworkMode", "RequiresCompatibilities", "ExecutionRoleArn", "TaskRoleArn"},
			SummaryColumns: []string{"Family", "Revision", "NetworkMode"},
			newCollector: func() collect.Collector {
				return ecscollector.NewTaskDefinition(clientFor())
			},
		},
	}
}
