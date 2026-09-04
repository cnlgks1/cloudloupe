// Package eventbridge는 EventBridge 리소스를 조회해 도메인 모델로 바꾼다.
//
// EventBridge에는 페이지네이터 헬퍼가 없어 NextToken을 직접 이어 조회한다. 이벤트 버스는
// ListEventBuses 한 번으로 상세까지 주지만, 규칙은 버스마다 ListRules로 나열해야 하므로
// 규칙 수집기는 [collect.FanOut]으로 버스 단위 팬아웃을 쓴다.
package eventbridge

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// eventBusAPI는 이벤트 버스 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type eventBusAPI interface {
	ListEventBuses(context.Context, *awseb.ListEventBusesInput, ...func(*awseb.Options)) (*awseb.ListEventBusesOutput, error)
}

// eventBusCollector는 EventBridge 이벤트 버스를 조회한다.
type eventBusCollector struct {
	api eventBusAPI
}

// NewEventBus는 EventBridge 이벤트 버스 수집기를 만든다.
func NewEventBus(api eventBusAPI) collect.Collector {
	return eventBusCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c eventBusCollector) Type() string { return model.TypeEventBridgeEventBus }

// Collect는 리전의 이벤트 버스를 모두 조회해 도메인 리소스로 변환한다.
//
// 페이지네이터 헬퍼가 없어 NextToken을 직접 이어 보낸다. 페이지 하나가 실패하면 그때까지
// 변환한 리소스를 오류와 함께 반환해 부분 결과를 살린다.
func (c eventBusCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	var (
		out   []model.Resource
		token *string
	)

	for {
		page, err := c.api.ListEventBuses(ctx, &awseb.ListEventBusesInput{NextToken: token})
		if err != nil {
			return out, fmt.Errorf("list event buses: %w", err)
		}

		for i := range page.EventBuses {
			out = append(out, eventBusToResource(req.Scope, page.EventBuses[i]))
		}

		if page.NextToken == nil {
			break
		}
		token = page.NextToken
	}

	return out, nil
}

// eventBusToResource는 SDK 이벤트 버스를 도메인 리소스로 변환한다.
//
// ID·이름은 Name, ARN은 Arn을 그대로 쓴다. 이벤트 버스는 하위를 나열하지 않으므로 관계를
// 만들지 않는다. 규칙→버스 방향은 규칙 수집기가 기록하고, 역방향은 graph가 만든다.
func eventBusToResource(scope collect.Scope, bus ebtypes.EventBus) model.Resource {
	return model.Resource{
		Type:      model.TypeEventBridgeEventBus,
		ID:        aws.ToString(bus.Name),
		Name:      aws.ToString(bus.Name),
		ARN:       aws.ToString(bus.Arn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Description", Value: orDash(aws.ToString(bus.Description))},
		},
	}
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

// appendNameRef는 비어 있지 않은 이름 참조 관계를 추가한다.
func appendNameRef(refs []model.Ref, typeID, relation, name string) []model.Ref {
	if name == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:     typeID,
		ID:       name,
		Relation: relation,
	})
}
