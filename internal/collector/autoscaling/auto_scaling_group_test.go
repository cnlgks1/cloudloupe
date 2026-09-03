package autoscaling_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	autoscalingcollector "github.com/cnlgks1/cloudloupe/internal/collector/autoscaling"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeDescribeGroups는 describeAutoScalingGroupsAPI를 만족하는 테스트 대역이다.
//
// errs는 호출 차수별 오류다. 두 번째 페이지만 실패시켜 부분 결과 보존을 확인하는 데 쓴다.
type fakeDescribeGroups struct {
	pages []*awsautoscaling.DescribeAutoScalingGroupsOutput
	errs  []error
	calls int
}

func (f *fakeDescribeGroups) DescribeAutoScalingGroups(
	_ context.Context,
	_ *awsautoscaling.DescribeAutoScalingGroupsInput,
	_ ...func(*awsautoscaling.Options),
) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

// singleGroup은 그룹 하나를 담은 응답을 만든다.
func singleGroup(group autoscalingtypes.AutoScalingGroup) *fakeDescribeGroups {
	return &fakeDescribeGroups{pages: []*awsautoscaling.DescribeAutoScalingGroupsOutput{{
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{group},
	}}}
}

func collectOne(t *testing.T, api *fakeDescribeGroups) model.Resource {
	t.Helper()

	got, err := autoscalingcollector.NewGroup(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	return got[0]
}

func TestGroupCollectorType(t *testing.T) {
	t.Parallel()

	if got := autoscalingcollector.NewGroup(&fakeDescribeGroups{}).Type(); got != model.TypeAutoScalingGroup {
		t.Errorf("Type() = %q, want %q", got, model.TypeAutoScalingGroup)
	}
}

// TestGroupCollectorConvertsFields는 SDK 응답이 표시 필드·태그·관계로 옮겨지는지 확인한다.
func TestGroupCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	got := collectOne(t, singleGroup(autoscalingtypes.AutoScalingGroup{
		AutoScalingGroupName: aws.String("web-asg"),
		AutoScalingGroupARN:  aws.String("arn:aws:autoscaling:ap-northeast-2:123456789012:autoScalingGroup:1:autoScalingGroupName/web-asg"),
		Status:               nil,
		CreatedTime:          &created,
		MinSize:              aws.Int32(2),
		MaxSize:              aws.Int32(6),
		DesiredCapacity:      aws.Int32(3),
		AvailabilityZones:    []string{"ap-northeast-2a", "ap-northeast-2c"},
		HealthCheckType:      aws.String("ELB"),
		VPCZoneIdentifier:    aws.String("subnet-a,subnet-c"),
		TargetGroupARNs: []string{
			"arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web/abc",
		},
		Instances: []autoscalingtypes.Instance{
			{InstanceId: aws.String("i-0a1b")},
			{InstanceId: aws.String("i-0c2d")},
		},
		Tags: []autoscalingtypes.TagDescription{
			{Key: aws.String("Name"), Value: aws.String("web")},
			{Key: aws.String("Environment"), Value: aws.String("production")},
		},
	}))

	if got.Type != model.TypeAutoScalingGroup || got.ID != "web-asg" || got.Name != "web-asg" {
		t.Errorf("기본 식별 정보 = %+v", got)
	}
	if got.CreatedAt == nil || !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	for _, want := range []model.Field{
		{Key: "MinSize", Value: "2"},
		{Key: "MaxSize", Value: "6"},
		{Key: "DesiredCapacity", Value: "3"},
		{Key: "AvailabilityZones", Value: "ap-northeast-2a, ap-northeast-2c"},
		{Key: "HealthCheckType", Value: "ELB"},
		{Key: "Instances", Value: "i-0a1b, i-0c2d"},
		{Key: "VPCZoneIdentifier", Value: "subnet-a,subnet-c"},
	} {
		if value := got.FieldValue(want.Key); value != want.Value {
			t.Errorf("%s = %q, want %q", want.Key, value, want.Value)
		}
	}

	wantTags := []model.Field{
		{Key: "Environment", Value: "production"},
		{Key: "Name", Value: "web"},
	}
	if !slices.Equal(got.Tags, wantTags) {
		t.Errorf("Tags = %+v, want %+v", got.Tags, wantTags)
	}

	// VPCZoneIdentifier는 쉼표로 이어진 문자열이므로 서브넷마다 관계를 나눠야 한다.
	wantRefs := []model.Ref{
		{Type: model.TypeEC2Instance, ID: "i-0a1b", Relation: "Instances.InstanceId"},
		{Type: model.TypeEC2Instance, ID: "i-0c2d", Relation: "Instances.InstanceId"},
		{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: "VPCZoneIdentifier"},
		{Type: model.TypeEC2Subnet, ID: "subnet-c", Relation: "VPCZoneIdentifier"},
		{
			Type:           model.TypeELBv2TargetGroup,
			ID:             "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web/abc",
			IdentifierKind: model.IdentifierARN,
			Relation:       "TargetGroupARNs",
		},
	}
	if !slices.Equal(got.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", got.Related, wantRefs)
	}
}

