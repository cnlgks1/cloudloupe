package collect_test

import (
	"reflect"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func TestRegistryPreservesOrder(t *testing.T) {
	t.Parallel()

	// 등록 순서가 유지되어야 한다. 조회 순서와 리포트 그룹 순서가 여기에 의존한다.
	reg := collect.NewRegistry()
	reg.Add(&fakeCollector{typ: model.TypeELBv2LoadBalancer})
	reg.Add(&fakeCollector{typ: model.TypeEC2Instance})
	reg.Add(&fakeCollector{typ: model.TypeEC2Volume})

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
	reg.Add(&fakeCollector{typ: model.TypeEC2Instance})
	reg.Add(&fakeCollector{typ: model.TypeEC2Volume})
	reg.Add(&fakeCollector{typ: model.TypeELBv2LoadBalancer})

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
		sel, unknown := reg.Select([]string{model.TypeEC2Instance, "ec2:instanse"})

		if len(sel.Types()) != 1 {
			t.Errorf("유효한 타입 1개만 선택되어야 한다: %v", sel.Types())
		}

		if len(unknown) != 1 || unknown[0] != "ec2:instanse" {
			t.Errorf("오타가 unknown으로 보고되어야 한다: %v", unknown)
		}
	})
}

func TestDefaultRegistryRegistersOnlyKnownReadOnlyTypes(t *testing.T) {
	t.Parallel()

	// DefaultRegistry가 등록하는 타입은 전부 model에 정의된 알려진 조회 대상이어야 한다.
	// 여기서는 nil 클라이언트로도 등록 자체를 확인할 수 있다. Collect를 호출하지 않는 한
	// 클라이언트는 쓰이지 않기 때문이다.
	reg := collect.DefaultRegistry(collect.Clients{})

	if len(reg.Types()) == 0 {
		t.Fatal("DefaultRegistry가 비어 있다")
	}

	known := map[string]bool{
		model.TypeEC2Instance:         true,
		model.TypeEC2Volume:           true,
		model.TypeEC2NetworkInterface: true,
		model.TypeEC2Address:          true,
		model.TypeELBv2LoadBalancer:   true,
		model.TypeELBv2TargetGroup:    true,
		model.TypeRoute53RecordSet:    true,
		model.TypeWAFv2WebACL:         true,
	}

	for _, typ := range reg.Types() {
		if !known[typ] {
			t.Errorf("DefaultRegistry에 알 수 없는 타입 %q가 등록되어 있다", typ)
		}
	}
}
