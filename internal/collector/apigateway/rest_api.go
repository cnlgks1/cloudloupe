// Package apigateway는 API Gateway 리소스를 조회해 도메인 모델로 바꾼다.
//
// API Gateway는 서비스가 둘로 나뉜다. REST API(v1)는 apigateway 서비스가, HTTP·WebSocket
// API(v2)는 apigatewayv2 서비스가 담당한다. 두 API는 필드 구성이 달라 수집기와 리소스 타입을
// 각각 둔다. 목록 조회 한 번이 상세까지 주므로 ECS 같은 항목별 상세 팬아웃은 필요 없다.
package apigateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsapigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// restAPI는 REST API 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// GetRestApis는 REST API 목록을 상세까지 담아 준다. 클라이언트 전체가 아니라 이 하나만
// 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type restAPI interface {
	GetRestApis(context.Context, *awsapigw.GetRestApisInput, ...func(*awsapigw.Options)) (*awsapigw.GetRestApisOutput, error)
}

// restAPICollector는 API Gateway REST API(v1)를 조회한다.
type restAPICollector struct {
	api restAPI
}

// NewRestAPI는 API Gateway REST API 수집기를 만든다.
func NewRestAPI(api restAPI) collect.Collector {
	return restAPICollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c restAPICollector) Type() string { return model.TypeAPIGatewayRestAPI }

// Collect는 리전의 REST API를 모두 조회해 도메인 리소스로 변환한다.
//
// GetRestApis 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한 리소스를 오류와
// 함께 반환해 부분 결과를 살린다.
func (c restAPICollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsapigw.NewGetRestApisPaginator(c.api, &awsapigw.GetRestApisInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("get rest apis: %w", err)
		}

		for i := range page.Items {
			out = append(out, restAPIToResource(req.Scope, page.Items[i]))
		}
	}

	return out, nil
}

// restAPIToResource는 SDK REST API를 도메인 리소스로 변환한다.
//
// ID는 Id, 이름은 Name을 그대로 쓴다. API Gateway v1은 ARN을 응답에 주지 않으므로 비워 둔다.
// 프라이빗 엔드포인트면 VpcEndpointIds에서 VPC 엔드포인트 관계를 만든다. 관계 이름에는 값을
// 꺼낸 SDK 응답 필드 경로를 넣는다.
func restAPIToResource(scope collect.Scope, api apigwtypes.RestApi) model.Resource {
	var refs []model.Ref

	var endpointTypes string
	if api.EndpointConfiguration != nil {
		endpointTypes = endpointTypeList(api.EndpointConfiguration.Types)

		for _, id := range api.EndpointConfiguration.VpcEndpointIds {
			refs = appendIDRef(refs, model.TypeEC2VPCEndpoint, "EndpointConfiguration.VpcEndpointIds", id)
		}
	}

	return model.Resource{
		Type:      model.TypeAPIGatewayRestAPI,
		ID:        aws.ToString(api.Id),
		Name:      aws.ToString(api.Name),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		CreatedAt: api.CreatedDate,
		Fields: []model.Field{
			{Key: "Id", Value: orDash(aws.ToString(api.Id))},
			{Key: "Description", Value: orDash(aws.ToString(api.Description))},
			{Key: "Version", Value: orDash(aws.ToString(api.Version))},
			{Key: "EndpointConfiguration.Types", Value: orDash(endpointTypes)},
			{Key: "ApiKeySource", Value: orDash(string(api.ApiKeySource))},
		},
		Related: refs,
	}
}

// endpointTypeList는 엔드포인트 종류 목록을 API 값 그대로 콤마로 잇는다.
func endpointTypeList(types []apigwtypes.EndpointType) string {
	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, string(t))
	}

	return strings.Join(parts, ", ")
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// appendIDRef는 비어 있지 않은 리소스 ID 관계를 추가한다.
//
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다.
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