// TestGroupCollectorShowsEveryLaunchShape는 시작 구성 세 형태를 모두 보존하는지 확인한다.
//
// SDK는 최상위 LaunchTemplate, MixedInstancesPolicy, LaunchConfigurationName을 각각 따로
// 준다. 최상위 LaunchTemplate만 보면 나머지 두 형태로 만든 그룹은 시작 구성이 있는데도
// 화면에 "-"로 나온다.
func TestGroupCollectorShowsEveryLaunchShape(t *testing.T) {
	t.Parallel()

	t.Run("시작 템플릿", func(t *testing.T) {
		t.Parallel()

		got := collectOne(t, singleGroup(autoscalingtypes.AutoScalingGroup{
			AutoScalingGroupName: aws.String("template-asg"),
			LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
				LaunchTemplateId:   aws.String("lt-0123"),
				LaunchTemplateName: aws.String("web-lt"),
				Version:            aws.String("$Latest"),
			},
		}))

		// ID와 이름을 모두 남긴다. 둘 중 하나만 남기면 콘솔·CLI와 대조할 때 부족하다.
		value := got.FieldValue("LaunchTemplate")
		for _, want := range []string{"lt-0123", "web-lt", "$Latest"} {
			if !strings.Contains(value, want) {
				t.Errorf("LaunchTemplate = %q, want %q 포함", value, want)
			}
		}
		if v := got.FieldValue("LaunchConfigurationName"); v != "-" {
			t.Errorf("LaunchConfigurationName = %q, want %q", v, "-")
		}
	})

	t.Run("시작 구성", func(t *testing.T) {
		t.Parallel()

		got := collectOne(t, singleGroup(autoscalingtypes.AutoScalingGroup{
			AutoScalingGroupName:    aws.String("legacy-asg"),
			LaunchConfigurationName: aws.String("web-lc"),
		}))

		if v := got.FieldValue("LaunchConfigurationName"); v != "web-lc" {
			t.Errorf("LaunchConfigurationName = %q, want %q", v, "web-lc")
		}
		if v := got.FieldValue("LaunchTemplate"); v != "-" {
			t.Errorf("LaunchTemplate = %q, want %q", v, "-")
		}
	})

	t.Run("혼합 인스턴스 정책", func(t *testing.T) {
		t.Parallel()

		got := collectOne(t, singleGroup(autoscalingtypes.AutoScalingGroup{
			AutoScalingGroupName: aws.String("mixed-asg"),
			MixedInstancesPolicy: &autoscalingtypes.MixedInstancesPolicy{
				LaunchTemplate: &autoscalingtypes.LaunchTemplate{
					LaunchTemplateSpecification: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateName: aws.String("mixed-lt"),
						Version:            aws.String("3"),
					},
					Overrides: []autoscalingtypes.LaunchTemplateOverrides{
						{InstanceType: aws.String("m6i.large")},
						{InstanceType: aws.String("m6a.large")},
					},
				},
			},
		}))

		value := got.FieldValue("MixedInstancesPolicy")
		for _, want := range []string{"mixed-lt", "3", "Overrides=2"} {
			if !strings.Contains(value, want) {
				t.Errorf("MixedInstancesPolicy = %q, want %q 포함", value, want)
			}
		}
		// 최상위 LaunchTemplate은 비어 있어야 한다. 혼합 정책과 섞어 보여주면 어느 쪽으로
		// 만든 그룹인지 알 수 없다.
		if v := got.FieldValue("LaunchTemplate"); v != "-" {
			t.Errorf("LaunchTemplate = %q, want %q", v, "-")
		}
	})
}

