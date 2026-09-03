package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeAddressesAPI는 describeAddressesAPI를 만족하는 테스트 대역이다. DescribeAddresses는
// 페이지네이터가 없으므로 한 번의 응답만 돌려준다.
type fakeAddressesAPI struct {
	out *awsec2.DescribeAddressesOutput
	err error
}

func (f *fakeAddressesAPI) DescribeAddresses(_ context.Context, _ *awsec2.DescribeAddressesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeAddressesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.out, nil
}

func TestEC2AddressCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	api := &fakeAddressesAPI{out: &awsec2.DescribeAddressesOutput{
		Addresses: []ec2types.Address{{
			AllocationId:     aws.String("eipalloc-0123456789abcdef0"),
			PublicIp:         aws.String("52.79.1.2"),
			PrivateIpAddress: aws.String("10.0.1.24"),
			Domain:           ec2types.DomainTypeVpc,
			InstanceId:       aws.String("i-0a1b"),
			AssociationId:    aws.String("eipassoc-1"),
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("nat-eip")},
			},
		}},
	}}

	c := ec2.NewAddress(api)

	got, err := c.Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	r := got[0]

	if r.Type != model.TypeEC2Address {
		t.Errorf("Type = %q, want %q", r.Type, model.TypeEC2Address)
	}

	if r.ID != "eipalloc-0123456789abcdef0" {
		t.Errorf("ID = %q, want 할당 ID", r.ID)
	}

	if r.Name != "nat-eip" {
		t.Errorf("Name = %q, want nat-eip (Name 태그에서)", r.Name)
	}

	if r.Status != "associated" {
		t.Errorf("Status = %q, want 연결됨", r.Status)
	}

	if got := r.FieldValue("PublicIp"); got != "52.79.1.2" {
		t.Errorf("공인 IP = %q", got)
	}
}

func TestEC2AddressCollectorUnassociatedName(t *testing.T) {
	t.Parallel()

	// 연결 안 되고 Name 태그도 없는 EIP는 이름을 공인 IP로 대체하고, 상태는 unassociated.
	api := &fakeAddressesAPI{out: &awsec2.DescribeAddressesOutput{
		Addresses: []ec2types.Address{{
			AllocationId: aws.String("eipalloc-free"),
			PublicIp:     aws.String("52.79.9.9"),
			Domain:       ec2types.DomainTypeVpc,
		}},
	}}

	c := ec2.NewAddress(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got[0].Name != "52.79.9.9" {
		t.Errorf("Name = %q, want 공인 IP로 대체", got[0].Name)
	}

	if got[0].Status != "unassociated" {
		t.Errorf("Status = %q, want 연결 안 됨", got[0].Status)
	}

	if len(got[0].Related) != 0 {
		t.Errorf("연결 안 된 EIP에 관계가 있으면 안 된다: %+v", got[0].Related)
	}
}

func TestEC2AddressCollectorRecordsAssociations(t *testing.T) {
	t.Parallel()

	api := &fakeAddressesAPI{out: &awsec2.DescribeAddressesOutput{
		Addresses: []ec2types.Address{{
			AllocationId:       aws.String("eipalloc-1"),
			PublicIp:           aws.String("1.2.3.4"),
			InstanceId:         aws.String("i-1"),
			NetworkInterfaceId: aws.String("eni-1"),
		}},
	}}

	c := ec2.NewAddress(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got[0].Related) != 2 {
		t.Fatalf("관계 2개(인스턴스+ENI)여야 한다, got %d: %+v", len(got[0].Related), got[0].Related)
	}
	instance := got[0].RelatedBy("InstanceId")
	eni := got[0].RelatedBy("NetworkInterfaceId")
	if len(instance) != 1 || len(eni) != 1 {
		t.Fatalf("InstanceId·NetworkInterfaceId 관계가 각 1개여야 한다: %+v", got[0].Related)
	}
}

func TestEC2AddressCollectorWrapsError(t *testing.T) {
	t.Parallel()

	api := &fakeAddressesAPI{err: errors.New("UnauthorizedOperation")}
	c := ec2.NewAddress(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("에러가 반환되어야 한다")
	}

	if got := err.Error(); got == "UnauthorizedOperation" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestEC2AddressCollectorType(t *testing.T) {
	t.Parallel()

	c := ec2.NewAddress(&fakeAddressesAPI{})
	if c.Type() != model.TypeEC2Address {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeEC2Address)
	}
}
