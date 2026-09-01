package collect

import (
	"context"
	"errors"
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
	Canceled  bool
}

// ErrorDetails는 공급자 오류에서 추출한 코드와 사용자 설명이다.
//
// collect는 AWS SDK를 모르므로 해석 규칙을 갖지 않는다. 상위 app 계층이 ErrorClassifier를
// 주입하고, 제로 값 Runner는 코드·설명 없이 원본 메시지만 보존한다.
type ErrorDetails struct {
	Code        string
	Explanation string
}

// ErrorClassifier는 원본 오류를 사용자 대면 정보로 분류한다.
type ErrorClassifier func(error) ErrorDetails

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
	// Classify는 원본 오류가 문자열로 바뀌기 전에 코드와 사용자 설명을 추출한다.
	Classify ErrorClassifier
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
		canceled  bool
		wg        sync.WaitGroup
	)

	// 버퍼 채널을 세마포어로 쓴다. 채널에 자리가 없으면 goroutine 시작이 막혀
	// 동시 실행 수가 limit을 넘지 않는다.
	sem := make(chan struct{}, limit)

	for _, job := range jobs {
		// 사용자가 취소한 작업은 AWS 실패로 만들지 않는다. 데드라인 초과는 실제 진단이
		// 필요하므로 범위 오류로 남긴다.
		if ctx.Err() != nil {
			mu.Lock()
			if errors.Is(ctx.Err(), context.Canceled) {
				canceled = true
			} else {
				errs = append(errs, collectError(job,
					fmt.Errorf("조회 시작 전 context 종료: %w", ctx.Err()), r.Classify))
			}
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

			// 수집기가 성공 데이터와 부분 오류를 함께 반환할 수 있다. 오류가 있다는 이유로
			// 이미 읽은 리소스를 버리지 않는다.
			resources = append(resources, res...)

			if err != nil {
				var wasCanceled bool
				errs, wasCanceled = appendCollectErrors(errs, job, err, r.Classify)
				canceled = canceled || wasCanceled
			}
		}(job)
	}

	wg.Wait()

	model.SortResources(resources)
	sortErrors(errs)

	return Result{Resources: resources, Errors: errs, Canceled: canceled}
}

// collectError는 수집 실패를 범위 정보와 함께 CollectError로 감싼다.
func collectError(job Job, err error, classify ErrorClassifier) model.CollectError {
	details := ErrorDetails{}
	if classify != nil {
		details = classify(err)
	}

	return model.CollectError{
		Type:        job.Collector.Type(),
		Profile:     job.Request.Scope.Profile,
		Region:      job.Request.Scope.Region,
		Code:        details.Code,
		Message:     err.Error(),
		Explanation: details.Explanation,
	}
}

// appendCollectErrors는 errors.Join으로 묶인 부분 오류를 각각의 CollectError로 펼친다.
// context.Canceled는 사용자 취소 상태로 분리하고 오류 목록에는 넣지 않는다.
func appendCollectErrors(
	dst []model.CollectError,
	job Job,
	err error,
	classify ErrorClassifier,
) ([]model.CollectError, bool) {
	type multiError interface {
		Unwrap() []error
	}

	if joined, ok := err.(multiError); ok {
		canceled := false
		for _, child := range joined.Unwrap() {
			var childCanceled bool
			dst, childCanceled = appendCollectErrors(dst, job, child, classify)
			canceled = canceled || childCanceled
		}

		return dst, canceled
	}

	if errors.Is(err, context.Canceled) {
		return dst, true
	}

	return append(dst, collectError(job, err, classify)), false
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