// TestGroupCollectorDistinguishesMissingGracePeriod는 응답에 없는 값과 실제 0초를
// 구분하는지 확인한다.
//
// HealthCheckGracePeriod는 선택 항목이다. nil을 0으로 표시하면 "유예 기간을 0으로
// 설정했다"로 읽혀서 헬스 체크 설정을 잘못 판단하게 된다.
func TestGroupCollectorDistinguishesMissingGracePeriod(t *testing.T) {
	t.Parallel()

	api := &fakeDescribeGroups{pages: []*awsautoscaling.DescribeAutoScalingGroupsOutput{{
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
			{AutoScalingGroupName: aws.String("absent")},
			{AutoScalingGroupName: aws.String("explicit-zero"), HealthCheckGracePeriod: aws.Int32(0)},
		},
	}}}

	got, err := autoscalingcollector.NewGroup(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if value := got[0].FieldValue("HealthCheckGracePeriod"); value != "-" {
		t.Errorf("값이 없는 HealthCheckGracePeriod = %q, want %q", value, "-")
	}
	if value := got[1].FieldValue("HealthCheckGracePeriod"); value != "0" {
		t.Errorf("명시된 0의 HealthCheckGracePeriod = %q, want %q", value, "0")
	}
}

// TestGroupCollectorSkipsClassicLoadBalancers는 Classic Load Balancer를 ELBv2 관계로
// 잘못 연결하지 않는지 확인한다.
//
// LoadBalancerNames는 Classic Load Balancer의 이름이고 cloudloupe에는 대응하는 타입이 없다.
// ELBv2 타깃 그룹으로 이으면 해석되지 않는 끊긴 관계가 생긴다.
func TestGroupCollectorSkipsClassicLoadBalancers(t *testing.T) {
	t.Parallel()

	got := collectOne(t, singleGroup(autoscalingtypes.AutoScalingGroup{
		AutoScalingGroupName: aws.String("classic-asg"),
		LoadBalancerNames:    []string{"legacy-elb"},
	}))

	for _, ref := range got.Related {
		if ref.ID == "legacy-elb" {
			t.Errorf("Classic Load Balancer가 관계로 들어갔다: %+v", ref)
		}
	}
}

func TestGroupCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeDescribeGroups{pages: []*awsautoscaling.DescribeAutoScalingGroupsOutput{
		{
			AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
				{AutoScalingGroupName: aws.String("asg-1")},
			},
			NextToken: aws.String("page2"),
		},
		{
			AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
				{AutoScalingGroupName: aws.String("asg-2")},
			},
		},
	}}

	got, err := autoscalingcollector.NewGroup(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 || api.calls != 2 {
		t.Errorf("그룹 %d개(호출 %d회), want 2개(2회)", len(got), api.calls)
	}
}

// TestGroupCollectorKeepsPartialResultsOnPaginationError는 페이지 중간 실패에도 앞 페이지
// 결과를 살리는지 확인한다.
func TestGroupCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("Throttling")
	api := &fakeDescribeGroups{
		pages: []*awsautoscaling.DescribeAutoScalingGroupsOutput{{
			AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
				{AutoScalingGroupName: aws.String("asg-1")},
			},
			NextToken: aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := autoscalingcollector.NewGroup(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "asg-1" {
		t.Errorf("부분 결과 = %+v, want asg-1", got)
	}
}

func TestGroupCollectorStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeDescribeGroups{errs: []error{context.Canceled}}
	if _, err := autoscalingcollector.NewGroup(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
