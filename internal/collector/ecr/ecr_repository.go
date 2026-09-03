// Package ecr는 ECR 리소스를 조회해 도메인 모델로 바꾼다.
//
// ECR 리포지토리는 DescribeRepositories 한 번으로 상세까지 다 주므로 ECS 같은
// "목록 + 항목별 상세"(N+1) 팬아웃이 필요 없다. 페이지네이션만 돌면 된다.
package ecr

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// repositoryAPI는 리포지토리 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// DescribeRepositories는 리포지토리 상세를 한 번에 준다. 클라이언트 전체가 아니라 이
// 메서드만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type repositoryAPI interface {
	DescribeRepositories(context.Context, *awsecr.DescribeRepositoriesInput, ...func(*awsecr.Options)) (*awsecr.DescribeRepositoriesOutput, error)
}

// repositoryCollector는 ECR 리포지토리를 조회한다.
type repositoryCollector struct {
	api repositoryAPI
}

// NewRepository는 ECR 리포지토리 수집기를 만든다.
func NewRepository(api repositoryAPI) collect.Collector {
	return repositoryCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c repositoryCollector) Type() string { return model.TypeECRRepository }

// Collect는 리전의 ECR 리포지토리를 모두 조회해 도메인 리소스로 변환한다.
//
// DescribeRepositories 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한
// 리소스를 오류와 함께 반환해 부분 결과를 살린다.
func (c repositoryCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsecr.NewDescribeRepositoriesPaginator(c.api, &awsecr.DescribeRepositoriesInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe repositories: %w", err)
		}

		for i := range page.Repositories {
			out = append(out, repositoryToResource(req.Scope, page.Repositories[i]))
		}
	}

	return out, nil
}

// repositoryToResource는 SDK 리포지토리를 도메인 리소스로 변환한다.
//
// ID·이름은 RepositoryName, ARN은 RepositoryArn을 그대로 쓴다. KMS 암호화면
// EncryptionConfiguration.KmsKey에서 키 관계를 만든다. 관계 이름에는 값을 꺼낸 SDK 응답
// 필드 경로를 넣는다.
func repositoryToResource(scope collect.Scope, repo ecrtypes.Repository) model.Resource {
	var refs []model.Ref

	if repo.EncryptionConfiguration != nil {
		refs = appendARNRef(refs, model.TypeKMSKey, "EncryptionConfiguration.KmsKey", aws.ToString(repo.EncryptionConfiguration.KmsKey))
	}

	res := model.Resource{
		Type:      model.TypeECRRepository,
		ID:        aws.ToString(repo.RepositoryName),
		Name:      aws.ToString(repo.RepositoryName),
		ARN:       aws.ToString(repo.RepositoryArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		CreatedAt: repo.CreatedAt,
		Fields: []model.Field{
			{Key: "RepositoryUri", Value: orDash(aws.ToString(repo.RepositoryUri))},
			{Key: "ImageTagMutability", Value: orDash(string(repo.ImageTagMutability))},
			{Key: "EncryptionType", Value: orDash(encryptionType(repo.EncryptionConfiguration))},
			{Key: "KmsKey", Value: orDash(kmsKey(repo.EncryptionConfiguration))},
		},
		Related: refs,
	}

	return res
}

// encryptionType은 암호화 설정의 종류를 API 값 그대로 반환한다. 설정이 없으면 빈 문자열이다.
func encryptionType(cfg *ecrtypes.EncryptionConfiguration) string {
	if cfg == nil {
		return ""
	}

	return string(cfg.EncryptionType)
}

// kmsKey는 KMS 암호화 키를 반환한다. 설정이 없거나 KMS가 아니면 빈 문자열이다.
func kmsKey(cfg *ecrtypes.EncryptionConfiguration) string {
	if cfg == nil {
		return ""
	}

	return aws.ToString(cfg.KmsKey)
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
