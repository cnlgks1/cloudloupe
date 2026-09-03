// Package ssm은 SSM Parameter Store 리소스를 조회해 도메인 모델로 바꾼다.
//
// 조회는 DescribeParameters 하나로 끝난다. 이 API는 파라미터의 메타데이터(이름, 타입, 티어,
// 암호화 키, 시각)만 주고 파라미터 값은 주지 않는다. cloudloupe는 조회 전용이므로 값을
// 가져오는 GetParameter는 부르지 않는다. SecureString 값이 노출될 여지를 원천 차단한다.
package ssm

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// parameterAPI는 파라미터 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// DescribeParameters는 파라미터 메타데이터 목록을 준다. 값을 읽는 메서드는 담지 않으므로 이
// 수집기는 구조적으로 파라미터 값에 접근할 수 없다.
type parameterAPI interface {
	DescribeParameters(context.Context, *awsssm.DescribeParametersInput, ...func(*awsssm.Options)) (*awsssm.DescribeParametersOutput, error)
}

// parameterCollector는 SSM Parameter Store 파라미터를 조회한다.
type parameterCollector struct {
	api parameterAPI
}

// NewParameter는 SSM 파라미터 수집기를 만든다.
func NewParameter(api parameterAPI) collect.Collector {
	return parameterCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c parameterCollector) Type() string { return model.TypeSSMParameter }

// Collect는 리전의 파라미터를 모두 조회해 도메인 리소스로 변환한다.
//
// DescribeParameters 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한 리소스를
// 오류와 함께 반환해 부분 결과를 살린다.
func (c parameterCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsssm.NewDescribeParametersPaginator(c.api, &awsssm.DescribeParametersInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe parameters: %w", err)
		}

		for i := range page.Parameters {
			out = append(out, parameterToResource(req.Scope, page.Parameters[i]))
		}
	}

	return out, nil
}

// parameterToResource는 SDK 파라미터 메타데이터를 도메인 리소스로 변환한다.
//
// ID·이름은 Name, ARN은 ARN을 그대로 쓴다. SecureString을 고객 관리 KMS 키로 암호화하면
// KeyId에서 키 관계를 만든다. 관계 이름에는 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func parameterToResource(scope collect.Scope, param ssmtypes.ParameterMetadata) model.Resource {
	var refs []model.Ref

	refs = appendKeyRef(refs, aws.ToString(param.KeyId))

	return model.Resource{
		Type:      model.TypeSSMParameter,
		ID:        aws.ToString(param.Name),
		Name:      aws.ToString(param.Name),
		ARN:       aws.ToString(param.ARN),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Type", Value: orDash(string(param.Type))},
			{Key: "Tier", Value: orDash(string(param.Tier))},
			{Key: "DataType", Value: orDash(aws.ToString(param.DataType))},
			{Key: "Version", Value: strconv.FormatInt(param.Version, 10)},
			{Key: "Description", Value: orDash(aws.ToString(param.Description))},
			{Key: "KeyId", Value: orDash(aws.ToString(param.KeyId))},
			{Key: "LastModifiedDate", Value: dateValue(param.LastModifiedDate)},
		},
		Related: refs,
	}
}

// appendKeyRef는 KMS 키 관계를 추가한다.
//
// SSM의 KeyId는 alias/aws/ssm(계정 기본 키)이거나 고객 키 ID·ARN이다. 값이 있으면 그대로
// 담아 KMS 키로 색인한다. 관계 이름에는 값을 꺼낸 SDK 응답 필드 KeyId를 쓴다.
func appendKeyRef(refs []model.Ref, keyID string) []model.Ref {
	if keyID == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:           model.TypeKMSKey,
		ID:             keyID,
		IdentifierKind: model.IdentifierARN,
		Relation:       "KeyId",
	})
}

// dateValue는 선택적인 시각을 RFC 3339로 표시한다. nil이면 "-"다.
func dateValue(t *time.Time) string {
	if t == nil {
		return "-"
	}

	return t.UTC().Format(time.RFC3339)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
