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

// describeNetworkInterfacesAPI는 ENI 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type describeNetworkInterfacesAPI interface {
	DescribeNetworkInterfaces(context.Context, *awsec2.DescribeNetworkInterfacesInput, ...func(*awsec2.Options)) (*awsec2.DescribeNetworkInterfacesOutput, error)
}

// ec2NetworkInterfaceCollector는 ENI(탄력적 네트워크 인터페이스)를 조회한다.
type ec2NetworkInterfaceCollector struct {
	api describeNetworkInterfacesAPI
}

// NewNetworkInterface는 ENI 수집기를 만든다.
func NewNetworkInterface(api describeNetworkInterfacesAPI) collect.Collector {
	return ec2NetworkInterfaceCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c ec2NetworkInterfaceCollector) Type() string { return model.TypeEC2NetworkInterface }

// Collect는 범위 안의 ENI를 모두 조회해 도메인 리소스로 변환한다.
func (c ec2NetworkInterfaceCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec2.NewDescribeNetworkInterfacesPaginator(c.api, &awsec2.DescribeNetworkInterfacesInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe network interfaces: %w", err)
		}

		for i := range page.NetworkInterfaces {
			out = append(out, networkInterfaceToResource(req.Scope, page.NetworkInterfaces[i]))
		}
	}

	return out, nil
}

// networkInterfaceToResource는 SDK ENI를 도메인 리소스로 변환한다.
//
// ENI의 태그는 다른 EC2 리소스와 달리 TagSet 필드에 담긴다. 이름 태그도 여기서 찾는다.
func networkInterfaceToResource(scope collect.Scope, eni ec2types.NetworkInterface) model.Resource {
	r := model.Resource{
		Type:      model.TypeEC2NetworkInterface,
		ID:        aws.ToString(eni.NetworkInterfaceId),
		Name:      tagValue(eni.TagSet, "Name"),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(eni.Status),
		Tags:      ec2Tags(eni.TagSet),
	}

	// ENI에는 생성 시각이 없다. CreatedAt은 비워 둔다.

	r.Fields = []model.Field{
		{Key: "InterfaceType", Value: string(eni.InterfaceType)},
		{Key: "PrivateIpAddress", Value: orDash(aws.ToString(eni.PrivateIpAddress))},
		{Key: "VpcId", Value: orDash(aws.ToString(eni.VpcId))},
		{Key: "SubnetId", Value: orDash(aws.ToString(eni.SubnetId))},
		{Key: "AvailabilityZone", Value: orDash(aws.ToString(eni.AvailabilityZone))},
		{Key: "Description", Value: orDash(aws.ToString(eni.Description))},
	}

	r.Related = networkInterfaceRelations(eni)

	return r
}

// networkInterfaceRelations는 ENI가 붙어 있는 인스턴스로의 관계를 만든다.
//
// 관계를 양쪽 끝에서 기록하는 원칙에 따라 ENI → 인스턴스(attached-to)를 남긴다.
// 인스턴스 → ENI(attached-eni)는 ec2_instance.go에서 남긴다.
func networkInterfaceRelations(eni ec2types.NetworkInterface) []model.Ref {
	if eni.Attachment == nil {
		return nil
	}

	id := aws.ToString(eni.Attachment.InstanceId)
	if id == "" {
		return nil
	}

	return []model.Ref{{
		Type:     model.TypeEC2Instance,
		ID:       id,
		Relation: model.RelationAttachedTo,
	}}
}
