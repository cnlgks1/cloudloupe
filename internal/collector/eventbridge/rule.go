package eventbridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// ruleAPI는 규칙 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 규칙은 이벤트 버스마다 나열되므로 먼저 ListEventBuses로 버스를 찾고, 버스마다 ListRules로
// 규칙을 받는다.
type ruleAPI interface {
	ListEventBuses(context.Context, *awseb.ListEventBusesInput, ...func(*awseb.Options)) (*awseb.ListEventBusesOutput, error)
	ListRules(context.Context, *awseb.ListRulesInput, ...func(*awseb.Options)) (*awseb.ListRulesOutput, error)
}

// ruleCollector는 EventBridge 규칙을 조회한다.
type ruleCollector struct {
	api ruleAPI
	// limit은 버스별 조회 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewRule은 EventBridge 규칙 수집기를 만든다.
func NewRule(api ruleAPI) collect.Collector {
	return ruleCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c ruleCollector) Type() string { return model.TypeEventBridgeRule }

// Collect는 리전의 EventBridge 규칙을 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListEventBuses로 이벤트 버스 이름 목록을 받는다(수동 NextToken).
//  2. 버스마다 ListRules로 규칙을 받는다. 버스 단위로 상한 있는 팬아웃을 돌린다.
//
// 버스 목록 조회가 중간에 실패하면 그때까지 받은 버스로 계속 진행한다. 특정 버스 조회가
// 실패해도 나머지 버스의 규칙은 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c ruleCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	busNames, listErr := c.busNames(ctx)
	if len(busNames) == 0 {
		return nil, listErr
	}

	perBus, fanErr := collect.FanOut(ctx, c.limit, busNames, c.rulesForBus)

	out := make([]model.Resource, 0)
	for _, rules := range perBus {
		for i := range rules {
			out = append(out, ruleToResource(req.Scope, rules[i]))
		}
	}

	return out, errors.Join(listErr, fanErr)
}

// busNames는 리전의 이벤트 버스 이름 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c ruleCollector) busNames(ctx context.Context) ([]string, error) {
	var (
		names []string
		token *string
	)

	for {
		page, err := c.api.ListEventBuses(ctx, &awseb.ListEventBusesInput{NextToken: token})
		if err != nil {
			return names, fmt.Errorf("list event buses: %w", err)
		}

		for _, bus := range page.EventBuses {
			names = append(names, aws.ToString(bus.Name))
		}

		if page.NextToken == nil {
			break
		}
		token = page.NextToken
	}

	return names, nil
}

// rulesForBus는 이벤트 버스 하나의 규칙을 모두 받는다.
func (c ruleCollector) rulesForBus(ctx context.Context, busName string) ([]ebtypes.Rule, error) {
	var (
		rules []ebtypes.Rule
		token *string
	)

	for {
		page, err := c.api.ListRules(ctx, &awseb.ListRulesInput{
			EventBusName: aws.String(busName),
			NextToken:    token,
		})
		if err != nil {
			return rules, fmt.Errorf("list rules (%s): %w", busName, err)
		}

		rules = append(rules, page.Rules...)

		if page.NextToken == nil {
			break
		}
		token = page.NextToken
	}

	return rules, nil
}

// ruleToResource는 SDK 규칙을 도메인 리소스로 변환한다.
//
// ID·이름은 Name, ARN은 Arn을 그대로 쓴다. EventBusName으로 소속 버스에, RoleArn으로 대상
// 호출에 쓰는 IAM 역할에 이어진다. 관계 이름에는 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func ruleToResource(scope collect.Scope, rule ebtypes.Rule) model.Resource {
	var refs []model.Ref

	refs = appendNameRef(refs, model.TypeEventBridgeEventBus, "EventBusName", aws.ToString(rule.EventBusName))
	refs = appendARNRef(refs, model.TypeIAMRole, "RoleArn", aws.ToString(rule.RoleArn))

	return model.Resource{
		Type:      model.TypeEventBridgeRule,
		ID:        aws.ToString(rule.Name),
		Name:      aws.ToString(rule.Name),
		ARN:       aws.ToString(rule.Arn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(rule.State),
		Fields: []model.Field{
			{Key: "State", Value: orDash(string(rule.State))},
			{Key: "EventBusName", Value: orDash(aws.ToString(rule.EventBusName))},
			{Key: "ScheduleExpression", Value: orDash(aws.ToString(rule.ScheduleExpression))},
			{Key: "EventPattern", Value: orDash(aws.ToString(rule.EventPattern))},
			{Key: "Description", Value: orDash(aws.ToString(rule.Description))},
			{Key: "ManagedBy", Value: orDash(aws.ToString(rule.ManagedBy))},
			{Key: "RoleArn", Value: orDash(aws.ToString(rule.RoleArn))},
		},
		Related: refs,
	}
}
