package ec2_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// --- 인터넷 게이트웨이 ---

type fakeInternetGatewayAPI struct {
	pages []*awsec2.DescribeInternetGatewaysOutput
	errs  []error
	calls int
}

func (f *fakeInternetGatewayAPI) DescribeInternetGateways(
	_ context.Context,
	_ *awsec2.DescribeInternetGatewaysInput,
	_ ...func(*awsec2.Options),
) (*awsec2.DescribeInternetGatewaysOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestInternetGatewayCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewInternetGateway(&fakeInternetGatewayAPI{}).Type(); got != model.TypeEC2InternetGateway {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2InternetGateway)
	}
}

// TestInternetGatewayCollectorReportsAttachment는 VPC에 붙은 게이트웨이의 상태와 관계를
// 확인한다.
func TestInternetGatewayCollectorReportsAttachment(t *testing.T) {
	t.Parallel()

	api := &fakeInternetGatewayAPI{pages: []*awsec2.DescribeInternetGatewaysOutput{{
		InternetGateways: []ec2types.InternetGateway{{
			InternetGatewayId: aws.String("igw-0123"),
			OwnerId:           aws.String("123456789012"),
			Attachments: []ec2types.InternetGatewayAttachment{
				{VpcId: aws.String("vpc-0123"), State: ec2types.AttachmentStatusAttached},
			},
			Tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("main-igw")}},
		}},
	}}}

	got, err := ec2.NewInternetGateway(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	gateway := got[0]
	if gateway.ID != "igw-0123" || gateway.Name != "main-igw" {
		t.Errorf("기본 식별 정보 = %+v", gateway)
	}

	wantFields := []model.Field{
		{Key: "VpcId", Value: "vpc-0123"},
		{Key: "AttachmentState", Value: "attached"},
		{Key: "OwnerId", Value: "123456789012"},
	}
	if !slices.Equal(gateway.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", gateway.Fields, wantFields)
	}

	wantRefs := []model.Ref{
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAttachedTo},
	}
	if !slices.Equal(gateway.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", gateway.Related, wantRefs)
	}
}

