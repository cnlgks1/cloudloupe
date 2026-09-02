package collect_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/collect"
)

// TestFanOutKeepsInputOrder는 완료 순서가 아니라 입력 순서로 결과가 나오는지 확인한다.
// 순서가 흔들리면 같은 조회에서 화면과 리포트가 매번 달라진다.
func TestFanOutKeepsInputOrder(t *testing.T) {
	t.Parallel()

	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	got, err := collect.FanOut(context.Background(), 4, items,
		func(_ context.Context, item int) (string, error) {
			return fmt.Sprintf("item-%d", item), nil
		})
	if err != nil {
		t.Fatalf("FanOut() 실패: %v", err)
	}

	want := []string{"item-1", "item-2", "item-3", "item-4", "item-5", "item-6", "item-7", "item-8"}
	if !slices.Equal(got, want) {
		t.Errorf("결과 = %v, want %v", got, want)
	}
}

// TestFanOutKeepsPartialResults는 항목 하나가 실패해도 나머지를 버리지 않는지 확인한다.
func TestFanOutKeepsPartialResults(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("access denied")
	got, err := collect.FanOut(context.Background(), 2, []int{1, 2, 3},
		func(_ context.Context, item int) (int, error) {
			if item == 2 {
				return 0, wantErr
			}

			return item * 10, nil
		})

	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if want := []int{10, 30}; !slices.Equal(got, want) {
		t.Errorf("성공한 결과 = %v, want %v", got, want)
	}
}

// TestFanOutRespectsLimit은 동시 실행 수가 상한을 넘지 않는지 확인한다.
// 상한이 없으면 항목이 많은 계정에서 AWS API 스로틀링에 걸린다.
func TestFanOutRespectsLimit(t *testing.T) {
	t.Parallel()

	const limit = 3

	var (
		mu      sync.Mutex
		running int
		peak    int
		release = make(chan struct{})
		started sync.WaitGroup
	)

	items := make([]int, 12)
	started.Add(len(items))

	done := make(chan struct{})
	go func() {
		defer close(done)

		_, _ = collect.FanOut(context.Background(), limit, items,
			func(_ context.Context, _ int) (int, error) {
				mu.Lock()
				running++
				peak = max(peak, running)
				mu.Unlock()

				started.Done()
				<-release

				mu.Lock()
				running--
				mu.Unlock()

				return 0, nil
			})
	}()

	// 상한만큼 동시에 시작한 뒤 모두 풀어준다.
	close(release)
	started.Wait()
	<-done

	mu.Lock()
	defer mu.Unlock()

	if peak > limit {
		t.Errorf("동시 실행 최대 %d개, want <= %d", peak, limit)
	}
}

// TestFanOutSkipsWorkAfterCancel은 취소된 뒤 남은 항목을 시작하지 않는지 확인한다.
// TUI에서 esc로 수집을 끊을 수 있어야 한다.
func TestFanOutSkipsWorkAfterCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int32
	got, err := collect.FanOut(ctx, 2, []int{1, 2, 3},
		func(_ context.Context, item int) (int, error) {
			calls.Add(1)

			return item, nil
		})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(got) != 0 {
		t.Errorf("취소 후 결과 = %v, want empty", got)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("취소 후 조회 %d회 실행, want 0", n)
	}
}

func TestFanOutHandlesEmptyInput(t *testing.T) {
	t.Parallel()

	got, err := collect.FanOut(context.Background(), 0, nil,
		func(_ context.Context, item int) (int, error) {
			return item, nil
		})
	if err != nil || got != nil {
		t.Errorf("빈 입력 결과 = %v, err = %v, want nil, nil", got, err)
	}
}
