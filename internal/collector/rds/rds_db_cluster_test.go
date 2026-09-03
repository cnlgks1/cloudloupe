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

// fakeDBClustersAPI는 describeDBClustersAPI를 만족하는 테스트 대역이다.
//
// errs는 호출 차수별 오류다. 두 번째 페이지만 실패시켜 부분 결과 보존을 확인하는 데 쓴다.
type fakeDBClustersAPI struct {
	pages []*awsrds.DescribeDBClustersOutput
	errs  []error
	calls int
}

func (f *fakeDBClustersAPI) DescribeDBClusters(
	_ context.Context,
	_ *awsrds.DescribeDBClustersInput,
	_ ...func(*awsrds.Options),
) (*awsrds.DescribeDBClustersOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestDBClusterCollectorType(t *testing.T) {
	t.Parallel()

	if got := rdscollector.NewDBCluster(&fakeDBClustersAPI{}).Type(); got != model.TypeRDSDBCluster {
		t.Errorf("Type() = %q, want %q", got, model.TypeRDSDBCluster)
	}
}

// TestDBClusterCollectorConvertsFields는 SDK 응답이 표시 필드와 관계로 옮겨지는지 확인한다.
//
// 값은 API가 준 것을 그대로 쓴다. 화면과 aws CLI 출력을 대조할 수 있어야 하기 때문이다.
func TestDBClusterCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	api := &fakeDBClustersAPI{pages: []*awsrds.DescribeDBClustersOutput{{
		DBClusters: []rdstypes.DBCluster{{
			DBClusterIdentifier: aws.String("orders-aurora"),
			DBClusterArn:        aws.String("arn:aws:rds:ap-northeast-2:123456789012:cluster:orders-aurora"),
			Status:              aws.String("available"),
			ClusterCreateTime:   &created,
			Engine:              aws.String("aurora-postgresql"),
			EngineMode:          aws.String("provisioned"),
			EngineVersion:       aws.String("15.4"),
			DatabaseName:        aws.String("orders"),
			Endpoint:            aws.String("orders.cluster-abc.ap-northeast-2.rds.amazonaws.com"),
			ReaderEndpoint:      aws.String("orders.cluster-ro-abc.ap-northeast-2.rds.amazonaws.com"),
			Port:                aws.Int32(5432),
			MultiAZ:             aws.Bool(true),
			StorageEncrypted:    aws.Bool(true),
			KmsKeyId:            aws.String("arn:aws:kms:ap-northeast-2:123456789012:key/key-1"),
			DBSubnetGroup:       aws.String("orders-subnets"),
			AvailabilityZones:   []string{"ap-northeast-2a", "ap-northeast-2c"},
			DBClusterMembers: []rdstypes.DBClusterMember{
				{DBInstanceIdentifier: aws.String("orders-aurora-1")},
				{DBInstanceIdentifier: aws.String("orders-aurora-2")},
			},
			VpcSecurityGroups: []rdstypes.VpcSecurityGroupMembership{
				{VpcSecurityGroupId: aws.String("sg-0123")},
			},
			TagList: []rdstypes.Tag{
				{Key: aws.String("Name"), Value: aws.String("orders")},
				{Key: aws.String("Environment"), Value: aws.String("production")},
			},
		}},
	}}}

	got, err := rdscollector.NewDBCluster(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	cluster := got[0]
	if cluster.Type != model.TypeRDSDBCluster || cluster.ID != "orders-aurora" ||
		cluster.Name != "orders-aurora" {
		t.Errorf("기본 식별 정보 = %+v", cluster)
	}
	if cluster.Status != "available" || cluster.Region != "ap-northeast-2" ||
		cluster.Profile != "prod" || cluster.AccountID != "123456789012" {
		t.Errorf("범위 또는 상태 = %+v", cluster)
	}
	if cluster.CreatedAt == nil || !cluster.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", cluster.CreatedAt, created)
	}

	for _, want := range []model.Field{
		{Key: "Engine", Value: "aurora-postgresql"},
		{Key: "EngineVersion", Value: "15.4"},
		{Key: "Port", Value: "5432"},
		{Key: "MultiAZ", Value: "true"},
		{Key: "StorageEncrypted", Value: "true"},
		{Key: "AvailabilityZones", Value: "ap-northeast-2a, ap-northeast-2c"},
	} {
		if got := cluster.FieldValue(want.Key); got != want.Value {
			t.Errorf("%s = %q, want %q", want.Key, got, want.Value)
		}
	}

	wantTags := []model.Field{
		{Key: "Environment", Value: "production"},
		{Key: "Name", Value: "orders"},
	}
	if !slices.Equal(cluster.Tags, wantTags) {
		t.Errorf("Tags = %+v, want %+v", cluster.Tags, wantTags)
	}

	// 엔드포인트는 Route 53 별칭 대상과 맞물리므로 DNS 보조 식별자로 남긴다.
	wantIdentifiers := []model.Identifier{
		{Kind: model.IdentifierDNS, Value: "orders.cluster-abc.ap-northeast-2.rds.amazonaws.com"},
		{Kind: model.IdentifierDNS, Value: "orders.cluster-ro-abc.ap-northeast-2.rds.amazonaws.com"},
	}
	if !slices.Equal(cluster.Identifiers, wantIdentifiers) {
		t.Errorf("Identifiers = %+v, want %+v", cluster.Identifiers, wantIdentifiers)
	}

	wantRefs := []model.Ref{
		{Type: model.TypeRDSDBInstance, ID: "orders-aurora-1", Relation: model.RelationAssociatedWith},
		{Type: model.TypeRDSDBInstance, ID: "orders-aurora-2", Relation: model.RelationAssociatedWith},
		{
			Type:           model.TypeKMSKey,
			ID:             "arn:aws:kms:ap-northeast-2:123456789012:key/key-1",
			IdentifierKind: model.IdentifierARN,
			Relation:       model.RelationAssociatedWith,
		},
		{Type: model.TypeEC2SecurityGroup, ID: "sg-0123", Relation: model.RelationAssociatedWith},
	}
	if !slices.Equal(cluster.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", cluster.Related, wantRefs)
	}
}

