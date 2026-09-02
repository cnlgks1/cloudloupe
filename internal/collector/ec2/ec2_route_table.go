package ec2

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeRouteTablesAPI는 라우팅 테이블 수집기가 필요한 SDK 메서드만 담는다.
type describeRouteTablesAPI interface {
	DescribeRouteTables(context.Context, *awsec2.DescribeRouteTablesInput, ...func(*awsec2.Options)) (*awsec2.DescribeRouteTablesOutput, error)
}

// routeTableCollector는 라우팅 테이블을 조회한다.
type routeTableCollector struct {
	api describeRouteTablesAPI
}

// NewRouteTable은 라우팅 테이블 수집기를 만든다.
func NewRouteTable(api describeRouteTablesAPI) collect.Collector {
	return routeTableCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c routeTableCollector) Type() string { return model.TypeEC2RouteTable }

// Collect는 범위 안의 라우팅 테이블을 모두 조회해 도메인 리소스로 변환한다.
func (c routeTableCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeRouteTablesPaginator(c.api, &awsec2.DescribeRouteTablesInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe route tables: %w", err)
		}

		for i := range page.RouteTables {
			out = append(out, routeTableToResource(req.Scope, page.RouteTables[i]))
		}
	}

	return out, nil
}

// routeTableToResource는 SDK 라우팅 테이블을 도메인 리소스로 변환한다.
func routeTableToResource(scope collect.Scope, routeTable ec2types.RouteTable) model.Resource {
	vpcID := aws.ToString(routeTable.VpcId)
	main := false
	for _, association := range routeTable.Associations {
		main = main || aws.ToBool(association.Main)
	}

	return model.Resource{
		Type:      model.TypeEC2RouteTable,
		ID:        aws.ToString(routeTable.RouteTableId),
		Name:      tagValue(routeTable.Tags, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "VpcId", Value: orDash(vpcID)},
			{Key: "Main", Value: boolValue(main)},
			{Key: "Associations", Value: strconv.Itoa(len(routeTable.Associations))},
			{Key: "Routes", Value: strconv.Itoa(len(routeTable.Routes))},
			{Key: "PropagatingVgws", Value: strconv.Itoa(len(routeTable.PropagatingVgws))},
			{Key: "OwnerId", Value: orDash(aws.ToString(routeTable.OwnerId))},
		},
		Tags:    ec2Tags(routeTable.Tags),
		Related: routeTableRelations(routeTable),
	}
}

// routeTableRelations는 VPC·서브넷 연결과 지원하는 라우팅 대상을 관계로 만든다.
func routeTableRelations(routeTable ec2types.RouteTable) []model.Ref {
	var refs []model.Ref

	if id := aws.ToString(routeTable.VpcId); id != "" {
		refs = append(refs, model.Ref{Type: model.TypeEC2VPC, ID: id, Relation: model.RelationAssociatedWith})
	}

	for _, association := range routeTable.Associations {
		if id := aws.ToString(association.SubnetId); id != "" {
			refs = append(refs, model.Ref{Type: model.TypeEC2Subnet, ID: id, Relation: model.RelationAssociatedWith})
		}
		if id := aws.ToString(association.GatewayId); strings.HasPrefix(id, "igw-") {
			refs = append(refs, model.Ref{Type: model.TypeEC2InternetGateway, ID: id, Relation: model.RelationAssociatedWith})
		}
	}

	for _, route := range routeTable.Routes {
		if ref, ok := routeTargetRef(route); ok {
			refs = append(refs, ref)
		}
	}

	return refs
}

func routeTargetRef(route ec2types.Route) (model.Ref, bool) {
	var typ, id string

	switch {
	case aws.ToString(route.NatGatewayId) != "":
		typ, id = model.TypeEC2NATGateway, aws.ToString(route.NatGatewayId)
	case strings.HasPrefix(aws.ToString(route.GatewayId), "igw-"):
		typ, id = model.TypeEC2InternetGateway, aws.ToString(route.GatewayId)
	case aws.ToString(route.NetworkInterfaceId) != "":
		typ, id = model.TypeEC2NetworkInterface, aws.ToString(route.NetworkInterfaceId)
	case aws.ToString(route.InstanceId) != "":
		typ, id = model.TypeEC2Instance, aws.ToString(route.InstanceId)
	default:
		return model.Ref{}, false
	}

	return model.Ref{Type: typ, ID: id, Relation: model.RelationRoutesTo, Via: routeDestination(route)}, true
}

func routeDestination(route ec2types.Route) string {
	for _, destination := range []string{
		aws.ToString(route.DestinationCidrBlock),
		aws.ToString(route.DestinationIpv6CidrBlock),
		aws.ToString(route.DestinationPrefixListId),
	} {
		if destination != "" {
			return destination
		}
	}

	return "-"
}
