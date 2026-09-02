package kms_test

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
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	kmscollector "github.com/cnlgks1/cloudloupe/internal/collector/kms"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeKMS는 KMS API 세 메서드를 대신한다. 테스트는 실제 AWS를 때리지 않는다.
//
// describe는 키 ID로 응답을 찾고, describeErr는 특정 키만 실패시킨다. 키마다 상세를 다시 묻는
// N+1 구조에서 "일부 키만 권한이 없는" 상황을 재현하려면 이 구분이 필요하다.
type fakeKMS struct {
	keyPages    [][]kmstypes.KeyListEntry
	keyPageErr  error
	aliases     []kmstypes.AliasListEntry
	aliasErr    error
	describe    map[string]*kmstypes.KeyMetadata
	describeErr map[string]error

	mu            sync.Mutex
	listCalls     int
	describeCalls atomic.Int32
	running       int32
	peakRunning   int32
}

func (f *fakeKMS) ListKeys(
	_ context.Context,
	in *awskms.ListKeysInput,
	_ ...func(*awskms.Options),
) (*awskms.ListKeysOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if f.keyPageErr != nil && i >= len(f.keyPages) {
		return nil, f.keyPageErr
	}
	if i >= len(f.keyPages) {
		return &awskms.ListKeysOutput{}, nil
	}

	out := &awskms.ListKeysOutput{Keys: f.keyPages[i]}
	// 다음 페이지가 남았으면 잘렸다고 알린다. 페이지네이터가 Marker를 이어 보내는지 본다.
	if i+1 < len(f.keyPages) || (f.keyPageErr != nil && i+1 == len(f.keyPages)) {
		out.Truncated = true
		out.NextMarker = aws.String("next")
	}

	_ = in

	return out, nil
}

func (f *fakeKMS) ListAliases(
	_ context.Context,
	_ *awskms.ListAliasesInput,
	_ ...func(*awskms.Options),
) (*awskms.ListAliasesOutput, error) {
	if f.aliasErr != nil {
		return nil, f.aliasErr
	}

	return &awskms.ListAliasesOutput{Aliases: f.aliases}, nil
}

func (f *fakeKMS) DescribeKey(
	_ context.Context,
	in *awskms.DescribeKeyInput,
	_ ...func(*awskms.Options),
) (*awskms.DescribeKeyOutput, error) {
	f.describeCalls.Add(1)

	running := atomic.AddInt32(&f.running, 1)
	for {
		peak := atomic.LoadInt32(&f.peakRunning)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peakRunning, peak, running) {
			break
		}
	}
	// 동시 실행이 겹칠 시간을 준다. 상한이 없으면 여기서 모든 키가 한꺼번에 들어온다.
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&f.running, -1)

	keyID := aws.ToString(in.KeyId)
	if err, ok := f.describeErr[keyID]; ok {
		return nil, err
	}

	return &awskms.DescribeKeyOutput{KeyMetadata: f.describe[keyID]}, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func keyEntry(id string) kmstypes.KeyListEntry {
	return kmstypes.KeyListEntry{KeyId: aws.String(id)}
}

func TestKeyCollectorType(t *testing.T) {
	t.Parallel()

	if got := kmscollector.NewKey(&fakeKMS{}).Type(); got != model.TypeKMSKey {
		t.Errorf("Type() = %q, want %q", got, model.TypeKMSKey)
	}
}

// TestKeyCollectJoinsAliasesAndMetadata는 목록·별칭·상세 세 조회를 하나의 리소스로 합치는지
// 확인한다. 별칭이 없으면 화면에 UUID만 남으므로 이름 연결이 핵심이다.
func TestKeyCollectJoinsAliasesAndMetadata(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	api := &fakeKMS{
		keyPages: [][]kmstypes.KeyListEntry{{keyEntry("key-1"), keyEntry("key-2")}},
		aliases: []kmstypes.AliasListEntry{
			{AliasName: aws.String("alias/app-data"), TargetKeyId: aws.String("key-1")},
			{AliasName: aws.String("alias/app-data-old"), TargetKeyId: aws.String("key-1")},
			{AliasName: aws.String("alias/aws/s3"), TargetKeyId: aws.String("key-2")},
		},
		describe: map[string]*kmstypes.KeyMetadata{
			"key-1": {
				KeyId:        aws.String("key-1"),
				Arn:          aws.String("arn:aws:kms:ap-northeast-2:123456789012:key/key-1"),
				KeyState:     kmstypes.KeyStateEnabled,
				KeyManager:   kmstypes.KeyManagerTypeCustomer,
				KeyUsage:     kmstypes.KeyUsageTypeEncryptDecrypt,
				KeySpec:      kmstypes.KeySpecSymmetricDefault,
				Origin:       kmstypes.OriginTypeAwsKms,
				Enabled:      true,
				MultiRegion:  aws.Bool(false),
				Description:  aws.String("애플리케이션 데이터"),
				CreationDate: &created,
			},
			"key-2": {
				KeyId:      aws.String("key-2"),
				KeyState:   kmstypes.KeyStateEnabled,
				KeyManager: kmstypes.KeyManagerTypeAws,
			},
		},
	}

	got, err := kmscollector.NewKey(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("키 %d개 수집, want 2", len(got))
	}

	first := got[0]
	if first.ID != "key-1" {
		t.Fatalf("첫 키 ID = %q, want key-1", first.ID)
	}
	// 표시 이름은 첫 별칭을 쓴다. 별칭이 여럿이면 필드에 모두 남는다.
	if first.Name != "alias/app-data" {
		t.Errorf("Name = %q, want alias/app-data", first.Name)
	}
	if got, want := first.FieldValue("별칭"), "alias/app-data, alias/app-data-old"; got != want {
		t.Errorf("별칭 = %q, want %q", got, want)
	}
	if got, want := first.FieldValue("관리 주체"), "고객 관리"; got != want {
		t.Errorf("관리 주체 = %q, want %q", got, want)
	}
	if first.CreatedAt == nil || !first.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", first.CreatedAt, created)
	}

	// AWS가 만든 키는 사용자가 만든 키와 구분되어야 한다.
	if got, want := got[1].FieldValue("관리 주체"), "AWS 관리"; got != want {
		t.Errorf("AWS 키 관리 주체 = %q, want %q", got, want)
	}
}

