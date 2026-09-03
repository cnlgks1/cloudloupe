package elbv2

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listenerAPI는 리스너 관계 수집에 필요한 조회 메서드만 담은 좁은 인터페이스다.
//
// 로드 밸런서 ARN을 먼저 찾고, 그 아래 리스너와 규칙을 조회한다. 세 메서드 모두
// Describe로 시작하므로 조회 전용 가드를 통과한다.
type listenerAPI interface {
	DescribeLoadBalancers(context.Context, *awselbv2.DescribeLoadBalancersInput, ...func(*awselbv2.Options)) (*awselbv2.DescribeLoadBalancersOutput, error)
	DescribeListeners(context.Context, *awselbv2.DescribeListenersInput, ...func(*awselbv2.Options)) (*awselbv2.DescribeListenersOutput, error)
	DescribeRules(context.Context, *awselbv2.DescribeRulesInput, ...func(*awselbv2.Options)) (*awselbv2.DescribeRulesOutput, error)
}

// listenerCollector는 ELBv2 리스너와 로드 밸런서·타깃 그룹 관계를 조회한다.
type listenerCollector struct {
	api listenerAPI
}

// NewListener는 ELBv2 리스너 수집기를 만든다.
func NewListener(api listenerAPI) collect.Collector {
	return listenerCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c listenerCollector) Type() string { return model.TypeELBv2Listener }

// Collect는 로드 밸런서별 리스너와 규칙을 조회한다.
//
// 한 로드 밸런서의 리스너나 한 리스너의 규칙 조회가 실패해도 이미 얻은 리스너는
// 보존한다. 규칙 조회 실패 시 DescribeListeners의 기본 동작을 관계 근거로 사용한다.
func (c listenerCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awselbv2.NewDescribeLoadBalancersPaginator(c.api, &awselbv2.DescribeLoadBalancersInput{})

	var (
		resources   []model.Resource
		partialErrs []error
	)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			partialErrs = append(partialErrs, fmt.Errorf("describe load balancers for listeners: %w", err))

			break
		}

		for _, loadBalancer := range page.LoadBalancers {
			loadBalancerARN := aws.ToString(loadBalancer.LoadBalancerArn)
			listeners, errs := loadListeners(ctx, c.api, req.Scope, loadBalancerARN)
			resources = append(resources, listeners...)
			partialErrs = append(partialErrs, errs...)

			if errors.Is(errors.Join(errs...), context.Canceled) {
				return resources, errors.Join(partialErrs...)
			}
		}
	}

	return resources, errors.Join(partialErrs...)
}

func loadListeners(
	ctx context.Context,
	api listenerAPI,
	scope collect.Scope,
	loadBalancerARN string,
) ([]model.Resource, []error) {
	paginator := awselbv2.NewDescribeListenersPaginator(api, &awselbv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(loadBalancerARN),
	})

	var (
		resources   []model.Resource
		partialErrs []error
	)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			partialErrs = append(partialErrs,
				fmt.Errorf("describe listeners (%s): %w", loadBalancerARN, err))

			break
		}

		for _, listener := range page.Listeners {
			listenerARN := aws.ToString(listener.ListenerArn)
			rules, err := loadRules(ctx, api, listenerARN)
			rulesLoaded := err == nil
			if err != nil {
				partialErrs = append(partialErrs, err)
			}

			resources = append(resources, listenerToResource(scope, listener, rules, rulesLoaded))
			if errors.Is(err, context.Canceled) {
				return resources, partialErrs
			}
		}
	}

	return resources, partialErrs
}

func loadRules(ctx context.Context, api listenerAPI, listenerARN string) ([]elbv2types.Rule, error) {
	paginator := awselbv2.NewDescribeRulesPaginator(api, &awselbv2.DescribeRulesInput{
		ListenerArn: aws.String(listenerARN),
	})

	var rules []elbv2types.Rule
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return rules, fmt.Errorf("describe listener rules (%s): %w", listenerARN, err)
		}
		rules = append(rules, page.Rules...)
	}

	return rules, nil
}

