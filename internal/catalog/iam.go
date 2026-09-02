package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	iamcollector "github.com/cnlgks1/cloudloupe/internal/collector/iam"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// iamDefinitions는 IAM 그룹의 리소스 타입을 조립한다.
//
// IAM은 글로벌 서비스라 Scope는 Global이다. 리전을 여러 개 골라도 한 번만 조회한다.
// 클라이언트는 이 그룹의 타입이 실제로 선택됐을 때만 만들도록 sync.Once로 미룬다.
func iamDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awsiam.Client
		clientOnce sync.Once
	)
	clientFor := func() *awsiam.Client {
		clientOnce.Do(func() {
			client = awsiam.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeIAMRole,
			Label:          "역할",
			Scope:          Global,
			Columns:        []string{"경로", "설명", "최대 세션 시간", "권한 경계", "마지막 사용", "역할 ID"},
			SummaryColumns: []string{"경로", "마지막 사용"},
			newCollector: func() collect.Collector {
				return iamcollector.NewRole(clientFor())
			},
		},
	}
}
