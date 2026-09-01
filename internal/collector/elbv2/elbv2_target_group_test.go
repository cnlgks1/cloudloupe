package elbv2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/elbv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeTargetGroupAPI는 targetGroupAPI(메서드 2개)를 만족하는 테스트 대역이다.
type fakeTargetGroupAPI struct {
	groupPages  []*awselbv2.DescribeTargetGroupsOutput
	groupCalls  int
	health      map[string]*awselbv2.DescribeTargetHealthOutput // ARN -> 헬스 응답
	groupErr    error
	healthErr   error
	healthCalls int
}

func (f *fakeTargetGroupAPI) DescribeTargetGroups(_ context.Context, _ *awselbv2.DescribeTargetGroupsInput, _ ...func(*awselbv2.Options)) (*awselbv2.DescribeTargetGroupsOutput, error) {
	if f.groupErr != nil {
		return nil, f.groupErr
	}

	page := f.groupPages[f.groupCalls]
	f.groupCalls++

	return page, nil
}

func (f *fakeTargetGroupAPI) DescribeTargetHealth(_ context.Context, in *awselbv2.DescribeTargetHealthInput, _ ...func(*awselbv2.Options)) (*awselbv2.DescribeTargetHealthOutput, error) {
	f.healthCalls++

	if f.healthErr != nil {
		return nil, f.healthErr
	}

	if out, ok := f.health[aws.ToString(in.TargetGroupArn)]; ok {
		return out, nil
	}

	return &awselbv2.DescribeTargetHealthOutput{}, nil
}

func TestELBv2TargetGroupCollectorConvertsFieldsAndTargets(t *testing.T) {
	t.Parallel()

	const arn = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web-tg/abc123"

	api := &fakeTargetGroupAPI{
		groupPages: []*awselbv2.DescribeTargetGroupsOutput{{
			TargetGroups: []elbv2types.TargetGroup{{
				TargetGroupName:  aws.String("web-tg"),
				TargetGroupArn:   aws.String(arn),
				Protocol:         elbv2types.ProtocolEnumHttp,
				Port:             aws.Int32(80),
				TargetType:       elbv2types.TargetTypeEnumInstance,
				VpcId:            aws.String("vpc-1"),
				LoadBalancerArns: []string{"arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:loadbalancer/app/web-alb/def456"},
			}},
		}},
		health: map[string]*awselbv2.DescribeTargetHealthOutput{
			arn: {TargetHealthDescriptions: []elbv2types.TargetHealthDescription{
				{Target: &elbv2types.TargetDescription{Id: aws.String("i-0a1b")}, TargetHealth: &elbv2types.TargetHealth{State: elbv2types.TargetHealthStateEnumHealthy}},
				{Target: &elbv2types.TargetDescription{Id: aws.String("i-0c2d")}, TargetHealth: &elbv2types.TargetHealth{State: elbv2types.TargetHealthStateEnumUnhealthy}},
			}},
		},
	}

	c := elbv2.NewTargetGroup(api)

	got, err := c.Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	r := got[0]

	if r.Type != model.TypeELBv2TargetGroup || r.ID != "web-tg" {
		t.Errorf("Type/ID = %q/%q", r.Type, r.ID)
	}

	if got := r.FieldValue("프로토콜"); got != "HTTP" {
		t.Errorf("프로토콜 = %q, want HTTP", got)
	}

	if got := r.FieldValue("포트"); got != "80" {
		t.Errorf("포트 = %q, want 80", got)
	}

	if got := r.FieldValue("타깃 수"); got != "2" {
		t.Errorf("타깃 수 = %q, want 2", got)
	}

	// 로드밸런서로의 forwards-to 관계는 ARN을 파싱하지 않고 그대로 보존한다.
	fwd := r.RelatedBy(model.RelationForwardsTo)
	if len(fwd) != 1 ||
		fwd[0].ID != "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:loadbalancer/app/web-alb/def456" ||
		fwd[0].IdentifierKind != model.IdentifierARN {
		t.Errorf("forwards-to LB 관계가 없거나 ARN이 틀렸다: %+v", r.Related)
	}

	// 타깃으로의 targets 관계 2개, 헬스 상태가 Via에 담겨야 한다.
	targets := r.RelatedBy(model.RelationTargets)
	if len(targets) != 2 {
		t.Fatalf("targets 관계 2개여야 한다, got %d: %+v", len(targets), r.Related)
	}

	if targets[0].Via != "healthy" {
		t.Errorf("첫 타깃 헬스 = %q, want healthy", targets[0].Via)
	}
}

func TestELBv2TargetGroupCollectorSurvivesHealthError(t *testing.T) {
	t.Parallel()

	// 타깃 헬스 조회가 실패해도 그룹 자체는 살아야 한다(부분 실패는 전체 실패가 아니다).
	api := &fakeTargetGroupAPI{
		groupPages: []*awselbv2.DescribeTargetGroupsOutput{{
			TargetGroups: []elbv2types.TargetGroup{{
				TargetGroupName: aws.String("tg-1"),
				TargetGroupArn:  aws.String("arn:...:targetgroup/tg-1/x"),
			}},
		}},
		healthErr: errors.New("AccessDenied"),
	}

	c := elbv2.NewTargetGroup(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("헬스 실패가 부분 오류로 반환되어야 한다")
	}

	if len(got) != 1 {
		t.Fatalf("그룹은 살아야 한다, got %d", len(got))
	}

	if len(got[0].RelatedBy(model.RelationTargets)) != 0 {
		t.Errorf("헬스 실패 시 타깃 관계는 비어야 한다: %+v", got[0].Related)
	}
}

func TestELBv2TargetGroupCollectorWrapsGroupError(t *testing.T) {
	t.Parallel()

	api := &fakeTargetGroupAPI{groupErr: errors.New("AccessDenied")}
	c := elbv2.NewTargetGroup(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("그룹 조회 실패는 에러여야 한다")
	}

	if got := err.Error(); got == "AccessDenied" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestELBv2TargetGroupCollectorType(t *testing.T) {
	t.Parallel()

	c := elbv2.NewTargetGroup(&fakeTargetGroupAPI{})
	if c.Type() != model.TypeELBv2TargetGroup {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeELBv2TargetGroup)
	}
}

func TestELBv2TargetGroupCollectorDoesNotMisclassifyIPTargets(t *testing.T) {
	t.Parallel()

	const arn = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/ip-tg/abc"
	api := &fakeTargetGroupAPI{
		groupPages: []*awselbv2.DescribeTargetGroupsOutput{{
			TargetGroups: []elbv2types.TargetGroup{{
				TargetGroupName: aws.String("ip-tg"),
				TargetGroupArn:  aws.String(arn),
				TargetType:      elbv2types.TargetTypeEnumIp,
			}},
		}},
		health: map[string]*awselbv2.DescribeTargetHealthOutput{
			arn: {TargetHealthDescriptions: []elbv2types.TargetHealthDescription{{
				Target: &elbv2types.TargetDescription{Id: aws.String("10.0.1.10")},
			}}},
		},
	}

	resources, err := elbv2.NewTargetGroup(api).Collect(context.Background(), collect.Request{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Resources = %+v, want 1", resources)
	}
	if targets := resources[0].RelatedBy(model.RelationTargets); len(targets) != 0 {
		t.Errorf("IP 타깃을 지원되지 않는 리소스 타입에 연결함: %+v", targets)
	}
}
