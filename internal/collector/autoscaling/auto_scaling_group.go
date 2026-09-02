package autoscaling

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeAutoScalingGroupsAPI는 Auto Scaling Group 조회에 필요한 최소 API다.
type describeAutoScalingGroupsAPI interface {
	DescribeAutoScalingGroups(context.Context, *awsautoscaling.DescribeAutoScalingGroupsInput, ...func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error)
}

// groupCollector는 EC2 Auto Scaling Group을 조회한다.
type groupCollector struct {
	api describeAutoScalingGroupsAPI
}

// NewGroup은 EC2 Auto Scaling Group 수집기를 만든다.
func NewGroup(api describeAutoScalingGroupsAPI) collect.Collector {
	return groupCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c groupCollector) Type() string { return model.TypeAutoScalingGroup }

// Collect는 범위 안의 EC2 Auto Scaling Group을 모두 조회해 도메인 리소스로 변환한다.
//
// 페이지 조회가 중간에 실패하면 이미 변환한 리소스와 오류를 함께 반환한다.
func (c groupCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsautoscaling.NewDescribeAutoScalingGroupsPaginator(c.api, &awsautoscaling.DescribeAutoScalingGroupsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe auto scaling groups: %w", err)
		}

		for i := range page.AutoScalingGroups {
			out = append(out, groupToResource(req.Scope, page.AutoScalingGroups[i]))
		}
	}

	return out, nil
}

// groupToResource는 SDK Auto Scaling Group을 도메인 리소스로 변환한다.
func groupToResource(scope collect.Scope, group autoscalingtypes.AutoScalingGroup) model.Resource {
	name := aws.ToString(group.AutoScalingGroupName)
	r := model.Resource{
		Type:      model.TypeAutoScalingGroup,
		ID:        name,
		Name:      name,
		ARN:       aws.ToString(group.AutoScalingGroupARN),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(group.Status),
		Fields: []model.Field{
			{Key: "MinSize", Value: int32Value(group.MinSize)},
			{Key: "MaxSize", Value: int32Value(group.MaxSize)},
			{Key: "DesiredCapacity", Value: int32Value(group.DesiredCapacity)},
			{Key: "AvailabilityZones", Value: displayString(strings.Join(group.AvailabilityZones, ", "))},
			{Key: "HealthCheckType", Value: displayString(aws.ToString(group.HealthCheckType))},
			{Key: "HealthCheckGracePeriod", Value: int32Value(group.HealthCheckGracePeriod)},
			{Key: "LaunchConfigurationName", Value: displayString(aws.ToString(group.LaunchConfigurationName))},
			{Key: "LaunchTemplate", Value: launchTemplateValue(group.LaunchTemplate)},
			{Key: "MixedInstancesPolicy", Value: mixedInstancesPolicyValue(group.MixedInstancesPolicy)},
			{Key: "Instances", Value: displayString(strings.Join(instanceIDs(group.Instances), ", "))},
			{Key: "TargetGroupARNs", Value: displayString(strings.Join(group.TargetGroupARNs, ", "))},
			{Key: "VPCZoneIdentifier", Value: displayString(aws.ToString(group.VPCZoneIdentifier))},
		},
		Tags:    groupTags(group.Tags),
		Related: groupRelations(group),
	}

	if group.CreatedTime != nil {
		createdAt := group.CreatedTime.UTC()
		r.CreatedAt = &createdAt
	}

	return r
}

// groupTags는 SDK 태그를 키 순서가 안정적인 도메인 필드로 변환한다.
func groupTags(tags []autoscalingtypes.TagDescription) []model.Field {
	values := make(map[string]string, len(tags))
	for _, tag := range tags {
		values[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	return model.TagFields(values)
}

// groupRelations는 Auto Scaling Group이 연결된 리소스 관계를 만든다.
//
// LoadBalancerNames는 Classic Load Balancer를 나타내지만 대응하는 도메인 타입이 없어
// 관계로 만들지 않는다.
func groupRelations(group autoscalingtypes.AutoScalingGroup) []model.Ref {
	var refs []model.Ref

	for _, instance := range group.Instances {
		if id := aws.ToString(instance.InstanceId); id != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeEC2Instance,
				ID:       id,
				Relation: model.RelationAssociatedWith,
			})
		}
	}

	for _, value := range strings.Split(aws.ToString(group.VPCZoneIdentifier), ",") {
		if subnetID := strings.TrimSpace(value); subnetID != "" {
			refs = append(refs, model.Ref{
				Type:     model.TypeEC2Subnet,
				ID:       subnetID,
				Relation: model.RelationAssociatedWith,
			})
		}
	}

	for _, arn := range group.TargetGroupARNs {
		if arn == "" {
			continue
		}

		refs = append(refs, model.Ref{
			Type:           model.TypeELBv2TargetGroup,
			ID:             arn,
			IdentifierKind: model.IdentifierARN,
			Relation:       model.RelationAssociatedWith,
		})
	}

	return refs
}

// instanceIDs는 Auto Scaling Group 인스턴스 ID를 응답 순서대로 반환한다.
func instanceIDs(instances []autoscalingtypes.Instance) []string {
	ids := make([]string, 0, len(instances))
	for _, instance := range instances {
		if id := aws.ToString(instance.InstanceId); id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}

// int32Value는 선택적 정수의 nil과 실제 0을 구분한다.
func int32Value(value *int32) string {
	if value == nil {
		return "-"
	}

	return strconv.FormatInt(int64(*value), 10)
}

// launchTemplateValue는 시작 템플릿의 ID·이름·버전을 API 필드명과 함께 보존한다.
func launchTemplateValue(template *autoscalingtypes.LaunchTemplateSpecification) string {
	if template == nil {
		return "-"
	}

	parts := make([]string, 0, 3)
	if id := aws.ToString(template.LaunchTemplateId); id != "" {
		parts = append(parts, "LaunchTemplateId="+id)
	}
	if name := aws.ToString(template.LaunchTemplateName); name != "" {
		parts = append(parts, "LaunchTemplateName="+name)
	}
	if version := aws.ToString(template.Version); version != "" {
		parts = append(parts, "Version="+version)
	}

	return displayString(strings.Join(parts, ", "))
}

// mixedInstancesPolicyValue는 혼합 인스턴스 정책의 기본 템플릿과 override 개수를 요약한다.
func mixedInstancesPolicyValue(policy *autoscalingtypes.MixedInstancesPolicy) string {
	if policy == nil || policy.LaunchTemplate == nil {
		return "-"
	}

	base := launchTemplateValue(policy.LaunchTemplate.LaunchTemplateSpecification)
	overrides := strconv.Itoa(len(policy.LaunchTemplate.Overrides))
	if base == "-" {
		return "Overrides=" + overrides
	}

	return base + ", Overrides=" + overrides
}

// displayString은 빈 문자열을 상세 화면의 없음 표기인 "-"로 바꾼다.
func displayString(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
