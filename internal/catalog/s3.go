package catalog

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/s3"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// s3Definitions는 S3 그룹의 리소스 타입을 조립한다.
//
// 버킷 이름은 전역이지만 버킷은 리전에 속하므로 Scope는 Regional이다. 수집기가 ListBuckets에
// BucketRegion 필터를 걸어 그 리전의 버킷만 받는다. Global로 두면 리전을 여러 개 고른 조회에서
// 같은 버킷이 리전마다 중복된다.
func s3Definitions(cfg aws.Config) []Definition {
	var (
		client     *awss3.Client
		clientOnce sync.Once
	)
	clientFor := func() *awss3.Client {
		clientOnce.Do(func() {
			client = awss3.NewFromConfig(cfg)
		})

		return client
	}

	return []Definition{
		{
			Type:           model.TypeS3Bucket,
			Label:          "Buckets",
			Scope:          Regional,
			Columns:        []string{"BucketRegion", "CreationDate"},
			SummaryColumns: []string{"BucketRegion", "CreationDate"},
			newCollector: func() collect.Collector {
				return s3.NewBucket(clientFor())
			},
		},
	}
}
