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

// ec2Groups는 EC2와 VPC 그룹이 공유하는 지연 생성 EC2 클라이언트를 조립한다.
func ec2Groups(cfg aws.Config) []Group {
	return ec2GroupsWithClientFactory(func() *awsec2.Client {
		return awsec2.NewFromConfig(cfg)
	})
}

func ec2GroupsWithClientFactory(newClient func() *awsec2.Client) []Group {
	clientFor := newEC2ClientFor(newClient)

	return []Group{
		{ID: "ec2", Label: "EC2", Types: ec2Definitions(clientFor)},
		{ID: "vpc", Label: "VPC", Types: vpcDefinitions(clientFor)},
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
			Label:          "인스턴스",
			Scope:          Regional,
			Columns:        []string{"인스턴스 타입", "가용 영역", "사설 IP", "공인 IP"},
			SummaryColumns: []string{"인스턴스 타입", "사설 IP", "공인 IP"},
			newCollector: func() collect.Collector {
				return ec2collector.NewInstance(clientFor())
			},
		},
		{
			Type:           model.TypeEC2Volume,
			Label:          "볼륨",
			Scope:          Regional,
			Columns:        []string{"타입", "크기(GiB)", "IOPS", "가용 영역", "암호화"},
			SummaryColumns: []string{"타입", "크기(GiB)", "가용 영역"},
			newCollector: func() collect.Collector {
				return ec2collector.NewVolume(clientFor())
			},
		},
		{
			Type:           model.TypeEC2NetworkInterface,
			Label:          "ENI",
			Scope:          Regional,
			Columns:        []string{"종류", "사설 IP", "VPC", "서브넷"},
			SummaryColumns: []string{"사설 IP", "VPC", "서브넷"},
			newCollector: func() collect.Collector {
				return ec2collector.NewNetworkInterface(clientFor())
			},
		},
		{
			Type:           model.TypeEC2Address,
			Label:          "Elastic IP",
			Scope:          Regional,
			Columns:        []string{"공인 IP", "사설 IP", "도메인"},
			SummaryColumns: []string{"공인 IP", "사설 IP"},
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
			Label:          "VPC",
			Scope:          Regional,
			Columns:        []string{"IPv4 CIDR", "기본 VPC", "인스턴스 테넌시", "DHCP 옵션 세트", "소유자 ID"},
			SummaryColumns: []string{"IPv4 CIDR", "기본 VPC", "인스턴스 테넌시"},
			newCollector: func() collect.Collector {
				return ec2collector.NewVPC(clientFor())
			},
		},
		{
			Type:           model.TypeEC2Subnet,
			Label:          "서브넷",
			Scope:          Regional,
			Columns:        []string{"IPv4 CIDR", "가용 영역", "가용 영역 ID", "사용 가능한 IPv4 주소", "VPC", "시작 시 공인 IPv4 자동 할당", "기본 서브넷", "IPv6 전용", "시작 시 IPv6 자동 할당", "소유자 ID"},
			SummaryColumns: []string{"IPv4 CIDR", "가용 영역", "VPC"},
			newCollector: func() collect.Collector {
				return ec2collector.NewSubnet(clientFor())
			},
		},
		{
			Type:           model.TypeEC2SecurityGroup,
			Label:          "보안 그룹",
			Scope:          Regional,
			Columns:        []string{"VPC", "인바운드 규칙 수", "아웃바운드 규칙 수", "설명", "소유자 ID"},
			SummaryColumns: []string{"VPC", "인바운드 규칙 수", "아웃바운드 규칙 수"},
			newCollector: func() collect.Collector {
				return ec2collector.NewSecurityGroup(clientFor())
			},
		},
	}
}
