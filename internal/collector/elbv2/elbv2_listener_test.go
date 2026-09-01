package elbv2_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/elbv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

type fakeListenerAPI struct {
	loadBalancerPages []*awselbv2.DescribeLoadBalancersOutput
	loadBalancerCalls int
	listenerPages     map[string][]*awselbv2.DescribeListenersOutput
	listenerCalls     map[string]int
	listenerErrs      map[string]error
	rulePages         map[string][]*awselbv2.DescribeRulesOutput
	ruleCalls         map[string]int
	ruleErrs          map[string]error
}

func (f *fakeListenerAPI) DescribeLoadBalancers(_ context.Context, _ *awselbv2.DescribeLoadBalancersInput, _ ...func(*awselbv2.Options)) (*awselbv2.DescribeLoadBalancersOutput, error) {
	page := f.loadBalancerPages[f.loadBalancerCalls]
	f.loadBalancerCalls++

	return page, nil
}

func (f *fakeListenerAPI) DescribeListeners(_ context.Context, in *awselbv2.DescribeListenersInput, _ ...func(*awselbv2.Options)) (*awselbv2.DescribeListenersOutput, error) {
	arn := aws.ToString(in.LoadBalancerArn)
	if err := f.listenerErrs[arn]; err != nil {
		return nil, err
	}
	call := f.listenerCalls[arn]
	f.listenerCalls[arn] = call + 1

	return f.listenerPages[arn][call], nil
}

func (f *fakeListenerAPI) DescribeRules(_ context.Context, in *awselbv2.DescribeRulesInput, _ ...func(*awselbv2.Options)) (*awselbv2.DescribeRulesOutput, error) {
	arn := aws.ToString(in.ListenerArn)
	if err := f.ruleErrs[arn]; err != nil {
		return nil, err
	}
	call := f.ruleCalls[arn]
	f.ruleCalls[arn] = call + 1

	return f.rulePages[arn][call], nil
}

func newFakeListenerAPI() *fakeListenerAPI {
	return &fakeListenerAPI{
		listenerPages: make(map[string][]*awselbv2.DescribeListenersOutput),
		listenerCalls: make(map[string]int),
		listenerErrs:  make(map[string]error),
		rulePages:     make(map[string][]*awselbv2.DescribeRulesOutput),
		ruleCalls:     make(map[string]int),
		ruleErrs:      make(map[string]error),
	}
}

func TestELBv2ListenerCollectorBuildsRuleRelations(t *testing.T) {
	t.Parallel()

	const (
		lbARN       = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:loadbalancer/app/web/abc"
		listenerARN = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:listener/app/web/abc/def"
		tgOneARN    = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/one/111"
		tgTwoARN    = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/two/222"
		tgThreeARN  = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/three/333"
		fallbackARN = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/fallback/444"
	)

	api := newFakeListenerAPI()
	api.loadBalancerPages = []*awselbv2.DescribeLoadBalancersOutput{{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(lbARN)}},
	}}
	api.listenerPages[lbARN] = []*awselbv2.DescribeListenersOutput{{
		Listeners: []elbv2types.Listener{{
			ListenerArn:     aws.String(listenerARN),
			LoadBalancerArn: aws.String(lbARN),
			Protocol:        elbv2types.ProtocolEnumHttps,
			Port:            aws.Int32(443),
			SslPolicy:       aws.String("ELBSecurityPolicy-TLS13-1-2-2021-06"),
			DefaultActions: []elbv2types.Action{{
				Type: elbv2types.ActionTypeEnumForward, TargetGroupArn: aws.String(fallbackARN),
			}},
		}},
		NextMarker: aws.String("page-2"),
	}, {}}
	api.rulePages[listenerARN] = []*awselbv2.DescribeRulesOutput{{
		Rules: []elbv2types.Rule{
			{
				Priority: aws.String("default"),
				Actions:  []elbv2types.Action{{Type: elbv2types.ActionTypeEnumForward, TargetGroupArn: aws.String(tgOneARN)}},
			},
			{
				Priority: aws.String("10"),
				Actions: []elbv2types.Action{{
					Type: elbv2types.ActionTypeEnumForward,
					ForwardConfig: &elbv2types.ForwardActionConfig{TargetGroups: []elbv2types.TargetGroupTuple{
						{TargetGroupArn: aws.String(tgTwoARN), Weight: aws.Int32(80)},
						{TargetGroupArn: aws.String(tgThreeARN), Weight: aws.Int32(20)},
					}},
				}},
			},
		},
	}}

	resources, err := elbv2.NewListener(api).Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Resources = %+v, want 1", resources)
	}

	listener := resources[0]
	if listener.Type != model.TypeELBv2Listener || listener.ID != listenerARN || listener.Name != "HTTPS:443" {
		t.Errorf("Listener = %+v", listener)
	}
	if listener.FieldValue("규칙 수") != "2" || listener.FieldValue("SSL 정책") == "-" {
		t.Errorf("Fields = %+v", listener.Fields)
	}
	if api.listenerCalls[lbARN] != 2 {
		t.Errorf("DescribeListeners 호출 = %d, want 2", api.listenerCalls[lbARN])
	}

	listenerOf := listener.RelatedBy(model.RelationListenerOf)
	if len(listenerOf) != 1 || listenerOf[0].ID != lbARN || listenerOf[0].IdentifierKind != model.IdentifierARN {
		t.Errorf("listener-of = %+v", listenerOf)
	}
	forward := listener.RelatedBy(model.RelationForwardsTo)
	gotARNs := make([]string, 0, len(forward))
	for _, ref := range forward {
		gotARNs = append(gotARNs, ref.ID)
		if ref.IdentifierKind != model.IdentifierARN {
			t.Errorf("ARN 관계 kind = %q", ref.IdentifierKind)
		}
	}
	wantARNs := []string{tgOneARN, tgThreeARN, tgTwoARN}
	slices.Sort(wantARNs)
	if !slices.Equal(gotARNs, wantARNs) {
		t.Errorf("TG 관계 = %v, want %v", gotARNs, wantARNs)
	}
	if slices.Contains(gotARNs, fallbackARN) {
		t.Error("규칙 조회 성공인데 기본 동작 fallback 관계를 사용함")
	}
}

