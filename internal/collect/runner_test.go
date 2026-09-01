package collect_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeCollector는 테스트용 수집기다. 실제 AWS를 호출하지 않는다.
//
// 이게 좁은 인터페이스(원칙 3)의 값이다. Collector 인터페이스가 작기 때문에 자격증명
// 없이 이렇게 간단히 대역을 만들 수 있다.
type fakeCollector struct {
	typ     string
	res     []model.Resource
	err     error
	delay   time.Duration
	running *int32 // 동시 실행 수 추적용 (nil이면 추적 안 함)
	peak    *int32
}

func (f *fakeCollector) Type() string { return f.typ }

func (f *fakeCollector) Collect(ctx context.Context, _ collect.Request) ([]model.Resource, error) {
	if f.running != nil {
		cur := atomic.AddInt32(f.running, 1)
		defer atomic.AddInt32(f.running, -1)

		for {
			peak := atomic.LoadInt32(f.peak)
			if cur <= peak || atomic.CompareAndSwapInt32(f.peak, peak, cur) {
				break
			}
		}
	}

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if f.err != nil {
		return nil, f.err
	}

	return f.res, nil
}

func res(typ, id string) model.Resource {
	return model.Resource{Type: typ, ID: id, Region: "ap-northeast-2"}
}

func TestRunCollectsFromAllCollectors(t *testing.T) {
	t.Parallel()

	reg := collect.NewRegistry()
	reg.Add(&fakeCollector{typ: model.TypeEC2Instance, res: []model.Resource{res(model.TypeEC2Instance, "i-1")}})
	reg.Add(&fakeCollector{typ: model.TypeEC2Volume, res: []model.Resource{res(model.TypeEC2Volume, "vol-1")}})

	jobs := collect.Plan(reg, []collect.Scope{{Profile: "p", Region: "ap-northeast-2"}})

	result := collect.Runner{}.Run(context.Background(), jobs)

	if len(result.Resources) != 2 {
		t.Fatalf("리소스 %d개, want 2", len(result.Resources))
	}

	if len(result.Errors) != 0 {
		t.Errorf("에러 %d개, want 0: %v", len(result.Errors), result.Errors)
	}
}

func TestRunPartialFailureKeepsSuccesses(t *testing.T) {
	t.Parallel()

	// 이 프로젝트에서 가장 중요한 동작. 한 수집기가 실패해도 나머지 결과는 살아야 한다.
	// 한 리전의 권한 부족이 전체 조회를 버리게 하면 도구가 쓸모없어진다.
	reg := collect.NewRegistry()
	reg.Add(&fakeCollector{typ: model.TypeEC2Instance, res: []model.Resource{res(model.TypeEC2Instance, "i-1")}})
	reg.Add(&fakeCollector{typ: model.TypeWAFv2WebACL, err: errors.New("AccessDeniedException")})
	reg.Add(&fakeCollector{typ: model.TypeEC2Volume, res: []model.Resource{res(model.TypeEC2Volume, "vol-1")}})

	jobs := collect.Plan(reg, []collect.Scope{{Profile: "prod", Region: "eu-west-1"}})
	result := collect.Runner{}.Run(context.Background(), jobs)

	if len(result.Resources) != 2 {
		t.Errorf("실패에도 성공한 리소스 2개가 남아야 한다, got %d", len(result.Resources))
	}

	if len(result.Errors) != 1 {
		t.Fatalf("에러 1개, want 1: %v", result.Errors)
	}

	e := result.Errors[0]
	if e.Type != model.TypeWAFv2WebACL || e.Profile != "prod" || e.Region != "eu-west-1" {
		t.Errorf("에러에 범위 정보가 붙어야 한다: %+v", e)
	}

	if e.Message == "" {
		t.Error("에러 메시지가 비어 있다")
	}
}

func TestRunRespectsConcurrencyLimit(t *testing.T) {
	t.Parallel()

	// 동시 실행 수가 상한을 넘지 않아야 한다. 넘으면 AWS API 스로틀링을 부른다.
	var running, peak int32

	reg := collect.NewRegistry()
	for i := range 20 {
		reg.Add(&fakeCollector{
			typ:     fmt.Sprintf("test:type%d", i),
			delay:   20 * time.Millisecond,
			running: &running,
			peak:    &peak,
		})
	}

	jobs := collect.Plan(reg, []collect.Scope{{Profile: "p", Region: "r"}})

	collect.Runner{Limit: 3}.Run(context.Background(), jobs)

	if got := atomic.LoadInt32(&peak); got > 3 {
		t.Errorf("동시 실행 최대 %d, 상한 3을 넘었다", got)
	}
}

