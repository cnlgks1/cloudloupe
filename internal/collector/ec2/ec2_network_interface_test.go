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

// fakeENIAPI는 describeNetworkInterfacesAPI를 만족하는 테스트 대역이다.
type fakeENIAPI struct {
	pages []*awsec2.DescribeNetworkInterfacesOutput
	err   error
	calls int
}

func (f *fakeENIAPI) DescribeNetworkInterfaces(_ context.Context, _ *awsec2.DescribeNetworkInterfacesInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeNetworkInterfacesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}

	page := f.pages[f.calls]
	f.calls++

	return page, nil
}

func TestEC2NetworkInterfaceCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	api := &fakeENIAPI{pages: []*awsec2.DescribeNetworkInterfacesOutput{{
		NetworkInterfaces: []ec2types.NetworkInterface{{
			NetworkInterfaceId: aws.String("eni-0aa11bb22cc33dd44"),
			InterfaceType:      ec2types.NetworkInterfaceTypeInterface,
			Status:             ec2types.NetworkInterfaceStatusInUse,
			PrivateIpAddress:   aws.String("10.0.1.24"),
			VpcId:              aws.String("vpc-1"),
			SubnetId:           aws.String("subnet-1"),
			AvailabilityZone:   aws.String("ap-northeast-2a"),
			Description:        aws.String("primary network interface"),
			TagSet: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("web-eni")},
			},
			Attachment: &ec2types.NetworkInterfaceAttachment{InstanceId: aws.String("i-0a1b")},
		}},
	}}}

	c := ec2.NewNetworkInterface(api)

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

	if r.Type != model.TypeEC2NetworkInterface {
		t.Errorf("Type = %q, want %q", r.Type, model.TypeEC2NetworkInterface)
	}

	if r.ID != "eni-0aa11bb22cc33dd44" {
		t.Errorf("ID = %q", r.ID)
	}

	if r.Name != "web-eni" {
		t.Errorf("Name = %q, want web-eni (Name 태그에서, TagSet)", r.Name)
	}

	if r.Status != "in-use" {
		t.Errorf("Status = %q, want in-use", r.Status)
	}

	if got := r.FieldValue("PrivateIpAddress"); got != "10.0.1.24" {
		t.Errorf("사설 IP = %q", got)
	}
}

func TestEC2NetworkInterfaceCollectorRecordsAttachment(t *testing.T) {
	t.Parallel()

	api := &fakeENIAPI{pages: []*awsec2.DescribeNetworkInterfacesOutput{{
		NetworkInterfaces: []ec2types.NetworkInterface{{
			NetworkInterfaceId: aws.String("eni-1"),
			Status:             ec2types.NetworkInterfaceStatusInUse,
			Attachment:         &ec2types.NetworkInterfaceAttachment{InstanceId: aws.String("i-1")},
		}},
	}}}

	c := ec2.NewNetworkInterface(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	att := got[0].RelatedBy("Attachment.InstanceId")
	if len(att) != 1 || att[0].ID != "i-1" {
		t.Errorf("인스턴스 attached-to 관계가 없다: %+v", got[0].Related)
	}
}

func TestEC2NetworkInterfaceCollectorUnattachedHasNoRelation(t *testing.T) {
	t.Parallel()

	// 어디에도 안 붙은 ENI(Attachment nil)는 관계가 없어야 한다. nil 역참조로 죽지
	// 않는지도 함께 지킨다.
	api := &fakeENIAPI{pages: []*awsec2.DescribeNetworkInterfacesOutput{{
		NetworkInterfaces: []ec2types.NetworkInterface{{
			NetworkInterfaceId: aws.String("eni-free"),
			Status:             ec2types.NetworkInterfaceStatusAvailable,
		}},
	}}}

	c := ec2.NewNetworkInterface(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got[0].Related) != 0 {
		t.Errorf("안 붙은 ENI에 관계가 있으면 안 된다: %+v", got[0].Related)
	}
}

func TestEC2NetworkInterfaceCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeENIAPI{pages: []*awsec2.DescribeNetworkInterfacesOutput{
		{
			NetworkInterfaces: []ec2types.NetworkInterface{{NetworkInterfaceId: aws.String("eni-1")}},
			NextToken:         aws.String("page2"),
		},
		{
			NetworkInterfaces: []ec2types.NetworkInterface{{NetworkInterfaceId: aws.String("eni-2")}},
		},
	}}

	c := ec2.NewNetworkInterface(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 2 || api.calls != 2 {
		t.Errorf("두 페이지에서 ENI 2개(호출 2회)가 나와야 한다, got %d개 호출 %d회", len(got), api.calls)
	}
}

func TestEC2NetworkInterfaceCollectorWrapsError(t *testing.T) {
	t.Parallel()

	api := &fakeENIAPI{err: errors.New("UnauthorizedOperation")}
	c := ec2.NewNetworkInterface(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("에러가 반환되어야 한다")
	}

	if got := err.Error(); got == "UnauthorizedOperation" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestEC2NetworkInterfaceCollectorType(t *testing.T) {
	t.Parallel()

	c := ec2.NewNetworkInterface(&fakeENIAPI{})
	if c.Type() != model.TypeEC2NetworkInterface {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeEC2NetworkInterface)
	}
}