// TestInternetGatewayCollectorMarksDetached는 VPC에 붙지 않은 게이트웨이를 detached로
// 표시하는지 확인한다.
//
// AWS는 붙어 있을 때만 attachment 상태를 준다. 떨어진 게이트웨이는 상태가 비는데, 그대로
// 두면 화면에서 "상태를 못 읽은 것"과 구분되지 않는다. 떨어진 게이트웨이는 요금이 붙지
// 않지만 정리 대상을 찾는 데 쓰이는 신호다.
func TestInternetGatewayCollectorMarksDetached(t *testing.T) {
	t.Parallel()

	api := &fakeInternetGatewayAPI{pages: []*awsec2.DescribeInternetGatewaysOutput{{
		InternetGateways: []ec2types.InternetGateway{{
			InternetGatewayId: aws.String("igw-orphan"),
		}},
	}}}

	got, err := ec2.NewInternetGateway(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got[0].Status != "detached" {
		t.Errorf("Status = %q, want %q", got[0].Status, "detached")
	}
	if value := got[0].FieldValue("VpcId"); value != "-" {
		t.Errorf("VpcId = %q, want %q", value, "-")
	}
	if len(got[0].Related) != 0 {
		t.Errorf("떨어진 게이트웨이에 관계가 있다: %+v", got[0].Related)
	}
}

func TestInternetGatewayCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeInternetGatewayAPI{
		pages: []*awsec2.DescribeInternetGatewaysOutput{{
			InternetGateways: []ec2types.InternetGateway{{InternetGatewayId: aws.String("igw-1")}},
			NextToken:        aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := ec2.NewInternetGateway(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "igw-1" {
		t.Errorf("부분 결과 = %+v, want igw-1", got)
	}
	if api.calls != 2 {
		t.Errorf("호출 수 = %d, want 2", api.calls)
	}
}

// --- NAT 게이트웨이 ---

type fakeNATGatewayAPI struct {
	pages []*awsec2.DescribeNatGatewaysOutput
	errs  []error
	calls int
}

func (f *fakeNATGatewayAPI) DescribeNatGateways(
	_ context.Context,
	_ *awsec2.DescribeNatGatewaysInput,
	_ ...func(*awsec2.Options),
) (*awsec2.DescribeNatGatewaysOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestNATGatewayCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewNATGateway(&fakeNATGatewayAPI{}).Type(); got != model.TypeEC2NATGateway {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2NATGateway)
	}
}

// TestNATGatewayCollectorConvertsFields는 필드 키가 SDK 필드명과 같고 주소 정보가 모두
// 남는지 확인한다.
//
// 필드 키는 카탈로그의 목록 열 이름과 문자열이 정확히 같아야 한다. 다르면 열은 보이는데
// 셀이 빈다. 실제로 PrivateIpAddress·ENI라는 카탈로그 열이 수집기의 PrivateIp·
// NetworkInterfaceId와 어긋나 빈 열로 보이던 회귀가 있었다.
func TestNATGatewayCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	api := &fakeNATGatewayAPI{pages: []*awsec2.DescribeNatGatewaysOutput{{
		NatGateways: []ec2types.NatGateway{{
			NatGatewayId:     aws.String("nat-0a1b"),
			State:            ec2types.NatGatewayStateAvailable,
			ConnectivityType: ec2types.ConnectivityTypePublic,
			VpcId:            aws.String("vpc-0123"),
			SubnetId:         aws.String("subnet-a"),
			CreateTime:       &created,
			NatGatewayAddresses: []ec2types.NatGatewayAddress{{
				PublicIp:           aws.String("3.35.1.1"),
				PrivateIp:          aws.String("10.0.1.10"),
				NetworkInterfaceId: aws.String("eni-0123"),
				AllocationId:       aws.String("eipalloc-0123"),
			}},
			Tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("nat-a")}},
		}},
	}}}

	got, err := ec2.NewNATGateway(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	gateway := got[0]
	if gateway.ID != "nat-0a1b" || gateway.Name != "nat-a" || gateway.Status != "available" {
		t.Errorf("기본 식별 정보 = %+v", gateway)
	}
	if gateway.CreatedAt == nil || !gateway.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", gateway.CreatedAt, created)
	}

	wantFields := []model.Field{
		{Key: "ConnectivityType", Value: "public"},
		{Key: "AvailabilityMode", Value: "-"},
		{Key: "VpcId", Value: "vpc-0123"},
		{Key: "SubnetId", Value: "subnet-a"},
		{Key: "PublicIp", Value: "3.35.1.1"},
		{Key: "PrivateIp", Value: "10.0.1.10"},
		{Key: "NetworkInterfaceId", Value: "eni-0123"},
		{Key: "AllocationId", Value: "eipalloc-0123"},
		{Key: "FailureCode", Value: "-"},
		{Key: "FailureMessage", Value: "-"},
	}
	if !slices.Equal(gateway.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", gateway.Fields, wantFields)
	}

	wantRefs := []model.Ref{
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2NetworkInterface, ID: "eni-0123", Relation: model.RelationAttachedENI},
		{Type: model.TypeEC2Address, ID: "eipalloc-0123", Relation: model.RelationAssociatedWith},
	}
	if !slices.Equal(gateway.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", gateway.Related, wantRefs)
	}
}

