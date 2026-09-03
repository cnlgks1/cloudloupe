package dynamodb_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ddbcollector "github.com/cnlgks1/cloudloupe/internal/collector/dynamodb"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeDDB는 테이블 수집기가 쓰는 ListTables·DescribeTable을 대신한다.
//
// listPages는 ListTables의 페이지들, listErr는 마지막 페이지 뒤에 낼 오류다. describe는
// 이름으로 상세를, describeErr는 특정 이름만 실패시킨다.
type fakeDDB struct {
	listPages   [][]string
	listErr     error
	describe    map[string]ddbtypes.TableDescription
	describeErr map[string]error

	mu            sync.Mutex
	listCalls     int
	describeCalls atomic.Int32
	running       int32
	peakRunning   int32
}

func (f *fakeDDB) ListTables(
	_ context.Context,
	_ *awsddb.ListTablesInput,
	_ ...func(*awsddb.Options),
) (*awsddb.ListTablesOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if i >= len(f.listPages) {
		if f.listErr != nil {
			return nil, f.listErr
		}

		return &awsddb.ListTablesOutput{}, nil
	}

	out := &awsddb.ListTablesOutput{TableNames: f.listPages[i]}
	if i+1 < len(f.listPages) || f.listErr != nil {
		out.LastEvaluatedTableName = aws.String("next")
	}

	return out, nil
}

func (f *fakeDDB) DescribeTable(
	_ context.Context,
	in *awsddb.DescribeTableInput,
	_ ...func(*awsddb.Options),
) (*awsddb.DescribeTableOutput, error) {
	f.describeCalls.Add(1)

	running := atomic.AddInt32(&f.running, 1)
	for {
		peak := atomic.LoadInt32(&f.peakRunning)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peakRunning, peak, running) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&f.running, -1)

	name := aws.ToString(in.TableName)
	if err, ok := f.describeErr[name]; ok {
		return nil, err
	}

	table, ok := f.describe[name]
	if !ok {
		return &awsddb.DescribeTableOutput{}, nil
	}

	return &awsddb.DescribeTableOutput{Table: &table}, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestTableCollectorType(t *testing.T) {
	t.Parallel()

	if got := ddbcollector.NewTable(&fakeDDB{}).Type(); got != model.TypeDynamoDBTable {
		t.Errorf("Type() = %q, want %q", got, model.TypeDynamoDBTable)
	}
}

