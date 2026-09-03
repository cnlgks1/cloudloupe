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

// fakeRouteTableAPI는 describeRouteTablesAPI를 만족하는 테스트 대역이다.
type fakeRouteTableAPI struct {
	pages []*awsec2.DescribeRouteTablesOutput
	errs  []error
	calls int
}

func (f *fakeRouteTableAPI) DescribeRouteTables(
	_ context.Context,
	_ *awsec2.DescribeRouteTablesInput,
	_ ...func(*awsec2.Options),
) (*awsec2.DescribeRouteTablesOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func routeTablePage(tables ...ec2types.RouteTable) *fakeRouteTableAPI {
	return &fakeRouteTableAPI{
		pages: []*awsec2.DescribeRouteTablesOutput{{RouteTables: tables}},
	}
}

// testScope는 네트워크 수집기 테스트가 공유하는 조회 범위다.
func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestRouteTableCollectorType(t *testing.T) {
	t.Parallel()

	if got := ec2.NewRouteTable(&fakeRouteTableAPI{}).Type(); got != model.TypeEC2RouteTable {
		t.Errorf("Type() = %q, want %q", got, model.TypeEC2RouteTable)
	}
}

// TestRouteTableCollectorConvertsFields는 필드와 VPC·서브넷 연결을 확인한다.
//
// 라우팅 항목은 개수만 보여준다. 목록에 전부 펼치면 폭이 부족해지고, 실제 대상은 관계로
// 남기므로 그래프에서 따라갈 수 있다.
func TestRouteTableCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	api := routeTablePage(ec2types.RouteTable{
		RouteTableId: aws.String("rtb-0123"),
		VpcId:        aws.String("vpc-0123"),
		OwnerId:      aws.String("123456789012"),
		Associations: []ec2types.RouteTableAssociation{
			{Main: aws.Bool(false), SubnetId: aws.String("subnet-a")},
			{Main: aws.Bool(false), SubnetId: aws.String("subnet-c")},
		},
		Routes: []ec2types.Route{
			{DestinationCidrBlock: aws.String("10.0.0.0/16"), GatewayId: aws.String("local")},
			{DestinationCidrBlock: aws.String("0.0.0.0/0"), NatGatewayId: aws.String("nat-0a1b")},
		},
		PropagatingVgws: []ec2types.PropagatingVgw{{GatewayId: aws.String("vgw-1")}},
		Tags:            []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("private-rt")}},
	})

	got, err := ec2.NewRouteTable(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	table := got[0]
	if table.ID != "rtb-0123" || table.Name != "private-rt" {
		t.Errorf("기본 식별 정보 = %+v", table)
	}

	wantFields := []model.Field{
		{Key: "VpcId", Value: "vpc-0123"},
		{Key: "Main", Value: "false"},
		{Key: "Associations", Value: "2"},
		{Key: "Routes", Value: "2"},
		{Key: "PropagatingVgws", Value: "1"},
		{Key: "OwnerId", Value: "123456789012"},
	}
	if !slices.Equal(table.Fields, wantFields) {
		t.Errorf("Fields = %+v, want %+v", table.Fields, wantFields)
	}

	wantRefs := []model.Ref{
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-c", Relation: model.RelationAssociatedWith},
		{
			Type: model.TypeEC2NATGateway, ID: "nat-0a1b",
			Relation: model.RelationRoutesTo, Via: "0.0.0.0/0",
		},
	}
	if !slices.Equal(table.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", table.Related, wantRefs)
	}
}

