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

// fakeVPCAPI는 describeVpcsAPI를 만족하는 테스트 대역이다.
type fakeVPCAPI struct {
	pages []*awsec2.DescribeVpcsOutput
	errs  []error
	calls int
}

func (f *fakeVPCAPI) DescribeVpcs(_ context.Context, _ *awsec2.DescribeVpcsInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestVPCCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	api := &fakeVPCAPI{pages: []*awsec2.DescribeVpcsOutput{{
		Vpcs: []ec2types.Vpc{{
			VpcId:           aws.String("vpc-0123"),
			CidrBlock:       aws.String("10.0.0.0/16"),
			DhcpOptionsId:   aws.String("dopt-0123"),
			InstanceTenancy: ec2types.TenancyDefault,
			IsDefault:       aws.Bool(true),
			OwnerId:         aws.String("123456789012"),
			State:           ec2types.VpcStateAvailable,
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("main-vpc")},
				{Key: aws.String("Environment"), Value: aws.String("production")},
			},
		}},
	}}}

	collector := ec2.NewVPC(api)
	resources, err := collector.Collect(context.Background(), collect.Request{Scope: collect.Scope{
		Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012",
	}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(resources))
	}

	got := resources[0]
	if got.Type != model.TypeEC2VPC || got.ID != "vpc-0123" || got.Name != "main-vpc" {
		t.Errorf("기본 식별 정보 = %+v", got)
	}
	if got.Status != "available" || got.Region != "ap-northeast-2" || got.Profile != "prod" || got.AccountID != "123456789012" {
		t.Errorf("범위 또는 상태 = %+v", got)
	}

	wantFields := []model.Field{
		{Key: "CidrBlock", Value: "10.0.0.0/16"},
		{Key: "IsDefault", Value: "true"},
		{Key: "InstanceTenancy", Value: "default"},
		{Key: "DhcpOptionsId", Value: "dopt-0123"},
		{Key: "OwnerId", Value: "123456789012"},
	}
	if !slices.Equal(got.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", got.Fields, wantFields)
	}
	wantTags := []model.Field{{Key: "Environment", Value: "production"}, {Key: "Name", Value: "main-vpc"}}
	if !slices.Equal(got.Tags, wantTags) {
		t.Errorf("Tags = %+v, want %+v", got.Tags, wantTags)
	}
}

func TestVPCCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeVPCAPI{pages: []*awsec2.DescribeVpcsOutput{
		{Vpcs: []ec2types.Vpc{{VpcId: aws.String("vpc-1")}}, NextToken: aws.String("page2")},
		{Vpcs: []ec2types.Vpc{{VpcId: aws.String("vpc-2")}}},
	}}

	resources, err := ec2.NewVPC(api).Collect(context.Background(), collect.Request{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 2 || api.calls != 2 {
		t.Errorf("두 페이지에서 VPC 2개(호출 2회)가 나와야 한다, got %d개 호출 %d회", len(resources), api.calls)
	}
}

func TestVPCCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeVPCAPI{
		pages: []*awsec2.DescribeVpcsOutput{{
			Vpcs:      []ec2types.Vpc{{VpcId: aws.String("vpc-1")}},
			NextToken: aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	resources, err := ec2.NewVPC(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wrapped %v", err, wantErr)
	}
	if len(resources) != 1 || resources[0].ID != "vpc-1" {
		t.Errorf("부분 결과 = %+v, want vpc-1", resources)
	}
	if api.calls != 2 {
		t.Errorf("호출 수 = %d, want 2", api.calls)
	}
}

func TestVPCCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewVPC(&fakeVPCAPI{}).Type(); got != model.TypeEC2VPC {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2VPC)
	}
}
