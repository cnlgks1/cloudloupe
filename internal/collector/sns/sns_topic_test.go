package sns_test

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
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	snscollector "github.com/cnlgks1/cloudloupe/internal/collector/sns"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeSNS는 토픽 수집기가 쓰는 ListTopics·GetTopicAttributes를 대신한다.
//
// listPages는 ListTopics의 페이지들, listErr는 마지막 페이지 뒤에 낼 오류다. attrs는 ARN으로
// 속성 맵을, attrErr는 특정 ARN만 실패시킨다.
type fakeSNS struct {
	listPages [][]string
	listErr   error
	attrs     map[string]map[string]string
	attrErr   map[string]error

	mu          sync.Mutex
	listCalls   int
	running     int32
	peakRunning int32
}

func (f *fakeSNS) ListTopics(
	_ context.Context,
	_ *awssns.ListTopicsInput,
	_ ...func(*awssns.Options),
) (*awssns.ListTopicsOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if i >= len(f.listPages) {
		if f.listErr != nil {
			return nil, f.listErr
		}

		return &awssns.ListTopicsOutput{}, nil
	}

	topics := make([]snstypes.Topic, 0, len(f.listPages[i]))
	for _, arn := range f.listPages[i] {
		topics = append(topics, snstypes.Topic{TopicArn: aws.String(arn)})
	}

	out := &awssns.ListTopicsOutput{Topics: topics}
	if i+1 < len(f.listPages) || f.listErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func (f *fakeSNS) GetTopicAttributes(
	_ context.Context,
	in *awssns.GetTopicAttributesInput,
	_ ...func(*awssns.Options),
) (*awssns.GetTopicAttributesOutput, error) {
	running := atomic.AddInt32(&f.running, 1)
	for {
		peak := atomic.LoadInt32(&f.peakRunning)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peakRunning, peak, running) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&f.running, -1)

	arn := aws.ToString(in.TopicArn)
	if err, ok := f.attrErr[arn]; ok {
		return nil, err
	}

	return &awssns.GetTopicAttributesOutput{Attributes: f.attrs[arn]}, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestTopicCollectorType(t *testing.T) {
	t.Parallel()

	if got := snscollector.NewTopic(&fakeSNS{}).Type(); got != model.TypeSNSTopic {
		t.Errorf("Type() = %q, want %q", got, model.TypeSNSTopic)
	}
}

// TestTopicCollectConvertsAttributesAndKMS는 속성 맵 값을 그대로 담고 이름을 ARN에서 뽑으며
// KMS 암호화면 키 관계를 만드는지 확인한다.
func TestTopicCollectConvertsAttributesAndKMS(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:sns:ap-northeast-2:123456789012:orders-events"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeSNS{
		listPages: [][]string{{arn}},
		attrs: map[string]map[string]string{
			arn: {
				"DisplayName":            "Orders",
				"Owner":                  "123456789012",
				"SubscriptionsConfirmed": "3",
				"SubscriptionsPending":   "0",
				"FifoTopic":              "false",
				"KmsMasterKeyId":         kmsKey,
			},
		},
	}

	got, err := snscollector.NewTopic(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("토픽 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != arn || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q, want %q", res.ID, res.ARN, arn)
	}
	if res.Name != "orders-events" {
		t.Errorf("Name = %q, want orders-events", res.Name)
	}
	if got, want := res.FieldValue("DisplayName"), "Orders"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("SubscriptionsConfirmed"), "3"; got != want {
		t.Errorf("SubscriptionsConfirmed = %q, want %q", got, want)
	}

	if len(res.Related) != 1 {
		t.Fatalf("관계 %d개, want 1", len(res.Related))
	}
	ref := res.Related[0]
	if ref.Type != model.TypeKMSKey || ref.Relation != "KmsMasterKeyId" || ref.ID != kmsKey {
		t.Errorf("관계 = %+v", ref)
	}
}

// TestTopicCollectWithoutKMSHasNoRelation은 암호화 키가 없으면 관계를 만들지 않는지 확인한다.
func TestTopicCollectWithoutKMSHasNoRelation(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:sns:ap-northeast-2:123456789012:plain"
	api := &fakeSNS{
		listPages: [][]string{{arn}},
		attrs:     map[string]map[string]string{arn: {"DisplayName": "Plain"}},
	}

	got, err := snscollector.NewTopic(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got[0].Related) != 0 {
		t.Errorf("암호화 없으면 관계 없음, got %+v", got[0].Related)
	}
	if got[0].FieldValue("KmsMasterKeyId") != "-" {
		t.Errorf("KmsMasterKeyId = %q, want -", got[0].FieldValue("KmsMasterKeyId"))
	}
}

// TestTopicCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를 살리는지
// 확인한다.
func TestTopicCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	a, b, c := "arn:aws:sns:x:1:a", "arn:aws:sns:x:1:b", "arn:aws:sns:x:1:c"
	api := &fakeSNS{
		listPages: [][]string{{a, b, c}},
		attrs: map[string]map[string]string{
			a: {}, c: {},
		},
		attrErr: map[string]error{b: denied},
	}

	got, err := snscollector.NewTopic(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), ":b") {
		t.Errorf("오류에 실패한 ARN이 없다: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.Name)
	}
	if want := []string{"a", "c"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}

// TestTopicCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestTopicCollectFollowsPages(t *testing.T) {
	t.Parallel()

	a, b := "arn:aws:sns:x:1:t1", "arn:aws:sns:x:1:t2"
	api := &fakeSNS{
		listPages: [][]string{{a}, {b}},
		attrs:     map[string]map[string]string{a: {}, b: {}},
	}

	got, err := snscollector.NewTopic(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("토픽 %d개 수집, want 2", len(got))
	}
	if api.listCalls != 2 {
		t.Errorf("ListTopics 호출 = %d회, want 2", api.listCalls)
	}
}

// TestTopicCollectLimitsConcurrentDescribes는 상세 조회가 무제한으로 퍼지지 않는지 확인한다.
func TestTopicCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	arns := make([]string, 0, 24)
	attrs := make(map[string]map[string]string, 24)
	for i := range 24 {
		arn := "arn:aws:sns:x:1:topic-" + string(rune('a'+i%26))
		arns = append(arns, arn)
		attrs[arn] = map[string]string{}
	}

	api := &fakeSNS{listPages: [][]string{arns}, attrs: attrs}

	if _, err := snscollector.NewTopic(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("GetTopicAttributes 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
}

// TestTopicCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestTopicCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeSNS{listErr: context.Canceled}
	if _, err := snscollector.NewTopic(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