// TestNATGatewayCollectorJoinsMultipleAddresses는 주소가 여러 개인 NAT 게이트웨이를
// 확인한다. 보조 IP를 붙이면 주소가 늘어난다.
func TestNATGatewayCollectorJoinsMultipleAddresses(t *testing.T) {
	t.Parallel()

	api := &fakeNATGatewayAPI{pages: []*awsec2.DescribeNatGatewaysOutput{{
		NatGateways: []ec2types.NatGateway{{
			NatGatewayId: aws.String("nat-multi"),
			NatGatewayAddresses: []ec2types.NatGatewayAddress{
				{PublicIp: aws.String("3.35.1.1"), NetworkInterfaceId: aws.String("eni-1")},
				{PublicIp: aws.String("3.35.1.2"), NetworkInterfaceId: aws.String("eni-1")},
			},
		}},
	}}}

	got, err := ec2.NewNATGateway(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if value := got[0].FieldValue("PublicIp"); value != "3.35.1.1, 3.35.1.2" {
		t.Errorf("PublicIp = %q, want %q", value, "3.35.1.1, 3.35.1.2")
	}
	if value := got[0].FieldValue("PrivateIp"); value != "-" {
		t.Errorf("PrivateIp = %q, want %q", value, "-")
	}
}

// TestNATGatewayCollectorKeepsFailureReason은 생성이 실패한 게이트웨이의 사유를 남기는지
// 확인한다. 삭제되지 않고 failed 상태로 남아 있는 것을 조사할 때 필요하다.
func TestNATGatewayCollectorKeepsFailureReason(t *testing.T) {
	t.Parallel()

	api := &fakeNATGatewayAPI{pages: []*awsec2.DescribeNatGatewaysOutput{{
		NatGateways: []ec2types.NatGateway{{
			NatGatewayId:   aws.String("nat-failed"),
			State:          ec2types.NatGatewayStateFailed,
			FailureCode:    aws.String("InsufficientFreeAddressesInSubnet"),
			FailureMessage: aws.String("Subnet has insufficient free addresses"),
		}},
	}}}

	got, err := ec2.NewNATGateway(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", got[0].Status, "failed")
	}
	if value := got[0].FieldValue("FailureCode"); value != "InsufficientFreeAddressesInSubnet" {
		t.Errorf("FailureCode = %q", value)
	}
}

func TestNATGatewayCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeNATGatewayAPI{
		pages: []*awsec2.DescribeNatGatewaysOutput{{
			NatGateways: []ec2types.NatGateway{{NatGatewayId: aws.String("nat-1")}},
			NextToken:   aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := ec2.NewNATGateway(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "nat-1" {
		t.Errorf("부분 결과 = %+v, want nat-1", got)
	}
}

// --- VPC 엔드포인트 ---

type fakeVPCEndpointAPI struct {
	pages []*awsec2.DescribeVpcEndpointsOutput
	errs  []error
	calls int
}

func (f *fakeVPCEndpointAPI) DescribeVpcEndpoints(
	_ context.Context,
	_ *awsec2.DescribeVpcEndpointsInput,
	_ ...func(*awsec2.Options),
) (*awsec2.DescribeVpcEndpointsOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestVPCEndpointCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewVPCEndpoint(&fakeVPCEndpointAPI{}).Type(); got != model.TypeEC2VPCEndpoint {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2VPCEndpoint)
	}
}

// TestVPCEndpointCollectorConvertsInterfaceEndpoint는 인터페이스 엔드포인트의 필드와
// 관계를 확인한다.
//
// 필드 키는 SDK 필드명을 그대로 쓴다. 보안 그룹은 SDK가 Groups로 주므로 SecurityGroups가
// 아니라 Groups다. 카탈로그 열도 이 이름과 같아야 셀이 채워진다.
func TestVPCEndpointCollectorConvertsInterfaceEndpoint(t *testing.T) {
	t.Parallel()

	created := time.Date(2024, 12, 1, 2, 3, 4, 0, time.UTC)
	api := &fakeVPCEndpointAPI{pages: []*awsec2.DescribeVpcEndpointsOutput{{
		VpcEndpoints: []ec2types.VpcEndpoint{{
			VpcEndpointId:       aws.String("vpce-0123"),
			VpcEndpointType:     ec2types.VpcEndpointTypeInterface,
			ServiceName:         aws.String("com.amazonaws.ap-northeast-2.secretsmanager"),
			State:               ec2types.StateAvailable,
			VpcId:               aws.String("vpc-0123"),
			SubnetIds:           []string{"subnet-a", "subnet-c"},
			NetworkInterfaceIds: []string{"eni-1", "eni-2"},
			PrivateDnsEnabled:   aws.Bool(true),
			RequesterManaged:    aws.Bool(false),
			OwnerId:             aws.String("123456789012"),
			CreationTimestamp:   &created,
			Groups: []ec2types.SecurityGroupIdentifier{
				{GroupId: aws.String("sg-0123"), GroupName: aws.String("vpce-sg")},
			},
			Tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("secrets-vpce")}},
		}},
	}}}

	got, err := ec2.NewVPCEndpoint(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	endpoint := got[0]
	// State enum은 NAT 게이트웨이와 대소문자가 다르다(Available vs available). API가 준
	// 값을 그대로 쓰므로 여기서 통일하지 않는다. aws CLI 출력과 대조할 수 있어야 한다.
	if endpoint.ID != "vpce-0123" || endpoint.Name != "secrets-vpce" ||
		endpoint.Status != "Available" {
		t.Errorf("기본 식별 정보 = %+v", endpoint)
	}
	if endpoint.CreatedAt == nil || !endpoint.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", endpoint.CreatedAt, created)
	}

	wantFields := []model.Field{
		{Key: "VpcEndpointType", Value: "Interface"},
		{Key: "ServiceName", Value: "com.amazonaws.ap-northeast-2.secretsmanager"},
		{Key: "ServiceRegion", Value: "-"},
		{Key: "IpAddressType", Value: "-"},
		{Key: "VpcId", Value: "vpc-0123"},
		{Key: "SubnetIds", Value: "2"},
		{Key: "RouteTableIds", Value: "0"},
		{Key: "Groups", Value: "1"},
		{Key: "PrivateDnsEnabled", Value: "true"},
		{Key: "RequesterManaged", Value: "false"},
		{Key: "OwnerId", Value: "123456789012"},
		{Key: "FailureReason", Value: "-"},
	}
	if !slices.Equal(endpoint.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", endpoint.Fields, wantFields)
	}

	wantRefs := []model.Ref{
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-c", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2SecurityGroup, ID: "sg-0123", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2NetworkInterface, ID: "eni-1", Relation: model.RelationAttachedENI},
		{Type: model.TypeEC2NetworkInterface, ID: "eni-2", Relation: model.RelationAttachedENI},
	}
	if !slices.Equal(endpoint.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", endpoint.Related, wantRefs)
	}
}

// TestVPCEndpointCollectorConvertsGatewayEndpoint는 게이트웨이 엔드포인트를 확인한다.
//
// 게이트웨이 엔드포인트(S3·DynamoDB)는 서브넷과 ENI 없이 라우팅 테이블에만 붙는다.
// 인터페이스 엔드포인트와 관계가 완전히 다르다.
func TestVPCEndpointCollectorConvertsGatewayEndpoint(t *testing.T) {
	t.Parallel()

	api := &fakeVPCEndpointAPI{pages: []*awsec2.DescribeVpcEndpointsOutput{{
		VpcEndpoints: []ec2types.VpcEndpoint{{
			VpcEndpointId:   aws.String("vpce-s3"),
			VpcEndpointType: ec2types.VpcEndpointTypeGateway,
			ServiceName:     aws.String("com.amazonaws.ap-northeast-2.s3"),
			VpcId:           aws.String("vpc-0123"),
			RouteTableIds:   []string{"rtb-a", "rtb-b"},
		}},
	}}}

	got, err := ec2.NewVPCEndpoint(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	endpoint := got[0]
	if value := endpoint.FieldValue("RouteTableIds"); value != "2" {
		t.Errorf("RouteTableIds = %q, want %q", value, "2")
	}
	if value := endpoint.FieldValue("SubnetIds"); value != "0" {
		t.Errorf("SubnetIds = %q, want %q", value, "0")
	}

	wantRefs := []model.Ref{
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2RouteTable, ID: "rtb-a", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2RouteTable, ID: "rtb-b", Relation: model.RelationAssociatedWith},
	}
	if !slices.Equal(endpoint.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", endpoint.Related, wantRefs)
	}
}

func TestVPCEndpointCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeVPCEndpointAPI{
		pages: []*awsec2.DescribeVpcEndpointsOutput{{
			VpcEndpoints: []ec2types.VpcEndpoint{{VpcEndpointId: aws.String("vpce-1")}},
			NextToken:    aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := ec2.NewVPCEndpoint(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "vpce-1" {
		t.Errorf("부분 결과 = %+v, want vpce-1", got)
	}
}

func TestVPCEndpointCollectorStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeVPCEndpointAPI{errs: []error{context.Canceled}}
	if _, err := ec2.NewVPCEndpoint(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