func listenerToResource(
	scope collect.Scope,
	listener elbv2types.Listener,
	rules []elbv2types.Rule,
	rulesLoaded bool,
) model.Resource {
	arn := aws.ToString(listener.ListenerArn)
	actions := actionTypeNames(listener.DefaultActions)
	ruleCount := "-"
	if rulesLoaded {
		ruleCount = strconv.Itoa(len(rules))
	}

	resource := model.Resource{
		Type:      model.TypeELBv2Listener,
		ID:        arn,
		Name:      listenerName(listener),
		ARN:       arn,
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Protocol", Value: displayString(string(listener.Protocol))},
			{Key: "Port", Value: displayInt32(aws.ToInt32(listener.Port))},
			{Key: "SslPolicy", Value: displayString(aws.ToString(listener.SslPolicy))},
			{Key: "DefaultActions", Value: displayString(strings.Join(actions, ", "))},
			{Key: "Rules", Value: ruleCount},
		},
	}

	loadBalancerARN := aws.ToString(listener.LoadBalancerArn)
	if loadBalancerARN != "" {
		resource.Related = append(resource.Related, model.Ref{
			Type:           model.TypeELBv2LoadBalancer,
			ID:             loadBalancerARN,
			IdentifierKind: model.IdentifierARN,
			Relation:       "LoadBalancerArn",
		})
	}

	for _, rule := range rules {
		resource.Related = append(resource.Related,
			actionRelations(rule.Actions, displayString(aws.ToString(rule.Priority)))...)
	}
	if !rulesLoaded {
		resource.Related = append(resource.Related,
			actionRelations(listener.DefaultActions, "default")...)
	}

	slices.SortFunc(resource.Related, compareRefs)
	resource.Related = deduplicateRefs(resource.Related)

	return resource
}

func listenerName(listener elbv2types.Listener) string {
	protocol := string(listener.Protocol)
	port := aws.ToInt32(listener.Port)
	if protocol == "" && port == 0 {
		return aws.ToString(listener.ListenerArn)
	}

	return fmt.Sprintf("%s:%d", protocol, port)
}

func actionTypeNames(actions []elbv2types.Action) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		name := string(action.Type)
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names
}

func actionRelations(actions []elbv2types.Action, via string) []model.Ref {
	var refs []model.Ref

	for _, action := range actions {
		if action.Type != elbv2types.ActionTypeEnumForward {
			continue
		}

		if arn := aws.ToString(action.TargetGroupArn); arn != "" {
			refs = append(refs, targetGroupRef(arn, via))
		}
		if action.ForwardConfig == nil {
			continue
		}
		for _, target := range action.ForwardConfig.TargetGroups {
			arn := aws.ToString(target.TargetGroupArn)
			if arn == "" {
				continue
			}
			targetVia := via
			if target.Weight != nil {
				targetVia += fmt.Sprintf(", weight=%d", aws.ToInt32(target.Weight))
			}
			refs = append(refs, targetGroupRef(arn, targetVia))
		}
	}

	return refs
}

func targetGroupRef(arn, via string) model.Ref {
	return model.Ref{
		Type:           model.TypeELBv2TargetGroup,
		ID:             arn,
		IdentifierKind: model.IdentifierARN,
		Relation:       "Actions.TargetGroupArn",
		Via:            via,
	}
}

func compareRefs(a, b model.Ref) int {
	if c := cmp.Compare(a.Relation, b.Relation); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Type, b.Type); c != 0 {
		return c
	}
	if c := cmp.Compare(a.IdentifierKind, b.IdentifierKind); c != 0 {
		return c
	}
	if c := cmp.Compare(a.ID, b.ID); c != 0 {
		return c
	}

	return cmp.Compare(a.Via, b.Via)
}

func deduplicateRefs(refs []model.Ref) []model.Ref {
	if len(refs) < 2 {
		return refs
	}

	out := refs[:1]
	for _, ref := range refs[1:] {
		if compareRefs(out[len(out)-1], ref) == 0 {
			continue
		}
		out = append(out, ref)
	}

	return out
}
