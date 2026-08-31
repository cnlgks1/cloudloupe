package catalog

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

type fakeCatalogCollector struct {
	typ string
}

func (c fakeCatalogCollector) Type() string { return c.typ }

func (c fakeCatalogCollector) Collect(context.Context, collect.Request) ([]model.Resource, error) {
	return nil, nil
}

func TestDefinitionsAreValidAndOrdered(t *testing.T) {
	t.Parallel()

	definitions := allDefinitions(aws.Config{})
	if err := validateDefinitions(definitions); err != nil {
		t.Fatalf("기본 카탈로그 검증 실패: %v", err)
	}

	want := []string{
		model.TypeEC2Instance,
		model.TypeEC2Volume,
		model.TypeEC2NetworkInterface,
		model.TypeEC2Address,
		model.TypeELBv2LoadBalancer,
		model.TypeELBv2TargetGroup,
		model.TypeRoute53RecordSet,
		model.TypeWAFv2WebACL,
	}

	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.Type)

		collector := definition.newCollector()
		if collector == nil {
			t.Errorf("%s 생성자가 nil 수집기를 반환함", definition.Type)
			continue
		}
		if collector.Type() != definition.Type {
			t.Errorf("%s 생성자 Type() = %q", definition.Type, collector.Type())
		}
	}

	if !slices.Equal(got, want) {
		t.Errorf("정의 순서 = %v, want %v", got, want)
	}
}

func TestDefinitionsReturnsCopies(t *testing.T) {
	t.Parallel()

	first := Definitions()
	first[0].Type = "changed"
	first[0].Columns[0] = "changed"

	second := Definitions()
	if second[0].Type != model.TypeEC2Instance {
		t.Errorf("Type = %q, want %q", second[0].Type, model.TypeEC2Instance)
	}
	if second[0].Columns[0] != "인스턴스 타입" {
		t.Errorf("Columns[0] = %q, want %q", second[0].Columns[0], "인스턴스 타입")
	}
}

func TestBuildRegistrySelection(t *testing.T) {
	t.Parallel()

	definitions := allDefinitions(aws.Config{})

	t.Run("선택 타입만 카탈로그 순서로 등록", func(t *testing.T) {
		t.Parallel()

		registry, unknown, err := buildRegistry(definitions, true, []string{
			model.TypeEC2Volume,
			model.TypeEC2Instance,
			model.TypeEC2Volume,
		})
		if err != nil {
			t.Fatalf("레지스트리 생성 실패: %v", err)
		}
		if len(unknown) != 0 {
			t.Errorf("unknown = %v, want empty", unknown)
		}

		want := []string{model.TypeEC2Instance, model.TypeEC2Volume}
		if got := registry.Types(); !slices.Equal(got, want) {
			t.Errorf("Types() = %v, want %v", got, want)
		}
	})

	t.Run("글로벌 타입 제외", func(t *testing.T) {
		t.Parallel()

		registry, _, err := buildRegistry(definitions, false, nil)
		if err != nil {
			t.Fatalf("레지스트리 생성 실패: %v", err)
		}
		if slices.Contains(registry.Types(), model.TypeRoute53RecordSet) {
			t.Errorf("글로벌 제외 결과에 %s가 포함됨", model.TypeRoute53RecordSet)
		}
	})

	t.Run("알 수 없는 타입은 입력 순서로 보고", func(t *testing.T) {
		t.Parallel()

		registry, unknown, err := buildRegistry(definitions, true, []string{
			"ec2:unknown",
			model.TypeEC2Volume,
			"route53:unknown",
		})
		if err != nil {
			t.Fatalf("레지스트리 생성 실패: %v", err)
		}
		if got, want := registry.Types(), []string{model.TypeEC2Volume}; !slices.Equal(got, want) {
			t.Errorf("Types() = %v, want %v", got, want)
		}
		if want := []string{"ec2:unknown", "route53:unknown"}; !slices.Equal(unknown, want) {
			t.Errorf("unknown = %v, want %v", unknown, want)
		}
	})
}

func TestValidateDefinitionsRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	valid := func(typ string) Definition {
		return Definition{
			Type:    typ,
			Label:   "테스트",
			Scope:   Regional,
			Columns: []string{"상태"},
			newCollector: func() collect.Collector {
				return fakeCatalogCollector{typ: typ}
			},
		}
	}

	tests := []struct {
		name        string
		definitions []Definition
		want        string
	}{
		{name: "빈 타입", definitions: []Definition{{Label: "테스트", Scope: Regional, Columns: []string{"상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{} }}}, want: "타입이 비어"},
		{name: "중복 타입", definitions: []Definition{valid("test:item"), valid("test:item")}, want: "타입 중복"},
		{name: "빈 표시명", definitions: []Definition{{Type: "test:item", Scope: Regional, Columns: []string{"상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "표시명이 비어"},
		{name: "잘못된 범위", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: scopeUnknown, Columns: []string{"상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "조회 범위"},
		{name: "빈 열 목록", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "목록 열이 비어"},
		{name: "빈 열", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, Columns: []string{" "}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "빈 목록 열"},
		{name: "중복 열", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, Columns: []string{"상태", "상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "목록 열 중복"},
		{name: "생성자 없음", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, Columns: []string{"상태"}}}, want: "생성자가 없음"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateDefinitions(tt.definitions)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestBuildRegistryRejectsBrokenFactory(t *testing.T) {
	t.Parallel()

	base := Definition{
		Type:    "test:item",
		Label:   "테스트",
		Scope:   Regional,
		Columns: []string{"상태"},
	}

	t.Run("nil 반환", func(t *testing.T) {
		t.Parallel()

		definition := base
		definition.newCollector = func() collect.Collector { return nil }

		_, _, err := buildRegistry([]Definition{definition}, true, nil)
		if err == nil || !strings.Contains(err.Error(), "nil 반환") {
			t.Errorf("err = %v, want nil 반환", err)
		}
	})

	t.Run("타입 불일치", func(t *testing.T) {
		t.Parallel()

		definition := base
		definition.newCollector = func() collect.Collector {
			return fakeCatalogCollector{typ: "test:other"}
		}

		_, _, err := buildRegistry([]Definition{definition}, true, nil)
		if err == nil || !strings.Contains(err.Error(), "타입 불일치") {
			t.Errorf("err = %v, want 타입 불일치", err)
		}
	})
}
