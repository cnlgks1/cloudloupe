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

// fakeSecurityGroupAPI는 describeSecurityGroupsAPI를 만족하는 테스트 대역이다.
type fakeSecurityGroupAPI struct {
	pages []*awsec2.DescribeSecurityGroupsOutput
	errs  []error
	calls int
}

func (f *fakeSecurityGroupAPI) DescribeSecurityGroups(_ context.Context, _ *awsec2.DescribeSecurityGroupsInput, _ ...func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestSecurityGroupCollectorConvertsFieldsAndRelation(t *testing.T) {
	t.Parallel()

	api := &fakeSecurityGroupAPI{pages: []*awsec2.DescribeSecurityGroupsOutput{{
		SecurityGroups: []ec2types.SecurityGroup{{
			GroupId:          aws.String("sg-0123"),
			GroupName:        aws.String("app-sg"),
			SecurityGroupArn: aws.String("arn:aws:ec2:ap-northeast-2:123456789012:security-group/sg-0123"),
			VpcId:            aws.String("vpc-0123"),
			IpPermissions: []ec2types.IpPermission{
				{
					IpRanges:         []ec2types.IpRange{{}, {}},
					Ipv6Ranges:       []ec2types.Ipv6Range{{}},
					PrefixListIds:    []ec2types.PrefixListId{{}},
					UserIdGroupPairs: []ec2types.UserIdGroupPair{{}, {}},
				},
				{},
			},
			IpPermissionsEgress: []ec2types.IpPermission{{
				IpRanges:         []ec2types.IpRange{{}},
				Ipv6Ranges:       []ec2types.Ipv6Range{{}},
				PrefixListIds:    []ec2types.PrefixListId{{}},
				UserIdGroupPairs: []ec2types.UserIdGroupPair{{}},
			}},
			Description: aws.String("애플리케이션 보안 그룹"),
			OwnerId:     aws.String("123456789012"),
			Tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("태그 이름")},
				{Key: aws.String("Environment"), Value: aws.String("production")},
			},
		}},
	}}}

	resources, err := ec2.NewSecurityGroup(api).Collect(context.Background(), collect.Request{Scope: collect.Scope{
		Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012",
	}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(resources))
	}

	got := resources[0]
	if got.Type != model.TypeEC2SecurityGroup || got.ID != "sg-0123" {
		t.Errorf("기본 식별 정보 = %+v", got)
	}
	if got.Name != "app-sg" {
		t.Errorf("Name = %q, want GroupName app-sg", got.Name)
	}
	if got.ARN == "" || got.Status != "" {
		t.Errorf("ARN 또는 상태 = %+v", got)
	}
	wantFields := []model.Field{
		{Key: "VpcId", Value: "vpc-0123"},
		{Key: "InboundRules", Value: "7"},
		{Key: "OutboundRules", Value: "4"},
		{Key: "Description", Value: "애플리케이션 보안 그룹"},
		{Key: "OwnerId", Value: "123456789012"},
	}
	if !slices.Equal(got.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", got.Fields, wantFields)
	}
	wantRelated := []model.Ref{{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith}}
	if !slices.Equal(got.Related, wantRelated) {
		t.Errorf("Related = %+v, want %+v", got.Related, wantRelated)
	}
	wantTags := []model.Field{{Key: "Environment", Value: "production"}, {Key: "Name", Value: "태그 이름"}}
	if !slices.Equal(got.Tags, wantTags) {
		t.Errorf("Tags = %+v, want %+v", got.Tags, wantTags)
	}
}

func TestSecurityGroupCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeSecurityGroupAPI{pages: []*awsec2.DescribeSecurityGroupsOutput{
		{SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-1")}}, NextToken: aws.String("page2")},
		{SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-2")}}},
	}}

	resources, err := ec2.NewSecurityGroup(api).Collect(context.Background(), collect.Request{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(resources) != 2 || api.calls != 2 {
		t.Errorf("두 페이지에서 보안 그룹 2개(호출 2회)가 나와야 한다, got %d개 호출 %d회", len(resources), api.calls)
	}
}

func TestSecurityGroupCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeSecurityGroupAPI{
		pages: []*awsec2.DescribeSecurityGroupsOutput{{
			SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-1")}},
			NextToken:      aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	resources, err := ec2.NewSecurityGroup(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wrapped %v", err, wantErr)
	}
	if len(resources) != 1 || resources[0].ID != "sg-1" {
		t.Errorf("부분 결과 = %+v, want sg-1", resources)
	}
	if api.calls != 2 {
		t.Errorf("호출 수 = %d, want 2", api.calls)
	}
}

func TestSecurityGroupCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewSecurityGroup(&fakeSecurityGroupAPI{}).Type(); got != model.TypeEC2SecurityGroup {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2SecurityGroup)
	}
}