// TestDBClusterCollectorClassifiesKMSIdentifier는 RDS가 주는 KMS 식별자 네 형태를 구분하는지
// 확인한다.
//
// RDS는 key ARN, key ID, alias ARN, alias name을 모두 반환할 수 있다. 전부 ARN으로 취급하면
// key ID는 ARN 색인에서 찾을 수 없고, alias는 해석 자체가 불가능한 관계가 된다.
func TestDBClusterCollectorClassifiesKMSIdentifier(t *testing.T) {
	t.Parallel()

	const keyARN = "arn:aws:kms:ap-northeast-2:123456789012:key/key-1"

	tests := []struct {
		name       string
		identifier string
		wantRefs   []model.Ref
	}{
		{
			name:       "key ARN은 ARN으로 참조한다",
			identifier: keyARN,
			wantRefs: []model.Ref{{
				Type:           model.TypeKMSKey,
				ID:             keyARN,
				IdentifierKind: model.IdentifierARN,
				Relation:       model.RelationAssociatedWith,
			}},
		},
		{
			name:       "key ID는 리소스 ID로 참조한다",
			identifier: "key-1",
			wantRefs: []model.Ref{{
				Type:     model.TypeKMSKey,
				ID:       "key-1",
				Relation: model.RelationAssociatedWith,
			}},
		},
		{
			// 별칭은 현재 KMS 모델이 색인하지 않는다. 관계를 만들면 끊긴 간선이 된다.
			name:       "alias name은 관계를 만들지 않는다",
			identifier: "alias/app-data",
			wantRefs:   nil,
		},
		{
			name:       "alias ARN도 관계를 만들지 않는다",
			identifier: "arn:aws:kms:ap-northeast-2:123456789012:alias/app-data",
			wantRefs:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := &fakeDBClustersAPI{pages: []*awsrds.DescribeDBClustersOutput{{
				DBClusters: []rdstypes.DBCluster{{
					DBClusterIdentifier: aws.String("orders"),
					KmsKeyId:            aws.String(tt.identifier),
				}},
			}}}

			got, err := rdscollector.NewDBCluster(api).Collect(
				context.Background(), collect.Request{Scope: testScope()})
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}

			if !slices.Equal(got[0].Related, tt.wantRefs) {
				t.Errorf("Related = %+v, want %+v", got[0].Related, tt.wantRefs)
			}

			// 관계를 만들지 않아도 원본 값은 필드에 남아야 조사에 쓸 수 있다.
			if value := got[0].FieldValue("KmsKeyId"); value != tt.identifier {
				t.Errorf("KmsKeyId = %q, want %q", value, tt.identifier)
			}
		})
	}
}

