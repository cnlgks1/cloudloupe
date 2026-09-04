package eventbridge_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ebcollector "github.com/cnlgks1/cloudloupe/internal/collector/eventbridge"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeRule은 규칙 수집기가 쓰는 ListEventBuses·ListRules를 대신한다.
//
// buses는 ListEventBuses가 줄 버스 이름들, rulesByBus는 버스별 규칙, listRulesErr는 특정
// 버스의 ListRules만 실패시킨다.
type fakeRule struct {
	buses        []string
	rulesByBus   map[string][]ebtypes.Rule
	listRulesErr map[string]error
}

func (f *fakeRule) ListEventBuses(
	_ context.Context,
	_ *awseb.ListEventBusesInput,
	_ ...func(*awseb.Options),
) (*awseb.ListEventBusesOutput, error) {
	buses := make([]ebtypes.EventBus, 0, len(f.buses))
	for _, name := range f.buses {
		buses = append(buses, ebtypes.EventBus{Name: aws.String(name)})
	}

	return &awseb.ListEventBusesOutput{EventBuses: buses}, nil
}

func (f *fakeRule) ListRules(
	_ context.Context,
	in *awseb.ListRulesInput,
	_ ...func(*awseb.Options),
) (*awseb.ListRulesOutput, error) {
	bus := aws.ToString(in.EventBusName)
	if err, ok := f.listRulesErr[bus]; ok {
		return nil, err
	}

	return &awseb.ListRulesOutput{Rules: f.rulesByBus[bus]}, nil
}

func TestRuleCollectorType(t *testing.T) {
	t.Parallel()

	if got := ebcollector.NewRule(&fakeRule{}).Type(); got != model.TypeEventBridgeRule {
		t.Errorf("Type() = %q, want %q", got, model.TypeEventBridgeRule)
	}
}

// TestRuleCollectBuildsRelations는 규칙이 소속 버스·IAM 역할로 이어지는 관계를 만들고
// 값을 그대로 담는지 확인한다.
func TestRuleCollectBuildsRelations(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:events:ap-northeast-2:123456789012:rule/default/nightly"
	role := "arn:aws:iam::123456789012:role/eventInvokeRole"
	api := &fakeRule{
		buses: []string{"default"},
		rulesByBus: map[string][]ebtypes.Rule{
			"default": {{
				Name:               aws.String("nightly"),
				Arn:                aws.String(arn),
				State:              ebtypes.RuleStateEnabled,
				EventBusName:       aws.String("default"),
				ScheduleExpression: aws.String("cron(0 0 * * ? *)"),
				RoleArn:            aws.String(role),
			}},
		},
	}

	got, err := ebcollector.NewRule(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("규칙 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "nightly" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Status != "ENABLED" {
		t.Errorf("Status = %q, want ENABLED", res.Status)
	}
	if got, want := res.FieldValue("ScheduleExpression"), "cron(0 0 * * ? *)"; got != want {
		t.Errorf("ScheduleExpression = %q, want %q", got, want)
	}

	type rel struct {
		relation string
		typ      string
		id       string
	}
	gotRels := make([]rel, 0, len(res.Related))
	for _, r := range res.Related {
		gotRels = append(gotRels, rel{r.Relation, r.Type, r.ID})
	}
	want := []rel{
		{"EventBusName", model.TypeEventBridgeEventBus, "default"},
		{"RoleArn", model.TypeIAMRole, role},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 = %+v, want %+v", gotRels, want)
	}
}

// TestRuleCollectWithoutRoleHasBusRelationOnly는 대상 호출 역할이 없으면 버스 관계만
// 만드는지 확인한다.
func TestRuleCollectWithoutRoleHasBusRelationOnly(t *testing.T) {
	t.Parallel()

	api := &fakeRule{
		buses: []string{"default"},
		rulesByBus: map[string][]ebtypes.Rule{
			"default": {{Name: aws.String("r"), EventBusName: aws.String("default")}},
		},
	}

	got, err := ebcollector.NewRule(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got[0].Related) != 1 || got[0].Related[0].Relation != "EventBusName" {
		t.Errorf("관계 = %+v, want EventBusName 하나", got[0].Related)
	}
}

// TestRuleCollectKeepsOtherBusesOnListFailure는 한 버스의 ListRules가 실패해도 다른 버스의
// 규칙은 살리는지 확인한다.
func TestRuleCollectKeepsOtherBusesOnListFailure(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeRule{
		buses: []string{"good", "bad"},
		rulesByBus: map[string][]ebtypes.Rule{
			"good": {{Name: aws.String("r-ok"), EventBusName: aws.String("good")}},
		},
		listRulesErr: map[string]error{"bad": denied},
	}

	got, err := ebcollector.NewRule(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "r-ok" {
		t.Errorf("수집 결과 = %+v, want r-ok 하나", got)
	}
}
