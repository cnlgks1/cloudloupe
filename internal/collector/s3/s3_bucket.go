// Package s3는 S3 버킷을 조회해 도메인 모델로 바꾼다.
//
// S3는 버킷 이름이 전 계정에서 유일한 글로벌 이름 공간을 쓰지만, 버킷 자체는 특정 리전에
// 존재한다. 그래서 이 수집기는 Global이 아니라 Regional로 등록한다. ListBuckets의
// BucketRegion 필터로 그 리전의 버킷만 받으면, 리전을 여러 개 고른 조회에서도 버킷이 중복되지
// 않고 사용자가 고르지 않은 리전의 버킷이 섞이지도 않는다.
//
// 예전에는 버킷마다 GetBucketLocation을 불러 리전을 알아내야 했다. BucketRegion 필터를 쓰면
// 그 N+1 조회가 사라진다. 요청은 해당 리전 엔드포인트로 보내야 하는데, 카탈로그가 리전별
// aws.Config로 클라이언트를 만들므로 이미 그렇게 동작한다.
package s3

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listBucketsAPI는 버킷 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type listBucketsAPI interface {
	ListBuckets(context.Context, *awss3.ListBucketsInput, ...func(*awss3.Options)) (*awss3.ListBucketsOutput, error)
}

// bucketCollector는 S3 버킷을 조회한다.
type bucketCollector struct {
	api listBucketsAPI
}

// NewBucket은 S3 버킷 수집기를 만든다.
func NewBucket(api listBucketsAPI) collect.Collector {
	return bucketCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c bucketCollector) Type() string { return model.TypeS3Bucket }

// Collect는 범위 리전의 버킷을 모두 조회해 도메인 리소스로 변환한다.
//
// 버킷의 암호화 설정, 퍼블릭 액세스 차단, 버전 관리, 태그는 각각 별도 API이고 버킷마다
// 불러야 한다. 버킷이 수백 개인 계정에서 호출 수가 몇 배로 늘어나므로 지금은 목록만 받는다.
// 넣을 때는 [collect.FanOut]으로 버킷당 한 번씩 묶어 부르고, "설정이 없음"을 뜻하는 오류
// (NoSuchPublicAccessBlockConfiguration 등)를 실패가 아니라 값으로 다뤄야 한다.
func (c bucketCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awss3.NewListBucketsPaginator(c.api, &awss3.ListBucketsInput{
		// 이 리전의 버킷만 받는다. 필터가 없으면 계정의 모든 버킷이 리전마다 중복 수집된다.
		BucketRegion: aws.String(req.Scope.Region),
	})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list buckets (%s): %w", req.Scope.Region, err)
		}

		for i := range page.Buckets {
			out = append(out, bucketToResource(req.Scope, page.Buckets[i]))
		}
	}

	return out, nil
}

// bucketToResource는 SDK 버킷을 도메인 리소스로 변환한다.
//
// ID는 버킷 이름이다. S3 이름 공간이 전역이므로 계정·리전을 넘어 유일하고, 콘솔과 정책에서도
// 이 이름으로 참조된다.
//
// ARN은 응답의 BucketArn만 쓴다. 일반 버킷에서는 이 값이 비어 있는데, 이름으로 ARN을 합성하지
// 않는다. 수집기가 만들어낸 값과 AWS가 준 값을 화면에서 구분할 수 없게 되기 때문이다.
func bucketToResource(scope collect.Scope, bucket s3types.Bucket) model.Resource {
	// 응답이 리전을 함께 주지만, 필터로 이미 리전을 지정했으므로 비어 있으면 범위 리전을 쓴다.
	region := aws.ToString(bucket.BucketRegion)
	if region == "" {
		region = scope.Region
	}

	r := model.Resource{
		Type:      model.TypeS3Bucket,
		ID:        aws.ToString(bucket.Name),
		Name:      aws.ToString(bucket.Name),
		ARN:       aws.ToString(bucket.BucketArn),
		Region:    region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "BucketRegion", Value: orDash(region)},
			{Key: "CreationDate", Value: creationDate(bucket.CreationDate)},
		},
	}

	if bucket.CreationDate != nil {
		createdAt := bucket.CreationDate.UTC()
		r.CreatedAt = &createdAt
	}

	// 버킷을 쓰는 쪽(CloudFront 배포, Lambda 트리거 등)에서 이 버킷으로 향하는 관계를 남기는
	// 편이 자연스럽다. 버킷에서 사용처를 찾으려면 서비스마다 따로 물어야 한다.

	return r
}

// creationDate는 생성 시각을 AWS가 주는 표기로 바꾼다.
//
// 날짜만 잘라 보여주면 짧지만 CLI 출력과 대조할 수 없다. 이 도구의 값은 aws CLI 출력과 같아야
// 하므로 RFC 3339를 그대로 쓴다.
func creationDate(t *time.Time) string {
	if t == nil {
		return "-"
	}

	return t.UTC().Format(time.RFC3339)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
