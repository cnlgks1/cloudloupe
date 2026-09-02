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

// describeNatGatewaysAPI는 NAT 게이트웨이 수집기가 필요한 SDK 메서드만 담는다.
type describeNatGatewaysAPI interface {
	DescribeNatGateways(context.Context, *awsec2.DescribeNatGatewaysInput, ...func(*awsec2.Options)) (*awsec2.DescribeNatGatewaysOutput, error)
}

// natGatewayCollector는 NAT 게이트웨이를 조회한다.
type natGatewayCollector struct {
	api describeNatGatewaysAPI
}

// NewNATGateway는 NAT 게이트웨이 수집기를 만든다.
func NewNATGateway(api describeNatGatewaysAPI) collect.Collector {
	return natGatewayCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c natGatewayCollector) Type() string { return model.TypeEC2NATGateway }

// Collect는 범위 안의 NAT 게이트웨이를 모두 조회해 도메인 리소스로 변환한다.
func (c natGatewayCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeNatGatewaysPaginator(c.api, &awsec2.DescribeNatGatewaysInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe NAT gateways: %w", err)
		}

		for i := range page.NatGateways {
			out = append(out, natGatewayToResource(req.Scope, page.NatGateways[i]))
		}
	}

	return out, nil
}

// natGatewayToResource는 SDK NAT 게이트웨이를 도메인 리소스로 변환한다.
func natGatewayToResource(scope collect.Scope, gateway ec2types.NatGateway) model.Resource {
	var publicIPs, privateIPs, interfaceIDs, allocationIDs []string
	for _, address := range gateway.NatGatewayAddresses {
		publicIPs = appendIfNotEmpty(publicIPs, aws.ToString(address.PublicIp))
		privateIPs = appendIfNotEmpty(privateIPs, aws.ToString(address.PrivateIp))
		interfaceIDs = appendIfNotEmpty(interfaceIDs, aws.ToString(address.NetworkInterfaceId))
		allocationIDs = appendIfNotEmpty(allocationIDs, aws.ToString(address.AllocationId))
	}

	r := model.Resource{
		Type:      model.TypeEC2NATGateway,
		ID:        aws.ToString(gateway.NatGatewayId),
		Name:      tagValue(gateway.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(gateway.State),
		Fields: []model.Field{
			{Key: "연결 유형", Value: orDash(string(gateway.ConnectivityType))},
			{Key: "가용 모드", Value: orDash(string(gateway.AvailabilityMode))},
			{Key: "VPC", Value: orDash(aws.ToString(gateway.VpcId))},
			{Key: "서브넷", Value: orDash(aws.ToString(gateway.SubnetId))},
			{Key: "공인 IP", Value: stringSliceOrDash(publicIPs)},
			{Key: "사설 IP", Value: stringSliceOrDash(privateIPs)},
			{Key: "ENI", Value: stringSliceOrDash(interfaceIDs)},
			{Key: "EIP 할당 ID", Value: stringSliceOrDash(allocationIDs)},
			{Key: "실패 코드", Value: orDash(aws.ToString(gateway.FailureCode))},
			{Key: "실패 메시지", Value: orDash(aws.ToString(gateway.FailureMessage))},
		},
		Tags:    ec2Tags(gateway.Tags),
		Related: natGatewayRelations(gateway),
	}

	if gateway.CreateTime != nil {
		createdAt := gateway.CreateTime.UTC()
		r.CreatedAt = &createdAt
	}

	return r
}

// natGatewayRelations는 NAT 게이트웨이의 VPC·서브넷·네트워크 연결을 만든다.
func natGatewayRelations(gateway ec2types.NatGateway) []model.Ref {
	var refs []model.Ref

	refs = appendResourceRef(refs, model.TypeEC2VPC, aws.ToString(gateway.VpcId), model.RelationAssociatedWith)
	refs = appendResourceRef(refs, model.TypeEC2Subnet, aws.ToString(gateway.SubnetId), model.RelationAssociatedWith)
	refs = appendResourceRef(refs, model.TypeEC2RouteTable, aws.ToString(gateway.RouteTableId), model.RelationAssociatedWith)

	for _, address := range gateway.NatGatewayAddresses {
		refs = appendResourceRef(refs, model.TypeEC2NetworkInterface, aws.ToString(address.NetworkInterfaceId), model.RelationAttachedENI)
		refs = appendResourceRef(refs, model.TypeEC2Address, aws.ToString(address.AllocationId), model.RelationAssociatedWith)
	}

	return refs
}

func appendIfNotEmpty(values []string, value string) []string {
	if value != "" {
		return append(values, value)
	}

	return values
}

func appendResourceRef(refs []model.Ref, typ, id, relation string) []model.Ref {
	if id == "" {
		return refs
	}

	return append(refs, model.Ref{Type: typ, ID: id, Relation: relation})
}
