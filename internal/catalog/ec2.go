package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ec2collector "github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func ec2Definitions(cfg aws.Config) []Definition {
	var (
		client     *awsec2.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsec2.Client {
		clientOnce.Do(func() {
			client = awsec2.NewFromConfig(cfg)
		})

		return client
	}

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