func TestELBv2ListenerCollectorKeepsDefaultRelationWhenRulesFail(t *testing.T) {
	t.Parallel()

	const (
		lbARN       = "arn:lb"
		listenerARN = "arn:listener"
		tgARN       = "arn:target-group"
	)
	api := newFakeListenerAPI()
	api.loadBalancerPages = []*awselbv2.DescribeLoadBalancersOutput{{
		LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerArn: aws.String(lbARN)}},
	}}
	api.listenerPages[lbARN] = []*awselbv2.DescribeListenersOutput{{
		Listeners: []elbv2types.Listener{{
			ListenerArn: aws.String(listenerARN), LoadBalancerArn: aws.String(lbARN),
			DefaultActions: []elbv2types.Action{{Type: elbv2types.ActionTypeEnumForward, TargetGroupArn: aws.String(tgARN)}},
		}},
	}}
	api.ruleErrs[listenerARN] = errors.New("AccessDenied")

	resources, err := elbv2.NewListener(api).Collect(context.Background(), collect.Request{})
	if err == nil {
		t.Fatal("규칙 조회 실패가 부분 오류로 반환되어야 함")
	}
	if len(resources) != 1 {
		t.Fatalf("Listener는 보존되어야 함: %+v", resources)
	}
	forward := resources[0].RelatedBy(model.RelationForwardsTo)
	if len(forward) != 1 || forward[0].ID != tgARN || forward[0].Via != "default" {
		t.Errorf("기본 동작 fallback = %+v", forward)
	}
	if resources[0].FieldValue("규칙 수") != "-" {
		t.Errorf("규칙 수 = %q, want -", resources[0].FieldValue("규칙 수"))
	}
}

func TestELBv2ListenerCollectorKeepsOtherLoadBalancersOnFailure(t *testing.T) {
	t.Parallel()

	const failedARN = "arn:failed-lb"
	const successARN = "arn:success-lb"
	api := newFakeListenerAPI()
	api.loadBalancerPages = []*awselbv2.DescribeLoadBalancersOutput{{
		LoadBalancers: []elbv2types.LoadBalancer{
			{LoadBalancerArn: aws.String(failedARN)},
			{LoadBalancerArn: aws.String(successARN)},
		},
	}}
	api.listenerErrs[failedARN] = errors.New("AccessDenied")
	api.listenerPages[successARN] = []*awselbv2.DescribeListenersOutput{{
		Listeners: []elbv2types.Listener{{ListenerArn: aws.String("arn:listener"), LoadBalancerArn: aws.String(successARN)}},
	}}
	api.rulePages["arn:listener"] = []*awselbv2.DescribeRulesOutput{{}}

	resources, err := elbv2.NewListener(api).Collect(context.Background(), collect.Request{})
	if err == nil {
		t.Fatal("한 LB의 리스너 실패가 부분 오류로 반환되어야 함")
	}
	if len(resources) != 1 || resources[0].ID != "arn:listener" {
		t.Errorf("다른 LB Listener가 보존되지 않음: %+v", resources)
	}
}

func TestELBv2ListenerCollectorType(t *testing.T) {
	t.Parallel()

	collector := elbv2.NewListener(newFakeListenerAPI())
	if collector.Type() != model.TypeELBv2Listener {
		t.Errorf("Type() = %q, want %q", collector.Type(), model.TypeELBv2Listener)
	}
}