func TestRunCleansUpAllGoroutines(t *testing.T) {
	t.Parallel()

	// 함수가 만든 고루틴은 함수가 리턴하기 전에 모두 정리되어야 한다. Run이 리턴한 뒤에도
	// Collect가 돌고 있으면 백그라운드로 새는 고루틴이다.
	var active int32

	reg := collect.NewRegistry()

	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		reg.Add(&fakeCollector{
			typ:     fmt.Sprintf("test:type%d", i),
			running: &active,
			peak:    new(int32),
		})
	}

	jobs := collect.Plan(reg, []collect.Scope{{Profile: "p", Region: "r"}})
	collect.Runner{}.Run(context.Background(), jobs)

	// Run이 리턴한 시점에 실행 중인 Collect가 없어야 한다.
	if got := atomic.LoadInt32(&active); got != 0 {
		t.Errorf("Run 리턴 후에도 실행 중인 수집기가 %d개 있다", got)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	t.Parallel()

	reg := collect.NewRegistry()
	for i := range 10 {
		reg.Add(&fakeCollector{typ: fmt.Sprintf("test:type%d", i), delay: time.Second})
	}

	jobs := collect.Plan(reg, []collect.Scope{{Profile: "p", Region: "r"}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 시작 전에 이미 취소된 상태

	start := time.Now()
	result := collect.Runner{Limit: 2}.Run(ctx, jobs)

	// 취소되었으므로 1초 delay를 다 기다리지 않고 빠르게 끝나야 한다.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("취소됐는데 %v나 걸렸다", elapsed)
	}

	// 사용자 취소는 AWS 실패가 아니므로 오류 목록에 넣지 않고 별도 상태로 보고한다.
	if !result.Canceled {
		t.Error("취소된 실행의 Canceled가 false다")
	}
	if len(result.Errors) != 0 {
		t.Errorf("사용자 취소가 수집 오류로 기록됐다: %+v", result.Errors)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	t.Parallel()

	// 병렬 실행은 완료 순서가 매번 다르다. 정렬하지 않으면 같은 입력에도 결과 순서가
	// 흔들려 스냅샷 diff가 무의미해진다.
	build := func() *collect.Registry {
		reg := collect.NewRegistry()
		reg.Add(&fakeCollector{typ: model.TypeEC2Volume, res: []model.Resource{res(model.TypeEC2Volume, "vol-b"), res(model.TypeEC2Volume, "vol-a")}, delay: time.Millisecond})
		reg.Add(&fakeCollector{typ: model.TypeELBv2LoadBalancer, res: []model.Resource{res(model.TypeELBv2LoadBalancer, "alb")}})
		reg.Add(&fakeCollector{typ: model.TypeEC2Instance, res: []model.Resource{res(model.TypeEC2Instance, "i-1")}, delay: 2 * time.Millisecond})

		return reg
	}

	var first []string

	for run := range 5 {
		reg := build()
		jobs := collect.Plan(reg, []collect.Scope{{Profile: "p", Region: "ap-northeast-2"}})
		result := collect.Runner{}.Run(context.Background(), jobs)

		ids := make([]string, 0, len(result.Resources))
		for _, r := range result.Resources {
			ids = append(ids, r.ID)
		}

		if run == 0 {
			first = ids

			continue
		}

		for i := range ids {
			if ids[i] != first[i] {
				t.Fatalf("실행 %d의 순서가 다르다: %v vs %v", run, ids, first)
			}
		}
	}

	// 타입 순위대로 정렬되었는지: 로드밸런서 → 인스턴스 → 볼륨.
	want := []string{"alb", "i-1", "vol-a", "vol-b"}
	for i, w := range want {
		if first[i] != w {
			t.Errorf("정렬 순서 %d = %q, want %q (전체: %v)", i, first[i], w, first)
		}
	}
}

func TestPlanCreatesJobPerScopePerCollector(t *testing.T) {
	t.Parallel()

	reg := collect.NewRegistry()
	reg.Add(&fakeCollector{typ: "a"})
	reg.Add(&fakeCollector{typ: "b"})

	scopes := []collect.Scope{
		{Profile: "p", Region: "ap-northeast-2"},
		{Profile: "p", Region: "us-east-1"},
	}

	jobs := collect.Plan(reg, scopes)

	// 범위 2개 × 수집기 2개 = 4개.
	if len(jobs) != 4 {
		t.Errorf("Job %d개, want 4", len(jobs))
	}
}

func TestRunSeparatesJoinedCancellationAndClassifiesFailure(t *testing.T) {
	t.Parallel()

	failure := errors.New("request limit exceeded")
	collector := &fakeCollector{
		typ: model.TypeEC2Instance,
		err: errors.Join(context.Canceled, failure),
	}
	job := collect.Job{
		Collector: collector,
		Request: collect.Request{Scope: collect.Scope{
			Profile: "prod",
			Region:  "ap-northeast-2",
		}},
	}

	result := collect.Runner{Classify: func(err error) collect.ErrorDetails {
		if !errors.Is(err, failure) {
			t.Errorf("Classify err = %v, want 실제 AWS 오류", err)
		}

		return collect.ErrorDetails{
			Code:        "ThrottlingException",
			Explanation: "AWS API 요청 한도를 초과했습니다.",
		}
	}}.Run(context.Background(), []collect.Job{job})

	if !result.Canceled {
		t.Error("errors.Join에 포함된 사용자 취소를 상태로 분리하지 않음")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %+v, want 실제 오류 1건", result.Errors)
	}

	got := result.Errors[0]
	if got.Code != "ThrottlingException" || got.Explanation != "AWS API 요청 한도를 초과했습니다." {
		t.Errorf("분류 결과 = %+v", got)
	}
	if got.Message != failure.Error() {
		t.Errorf("Message = %q, want %q", got.Message, failure.Error())
	}
}
