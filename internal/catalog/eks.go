package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ekscollector "github.com/cnlgks1/cloudloupe/internal/collector/eks"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func eksDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awseks.Client
		clientOnce sync.Once
	)
	clientFor := func() *awseks.Client {
		clientOnce.Do(func() {
			client = awseks.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeEKSCluster,
			Label:          "Clusters",
			Scope:          Regional,
			Columns:        []string{"Status", "Version", "PlatformVersion", "Endpoint", "RoleArn", "VpcId", "EndpointPublicAccess", "EndpointPrivateAccess"},
			SummaryColumns: []string{"Status", "Version", "VpcId"},
			newCollector: func() collect.Collector {
				return ekscollector.NewCluster(clientFor())
			},
		},
		{
			Type:           model.TypeEKSNodegroup,
			Label:          "Node groups",
			Scope:          Regional,
			Columns:        []string{"ClusterName", "Status", "InstanceTypes", "AmiType", "CapacityType", "DesiredSize", "MinSize", "MaxSize", "DiskSize", "Version", "NodeRole"},
			SummaryColumns: []string{"ClusterName", "Status", "InstanceTypes"},
			newCollector: func() collect.Collector {
				return ekscollector.NewNodegroup(clientFor())
			},
		},
		{
			Type:           model.TypeEKSFargateProfile,
			Label:          "Fargate profiles",
			Scope:          Regional,
			Columns:        []string{"ClusterName", "Status", "PodExecutionRoleArn", "Subnets", "Selectors.Namespace"},
			SummaryColumns: []string{"ClusterName", "Status", "Selectors.Namespace"},
			newCollector: func() collect.Collector {
				return ekscollector.NewFargateProfile(clientFor())
			},
		},
	}
}
