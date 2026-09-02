package ec2_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeSubnetAPI는 describeSubnetsAPI를 만족하는 테스트 대역이다.
type fakeSubnetAPI struct {
	pages []*awsec2.DescribeSubnetsOutput
	errs  []error
	calls int
}

func (f *fakeSubnetAPI) DescribeSubnets(_ context.Context, _ *awsec2.DescribeSubnetsInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeSubnetsOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestSubnetCollectorConvertsFieldsAndRelation(t *testing.T) {
	t.Parallel()

	api := &fakeSubnetAPI{pages: []*awsec2.DescribeSubnetsOutput{{
		Subnets: []ec2types.Subnet{{
			SubnetId:                    aws.String("subnet-0123"),
			SubnetArn:                   aws.String("arn:aws:ec2:ap-northeast-2:123456789012:subnet/subnet-0123"),
			State:                       ec2types.SubnetStateAvailable,
			CidrBlock:                   aws.String("10.0.1.0/24"),
			AvailabilityZone:            aws.String("ap-northeast-2a"),
			AvailabilityZoneId:          aws.String("apne2-az1"),
			AvailableIpAddressCount:     aws.Int32(247),
			VpcId:                       aws.String("vpc-0123"),
			MapPublicIpOnLaunch:         aws.Bool(true),
			DefaultForAz:                aws.Bool(false),
			Ipv6Native:                  aws.Bool(false),
			AssignIpv6AddressOnCreation: aws.Bool(true),
			OwnerId:                     aws.String("123456789012"),
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("app-a")},
				{Key: aws.String("Environment"), Value: aws.String("production")},
			},
		}},
	}}}

	resources, err := ec2.NewSubnet(api).Collect(context.Background(), collect.Request{Scope: collect.Scope{
		Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012",
	}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(resources))
	}

	got := resources[0]
	if got.Type != model.TypeEC2Subnet || got.ID != "subnet-0123" || got.Name != "app-a" {
		t.Errorf("기본 식별 정보 = %+v", got)
	}
	if got.ARN == "" || got.Status != "available" {
		t.Errorf("ARN 또는 상태 = %+v", got)
	}
	wantFields := []model.Field{
		{Key: "CidrBlock", Value: "10.0.1.0/24"},
		{Key: "AvailabilityZone", Value: "ap-northeast-2a"},
		{Key: "AvailabilityZoneId", Value: "apne2-az1"},
		{Key: "AvailableIpAddressCount", Value: "247"},
		{Key: "VpcId", Value: "vpc-0123"},
		{Key: "MapPublicIpOnLaunch", Value: "true"},
		{Key: "DefaultForAz", Value: "false"},
		{Key: "Ipv6Native", Value: "false"},
		{Key: "AssignIpv6AddressOnCreation", Value: "true"},
		{Key: "OwnerId", Value: "123456789012"},
	}
	if !slices.Equal(got.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", got.Fields, wantFields)
	}
	wantRelated := []model.Ref{{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith}}
	if !slices.Equal(got.Related, wantRelated) {
		t.Errorf("Related = %+v, want %+v", got.Related, wantRelated)
	}
	wantTags := []model.Field{{Key: "Environment", Value: "production"}, {Key: "Name", Value: "app-a"}}
	if !slices.Equal(got.Tags, wantTags) {
		t.Errorf("Tags = %+v, want %+v", got.Tags, wantTags)
	}
}

func TestSubnetCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeSubnetAPI{pages: []*awsec2.DescribeSubnetsOutput{
		{Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-1")}}, NextToken: aws.String("page2")},
		{Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-2")}}},
	}}

	resources, err := ec2.NewSubnet(api).Collect(context.Background(), collect.Request{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 2 || api.calls != 2 {
		t.Errorf("두 페이지에서 서브넷 2개(호출 2회)가 나와야 한다, got %d개 호출 %d회", len(resources), api.calls)
	}
}

func TestSubnetCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeSubnetAPI{
		pages: []*awsec2.DescribeSubnetsOutput{{
			Subnets:   []ec2types.Subnet{{SubnetId: aws.String("subnet-1")}},
			NextToken: aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	resources, err := ec2.NewSubnet(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wrapped %v", err, wantErr)
	}
	if len(resources) != 1 || resources[0].ID != "subnet-1" {
		t.Errorf("부분 결과 = %+v, want subnet-1", resources)
	}
	if api.calls != 2 {
		t.Errorf("호출 수 = %d, want 2", api.calls)
	}
}

func TestSubnetCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewSubnet(&fakeSubnetAPI{}).Type(); got != model.TypeEC2Subnet {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2Subnet)
	}
}