// TestRouteTableCollectorMarksMainTable은 연결 중 하나라도 main이면 main으로 표시하는지
// 확인한다. VPC의 기본 라우팅 테이블을 구분하는 것이 조사에서 첫 단서가 된다.
func TestRouteTableCollectorMarksMainTable(t *testing.T) {
	t.Parallel()

	api := routeTablePage(ec2types.RouteTable{
		RouteTableId: aws.String("rtb-main"),
		Associations: []ec2types.RouteTableAssociation{
			{Main: aws.Bool(false)},
			{Main: aws.Bool(true)},
		},
	})

	got, err := ec2.NewRouteTable(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if value := got[0].FieldValue("Main"); value != "true" {
		t.Errorf("Main = %q, want %q", value, "true")
	}
}

// TestRouteTableCollectorResolvesRouteTargets는 라우팅 대상 종류별로 올바른 타입에
// 연결하는지 확인한다.
//
// 지원하지 않는 대상(Transit Gateway, VPC 피어링, VPC 엔드포인트 등)은 관계로 만들지
// 않는다. 대응하는 타입이 없으면 해석되지 않는 끊긴 관계가 된다.
func TestRouteTableCollectorResolvesRouteTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		route ec2types.Route
		want  []model.Ref
	}{
		{
			name: "NAT 게이트웨이",
			route: ec2types.Route{
				DestinationCidrBlock: aws.String("0.0.0.0/0"),
				NatGatewayId:         aws.String("nat-1"),
			},
			want: []model.Ref{{
				Type: model.TypeEC2NATGateway, ID: "nat-1",
				Relation: model.RelationRoutesTo, Via: "0.0.0.0/0",
			}},
		},
		{
			name: "인터넷 게이트웨이",
			route: ec2types.Route{
				DestinationCidrBlock: aws.String("0.0.0.0/0"),
				GatewayId:            aws.String("igw-1"),
			},
			want: []model.Ref{{
				Type: model.TypeEC2InternetGateway, ID: "igw-1",
				Relation: model.RelationRoutesTo, Via: "0.0.0.0/0",
			}},
		},
		{
			name: "네트워크 인터페이스",
			route: ec2types.Route{
				DestinationCidrBlock: aws.String("192.168.0.0/16"),
				NetworkInterfaceId:   aws.String("eni-1"),
			},
			want: []model.Ref{{
				Type: model.TypeEC2NetworkInterface, ID: "eni-1",
				Relation: model.RelationRoutesTo, Via: "192.168.0.0/16",
			}},
		},
		{
			name: "IPv6 목적지",
			route: ec2types.Route{
				DestinationIpv6CidrBlock: aws.String("::/0"),
				NatGatewayId:             aws.String("nat-6"),
			},
			want: []model.Ref{{
				Type: model.TypeEC2NATGateway, ID: "nat-6",
				Relation: model.RelationRoutesTo, Via: "::/0",
			}},
		},
		{
			// local 라우팅은 VPC 자체를 가리키는 가상 대상이라 별도 리소스가 없다.
			name: "local은 관계를 만들지 않는다",
			route: ec2types.Route{
				DestinationCidrBlock: aws.String("10.0.0.0/16"),
				GatewayId:            aws.String("local"),
			},
			want: nil,
		},
		{
			name: "지원하지 않는 대상은 건너뛴다",
			route: ec2types.Route{
				DestinationCidrBlock: aws.String("10.1.0.0/16"),
				TransitGatewayId:     aws.String("tgw-1"),
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := routeTablePage(ec2types.RouteTable{
				RouteTableId: aws.String("rtb-1"),
				Routes:       []ec2types.Route{tt.route},
			})

			got, err := ec2.NewRouteTable(api).Collect(
				context.Background(), collect.Request{Scope: testScope()})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}

			if !slices.Equal(got[0].Related, tt.want) {
				t.Errorf("Related = %+v, want %+v", got[0].Related, tt.want)
			}
		})
	}
}

// TestRouteTableCollectorLinksGatewayAssociation은 게이트웨이 라우팅 테이블 연결을
// 확인한다. 엣지 연결은 SubnetId 대신 GatewayId로 온다.
func TestRouteTableCollectorLinksGatewayAssociation(t *testing.T) {
	t.Parallel()

	api := routeTablePage(ec2types.RouteTable{
		RouteTableId: aws.String("rtb-edge"),
		Associations: []ec2types.RouteTableAssociation{
			{GatewayId: aws.String("igw-1")},
			// 지원하지 않는 게이트웨이 종류는 건너뛴다.
			{GatewayId: aws.String("vgw-1")},
		},
	})

	got, err := ec2.NewRouteTable(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := []model.Ref{
		{Type: model.TypeEC2InternetGateway, ID: "igw-1", Relation: model.RelationAssociatedWith},
	}
	if !slices.Equal(got[0].Related, want) {
		t.Errorf("Related = %+v, want %+v", got[0].Related, want)
	}
}

func TestRouteTableCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeRouteTableAPI{pages: []*awsec2.DescribeRouteTablesOutput{
		{
			RouteTables: []ec2types.RouteTable{{RouteTableId: aws.String("rtb-1")}},
			NextToken:   aws.String("page2"),
		},
		{RouteTables: []ec2types.RouteTable{{RouteTableId: aws.String("rtb-2")}}},
	}}

	got, err := ec2.NewRouteTable(api).Collect(context.Background(), collect.Request{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 || api.calls != 2 {
		t.Errorf("라우팅 테이블 %d개(호출 %d회), want 2개(2회)", len(got), api.calls)
	}
}

func TestRouteTableCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeRouteTableAPI{
		pages: []*awsec2.DescribeRouteTablesOutput{{
			RouteTables: []ec2types.RouteTable{{RouteTableId: aws.String("rtb-1")}},
			NextToken:   aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := ec2.NewRouteTable(api).Collect(context.Background(), collect.Request{})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "rtb-1" {
		t.Errorf("부분 결과 = %+v, want rtb-1", got)
	}
}