// TestDBClusterCollectorDistinguishesMissingValues는 응답에 없는 값과 실제 0/false를
// 구분하는지 확인한다. 둘을 같게 보이면 설정을 잘못 판단하게 된다.
func TestDBClusterCollectorDistinguishesMissingValues(t *testing.T) {
	t.Parallel()

	api := &fakeDBClustersAPI{pages: []*awsrds.DescribeDBClustersOutput{{
		DBClusters: []rdstypes.DBCluster{
			{DBClusterIdentifier: aws.String("explicit"), Port: aws.Int32(0), MultiAZ: aws.Bool(false)},
			{DBClusterIdentifier: aws.String("absent")},
		},
	}}}

	got, err := rdscollector.NewDBCluster(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if value := got[0].FieldValue("Port"); value != "0" {
		t.Errorf("명시된 0의 Port = %q, want %q", value, "0")
	}
	if value := got[0].FieldValue("MultiAZ"); value != "false" {
		t.Errorf("명시된 false의 MultiAZ = %q, want %q", value, "false")
	}
	if value := got[1].FieldValue("Port"); value != "-" {
		t.Errorf("값이 없는 Port = %q, want %q", value, "-")
	}
	if value := got[1].FieldValue("MultiAZ"); value != "-" {
		t.Errorf("값이 없는 MultiAZ = %q, want %q", value, "-")
	}
	if got[1].CreatedAt != nil {
		t.Errorf("생성 시각이 없으면 CreatedAt은 nil이어야 한다: %v", got[1].CreatedAt)
	}
	if got[1].Identifiers != nil && len(got[1].Identifiers) != 0 {
		t.Errorf("엔드포인트가 없으면 DNS 식별자를 만들지 않아야 한다: %+v", got[1].Identifiers)
	}
}

func TestDBClusterCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeDBClustersAPI{pages: []*awsrds.DescribeDBClustersOutput{
		{
			DBClusters: []rdstypes.DBCluster{{DBClusterIdentifier: aws.String("cluster-1")}},
			Marker:     aws.String("page2"),
		},
		{DBClusters: []rdstypes.DBCluster{{DBClusterIdentifier: aws.String("cluster-2")}}},
	}}

	got, err := rdscollector.NewDBCluster(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 || api.calls != 2 {
		t.Errorf("클러스터 %d개(호출 %d회), want 2개(2회)", len(got), api.calls)
	}
}

// TestDBClusterCollectorKeepsPartialResultsOnPaginationError는 페이지 중간 실패에도 앞
// 페이지 결과를 살리는지 확인한다. 절반이라도 보여주는 편이 낫다.
func TestDBClusterCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("RequestLimitExceeded")
	api := &fakeDBClustersAPI{
		pages: []*awsrds.DescribeDBClustersOutput{{
			DBClusters: []rdstypes.DBCluster{{DBClusterIdentifier: aws.String("cluster-1")}},
			Marker:     aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := rdscollector.NewDBCluster(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "cluster-1" {
		t.Errorf("부분 결과 = %+v, want cluster-1", got)
	}
}

func TestDBClusterCollectorStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeDBClustersAPI{errs: []error{context.Canceled}}
	if _, err := rdscollector.NewDBCluster(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
