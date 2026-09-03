package sqs_test

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
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	sqscollector "github.com/cnlgks1/cloudloupe/internal/collector/sqs"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeSQS는 큐 수집기가 쓰는 ListQueues·GetQueueAttributes를 대신한다.
//
// listPages는 ListQueues의 페이지들(큐 URL), listErr는 마지막 페이지 뒤에 낼 오류다. attrs는
// URL로 속성 맵을, attrErr는 특정 URL만 실패시킨다.
type fakeSQS struct {
	listPages [][]string
	listErr   error
	attrs     map[string]map[string]string
	attrErr   map[string]error

	mu          sync.Mutex
	listCalls   int
	running     int32
	peakRunning int32
}

func (f *fakeSQS) ListQueues(
	_ context.Context,
	_ *awssqs.ListQueuesInput,
	_ ...func(*awssqs.Options),
) (*awssqs.ListQueuesOutput, error) {
	f.mu.Lock()
	i := f.listCalls
	f.listCalls++
	f.mu.Unlock()

	if i >= len(f.listPages) {
		if f.listErr != nil {
			return nil, f.listErr
		}

		return &awssqs.ListQueuesOutput{}, nil
	}

	out := &awssqs.ListQueuesOutput{QueueUrls: f.listPages[i]}
	if i+1 < len(f.listPages) || f.listErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func (f *fakeSQS) GetQueueAttributes(
	_ context.Context,
	in *awssqs.GetQueueAttributesInput,
	_ ...func(*awssqs.Options),
) (*awssqs.GetQueueAttributesOutput, error) {
	running := atomic.AddInt32(&f.running, 1)
	for {
		peak := atomic.LoadInt32(&f.peakRunning)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peakRunning, peak, running) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(&f.running, -1)

	url := aws.ToString(in.QueueUrl)
	if err, ok := f.attrErr[url]; ok {
		return nil, err
	}

	return &awssqs.GetQueueAttributesOutput{Attributes: f.attrs[url]}, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestQueueCollectorType(t *testing.T) {
	t.Parallel()

	if got := sqscollector.NewQueue(&fakeSQS{}).Type(); got != model.TypeSQSQueue {
		t.Errorf("Type() = %q, want %q", got, model.TypeSQSQueue)
	}
}

// TestQueueCollectConvertsAttributesAndRelations는 속성 값을 그대로 담고 이름을 URL에서
// 뽑으며 KMS 키와 데드레터 큐 관계를 만드는지 확인한다.
func TestQueueCollectConvertsAttributesAndRelations(t *testing.T) {
	t.Parallel()

	url := "https://sqs.ap-northeast-2.amazonaws.com/123456789012/orders"
	arn := "arn:aws:sqs:ap-northeast-2:123456789012:orders"
	dlqARN := "arn:aws:sqs:ap-northeast-2:123456789012:orders-dlq"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeSQS{
		listPages: [][]string{{url}},
		attrs: map[string]map[string]string{
			url: {
				"QueueArn":                    arn,
				"FifoQueue":                   "false",
				"ApproximateNumberOfMessages": "42",
				"VisibilityTimeout":           "30",
				"MessageRetentionPeriod":      "345600",
				"KmsMasterKeyId":              kmsKey,
				"RedrivePolicy":               `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":5}`,
			},
		},
	}

	got, err := sqscollector.NewQueue(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("큐 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "orders" || res.Name != "orders" {
		t.Errorf("ID/Name = %q/%q, want orders", res.ID, res.Name)
	}
	if res.ARN != arn {
		t.Errorf("ARN = %q, want %q", res.ARN, arn)
	}
	if got, want := res.FieldValue("ApproximateNumberOfMessages"), "42"; got != want {
		t.Errorf("ApproximateNumberOfMessages = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("VisibilityTimeout"), "30"; got != want {
		t.Errorf("VisibilityTimeout = %q, want %q", got, want)
	}

	type rel struct {
		relation string
		typ      string
		id       string
	}
	gotRels := make([]rel, 0, len(res.Related))
	for _, r := range res.Related {
		gotRels = append(gotRels, rel{r.Relation, r.Type, r.ID})
	}
	want := []rel{
		{"KmsMasterKeyId", model.TypeKMSKey, kmsKey},
		{"RedrivePolicy.deadLetterTargetArn", model.TypeSQSQueue, dlqARN},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 =\n  %+v\nwant\n  %+v", gotRels, want)
	}
}

// TestQueueCollectSkipsMalformedRedrivePolicy는 RedrivePolicy가 없거나 깨졌을 때 DLQ 관계를
// 만들지 않는지 확인한다.
func TestQueueCollectSkipsMalformedRedrivePolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"없음":      "",
		"깨진 JSON": "{not json",
		"필드 없음":   `{"maxReceiveCount":5}`,
	}

	for name, policy := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			url := "https://sqs.x.amazonaws.com/1/q"
			api := &fakeSQS{
				listPages: [][]string{{url}},
				attrs:     map[string]map[string]string{url: {"RedrivePolicy": policy}},
			}

			got, err := sqscollector.NewQueue(api).Collect(context.Background(), collect.Request{Scope: testScope()})
			if err != nil {
				t.Fatalf("Collect() 실패: %v", err)
			}
			for _, r := range got[0].Related {
				if r.Relation == "RedrivePolicy.deadLetterTargetArn" {
					t.Errorf("깨진 정책에서 DLQ 관계를 만들었다: %+v", r)
				}
			}
		})
	}
}

// TestQueueCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를 살리는지
// 확인한다.
func TestQueueCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	a := "https://sqs.x.amazonaws.com/1/a"
	b := "https://sqs.x.amazonaws.com/1/b"
	c := "https://sqs.x.amazonaws.com/1/c"
	api := &fakeSQS{
		listPages: [][]string{{a, b, c}},
		attrs:     map[string]map[string]string{a: {}, c: {}},
		attrErr:   map[string]error{b: denied},
	}

	got, err := sqscollector.NewQueue(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if !strings.Contains(err.Error(), "/b") {
		t.Errorf("오류에 실패한 URL이 없다: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a", "c"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}

// TestQueueCollectFollowsPages는 잘린 목록에서 다음 페이지를 이어 받는지 확인한다.
func TestQueueCollectFollowsPages(t *testing.T) {
	t.Parallel()

	a := "https://sqs.x.amazonaws.com/1/q1"
	b := "https://sqs.x.amazonaws.com/1/q2"
	api := &fakeSQS{
		listPages: [][]string{{a}, {b}},
		attrs:     map[string]map[string]string{a: {}, b: {}},
	}

	got, err := sqscollector.NewQueue(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("큐 %d개 수집, want 2", len(got))
	}
	if api.listCalls != 2 {
		t.Errorf("ListQueues 호출 = %d회, want 2", api.listCalls)
	}
}

// TestQueueCollectLimitsConcurrentDescribes는 상세 조회가 무제한으로 퍼지지 않는지 확인한다.
func TestQueueCollectLimitsConcurrentDescribes(t *testing.T) {
	t.Parallel()

	urls := make([]string, 0, 24)
	attrs := make(map[string]map[string]string, 24)
	for i := range 24 {
		url := "https://sqs.x.amazonaws.com/1/q-" + string(rune('a'+i%26))
		urls = append(urls, url)
		attrs[url] = map[string]string{}
	}

	api := &fakeSQS{listPages: [][]string{urls}, attrs: attrs}

	if _, err := sqscollector.NewQueue(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	if peak := atomic.LoadInt32(&api.peakRunning); peak > int32(collect.ItemLimit) {
		t.Errorf("GetQueueAttributes 동시 실행 최대 %d개, want <= %d", peak, collect.ItemLimit)
	}
}

// TestQueueCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
func TestQueueCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeSQS{listErr: context.Canceled}
	if _, err := sqscollector.NewQueue(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
