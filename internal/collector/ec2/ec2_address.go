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

// describeAddressesAPI는 Elastic IP 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type describeAddressesAPI interface {
	DescribeAddresses(context.Context, *awsec2.DescribeAddressesInput, ...func(*awsec2.Options)) (*awsec2.DescribeAddressesOutput, error)
}

// ec2AddressCollector는 Elastic IP(EIP)를 조회한다.
type ec2AddressCollector struct {
	api describeAddressesAPI
}

// NewAddress는 EIP 수집기를 만든다.
func NewAddress(api describeAddressesAPI) collect.Collector {
	return ec2AddressCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c ec2AddressCollector) Type() string { return model.TypeEC2Address }

// Collect는 범위 안의 Elastic IP를 모두 조회해 도메인 리소스로 변환한다.
//
// DescribeAddresses는 다른 EC2 조회와 달리 페이지네이터가 없다. 한 번의 응답에 전체
// 목록이 담겨 온다. EIP는 계정당 기본 상한이 낮아(리전당 5개) 페이지네이션이 필요 없다.
func (c ec2AddressCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	out, err := c.api.DescribeAddresses(ctx, &awsec2.DescribeAddressesInput{})
	if err != nil {
		return nil, fmt.Errorf("describe addresses: %w", err)
	}

	resources := make([]model.Resource, 0, len(out.Addresses))
	for i := range out.Addresses {
		resources = append(resources, addressToResource(req.Scope, out.Addresses[i]))
	}

	return resources, nil
}

// addressToResource는 SDK Elastic IP를 도메인 리소스로 변환한다.
//
// ID로는 할당 ID(AllocationId)를 쓴다. VPC용 EIP의 안정적 식별자이기 때문이다. 표시
// 이름은 Name 태그가 있으면 그것을, 없으면 공인 IP 자체를 쓴다(DisplayName이 ID로
// 폴백하지만, EIP는 IP 주소가 더 알아보기 쉽다).
func addressToResource(scope collect.Scope, addr ec2types.Address) model.Resource {
	publicIP := aws.ToString(addr.PublicIp)

	name := tagValue(addr.Tags, "Name")
	if name == "" {
		name = publicIP
	}

	// 연결되지 않은 EIP는 요금이 부과되므로, 상태로 연결 여부를 드러낸다. EIP에는 AWS가 주는
	// 상태 필드가 없어 이 값만 우리가 만든다. AWS 용어를 따라 associated/unassociated로 쓴다.
	status := "unassociated"
	if aws.ToString(addr.AssociationId) != "" || aws.ToString(addr.InstanceId) != "" {
		status = "associated"
	}

	r := model.Resource{
		Type:      model.TypeEC2Address,
		ID:        aws.ToString(addr.AllocationId),
		Name:      name,
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    status,
		Tags:      ec2Tags(addr.Tags),
	}

	r.Fields = []model.Field{
		{Key: "PublicIp", Value: orDash(publicIP)},
		{Key: "PrivateIpAddress", Value: orDash(aws.ToString(addr.PrivateIpAddress))},
		{Key: "Domain", Value: string(addr.Domain)},
		{Key: "AssociationId", Value: orDash(aws.ToString(addr.AssociationId))},
	}

	r.Related = addressRelations(addr)

	return r
}

// addressRelations는 EIP가 연결된 인스턴스·ENI로의 관계를 만든다.
//
// EIP는 인스턴스 또는 ENI에 연결된다. 두 방향 모두 associated-with로 남긴다. 반대
// 방향은 3단계 그래프에서 채운다.
func addressRelations(addr ec2types.Address) []model.Ref {
	var refs []model.Ref

	if id := aws.ToString(addr.InstanceId); id != "" {
		refs = append(refs, model.Ref{
			Type:     model.TypeEC2Instance,
			ID:       id,
			Relation: "InstanceId",
		})
	}

	if id := aws.ToString(addr.NetworkInterfaceId); id != "" {
		refs = append(refs, model.Ref{
			Type:     model.TypeEC2NetworkInterface,
			ID:       id,
			Relation: "NetworkInterfaceId",
		})
	}

	return refs
}
