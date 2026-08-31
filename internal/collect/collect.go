// Package collect는 AWS 리소스를 조회하는 수집기와 그 실행 엔진을 정의한다.
//
// 이 패키지가 cloudloupe의 조회 전용 경계다. 아래 [Collector] 인터페이스에는 Collect
// 메서드 하나뿐이며, 리소스를 만들거나 바꾸거나 지우는 메서드가 존재하지 않는다.
// 쓰기 동작이 들어앉을 자리가 타입 레벨에 없다는 뜻이다. 이것이 "조회 전용"을 약속이
// 아니라 구조로 만든다.
//
// 이 규율의 뿌리는 Rob Pike의 Go Proverbs 중 "The bigger the interface, the weaker
// the abstraction"이다. 인터페이스를 최소로 유지할수록 그 경계가 강해진다.
package collect

import (
	"context"
	"errors"
	"fmt"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// Scope는 한 번의 수집이 대상으로 삼는 범위다.
//
// 수집기는 이 범위 안에서만 조회한다. Profile과 Region은 어떤 자격증명으로 어느 리전을
// 볼지 정하고, AccountID는 수집된 리소스에 계정 정보를 붙이기 위해 미리 확인된 값이다.
type Scope struct {
	Profile   string
	Region    string
	AccountID string
}

// Request는 Collect 한 번에 전달되는 입력이다.
//
// 구조체로 감싼 이유는 앞으로 필드가 늘어도(태그 필터, 페이지 크기 등) 호출부 시그니처가
// 바뀌지 않게 하기 위함이다.
type Request struct {
	Scope Scope
}

// Collector는 한 종류의 AWS 리소스를 조회하는 수집기다.
//
// 메서드가 둘뿐이며 그중 하나는 자기 종류를 알려주는 Type()이다. 실제 동작은 Collect
// 하나뿐이고, 이름 그대로 "수집"만 한다. 새 리소스 타입은 서비스 구현 패키지에서
// 이 인터페이스를 구현하고 상위 catalog 조립 계층에 명시적으로 등록한다.
//
// 구현체는 자신이 필요한 SDK 메서드만 담은 좁은 인터페이스를 받아야 한다("accept
// interfaces, return structs"). 그래야 자격증명 없이 fake로 테스트할 수 있다.
type Collector interface {
	// Type은 이 수집기가 만들어내는 리소스의 타입 ID를 반환한다(예: "ec2:instance").
	Type() string

	// Collect는 범위 안의 리소스를 조회해 반환한다.
	//
	// ctx는 취소와 타임아웃을 위한 것이다. TUI에서 esc로 진행 중인 수집을 끊을 수 있어야
	// 하므로, 구현체는 ctx.Done()을 존중해야 한다.
	Collect(ctx context.Context, req Request) ([]model.Resource, error)
}

// Registry는 수집기들을 타입 ID로 모아 둔 목록이다.
//
// 이 패키지는 등록할 AWS 서비스나 기본 수집기 목록을 알지 않는다. 어떤 수집기를 쓸지는
// 상위 조립 계층이 명시적으로 결정한다. 덕분에 collect는 AWS SDK를 의존하지 않는 순수한
// 실행 코어로 유지된다.
type Registry struct {
	collectors []Collector
	types      map[string]struct{}
}

// NewRegistry는 빈 레지스트리를 만든다.
func NewRegistry() *Registry {
	return &Registry{types: make(map[string]struct{})}
}

// Add는 수집기를 등록한다. 같은 순서로 조회되도록 등록 순서를 유지한다.
//
// 빈 타입과 중복 타입을 오류로 거부한다. 등록 실수로 같은 AWS API를 두 번 호출하거나
// 결과가 중복되는 문제를 시작 시점에 드러내기 위한 검증이다.
func (r *Registry) Add(c Collector) error {
	if c == nil {
		return errors.New("nil 수집기는 등록할 수 없음")
	}

	typ := c.Type()
	if typ == "" {
		return errors.New("타입이 빈 수집기는 등록할 수 없음")
	}

	if _, exists := r.types[typ]; exists {
		return fmt.Errorf("수집기 타입 중복: %s", typ)
	}

	r.collectors = append(r.collectors, c)
	r.types[typ] = struct{}{}

	return nil
}

// Collectors는 등록된 수집기 목록의 복사본을 반환한다.
func (r *Registry) Collectors() []Collector {
	return append([]Collector(nil), r.collectors...)
}

// Types는 등록된 수집기들의 타입 ID 목록을 등록 순서대로 반환한다.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.collectors))
	for _, c := range r.collectors {
		out = append(out, c.Type())
	}

	return out
}

// Select는 주어진 타입 ID에 해당하는 수집기만 골라 새 레지스트리로 반환한다.
//
// 사용자가 --types로 일부만 조회하도록 지정한 경우에 쓴다. 알 수 없는 타입은 조용히
// 무시하지 않고 unknown으로 함께 반환해, 호출부가 오타를 사용자에게 알릴 수 있게 한다.
func (r *Registry) Select(types []string) (selected *Registry, unknown []string) {
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}

	found := make(map[string]bool, len(types))
	selected = NewRegistry()

	for _, c := range r.collectors {
		if want[c.Type()] {
			// 원본 Registry가 유효하므로 여기서는 실패할 수 없다.
			_ = selected.Add(c)
			found[c.Type()] = true
		}
	}

	for _, t := range types {
		if !found[t] {
			unknown = append(unknown, t)
		}
	}

	return selected, unknown
}
