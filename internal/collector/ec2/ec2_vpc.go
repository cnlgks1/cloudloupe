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

// describeVpcsAPI는 VPC 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type describeVpcsAPI interface {
	DescribeVpcs(context.Context, *awsec2.DescribeVpcsInput, ...func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error)
}

// vpcCollector는 VPC를 조회한다.
type vpcCollector struct {
	api describeVpcsAPI
}

// NewVPC는 VPC 수집기를 만든다.
func NewVPC(api describeVpcsAPI) collect.Collector {
	return vpcCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c vpcCollector) Type() string { return model.TypeEC2VPC }

// Collect는 범위 안의 VPC를 모두 조회해 도메인 리소스로 변환한다.
func (c vpcCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeVpcsPaginator(c.api, &awsec2.DescribeVpcsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe VPCs: %w", err)
		}

		for i := range page.Vpcs {
			out = append(out, vpcToResource(req.Scope, page.Vpcs[i]))
		}
	}

	return out, nil
}

// vpcToResource는 SDK VPC를 도메인 리소스로 변환한다.
func vpcToResource(scope collect.Scope, vpc ec2types.Vpc) model.Resource {
	return model.Resource{
		Type:      model.TypeEC2VPC,
		ID:        aws.ToString(vpc.VpcId),
		Name:      tagValue(vpc.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(vpc.State),
		Fields: []model.Field{
			{Key: "CidrBlock", Value: orDash(aws.ToString(vpc.CidrBlock))},
			{Key: "IsDefault", Value: boolValue(aws.ToBool(vpc.IsDefault))},
			{Key: "InstanceTenancy", Value: orDash(string(vpc.InstanceTenancy))},
			{Key: "DhcpOptionsId", Value: orDash(aws.ToString(vpc.DhcpOptionsId))},
			{Key: "OwnerId", Value: orDash(aws.ToString(vpc.OwnerId))},
		},
		Tags: ec2Tags(vpc.Tags),
	}
}
