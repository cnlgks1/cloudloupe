// Package secretsmanager는 Secrets Manager 리소스를 조회해 도메인 모델로 바꾼다.
//
// 조회는 ListSecrets 하나로 끝난다. 이 API는 시크릿의 메타데이터(이름, 암호화 키, 로테이션
// 설정, 시각)만 주고 시크릿 값은 주지 않는다. cloudloupe는 조회 전용이므로 값을 가져오는
// GetSecretValue는 부르지 않는다. 목록 조회가 상세까지 주므로 항목별 팬아웃도 필요 없다.
package secretsmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// secretAPI는 시크릿 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListSecrets는 시크릿 메타데이터 목록을 준다. 값을 읽는 메서드는 담지 않으므로 이
// 수집기는 구조적으로 시크릿 값에 접근할 수 없다.
type secretAPI interface {
	ListSecrets(context.Context, *awssm.ListSecretsInput, ...func(*awssm.Options)) (*awssm.ListSecretsOutput, error)
}

// secretCollector는 Secrets Manager 시크릿을 조회한다.
type secretCollector struct {
	api secretAPI
}

// NewSecret은 Secrets Manager 시크릿 수집기를 만든다.
func NewSecret(api secretAPI) collect.Collector {
	return secretCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c secretCollector) Type() string { return model.TypeSecretsManagerSecret }

// Collect는 리전의 시크릿을 모두 조회해 도메인 리소스로 변환한다.
//
// ListSecrets 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한 리소스를 오류와
// 함께 반환해 부분 결과를 살린다.
func (c secretCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awssm.NewListSecretsPaginator(c.api, &awssm.ListSecretsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list secrets: %w", err)
		}

		for i := range page.SecretList {
			out = append(out, secretToResource(req.Scope, page.SecretList[i]))
		}
	}

	return out, nil
}

// secretToResource는 SDK 시크릿 메타데이터를 도메인 리소스로 변환한다.
//
// ID·이름은 Name, ARN은 ARN을 그대로 쓴다. 고객 관리 KMS 키로 암호화하면 KmsKeyId에서
// 키 관계를, 로테이션이 있으면 RotationLambdaARN에서 Lambda 관계를 만든다. 관계 이름에는
// 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func secretToResource(scope collect.Scope, secret smtypes.SecretListEntry) model.Resource {
	var refs []model.Ref

	refs = appendARNRef(refs, model.TypeKMSKey, "KmsKeyId", aws.ToString(secret.KmsKeyId))
	refs = appendARNRef(refs, model.TypeLambdaFunction, "RotationLambdaARN", aws.ToString(secret.RotationLambdaARN))

	return model.Resource{
		Type:      model.TypeSecretsManagerSecret,
		ID:        aws.ToString(secret.Name),
		Name:      aws.ToString(secret.Name),
		ARN:       aws.ToString(secret.ARN),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		CreatedAt: secret.CreatedDate,
		Fields: []model.Field{
			{Key: "Description", Value: orDash(aws.ToString(secret.Description))},
			{Key: "RotationEnabled", Value: boolPtrValue(secret.RotationEnabled)},
			{Key: "KmsKeyId", Value: orDash(aws.ToString(secret.KmsKeyId))},
			{Key: "RotationLambdaARN", Value: orDash(aws.ToString(secret.RotationLambdaARN))},
			{Key: "LastChangedDate", Value: dateValue(secret.LastChangedDate)},
			{Key: "LastAccessedDate", Value: dateValue(secret.LastAccessedDate)},
		},
		Related: refs,
	}
}

// dateValue는 선택적인 시각을 RFC 3339로 표시한다. nil이면 "-"다.
func dateValue(t *time.Time) string {
	if t == nil {
		return "-"
	}

	return t.UTC().Format(time.RFC3339)
}

// boolPtrValue는 선택적인 불리언 값을 API 값 그대로 표시한다. nil이면 "-"다.
func boolPtrValue(value *bool) string {
	if value == nil {
		return "-"
	}

	if *value {
		return "true"
	}

	return "false"
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// appendARNRef는 비어 있지 않은 ARN 관계를 추가한다.
//
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다.
func appendARNRef(refs []model.Ref, typeID, relation, arn string) []model.Ref {
	if arn == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:           typeID,
		ID:             arn,
		IdentifierKind: model.IdentifierARN,
		Relation:       relation,
	})
}
