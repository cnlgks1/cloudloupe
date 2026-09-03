package rds_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	rdscollector "github.com/cnlgks1/cloudloupe/internal/collector/rds"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeDBInstancesAPI는 describeDBInstancesAPI를 만족하는 테스트 대역이다.
type fakeDBInstancesAPI struct {
	pages []*awsrds.DescribeDBInstancesOutput
	errs  []error
	calls int
}

func (f *fakeDBInstancesAPI) DescribeDBInstances(
	_ context.Context,
	_ *awsrds.DescribeDBInstancesInput,
	_ ...func(*awsrds.Options),
) (*awsrds.DescribeDBInstancesOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func TestDBInstanceCollectorType(t *testing.T) {
	t.Parallel()

	if got := rdscollector.NewDBInstance(&fakeDBInstancesAPI{}).Type(); got != model.TypeRDSDBInstance {
		t.Errorf("Type() = %q, want %q", got, model.TypeRDSDBInstance)
	}
}

// TestDBInstanceCollectorConvertsFields는 인스턴스 응답의 필드와 관계 변환을 확인한다.
//
// 서브넷 그룹은 VPC와 서브넷을 함께 들고 있어 네트워크 관계의 출발점이 된다.
func TestDBInstanceCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2024, 11, 12, 13, 14, 15, 0, time.UTC)
	api := &fakeDBInstancesAPI{pages: []*awsrds.DescribeDBInstancesOutput{{
		DBInstances: []rdstypes.DBInstance{{
			DBInstanceIdentifier: aws.String("orders-aurora-1"),
			DBInstanceArn:        aws.String("arn:aws:rds:ap-northeast-2:123456789012:db:orders-aurora-1"),
			DBInstanceStatus:     aws.String("available"),
			InstanceCreateTime:   &created,
			DBInstanceClass:      aws.String("db.r6g.large"),
			Engine:               aws.String("aurora-postgresql"),
			EngineVersion:        aws.String("15.4"),
			AvailabilityZone:     aws.String("ap-northeast-2a"),
			MultiAZ:              aws.Bool(false),
			StorageType:          aws.String("aurora"),
			StorageEncrypted:     aws.Bool(true),
			PubliclyAccessible:   aws.Bool(false),
			DBClusterIdentifier:  aws.String("orders-aurora"),
			KmsKeyId:             aws.String("key-1"),
			Endpoint: &rdstypes.Endpoint{
				Address: aws.String("orders-aurora-1.abc.ap-northeast-2.rds.amazonaws.com"),
				Port:    aws.Int32(5432),
			},
			DBSubnetGroup: &rdstypes.DBSubnetGroup{
				DBSubnetGroupName: aws.String("orders-subnets"),
				VpcId:             aws.String("vpc-0123"),
				Subnets: []rdstypes.Subnet{
					{SubnetIdentifier: aws.String("subnet-a")},
					{SubnetIdentifier: aws.String("subnet-c")},
				},
			},
			VpcSecurityGroups: []rdstypes.VpcSecurityGroupMembership{
				{VpcSecurityGroupId: aws.String("sg-0123")},
			},
			TagList: []rdstypes.Tag{{Key: aws.String("Name"), Value: aws.String("orders-writer")}},
		}},
	}}}

	got, err := rdscollector.NewDBInstance(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	instance := got[0]
	if instance.Type != model.TypeRDSDBInstance || instance.ID != "orders-aurora-1" {
		t.Errorf("기본 식별 정보 = %+v", instance)
	}
	if instance.Status != "available" || instance.CreatedAt == nil ||
		!instance.CreatedAt.Equal(created) {
		t.Errorf("상태 또는 생성 시각 = %+v", instance)
	}

	// 엔드포인트는 주소와 포트를 함께 보여준다. 접속 대상을 한 줄로 확인하기 위한 것이다.
	wantEndpoint := "orders-aurora-1.abc.ap-northeast-2.rds.amazonaws.com:5432"
	if value := instance.FieldValue("Endpoint"); value != wantEndpoint {
		t.Errorf("Endpoint = %q, want %q", value, wantEndpoint)
	}
	if value := instance.FieldValue("DBSubnetGroup"); value != "orders-subnets" {
		t.Errorf("DBSubnetGroup = %q, want %q", value, "orders-subnets")
	}
	if value := instance.FieldValue("MultiAZ"); value != "false" {
		t.Errorf("MultiAZ = %q, want %q", value, "false")
	}

	wantRefs := []model.Ref{
		{Type: model.TypeRDSDBCluster, ID: "orders-aurora", Relation: "DBClusterIdentifier"},
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: "DBSubnetGroup.VpcId"},
		{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"},
		{Type: model.TypeEC2Subnet, ID: "subnet-c", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"},
		{Type: model.TypeEC2SecurityGroup, ID: "sg-0123", Relation: "VpcSecurityGroups.VpcSecurityGroupId"},
		// key ID 형태이므로 ARN이 아니라 리소스 ID로 참조해야 한다.
		{Type: model.TypeKMSKey, ID: "key-1", Relation: "KmsKeyId"},
	}
	if !slices.Equal(instance.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", instance.Related, wantRefs)
	}

	wantIdentifiers := []model.Identifier{{
		Kind:  model.IdentifierDNS,
		Value: "orders-aurora-1.abc.ap-northeast-2.rds.amazonaws.com",
	}}
	if !slices.Equal(instance.Identifiers, wantIdentifiers) {
		t.Errorf("Identifiers = %+v, want %+v", instance.Identifiers, wantIdentifiers)
	}
}

// TestDBInstanceCollectorHandlesMissingOptionalStructs는 선택적 구조체가 없어도 변환이
// 죽지 않는지 확인한다. 생성 중인 인스턴스는 엔드포인트와 서브넷 그룹이 아직 비어 있다.
func TestDBInstanceCollectorHandlesMissingOptionalStructs(t *testing.T) {
	t.Parallel()

	api := &fakeDBInstancesAPI{pages: []*awsrds.DescribeDBInstancesOutput{{
		DBInstances: []rdstypes.DBInstance{{
			DBInstanceIdentifier: aws.String("creating-db"),
			DBInstanceStatus:     aws.String("creating"),
		}},
	}}}

	got, err := rdscollector.NewDBInstance(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	instance := got[0]
	if value := instance.FieldValue("Endpoint"); value != "-" {
		t.Errorf("Endpoint = %q, want %q", value, "-")
	}
	if value := instance.FieldValue("DBSubnetGroup"); value != "-" {
		t.Errorf("DBSubnetGroup = %q, want %q", value, "-")
	}
	if len(instance.Related) != 0 {
		t.Errorf("관계가 없어야 한다: %+v", instance.Related)
	}
}

// TestDBInstanceCollectorUsesAddressWhenPortMissing은 포트가 없을 때 주소만 보여주는지
// 확인한다. 없는 포트를 0으로 채우면 접속 정보를 잘못 읽게 된다.
func TestDBInstanceCollectorUsesAddressWhenPortMissing(t *testing.T) {
	t.Parallel()

	api := &fakeDBInstancesAPI{pages: []*awsrds.DescribeDBInstancesOutput{{
		DBInstances: []rdstypes.DBInstance{{
			DBInstanceIdentifier: aws.String("no-port"),
			Endpoint:             &rdstypes.Endpoint{Address: aws.String("db.example.com")},
		}},
	}}}

	got, err := rdscollector.NewDBInstance(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if value := got[0].FieldValue("Endpoint"); value != "db.example.com" {
		t.Errorf("Endpoint = %q, want %q", value, "db.example.com")
	}
}

func TestDBInstanceCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeDBInstancesAPI{pages: []*awsrds.DescribeDBInstancesOutput{
		{
			DBInstances: []rdstypes.DBInstance{{DBInstanceIdentifier: aws.String("db-1")}},
			Marker:      aws.String("page2"),
		},
		{DBInstances: []rdstypes.DBInstance{{DBInstanceIdentifier: aws.String("db-2")}}},
	}}

	got, err := rdscollector.NewDBInstance(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 || api.calls != 2 {
		t.Errorf("인스턴스 %d개(호출 %d회), want 2개(2회)", len(got), api.calls)
	}
}

func TestDBInstanceCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("AccessDenied")
	api := &fakeDBInstancesAPI{
		pages: []*awsrds.DescribeDBInstancesOutput{{
			DBInstances: []rdstypes.DBInstance{{DBInstanceIdentifier: aws.String("db-1")}},
			Marker:      aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := rdscollector.NewDBInstance(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "db-1" {
		t.Errorf("부분 결과 = %+v, want db-1", got)
	}
}
