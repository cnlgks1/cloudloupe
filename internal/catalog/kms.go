package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	kmscollector "github.com/cnlgks1/cloudloupe/internal/collector/kms"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// kmsDefinitions는 KMS 그룹의 리소스 타입을 조립한다.
//
// KMS 키는 리전마다 따로 존재하므로 Scope는 Regional이다. 다중 리전 키도 리전별 복제본이
// 각각 조회되며, 원본인지 복제본인지는 "다중 리전" 필드로 구분한다.
func kmsDefinitions(cfg aws.Config) []Definition {
	var (
		client     *awskms.Client
		clientOnce sync.Once
	)
	clientFor := func() *awskms.Client {
		clientOnce.Do(func() {
			client = awskms.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:  model.TypeKMSKey,
			Label: "키",
			Scope: Regional,
			Columns: []string{
				"별칭", "관리 주체", "용도", "키 스펙", "키 원본",
				"다중 리전", "사용 가능", "삭제 예정", "설명",
			},
			SummaryColumns: []string{"별칭", "관리 주체", "용도"},
			newCollector: func() collect.Collector {
				return kmscollector.NewKey(clientFor())
			},
		},
	}
}
