package apigateway

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsapigwv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// v2API는 HTTP·WebSocket API 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// GetApis는 v2 API 목록을 상세까지 담아 준다. 클라이언트 전체가 아니라 이 하나만 받으므로
// 자격증명 없이 fake로 테스트할 수 있다.
type v2API interface {
	GetApis(context.Context, *awsapigwv2.GetApisInput, ...func(*awsapigwv2.Options)) (*awsapigwv2.GetApisOutput, error)
}

// v2APICollector는 API Gateway HTTP·WebSocket API(v2)를 조회한다.
type v2APICollector struct {
	api v2API
}

// NewV2API는 API Gateway v2 API 수집기를 만든다.
func NewV2API(api v2API) collect.Collector {
	return v2APICollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c v2APICollector) Type() string { return model.TypeAPIGatewayV2API }

// Collect는 리전의 v2 API를 모두 조회해 도메인 리소스로 변환한다.
//
// apigatewayv2에는 페이지네이터 헬퍼가 없어 NextToken을 직접 이어 보낸다. 페이지 하나가
// 실패하면 그때까지 변환한 리소스를 오류와 함께 반환해 부분 결과를 살린다.
func (c v2APICollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	var (
		out   []model.Resource
		token *string
	)

	for {
		page, err := c.api.GetApis(ctx, &awsapigwv2.GetApisInput{NextToken: token})
		if err != nil {
			return out, fmt.Errorf("get apis: %w", err)
		}

		for i := range page.Items {
			out = append(out, v2APIToResource(req.Scope, page.Items[i]))
		}

		if page.NextToken == nil {
			break
		}
		token = page.NextToken
	}

	return out, nil
}

// v2APIToResource는 SDK v2 API를 도메인 리소스로 변환한다.
//
// ID는 ApiId, 이름은 Name을 그대로 쓴다. ProtocolType으로 HTTP인지 WEBSOCKET인지 구분된다.
// v2도 ARN을 응답에 주지 않아 비워 둔다.
func v2APIToResource(scope collect.Scope, api apigwv2types.Api) model.Resource {
	return model.Resource{
		Type:      model.TypeAPIGatewayV2API,
		ID:        aws.ToString(api.ApiId),
		Name:      aws.ToString(api.Name),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		CreatedAt: api.CreatedDate,
		Fields: []model.Field{
			{Key: "ApiId", Value: orDash(aws.ToString(api.ApiId))},
			{Key: "ProtocolType", Value: orDash(string(api.ProtocolType))},
			{Key: "ApiEndpoint", Value: orDash(aws.ToString(api.ApiEndpoint))},
			{Key: "Version", Value: orDash(aws.ToString(api.Version))},
			{Key: "RouteSelectionExpression", Value: orDash(aws.ToString(api.RouteSelectionExpression))},
			{Key: "DisableExecuteApiEndpoint", Value: boolPtrValue(api.DisableExecuteApiEndpoint)},
		},
	}
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