// TestKeyCollectKeepsOrderAndPartialFailures는 키 하나의 상세 조회가 실패해도 나머지를
// 살리고, 결과 순서가 목록 순서와 같은지 확인한다.
func TestKeyCollectKeepsOrderAndPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeKMS{
		keyPages: [][]kmstypes.KeyListEntry{{
			keyEntry("key-a"), keyEntry("key-b"), keyEntry("key-c"),
		}},
		describe: map[string]*kmstypes.KeyMetadata{
			"key-a": {KeyId: aws.String("key-a")},
			"key-c": {KeyId: aws.String("key-c")},
		},
		describeErr: map[string]error{"key-b": denied},
	}

	got, err := kmscollector.NewKey(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), "key-b") {
		t.Errorf("오류에 실패한 키 ID가 없다: %v", err)
	}

	ids := make([]string, 0, len(got))
	for _, res := range got {
		ids = append(ids, res.ID)
	}
	if want := []string{"key-a", "key-c"}; !slices.Equal(ids, want) {
		t.Errorf("수집 결과 = %v, want %v", ids, want)
	}
}

// TestKeyCollectSurvivesAliasFailure는 별칭 조회가 실패해도 키를 보여주는지 확인한다.
// 이름을 못 붙이는 것이 키를 아예 못 보여주는 것보다 낫다.
func TestKeyCollectSurvivesAliasFailure(t *testing.T) {
	t.Parallel()

	aliasErr := errors.New("alias denied")
	api := &fakeKMS{
		keyPages: [][]kmstypes.KeyListEntry{{keyEntry("key-1")}},
		aliasErr: aliasErr,
		describe: map[string]*kmstypes.KeyMetadata{"key-1": {KeyId: aws.String("key-1")}},
	}

	got, err := kmscollector.NewKey(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, aliasErr) {
		t.Errorf("err = %v, want %v", err, aliasErr)
	}
	if len(got) != 1 || got[0].FieldValue("별칭") != "-" {
		t.Errorf("별칭 없이도 키가 남아야 한다: %+v", got)
	}
}

// TestKeyCollectFollowsKeyPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestKeyCollectFollowsKeyPages(t *testing.T) {
	t.Parallel()

	api := &fakeKMS{
		keyPages: [][]kmstypes.KeyListEntry{{keyEntry("key-1")}, {keyEntry("key-2")}},
		describe: map[string]*kmstypes.KeyMetadata{
			"key-1": {KeyId: aws.String("key-1")},
			"key-2": {KeyId: aws.String("key-2")},
		},
	}

	got, err := kmscollector.NewKey(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("키 %d개 수집, want 2", len(got))
	}
	if api.listCalls != 2 {
		t.Errorf("ListKeys 호출 = %d회, want 2", api.listCalls)
	}
}

// TestKeyCollectLimitsConcurrentDescribes는 상세 조회가 무제한으로 퍼지지 않는지 확인한다.
// 키가 많은 계정에서 스로틀링을 피하려면 상한이 실제로 걸려 있어야 한다.
func TestKeyCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	entries := make([]kmstypes.KeyListEntry, 0, 24)
	describe := make(map[string]*kmstypes.KeyMetadata, 24)
	for i := range 24 {
		id := "key-" + string(rune('a'+i%26))
		entries = append(entries, keyEntry(id))
		describe[id] = &kmstypes.KeyMetadata{KeyId: aws.String(id)}
	}

	api := &fakeKMS{keyPages: [][]kmstypes.KeyListEntry{entries}, describe: describe}

	if _, err := kmscollector.NewKey(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("DescribeKey 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
	if got := api.describeCalls.Load(); got != int32(len(entries)) {
		t.Errorf("DescribeKey 호출 = %d회, want %d", got, len(entries))
	}
}

// TestKeyCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestKeyCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeKMS{keyPageErr: context.Canceled}
	if _, err := kmscollector.NewKey(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
