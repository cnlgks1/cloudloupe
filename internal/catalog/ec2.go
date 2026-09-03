package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ec2collector "github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

type ec2ClientFor func() *awsec2.Client

// ec2Groups는 EC2 API를 쓰는 그룹들이 공유하는 지연 생성 클라이언트를 조립한다.
func ec2Groups(cfg aws.Config) []Group {
	return ec2GroupsWithClientFactory(func() *awsec2.Client {
		return awsec2.NewFromConfig(cfg)
	})
}

// ec2GroupsWithClientFactory는 EC2 계열 그룹을 하나의 클라이언트 위에 조립한다.
//
// VPC와 네트워크를 다른 그룹으로 나눈 이유는 선택 화면 때문이다. 한 그룹에 타입을
// 계속 늘리면 포함 항목이 잘려서 무엇을 수집하는지 읽을 수 없다. VPC는 주소 공간과
// 접근 제어를, 네트워크는 트래픽 경로를 담당하므로 조사 단위로도 갈라진다.
func ec2GroupsWithClientFactory(newClient func() *awsec2.Client) []Group {
	clientFor := newEC2ClientFor(newClient)

	return []Group{
		{ID: "ec2", Label: "EC2", Types: ec2Definitions(clientFor)},
		{ID: "vpc", Label: "VPC", Types: vpcDefinitions(clientFor)},
		{ID: "network", Label: "Network", Types: networkDefinitions(clientFor)},
	}
}

func newEC2ClientFor(newClient func() *awsec2.Client) ec2ClientFor {
	var (
		client     *awsec2.Client
		clientOnce sync.Once
	)

	return func() *awsec2.Client {
		clientOnce.Do(func() {
			client = newClient()
		})

		return client
	}
}

func ec2Definitions(clientFor ec2ClientFor) []Definition {
	return []Definition{
		{
			Type:           model.TypeEC2Instance,
			Label:          "Instances",
			Scope:          Regional,
			Columns:        []string{"InstanceType", "AvailabilityZone", "PrivateIpAddress", "PublicIp"},
			SummaryColumns: []string{"InstanceType", "PrivateIpAddress", "PublicIp"},
			newCollector: func() collect.Collector {
				return ec2collector.NewInstance(clientFor())
			},
		},
		{
			Type:           model.TypeEC2Volume,
			Label:          "Volumes",
			Scope:          Regional,
			Columns:        []string{"VolumeType", "Size", "Iops", "AvailabilityZone", "Encrypted"},
			SummaryColumns: []string{"VolumeType", "Size", "AvailabilityZone"},
			newCollector: func() collect.Collector {
				return ec2collector.NewVolume(clientFor())
			},
		},
		{
			Type:           model.TypeEC2NetworkInterface,
			Label:          "Network interfaces",
			Scope:          Regional,
			Columns:        []string{"InterfaceType", "PrivateIpAddress", "VpcId", "SubnetId"},
			SummaryColumns: []string{"PrivateIpAddress", "VpcId", "SubnetId"},
			newCollector: func() collect.Collector {
				return ec2collector.NewNetworkInterface(clientFor())
			},
		},
		{
			Type:           model.TypeEC2Address,
			Label:          "Elastic IPs",
			Scope:          Regional,
			Columns:        []string{"PublicIp", "PrivateIpAddress", "Domain"},
			SummaryColumns: []string{"PublicIp", "PrivateIpAddress"},
			newCollector: func() collect.Collector {
				return ec2collector.NewAddress(clientFor())
			},
		},
	}
}

func vpcDefinitions(clientFor ec2ClientFor) []Definition {
	return []Definition{
		{
			Type:           model.TypeEC2VPC,
			Label:          "VPCs",
			Scope:          Regional,
			Columns:        []string{"CidrBlock", "IsDefault", "InstanceTenancy", "DhcpOptionsId", "OwnerId"},
			SummaryColumns: []string{"CidrBlock", "IsDefault", "InstanceTenancy"},
			newCollector: func() collect.Collector {
				return ec2collector.NewVPC(clientFor())
			},
		},
		{
			Type:           model.TypeEC2Subnet,
			Label:          "Subnets",
			Scope:          Regional,
			Columns:        []string{"CidrBlock", "AvailabilityZone", "AvailabilityZoneId", "AvailableIpAddressCount", "VpcId", "MapPublicIpOnLaunch", "DefaultForAz", "Ipv6Native", "AssignIpv6AddressOnCreation", "OwnerId"},
			SummaryColumns: []string{"CidrBlock", "AvailabilityZone", "VpcId"},
			newCollector: func() collect.Collector {
				return ec2collector.NewSubnet(clientFor())
			},
		},
		{
			Type:           model.TypeEC2SecurityGroup,
			Label:          "Security groups",
			Scope:          Regional,
			Columns:        []string{"VpcId", "InboundRules", "OutboundRules", "Description", "OwnerId"},
			SummaryColumns: []string{"VpcId", "InboundRules", "OutboundRules"},
			newCollector: func() collect.Collector {
				return ec2collector.NewSecurityGroup(clientFor())
			},
		},
	}
}

// networkDefinitions는 트래픽 경로를 결정하는 리소스를 모은다.
func networkDefinitions(clientFor ec2ClientFor) []Definition {
	return []Definition{
		{
			Type:           model.TypeEC2RouteTable,
			Label:          "Route tables",
			Scope:          Regional,
			Columns:        []string{"VpcId", "Main", "Associations", "Routes", "PropagatingVgws", "OwnerId"},
			SummaryColumns: []string{"VpcId", "Main", "Routes"},
			newCollector: func() collect.Collector {
				return ec2collector.NewRouteTable(clientFor())
			},
		},
		{
			Type:           model.TypeEC2InternetGateway,
			Label:          "Internet gateways",
			Scope:          Regional,
			Columns:        []string{"VpcId", "AttachmentState", "OwnerId"},
			SummaryColumns: []string{"VpcId", "AttachmentState"},
			newCollector: func() collect.Collector {
				return ec2collector.NewInternetGateway(clientFor())
			},
		},
		{
			Type:           model.TypeEC2NATGateway,
			Label:          "NAT gateways",
			Scope:          Regional,
			Columns:        []string{"ConnectivityType", "AvailabilityMode", "VpcId", "SubnetId", "PublicIp", "PrivateIp", "NetworkInterfaceId", "AllocationId", "FailureCode", "FailureMessage"},
			SummaryColumns: []string{"ConnectivityType", "VpcId", "SubnetId", "PublicIp"},
			newCollector: func() collect.Collector {
				return ec2collector.NewNATGateway(clientFor())
			},
		},
		{
			Type:           model.TypeEC2VPCEndpoint,
			Label:          "Endpoints",
			Scope:          Regional,
			Columns:        []string{"VpcEndpointType", "ServiceName", "ServiceRegion", "IpAddressType", "VpcId", "SubnetIds", "RouteTableIds", "Groups", "PrivateDnsEnabled", "RequesterManaged", "OwnerId", "FailureReason"},
			SummaryColumns: []string{"VpcEndpointType", "ServiceName", "VpcId"},
			newCollector: func() collect.Collector {
				return ec2collector.NewVPCEndpoint(clientFor())
			},
		},
	}
}
