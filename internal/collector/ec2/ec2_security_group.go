package ec2

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeSecurityGroupsAPI는 보안 그룹 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type describeSecurityGroupsAPI interface {
	DescribeSecurityGroups(context.Context, *awsec2.DescribeSecurityGroupsInput, ...func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error)
}

// securityGroupCollector는 보안 그룹을 조회한다.
type securityGroupCollector struct {
	api describeSecurityGroupsAPI
}

// NewSecurityGroup은 보안 그룹 수집기를 만든다.
func NewSecurityGroup(api describeSecurityGroupsAPI) collect.Collector {
	return securityGroupCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c securityGroupCollector) Type() string { return model.TypeEC2SecurityGroup }

// Collect는 범위 안의 보안 그룹을 모두 조회해 도메인 리소스로 변환한다.
func (c securityGroupCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeSecurityGroupsPaginator(c.api, &awsec2.DescribeSecurityGroupsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe security groups: %w", err)
		}

		for i := range page.SecurityGroups {
			out = append(out, securityGroupToResource(req.Scope, page.SecurityGroups[i]))
		}
	}

	return out, nil
}

// securityGroupRuleCount는 permission 안의 source 또는 destination 항목을 규칙으로 센다.
// 항목이 없는 permission도 AWS가 반환한 규칙 하나로 간주한다.
func securityGroupRuleCount(permissions []ec2types.IpPermission) int {
	count := 0

	for _, permission := range permissions {
		items := len(permission.IpRanges) + len(permission.Ipv6Ranges) + len(permission.PrefixListIds) + len(permission.UserIdGroupPairs)
		if items == 0 {
			items = 1
		}

		count += items
	}

	return count
}

// securityGroupToResource는 SDK 보안 그룹을 도메인 리소스로 변환한다.
func securityGroupToResource(scope collect.Scope, group ec2types.SecurityGroup) model.Resource {
	name := aws.ToString(group.GroupName)
	if name == "" {
		name = tagValue(group.Tags, "Name")
	}

	vpcID := aws.ToString(group.VpcId)
	r := model.Resource{
		Type:      model.TypeEC2SecurityGroup,
		ID:        aws.ToString(group.GroupId),
		Name:      name,
		ARN:       aws.ToString(group.SecurityGroupArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "VpcId", Value: orDash(vpcID)},
			{Key: "InboundRules", Value: strconv.Itoa(securityGroupRuleCount(group.IpPermissions))},
			{Key: "OutboundRules", Value: strconv.Itoa(securityGroupRuleCount(group.IpPermissionsEgress))},
			{Key: "Description", Value: orDash(aws.ToString(group.Description))},
			{Key: "OwnerId", Value: orDash(aws.ToString(group.OwnerId))},
		},
		Tags: ec2Tags(group.Tags),
	}

	if vpcID != "" {
		r.Related = []model.Ref{{
			Type:     model.TypeEC2VPC,
			ID:       vpcID,
			Relation: model.RelationAssociatedWith,
		}}
	}

	return r
}
