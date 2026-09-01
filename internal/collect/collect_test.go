package collect_test

import (
	"reflect"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func addCollector(t *testing.T, registry *collect.Registry, collector collect.Collector) {
	t.Helper()

	if err := registry.Add(collector); err != nil {
		t.Fatalf("수집기 등록 실패: %v", err)
	}
}

func TestRegistryPreservesOrder(t *testing.T) {
	t.Parallel()

	// 등록 순서가 유지되어야 한다. 조회 순서와 리포트 그룹 순서가 여기에 의존한다.
	reg := collect.NewRegistry()
	addCollector(t, reg, &fakeCollector{typ: model.TypeELBv2LoadBalancer})
	addCollector(t, reg, &fakeCollector{typ: model.TypeEC2Instance})
	addCollector(t, reg, &fakeCollector{typ: model.TypeEC2Volume})

	want := []string{
		model.TypeELBv2LoadBalancer,
		model.TypeEC2Instance,
		model.TypeEC2Volume,
	}

	if got := reg.Types(); !reflect.DeepEqual(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
}

func TestRegistrySelect(t *testing.T) {
	t.Parallel()

	reg := collect.NewRegistry()
	addCollector(t, reg, &fakeCollector{typ: model.TypeEC2Instance})
	addCollector(t, reg, &fakeCollector{typ: model.TypeEC2Volume})
	addCollector(t, reg, &fakeCollector{typ: model.TypeELBv2LoadBalancer})

	t.Run("일부만 선택", func(t *testing.T) {
		t.Parallel()

		sel, unknown := reg.Select([]string{model.TypeEC2Instance, model.TypeELBv2LoadBalancer})

		if len(unknown) != 0 {
			t.Errorf("알 수 없는 타입 %v", unknown)
		}

		want := []string{model.TypeEC2Instance, model.TypeELBv2LoadBalancer}
		if got := sel.Types(); !reflect.DeepEqual(got, want) {
			t.Errorf("선택 결과 %v, want %v", got, want)
		}
	})

	t.Run("오타는 unknown으로 보고", func(t *testing.T) {
		t.Parallel()

		// 오타를 조용히 무시하면 사용자는 왜 그 타입이 안 나오는지 알 수 없다.
		sel, unknown := reg.Select([]string{model.TypeEC2Instance, "ec2:unknown"})

		if len(sel.Types()) != 1 {
			t.Errorf("유효한 타입 1개만 선택되어야 한다: %v", sel.Types())
		}

		if len(unknown) != 1 || unknown[0] != "ec2:unknown" {
			t.Errorf("오타가 unknown으로 보고되어야 한다: %v", unknown)
		}
	})
}

func TestRegistryRejectsDuplicateType(t *testing.T) {
	t.Parallel()

	reg := collect.NewRegistry()
	if err := reg.Add(&fakeCollector{typ: model.TypeEC2Instance}); err != nil {
		t.Fatalf("첫 등록 실패: %v", err)
	}

	if err := reg.Add(&fakeCollector{typ: model.TypeEC2Instance}); err == nil {
		t.Fatal("같은 타입의 수집기를 중복 등록했는데 오류가 없다")
	}

	if got := reg.Types(); !reflect.DeepEqual(got, []string{model.TypeEC2Instance}) {
		t.Errorf("중복 거부 후 Types() = %v", got)
	}
}
