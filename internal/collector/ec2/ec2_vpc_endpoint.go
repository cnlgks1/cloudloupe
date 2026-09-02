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

// describeVpcEndpointsAPI는 VPC 엔드포인트 수집기가 필요한 SDK 메서드만 담는다.
type describeVpcEndpointsAPI interface {
	DescribeVpcEndpoints(context.Context, *awsec2.DescribeVpcEndpointsInput, ...func(*awsec2.Options)) (*awsec2.DescribeVpcEndpointsOutput, error)
}

// vpcEndpointCollector는 VPC 엔드포인트를 조회한다.
type vpcEndpointCollector struct {
	api describeVpcEndpointsAPI
}

// NewVPCEndpoint는 VPC 엔드포인트 수집기를 만든다.
func NewVPCEndpoint(api describeVpcEndpointsAPI) collect.Collector {
	return vpcEndpointCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c vpcEndpointCollector) Type() string { return model.TypeEC2VPCEndpoint }

// Collect는 범위 안의 VPC 엔드포인트를 모두 조회해 도메인 리소스로 변환한다.
func (c vpcEndpointCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeVpcEndpointsPaginator(c.api, &awsec2.DescribeVpcEndpointsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe VPC endpoints: %w", err)
		}

		for i := range page.VpcEndpoints {
			out = append(out, vpcEndpointToResource(req.Scope, page.VpcEndpoints[i]))
		}
	}

	return out, nil
}

// vpcEndpointToResource는 SDK VPC 엔드포인트를 도메인 리소스로 변환한다.
func vpcEndpointToResource(scope collect.Scope, endpoint ec2types.VpcEndpoint) model.Resource {
	r := model.Resource{
		Type:      model.TypeEC2VPCEndpoint,
		ID:        aws.ToString(endpoint.VpcEndpointId),
		Name:      tagValue(endpoint.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(endpoint.State),
		Fields: []model.Field{
			{Key: "VpcEndpointType", Value: orDash(string(endpoint.VpcEndpointType))},
			{Key: "ServiceName", Value: orDash(aws.ToString(endpoint.ServiceName))},
			{Key: "ServiceRegion", Value: orDash(aws.ToString(endpoint.ServiceRegion))},
			{Key: "IpAddressType", Value: orDash(string(endpoint.IpAddressType))},
			{Key: "VpcId", Value: orDash(aws.ToString(endpoint.VpcId))},
			{Key: "SubnetIds", Value: strconv.Itoa(len(endpoint.SubnetIds))},
			{Key: "RouteTableIds", Value: strconv.Itoa(len(endpoint.RouteTableIds))},
			{Key: "Groups", Value: strconv.Itoa(len(endpoint.Groups))},
			{Key: "PrivateDnsEnabled", Value: boolValue(aws.ToBool(endpoint.PrivateDnsEnabled))},
			{Key: "RequesterManaged", Value: boolValue(aws.ToBool(endpoint.RequesterManaged))},
			{Key: "OwnerId", Value: orDash(aws.ToString(endpoint.OwnerId))},
			{Key: "FailureReason", Value: orDash(aws.ToString(endpoint.FailureReason))},
		},
		Tags:    ec2Tags(endpoint.Tags),
		Related: vpcEndpointRelations(endpoint),
	}

	if endpoint.CreationTimestamp != nil {
		createdAt := endpoint.CreationTimestamp.UTC()
		r.CreatedAt = &createdAt
	}

	return r
}

// vpcEndpointRelations는 엔드포인트와 VPC 네트워크 리소스의 관계를 만든다.
func vpcEndpointRelations(endpoint ec2types.VpcEndpoint) []model.Ref {
	var refs []model.Ref

	refs = appendResourceRef(refs, model.TypeEC2VPC, aws.ToString(endpoint.VpcId), model.RelationAssociatedWith)
	for _, id := range endpoint.SubnetIds {
		refs = appendResourceRef(refs, model.TypeEC2Subnet, id, model.RelationAssociatedWith)
	}
	for _, id := range endpoint.RouteTableIds {
		refs = appendResourceRef(refs, model.TypeEC2RouteTable, id, model.RelationAssociatedWith)
	}
	for _, group := range endpoint.Groups {
		refs = appendResourceRef(refs, model.TypeEC2SecurityGroup, aws.ToString(group.GroupId), model.RelationAssociatedWith)
	}
	for _, id := range endpoint.NetworkInterfaceIds {
		refs = appendResourceRef(refs, model.TypeEC2NetworkInterface, id, model.RelationAttachedENI)
	}

	return refs
}
