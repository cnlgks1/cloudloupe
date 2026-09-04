package eventbridge_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ebcollector "github.com/cnlgks1/cloudloupe/internal/collector/eventbridge"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeEventBus는 이벤트 버스 수집기가 쓰는 ListEventBuses를 대신한다.
//
// pages는 ListEventBuses의 페이지들, pageErr는 마지막 페이지 뒤에 낼 오류다. 수집기가
// NextToken을 직접 잇는지 확인하려고 페이지마다 토큰을 채운다.
type fakeEventBus struct {
	pages   [][]ebtypes.EventBus
	pageErr error

	calls int
}

func (f *fakeEventBus) ListEventBuses(
	_ context.Context,
	in *awseb.ListEventBusesInput,
	_ ...func(*awseb.Options),
) (*awseb.ListEventBusesOutput, error) {
	if f.calls == 0 && in.NextToken != nil {
		return nil, errors.New("첫 호출에 NextToken이 들어왔다")
	}

	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awseb.ListEventBusesOutput{}, nil
	}

	out := &awseb.ListEventBusesOutput{EventBuses: f.pages[i]}
	if i+1 < len(f.pages) || (f.pageErr != nil && i+1 == len(f.pages)) {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestEventBusCollectorType(t *testing.T) {
	t.Parallel()

	if got := ebcollector.NewEventBus(&fakeEventBus{}).Type(); got != model.TypeEventBridgeEventBus {
		t.Errorf("Type() = %q, want %q", got, model.TypeEventBridgeEventBus)
	}
}

// TestEventBusCollectConvertsFields는 SDK 값을 그대로 담고 관계를 만들지 않는지 확인한다.
func TestEventBusCollectConvertsFields(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:events:ap-northeast-2:123456789012:event-bus/default"
	api := &fakeEventBus{
		pages: [][]ebtypes.EventBus{{
			{Name: aws.String("default"), Arn: aws.String(arn), Description: aws.String("기본 버스")},
		}},
	}

	got, err := ebcollector.NewEventBus(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("버스 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "default" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if got, want := res.FieldValue("Description"), "기본 버스"; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	// 버스는 하위를 나열하지 않으므로 관계가 없어야 한다.
	if len(res.Related) != 0 {
		t.Errorf("Related = %+v, want 없음", res.Related)
	}
}

// TestEventBusCollectFollowsNextToken은 NextToken을 직접 이어 다음 페이지를 받는지 확인한다.
func TestEventBusCollectFollowsNextToken(t *testing.T) {
	t.Parallel()

	api := &fakeEventBus{
		pages: [][]ebtypes.EventBus{
			{{Name: aws.String("a")}},
			{{Name: aws.String("b")}},
		},
	}

	got, err := ebcollector.NewEventBus(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a", "b"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
	if api.calls != 2 {
		t.Errorf("ListEventBuses 호출 = %d회, want 2", api.calls)
	}
}

// TestEventBusCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestEventBusCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeEventBus{
		pages:   [][]ebtypes.EventBus{{{Name: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := ebcollector.NewEventBus(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
