package elbv2_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/elbv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeLoadBalancersAPI는 describeLoadBalancersAPI를 만족하는 테스트 대역이다.
type fakeLoadBalancersAPI struct {
	pages []*awselbv2.DescribeLoadBalancersOutput
	err   error
	calls int
}

func (f *fakeLoadBalancersAPI) DescribeLoadBalancers(_ context.Context, _ *awselbv2.DescribeLoadBalancersInput, _ ...func(*awselbv2.Options)) (*awselbv2.DescribeLoadBalancersOutput, error) {
	if f.err != nil {
		return nil, f.err
	}

	page := f.pages[f.calls]
	f.calls++

	return page, nil
}

func TestELBv2LoadBalancerCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, time.March, 11, 2, 51, 19, 0, time.UTC)

	api := &fakeLoadBalancersAPI{pages: []*awselbv2.DescribeLoadBalancersOutput{{
		LoadBalancers: []elbv2types.LoadBalancer{{
			LoadBalancerName: aws.String("web-alb"),
			LoadBalancerArn:  aws.String("arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:loadbalancer/app/web-alb/abc123"),
			DNSName:          aws.String("web-alb-123.ap-northeast-2.elb.amazonaws.com"),
			Type:             elbv2types.LoadBalancerTypeEnumApplication,
			Scheme:           elbv2types.LoadBalancerSchemeEnumInternetFacing,
			VpcId:            aws.String("vpc-1"),
			CreatedTime:      aws.Time(created),
			State:            &elbv2types.LoadBalancerState{Code: elbv2types.LoadBalancerStateEnumActive},
			AvailabilityZones: []elbv2types.AvailabilityZone{
				{ZoneName: aws.String("ap-northeast-2a")},
				{ZoneName: aws.String("ap-northeast-2c")},
			},
		}},
	}}}

	c := elbv2.NewLoadBalancer(api)

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

	if r.Type != model.TypeELBv2LoadBalancer {
		t.Errorf("Type = %q, want %q", r.Type, model.TypeELBv2LoadBalancer)
	}

	if r.ID != "web-alb" || r.Name != "web-alb" {
		t.Errorf("ID/Name = %q/%q, want web-alb", r.ID, r.Name)
	}

	if r.ARN == "" {
		t.Error("ARN이 비었다")
	}
	if len(r.Identifiers) != 2 ||
		r.Identifiers[0] != (model.Identifier{Kind: model.IdentifierDNS, Value: "web-alb-123.ap-northeast-2.elb.amazonaws.com"}) ||
		r.Identifiers[1] != (model.Identifier{Kind: model.IdentifierDNS, Value: "dualstack.web-alb-123.ap-northeast-2.elb.amazonaws.com"}) {
		t.Errorf("DNS 식별자 = %+v", r.Identifiers)
	}

	if r.Status != "active" {
		t.Errorf("Status = %q, want active", r.Status)
	}

	if r.CreatedAt == nil || !r.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", r.CreatedAt, created)
	}

	if got := r.FieldValue("종류"); got != "application" {
		t.Errorf("종류 = %q, want application", got)
	}

	if got := r.FieldValue("스킴"); got != "internet-facing" {
		t.Errorf("스킴 = %q, want internet-facing", got)
	}

	if got := r.FieldValue("가용 영역"); got != "2" {
		t.Errorf("가용 영역 = %q, want 2", got)
	}
}

func TestELBv2LoadBalancerCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeLoadBalancersAPI{pages: []*awselbv2.DescribeLoadBalancersOutput{
		{
			LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerName: aws.String("alb-1")}},
			NextMarker:    aws.String("page2"),
		},
		{
			LoadBalancers: []elbv2types.LoadBalancer{{LoadBalancerName: aws.String("alb-2")}},
		},
	}}

	c := elbv2.NewLoadBalancer(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 2 || api.calls != 2 {
		t.Errorf("두 페이지에서 LB 2개(호출 2회)여야 한다, got %d개 호출 %d회", len(got), api.calls)
	}
}

func TestELBv2LoadBalancerCollectorWrapsError(t *testing.T) {
	t.Parallel()

	api := &fakeLoadBalancersAPI{err: errors.New("AccessDenied")}
	c := elbv2.NewLoadBalancer(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("에러가 반환되어야 한다")
	}

	if got := err.Error(); got == "AccessDenied" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestELBv2LoadBalancerCollectorType(t *testing.T) {
	t.Parallel()

	c := elbv2.NewLoadBalancer(&fakeLoadBalancersAPI{})
	if c.Type() != model.TypeELBv2LoadBalancer {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeELBv2LoadBalancer)
	}
}
