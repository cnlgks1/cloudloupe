package collect

import (
	"context"
	"errors"
	"sync"
)

// 이 파일은 수집기 하나가 내부에서 여러 번 조회해야 할 때 쓰는 팬아웃 헬퍼다.
//
// AWS의 여러 서비스는 "목록을 받고 항목마다 상세를 다시 묻는" 형태다. KMS는 ListKeys 뒤에
// 키마다 DescribeKey를 불러야 한다. 항목이 수백 개면 순차 조회는 느리고, 무제한 고루틴은 API
// 스로틀링을 부른다.
//
// Runner가 Job 사이의 동시성을 제한하는 것과 같은 문제를 수집기 안에서도 풀어야 하므로,
// 구현 방식도 Runner와 같게 맞췄다. 세마포어(버퍼 채널)로 상한을 두고 뮤텍스 없이 인덱스로
// 결과를 채운다. 채널 파이프라인보다 단순하다.

// ItemLimit은 수집기 하나가 내부 조회에 사용할 동시 실행 수의 기본 상한이다.
//
// Runner의 DefaultLimit과 곱해지는 값이라는 점이 중요하다. Job 8개가 각각 항목 조회를
// 8개씩 돌리면 동시 호출은 64개가 된다. 그래서 이 값은 Job 상한보다 작게 잡는다.
const ItemLimit = 4

// FanOut은 항목마다 조회 함수를 상한 있는 동시성으로 실행한다.
//
// 동작 원칙은 Runner.Run과 같다.
//   - 상한 있는 동시성. limit이 0 이하면 ItemLimit을 쓴다.
//   - 부분 실패 허용. 항목 하나가 실패해도 나머지는 계속 진행하고, 성공한 결과와 함께
//     errors.Join으로 묶은 오류를 반환한다. 호출자는 둘을 그대로 Collect의 반환값으로
//     넘기면 된다.
//   - 결과 순서는 입력 순서와 같다. 완료 순서에 좌우되면 같은 입력에서 화면과 리포트가
//     매번 달라진다.
//   - 이 함수가 만든 고루틴은 리턴 전에 모두 정리된다.
//   - ctx가 끝나면 아직 시작하지 않은 항목은 건너뛴다. ctx.Err()도 오류에 포함되므로
//     상위에서 취소인지 데드라인인지 구분할 수 있다.
func FanOut[T, R any](
	ctx context.Context,
	limit int,
	items []T,
	fetch func(context.Context, T) (R, error),
) ([]R, error) {
	if len(items) == 0 {
		return nil, nil
	}

	if limit <= 0 {
		limit = ItemLimit
	}

	type outcome struct {
		value R
		ok    bool
		err   error
	}

	outcomes := make([]outcome, len(items))
	sem := make(chan struct{}, limit)

	var wg sync.WaitGroup

	for i := range items {
		if err := ctx.Err(); err != nil {
			outcomes[i].err = err

			continue
		}

		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			value, err := fetch(ctx, items[i])
			// 각 고루틴은 자기 인덱스만 쓰므로 잠금이 필요 없다.
			outcomes[i] = outcome{value: value, ok: err == nil, err: err}
		}(i)
	}

	wg.Wait()

	results := make([]R, 0, len(items))
	var errs []error

	for _, o := range outcomes {
		if o.ok {
			results = append(results, o.value)
		}
		if o.err != nil {
			errs = append(errs, o.err)
		}
	}

	return results, errors.Join(errs...)
}
