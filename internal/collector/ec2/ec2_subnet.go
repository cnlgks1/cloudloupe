package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeSubnetsAPI는 서브넷 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type describeSubnetsAPI interface {
	DescribeSubnets(context.Context, *awsec2.DescribeSubnetsInput, ...func(*awsec2.Options)) (*awsec2.DescribeSubnetsOutput, error)
}

// subnetCollector는 서브넷을 조회한다.
type subnetCollector struct {
	api describeSubnetsAPI
}

// NewSubnet은 서브넷 수집기를 만든다.
func NewSubnet(api describeSubnetsAPI) collect.Collector {
	return subnetCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c subnetCollector) Type() string { return model.TypeEC2Subnet }

// Collect는 범위 안의 서브넷을 모두 조회해 도메인 리소스로 변환한다.
func (c subnetCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeSubnetsPaginator(c.api, &awsec2.DescribeSubnetsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe subnets: %w", err)
		}

		for i := range page.Subnets {
			out = append(out, subnetToResource(req.Scope, page.Subnets[i]))
		}
	}

	return out, nil
}

// subnetToResource는 SDK 서브넷을 도메인 리소스로 변환한다.
func subnetToResource(scope collect.Scope, subnet ec2types.Subnet) model.Resource {
	vpcID := aws.ToString(subnet.VpcId)
	r := model.Resource{
		Type:      model.TypeEC2Subnet,
		ID:        aws.ToString(subnet.SubnetId),
		Name:      tagValue(subnet.Tags, "Name"),
		ARN:       aws.ToString(subnet.SubnetArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(subnet.State),
		Fields: []model.Field{
			{Key: "CidrBlock", Value: orDash(aws.ToString(subnet.CidrBlock))},
			{Key: "AvailabilityZone", Value: orDash(aws.ToString(subnet.AvailabilityZone))},
			{Key: "AvailabilityZoneId", Value: orDash(aws.ToString(subnet.AvailabilityZoneId))},
			{Key: "AvailableIpAddressCount", Value: itoa32(aws.ToInt32(subnet.AvailableIpAddressCount))},
			{Key: "VpcId", Value: orDash(vpcID)},
			{Key: "MapPublicIpOnLaunch", Value: boolValue(aws.ToBool(subnet.MapPublicIpOnLaunch))},
			{Key: "DefaultForAz", Value: boolValue(aws.ToBool(subnet.DefaultForAz))},
			{Key: "Ipv6Native", Value: boolValue(aws.ToBool(subnet.Ipv6Native))},
			{Key: "AssignIpv6AddressOnCreation", Value: boolValue(aws.ToBool(subnet.AssignIpv6AddressOnCreation))},
			{Key: "OwnerId", Value: orDash(aws.ToString(subnet.OwnerId))},
		},
		Tags: ec2Tags(subnet.Tags),
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
