package collect

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// Result는 한 번의 수집 실행 결과다.
//
// 리소스와 에러를 함께 담는다. 이것이 멀티 계정·멀티 리전 조회에서 가장 중요한 설계다.
// 한 리전의 권한 부족이 전체 실행을 죽이면 도구가 쓸모없어지므로, 성공한 부분은 살리고
// 실패는 실패대로 기록한다. "Errors are values" — 에러는 흐름을 끊는 예외가 아니라
// 결과의 일부다.
type Result struct {
	Resources []model.Resource
	Errors    []model.CollectError
}

// Job은 수집기 하나를 한 범위에 대해 실행하는 단위다.
//
// 러너는 이 Job들을 병렬로 돌린다. (수집기 × 범위)가 곱셈으로 늘어나므로 각 조합을
// 하나의 Job으로 만들어 상한 있는 동시성으로 처리한다.
type Job struct {
	Collector Collector
	Request   Request
}

// Runner는 여러 Job을 상한 있는 동시성으로 실행한다.
//
// 동시 실행 수에 상한을 두는 이유: 리전 × 리소스 타입은 곱셈으로 늘어난다. 무제한
// 고루틴을 띄우면 AWS API 스로틀링(RequestLimitExceeded)에 걸려 오히려 느려지고
// 실패한다. Limit이 그 상한이다.
type Runner struct {
	// Limit은 동시에 실행할 Job의 최대 개수다. 0 이하면 DefaultLimit을 쓴다.
	Limit int
}

// DefaultLimit은 동시 실행 Job 수의 기본 상한이다.
//
// AWS 계정 수준 API 스로틀링에 여유를 두면서도 멀티 리전 조회를 병렬화하기에 적당한
// 값이다. 필요하면 호출부가 Runner.Limit으로 조정한다.
const DefaultLimit = 8

// Run은 모든 Job을 실행하고 결과를 하나로 모은다.
//
// 동작 원칙:
//   - 상한 있는 동시성. 세마포어(버퍼 채널)로 동시 실행 수를 Limit으로 제한한다.
//     채널 파이프라인 대신 세마포어 + 뮤텍스를 쓴 것은 이쪽이 단순하기 때문이다.
//     "Clear is better than clever."
//   - 부분 실패 허용. 한 Job이 에러를 내도 다른 Job은 계속 진행한다. 에러는 범위 정보를
//     붙여 Result.Errors에 모은다.
//   - 함수가 만든 고루틴은 함수가 리턴하기 전에 모두 정리된다. 백그라운드로 새는
//     고루틴을 만들지 않는다(WaitGroup으로 보장).
//   - ctx 취소를 존중한다. ctx가 끝나면 아직 시작하지 않은 Job은 건너뛴다. TUI에서
//     esc로 수집을 끊을 수 있어야 하기 때문이다.
//
// 결과는 정렬해 반환한다. 병렬 실행은 완료 순서가 매번 다르므로, 정렬하지 않으면 같은
// 입력에도 리소스 순서가 흔들려 스냅샷 diff가 무의미해진다.
func (r Runner) Run(ctx context.Context, jobs []Job) Result {
	limit := r.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	var (
		mu        sync.Mutex // resources와 errs를 보호한다
		resources []model.Resource
		errs      []model.CollectError
		wg        sync.WaitGroup
	)

	// 버퍼 채널을 세마포어로 쓴다. 채널에 자리가 없으면 goroutine 시작이 막혀
	// 동시 실행 수가 limit을 넘지 않는다.
	sem := make(chan struct{}, limit)

	for _, job := range jobs {
		// ctx가 이미 끝났으면 남은 Job을 시작하지 않는다.
		if ctx.Err() != nil {
			mu.Lock()
			errs = append(errs, canceledError(job, ctx.Err()))
			mu.Unlock()

			continue
		}

		wg.Add(1)

		go func(job Job) {
			defer wg.Done()

			sem <- struct{}{}        // 자리 확보 (없으면 대기)
			defer func() { <-sem }() // 자리 반납

			res, err := job.Collector.Collect(ctx, job.Request)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errs = append(errs, collectError(job, err))

				return
			}

			resources = append(resources, res...)
		}(job)
	}

	wg.Wait()

	model.SortResources(resources)
	sortErrors(errs)

	return Result{Resources: resources, Errors: errs}
}

// collectError는 수집 실패를 범위 정보와 함께 CollectError로 감싼다.
//
// 여기서는 에러 코드나 사용자 설명을 채우지 않는다. 그 해석은 AWS를 아는 계층
// (awsclient.Explain)의 몫이고, collect는 도메인 타입만 안다. 원본 메시지만 담는다.
func collectError(job Job, err error) model.CollectError {
	return model.CollectError{
		Type:    job.Collector.Type(),
		Profile: job.Request.Scope.Profile,
		Region:  job.Request.Scope.Region,
		Message: err.Error(),
	}
}

func canceledError(job Job, err error) model.CollectError {
	return model.CollectError{
		Type:    job.Collector.Type(),
		Profile: job.Request.Scope.Profile,
		Region:  job.Request.Scope.Region,
		Message: fmt.Sprintf("취소되어 조회하지 않음: %v", err),
	}
}

// sortErrors는 에러를 결정적 순서로 정렬한다. 리소스와 같은 이유로, 병렬 실행의
// 완료 순서에 결과가 좌우되지 않게 한다.
func sortErrors(errs []model.CollectError) {
	sort.SliceStable(errs, func(i, j int) bool {
		a, b := errs[i], errs[j]
		if a.Profile != b.Profile {
			return a.Profile < b.Profile
		}

		if a.Region != b.Region {
			return a.Region < b.Region
		}

		return a.Type < b.Type
	})
}

// Plan은 레지스트리와 범위 목록으로부터 실행할 Job들을 만든다.
//
// 범위 하나마다 등록된 모든 수집기를 돌리므로, Job 수는 (범위 수 × 수집기 수)가 된다.
func Plan(reg *Registry, scopes []Scope) []Job {
	collectors := reg.Collectors()
	jobs := make([]Job, 0, len(scopes)*len(collectors))

	for _, scope := range scopes {
		for _, c := range collectors {
			jobs = append(jobs, Job{
				Collector: c,
				Request:   Request{Scope: scope},
			})
		}
	}

	return jobs
}
