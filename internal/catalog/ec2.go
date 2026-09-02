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
		{ID: "network", Label: "네트워크", Types: networkDefinitions(clientFor)},
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

// networkDefinitions는 트래픽 경로를 결정하는 리소스를 모은다.
func networkDefinitions(clientFor ec2ClientFor) []Definition {
	return []Definition{
		{
			Type:           model.TypeEC2RouteTable,
			Label:          "라우팅 테이블",
			Scope:          Regional,
			Columns:        []string{"VPC", "기본 라우팅 테이블", "연결 수", "라우트 수", "전파 VGW 수", "소유자 ID"},
			SummaryColumns: []string{"VPC", "기본 라우팅 테이블", "라우트 수"},
			newCollector: func() collect.Collector {
				return ec2collector.NewRouteTable(clientFor())
			},
		},
		{
			Type:           model.TypeEC2InternetGateway,
			Label:          "인터넷 게이트웨이",
			Scope:          Regional,
			Columns:        []string{"VPC", "연결 상태", "소유자 ID"},
			SummaryColumns: []string{"VPC", "연결 상태"},
			newCollector: func() collect.Collector {
				return ec2collector.NewInternetGateway(clientFor())
			},
		},
		{
			Type:           model.TypeEC2NATGateway,
			Label:          "NAT 게이트웨이",
			Scope:          Regional,
			Columns:        []string{"연결 유형", "가용 모드", "VPC", "서브넷", "공인 IP", "사설 IP", "ENI", "EIP 할당 ID", "실패 코드", "실패 메시지"},
			SummaryColumns: []string{"연결 유형", "VPC", "서브넷", "공인 IP"},
			newCollector: func() collect.Collector {
				return ec2collector.NewNATGateway(clientFor())
			},
		},
		{
			Type:           model.TypeEC2VPCEndpoint,
			Label:          "VPC 엔드포인트",
			Scope:          Regional,
			Columns:        []string{"종류", "서비스 이름", "서비스 리전", "IP 주소 유형", "VPC", "서브넷 수", "라우팅 테이블 수", "보안 그룹 수", "사설 DNS", "서비스 관리", "소유자 ID", "실패 원인"},
			SummaryColumns: []string{"종류", "서비스 이름", "VPC"},
			newCollector: func() collect.Collector {
				return ec2collector.NewVPCEndpoint(clientFor())
			},
		},
	}
}