// TestTableCollectConvertsFieldsAndKMS는 SDK 값을 그대로 담고 키 스키마·GSI를 표기하며
// KMS 암호화면 키 관계를 만드는지 확인한다.
func TestTableCollectConvertsFieldsAndKMS(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	arn := "arn:aws:dynamodb:ap-northeast-2:123456789012:table/orders"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeDDB{
		listPages: [][]string{{"orders"}},
		describe: map[string]ddbtypes.TableDescription{
			"orders": {
				TableName:        aws.String("orders"),
				TableArn:         aws.String(arn),
				TableStatus:      ddbtypes.TableStatusActive,
				CreationDateTime: &created,
				ItemCount:        aws.Int64(1500),
				TableSizeBytes:   aws.Int64(204800),
				KeySchema: []ddbtypes.KeySchemaElement{
					{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
					{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
				},
				BillingModeSummary: &ddbtypes.BillingModeSummary{
					BillingMode: ddbtypes.BillingModePayPerRequest,
				},
				GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndexDescription{
					{IndexName: aws.String("gsi-status")},
				},
				SSEDescription: &ddbtypes.SSEDescription{
					Status:          ddbtypes.SSEStatusEnabled,
					SSEType:         ddbtypes.SSETypeKms,
					KMSMasterKeyArn: aws.String(kmsKey),
				},
			},
		},
	}

	got, err := ddbcollector.NewTable(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("테이블 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "orders" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", res.Status)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	if got, want := res.FieldValue("KeySchema"), "pk(HASH), sk(RANGE)"; got != want {
		t.Errorf("KeySchema = %q, want %q", got, want)
	}
	// 값은 AWS가 준 그대로. PAY_PER_REQUEST/KMS를 번역하지 않는다.
	if got, want := res.FieldValue("BillingMode"), "PAY_PER_REQUEST"; got != want {
		t.Errorf("BillingMode = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("ItemCount"), "1500"; got != want {
		t.Errorf("ItemCount = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("GlobalSecondaryIndexes"), "gsi-status"; got != want {
		t.Errorf("GSI = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("SSEType"), "KMS"; got != want {
		t.Errorf("SSEType = %q, want %q", got, want)
	}

	if len(res.Related) != 1 {
		t.Fatalf("관계 %d개, want 1", len(res.Related))
	}
	ref := res.Related[0]
	if ref.Type != model.TypeKMSKey || ref.Relation != "SSEDescription.KMSMasterKeyArn" || ref.ID != kmsKey {
		t.Errorf("관계 = %+v", ref)
	}
	if ref.IdentifierKind != model.IdentifierARN {
		t.Errorf("IdentifierKind = %q, want %q", ref.IdentifierKind, model.IdentifierARN)
	}
}

// TestTableCollectDistinguishesOnDemand는 온디맨드 테이블에서 프로비저닝 용량을 "-"로
// 두는지 확인한다. 온디맨드는 용량 설정 자체가 없다.
func TestTableCollectDistinguishesOnDemand(t *testing.T) {
	t.Parallel()

	api := &fakeDDB{
		listPages: [][]string{{"orders"}},
		describe: map[string]ddbtypes.TableDescription{
			"orders": {TableName: aws.String("orders"), TableStatus: ddbtypes.TableStatusActive},
		},
	}

	got, err := ddbcollector.NewTable(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if v := got[0].FieldValue("ReadCapacityUnits"); v != "-" {
		t.Errorf("온디맨드 ReadCapacityUnits = %q, want -", v)
	}
	if v := got[0].FieldValue("ItemCount"); v != "-" {
		t.Errorf("ItemCount 없음 = %q, want -", v)
	}
	if len(got[0].Related) != 0 {
		t.Errorf("암호화 없으면 관계 없음, got %+v", got[0].Related)
	}
}

// TestTableCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를 살리는지
// 확인한다.
func TestTableCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeDDB{
		listPages: [][]string{{"a", "b", "c"}},
		describe: map[string]ddbtypes.TableDescription{
			"a": {TableName: aws.String("a")},
			"c": {TableName: aws.String("c")},
		},
		describeErr: map[string]error{"b": denied},
	}

	got, err := ddbcollector.NewTable(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), "b") {
		t.Errorf("오류에 실패한 이름이 없다: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a", "c"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}

// TestTableCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestTableCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeDDB{
		listPages: [][]string{{"t1"}, {"t2"}},
		describe: map[string]ddbtypes.TableDescription{
			"t1": {TableName: aws.String("t1")},
			"t2": {TableName: aws.String("t2")},
		},
	}

	got, err := ddbcollector.NewTable(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("테이블 %d개 수집, want 2", len(got))
	}
	if api.listCalls != 2 {
		t.Errorf("ListTables 호출 = %d회, want 2", api.listCalls)
	}
}

// TestTableCollectLimitsConcurrentDescribes는 상세 조회가 무제한으로 퍼지지 않는지
// 확인한다.
func TestTableCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	names := make([]string, 0, 24)
	describe := make(map[string]ddbtypes.TableDescription, 24)
	for i := range 24 {
		name := "table-" + string(rune('a'+i%26))
		names = append(names, name)
		describe[name] = ddbtypes.TableDescription{TableName: aws.String(name)}
	}

	api := &fakeDDB{listPages: [][]string{names}, describe: describe}

	if _, err := ddbcollector.NewTable(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("DescribeTable 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
}

// TestTableCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestTableCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeDDB{listErr: context.Canceled}
	if _, err := ddbcollector.NewTable(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
