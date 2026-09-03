// Package lambda는 Lambda 리소스를 조회해 도메인 모델로 바꾼다.
package lambda

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listFunctionsAPI는 함수 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 클라이언트 전체가 아니라 이 메서드 하나만 받으므로 자격증명 없이 단위 테스트를 할 수 있다.
type listFunctionsAPI interface {
	ListFunctions(context.Context, *awslambda.ListFunctionsInput, ...func(*awslambda.Options)) (*awslambda.ListFunctionsOutput, error)
}

// functionCollector는 Lambda 함수를 조회한다.
type functionCollector struct {
	api listFunctionsAPI
}

// NewFunction은 Lambda 함수 수집기를 만든다.
func NewFunction(api listFunctionsAPI) collect.Collector {
	return functionCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c functionCollector) Type() string { return model.TypeLambdaFunction }

// Collect는 리전의 Lambda 함수를 모두 조회해 도메인 리소스로 변환한다.
//
// 페이지 조회가 중간에 실패하면 그때까지 모은 함수와 오류를 함께 반환한다. ListFunctions
// 응답에는 Tags가 없으며, 태그를 얻기 위해 함수마다 ListTags를 부르는 N+1 조회는 하지 않는다.
func (c functionCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awslambda.NewListFunctionsPaginator(c.api, &awslambda.ListFunctionsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list functions: %w", err)
		}

		for i := range page.Functions {
			out = append(out, functionToResource(req.Scope, page.Functions[i]))
		}
	}

	return out, nil
}

// functionToResource는 SDK 함수 설정을 도메인 리소스로 변환한다.
//
// LastModified는 생성 시각이 아니라 마지막 수정 시각이므로 CreatedAt에는 넣지 않고 표시
// 필드에만 둔다. ListFunctions에 없는 Tags를 채우기 위한 추가 API 호출도 하지 않는다.
func functionToResource(scope collect.Scope, function lambdatypes.FunctionConfiguration) model.Resource {
	return model.Resource{
		Type:      model.TypeLambdaFunction,
		ID:        aws.ToString(function.FunctionName),
		Name:      aws.ToString(function.FunctionName),
		ARN:       aws.ToString(function.FunctionArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(function.State),
		Fields: []model.Field{
			{Key: "FunctionName", Value: orDash(aws.ToString(function.FunctionName))},
			{Key: "FunctionArn", Value: orDash(aws.ToString(function.FunctionArn))},
			{Key: "Runtime", Value: orDash(string(function.Runtime))},
			{Key: "PackageType", Value: orDash(string(function.PackageType))},
			{Key: "Architectures", Value: architecturesValue(function.Architectures)},
			{Key: "MemorySize", Value: int32Value(function.MemorySize)},
			{Key: "Timeout", Value: int32Value(function.Timeout)},
			{Key: "LastModified", Value: orDash(aws.ToString(function.LastModified))},
			{Key: "Version", Value: orDash(aws.ToString(function.Version))},
			{Key: "Role", Value: orDash(aws.ToString(function.Role))},
			{Key: "Handler", Value: orDash(aws.ToString(function.Handler))},
			{Key: "CodeSize", Value: strconv.FormatInt(function.CodeSize, 10)},
			{Key: "EphemeralStorage", Value: ephemeralStorageValue(function.EphemeralStorage)},
			{Key: "Description", Value: orDash(aws.ToString(function.Description))},
		},
		Related: functionRelations(function),
	}
}

// functionRelations는 함수와 네트워크, IAM 역할, KMS 키의 관계를 만든다.
//
// 관계 이름에는 값을 꺼낸 SDK 응답 필드 경로를 넣는다. 상세 화면이 그대로 보여주므로
// aws lambda list-functions 출력에서 같은 경로를 찾아 대조할 수 있다.
func functionRelations(function lambdatypes.FunctionConfiguration) []model.Ref {
	var refs []model.Ref

	if function.VpcConfig != nil {
		refs = appendIDRef(refs, model.TypeEC2VPC, "VpcConfig.VpcId", aws.ToString(function.VpcConfig.VpcId))
		for _, subnetID := range function.VpcConfig.SubnetIds {
			refs = appendIDRef(refs, model.TypeEC2Subnet, "VpcConfig.SubnetIds", subnetID)
		}
		for _, securityGroupID := range function.VpcConfig.SecurityGroupIds {
			refs = appendIDRef(refs, model.TypeEC2SecurityGroup, "VpcConfig.SecurityGroupIds", securityGroupID)
		}
	}

	refs = appendARNRef(refs, model.TypeIAMRole, "Role", aws.ToString(function.Role))
	refs = appendARNRef(refs, model.TypeKMSKey, "KMSKeyArn", aws.ToString(function.KMSKeyArn))

	return refs
}

// appendIDRef는 비어 있지 않은 리소스 ID 관계를 추가한다.
func appendIDRef(refs []model.Ref, typeID, relation, id string) []model.Ref {
	if id == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:     typeID,
		ID:       id,
		Relation: relation,
	})
}

// appendARNRef는 비어 있지 않은 ARN 관계를 추가한다.
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

// architecturesValue는 아키텍처 목록을 API 응답 순서대로 표시한다.
func architecturesValue(architectures []lambdatypes.Architecture) string {
	values := make([]string, 0, len(architectures))
	for _, architecture := range architectures {
		values = append(values, string(architecture))
	}

	return orDash(strings.Join(values, ", "))
}

// int32Value는 선택적인 정수 값을 API가 준 단위 그대로 표시한다.
func int32Value(value *int32) string {
	if value == nil {
		return "-"
	}

	return strconv.Itoa(int(*value))
}

// ephemeralStorageValue는 임시 스토리지 크기를 API가 준 MiB 단위 그대로 표시한다.
func ephemeralStorageValue(storage *lambdatypes.EphemeralStorage) string {
	if storage == nil {
		return "-"
	}

	return int32Value(storage.Size)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
