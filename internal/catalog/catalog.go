// Package catalog는 cloudloupe가 지원하는 AWS 리소스 타입을 명시적으로 조립한다.
//
// 타입 ID, 사용자 표시명, 조회 범위, 목록 열과 수집기 생성자를 한 Definition에 둔다.
// 신규 리소스를 추가할 때 서로 다른 파일의 라벨·글로벌 여부·등록 목록이 어긋나지 않게
// 하는 단일 출처다. init 부수효과나 변경 가능한 전역 레지스트리는 사용하지 않는다.
package catalog

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/cnlgks1/cloudloupe/internal/collect"
)

// ScopeKind는 리소스가 리전별인지 계정 전체의 글로벌 리소스인지 나타낸다.
type ScopeKind uint8

const (
	scopeUnknown ScopeKind = iota
	// Regional은 선택한 각 리전에서 조회하는 리소스다.
	Regional
	// Global은 선택 리전 수와 관계없이 실행당 한 번만 조회하는 리소스다.
	Global
)

// Definition은 지원 리소스 타입 하나의 변경되지 않는 메타데이터다.
//
// Columns는 Resource.Fields에 들어가는 키 순서다. TUI가 첫 번째 결과의 우연한 필드
// 모양으로 열을 결정하지 않고, 결과가 없어도 안정적인 스키마를 사용하게 한다.
type Definition struct {
	Type    string
	Label   string
	Scope   ScopeKind
	Columns []string

	newCollector func() collect.Collector
}

// Definitions는 지원 타입 정의를 표시·실행 순서대로 반환한다.
//
// 호출자가 내부 상태를 바꿀 수 없도록 매번 새 슬라이스와 Columns 복사본을 반환한다.
func Definitions() []Definition {
	definitions := allDefinitions(aws.Config{})
	out := make([]Definition, len(definitions))

	for i, definition := range definitions {
		out[i] = definition
		out[i].Columns = append([]string(nil), definition.Columns...)
	}

	return out
}

// Registry는 AWS 설정과 선택 타입으로 실행 가능한 수집기 레지스트리를 만든다.
//
// includeGlobal이 false면 글로벌 타입은 제외한다. selected가 비면 지원 타입을 모두
// 포함하며, 알 수 없는 타입은 unknown으로 반환해 호출자가 사용자에게 알려줄 수 있다.
func Registry(cfg aws.Config, includeGlobal bool, selected []string) (*collect.Registry, []string, error) {
	return buildRegistry(allDefinitions(cfg), includeGlobal, selected)
}

func buildRegistry(definitions []Definition, includeGlobal bool, selected []string) (*collect.Registry, []string, error) {
	if err := validateDefinitions(definitions); err != nil {
		return nil, nil, fmt.Errorf("카탈로그 검증: %w", err)
	}

	known := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		known[definition.Type] = struct{}{}
	}

	var unknown []string
	for _, typ := range selected {
		if _, ok := known[typ]; !ok {
			unknown = append(unknown, typ)
		}
	}

	wanted := make(map[string]struct{}, len(selected))
	for _, typ := range selected {
		wanted[typ] = struct{}{}
	}

	registry := collect.NewRegistry()

	for _, definition := range definitions {
		if definition.Scope == Global && !includeGlobal {
			continue
		}

		if len(wanted) > 0 {
			if _, ok := wanted[definition.Type]; !ok {
				continue
			}
		}

		collector := definition.newCollector()
		if collector == nil {
			return nil, unknown, fmt.Errorf("%s 수집기 생성: nil 반환", definition.Type)
		}
		if collector.Type() != definition.Type {
			return nil, unknown, fmt.Errorf("%s 수집기 생성: 타입 불일치 (%s)", definition.Type, collector.Type())
		}

		if err := registry.Add(collector); err != nil {
			return nil, unknown, fmt.Errorf("%s 수집기 등록: %w", definition.Type, err)
		}
	}

	return registry, unknown, nil
}

func validateDefinitions(definitions []Definition) error {
	seenTypes := make(map[string]struct{}, len(definitions))

	for i, definition := range definitions {
		if strings.TrimSpace(definition.Type) == "" {
			return fmt.Errorf("정의 %d: 타입이 비어 있음", i)
		}
		if _, exists := seenTypes[definition.Type]; exists {
			return fmt.Errorf("리소스 타입 중복: %s", definition.Type)
		}
		seenTypes[definition.Type] = struct{}{}

		if strings.TrimSpace(definition.Label) == "" {
			return fmt.Errorf("%s: 표시명이 비어 있음", definition.Type)
		}
		if definition.Scope != Regional && definition.Scope != Global {
			return fmt.Errorf("%s: 조회 범위가 올바르지 않음", definition.Type)
		}
		if len(definition.Columns) == 0 {
			return fmt.Errorf("%s: 목록 열이 비어 있음", definition.Type)
		}
		if definition.newCollector == nil {
			return fmt.Errorf("%s: 수집기 생성자가 없음", definition.Type)
		}

		seenColumns := make(map[string]struct{}, len(definition.Columns))
		for _, column := range definition.Columns {
			if strings.TrimSpace(column) == "" {
				return fmt.Errorf("%s: 빈 목록 열이 있음", definition.Type)
			}
			if _, exists := seenColumns[column]; exists {
				return fmt.Errorf("%s: 목록 열 중복 (%s)", definition.Type, column)
			}
			seenColumns[column] = struct{}{}
		}
	}

	return nil
}

// allDefinitions는 서비스별 정의를 최종 표시·실행 순서로 조립한다.
//
// 새 리소스는 해당 서비스의 definitions 함수에 추가한다. 새 서비스는 definitions 함수를
// 만들고 이곳에 한 줄을 추가한다. 순서를 숨기는 init 자동 등록은 사용하지 않는다.
func allDefinitions(cfg aws.Config) []Definition {
	var definitions []Definition
	definitions = append(definitions, ec2Definitions(cfg)...)
	definitions = append(definitions, elbv2Definitions(cfg)...)
	definitions = append(definitions, route53Definitions(cfg)...)
	definitions = append(definitions, wafv2Definitions(cfg)...)

	return definitions
}
