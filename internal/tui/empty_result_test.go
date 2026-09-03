package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
	"github.com/cnlgks1/cloudloupe/internal/tui"
)

// collectResult는 지정한 결과를 그대로 돌려주는 가짜 수집기를 붙인 Deps를 만든다.
func depsReturning(result collect.Result) tui.Deps {
	deps := okDeps(nil)
	deps.Collect = func(context.Context, string, []string, []string, awsclient.Locations) collect.Result {
		return result
	}

	return deps
}

// queryFirstGroupItem은 첫 서비스를 조회한 목록 화면까지 진행한다.
func queryFirstGroupItem(t *testing.T, deps tui.Deps) tui.Model {
	t.Helper()

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 프로필 → 리전
	m = send(m, keyMsg("enter")) // 리전 → 리소스 선택
	m = step(m, keyMsg("enter")) // 커서 서비스 조회

	if m.Screen() != tui.ScreenList {
		t.Fatalf("조회 후 화면 = %v, want 리소스 목록", m.Screen())
	}

	return m
}

// TestEmptyResultExplainsWhyNothingIsShown은 결과가 0개일 때 이유를 설명하는지 확인한다.
//
// 빈 표만 보여주면 조회가 실패한 것인지 정말 없는 것인지 알 수 없다. 그룹 화면의 타입 수를
// 리소스 개수로 오해한 사용자는 특히 값이 갱신되지 않은 것으로 읽는다.
func TestEmptyResultExplainsWhyNothingIsShown(t *testing.T) {
	t.Parallel()

	m := queryFirstGroupItem(t, depsReturning(collect.Result{}))

	view := m.View()
	for _, want := range []string{"No ", "ap-northeast-2", "found nothing"} {
		if !strings.Contains(view, want) {
			t.Errorf("빈 결과 안내에 %q가 없다:\n%s", want, view)
		}
	}

	// 다음에 할 일을 알려줘야 한다.
	if !strings.Contains(view, "another region") {
		t.Errorf("빈 결과 안내에 다음 동작이 없다:\n%s", view)
	}
}

// TestEmptyResultPointsToErrorsWhenQueriesFailed는 부분 실패로 비었을 때 오류를 먼저
// 가리키는지 확인한다. "없음"과 "못 봤음"은 사용자가 할 일이 다르다.
func TestEmptyResultPointsToErrorsWhenQueriesFailed(t *testing.T) {
	t.Parallel()

	result := collect.Result{Errors: []model.CollectError{{
		Type:        model.TypeEC2Instance,
		Profile:     "prod",
		Region:      "ap-northeast-2",
		Code:        "AccessDeniedException",
		Message:     "raw access denied",
		Explanation: "조회 권한이 없습니다.",
	}}}

	m := queryFirstGroupItem(t, depsReturning(result))

	view := m.View()
	if !strings.Contains(view, "Press e") {
		t.Errorf("실패로 비었을 때 오류 안내가 없다:\n%s", view)
	}
	if strings.Contains(view, "found nothing") {
		t.Errorf("실패했는데 리소스가 없다고 단정했다:\n%s", view)
	}
}

// TestEmptyNoticeIsSameForEveryGroup은 어느 그룹을 골라도 같은 안내가 나오는지 확인한다.
//
// 그룹마다 다른 화면을 보여주면 사용자가 매번 다시 해석해야 한다.
func TestEmptyNoticeIsSameForEveryGroup(t *testing.T) {
	t.Parallel()

	deps := depsReturning(collect.Result{})

	// 첫 그룹과 두 번째 그룹 각각의 첫 항목을 조회한다.
	first := queryFirstGroupItem(t, deps)

	second := newTestModel(t, deps)
	second = step(second, keyMsg("enter"))
	second = send(second, keyMsg("enter"))
	second = send(second, keyMsg("down")) // 두 번째 서비스
	second = step(second, keyMsg("enter"))

	for _, want := range []string{"found nothing", "another region"} {
		if !strings.Contains(first.View(), want) || !strings.Contains(second.View(), want) {
			t.Errorf("그룹에 따라 빈 결과 안내가 다르다 (%q)", want)
		}
	}
}

// TestFilterMissIsNotTreatedAsEmptyResult는 필터로 행이 사라진 경우를 "리소스 없음"으로
// 설명하지 않는지 확인한다. 그때는 필터를 지우면 결과가 돌아온다.
func TestFilterMissIsNotTreatedAsEmptyResult(t *testing.T) {
	t.Parallel()

	m := queryFirstGroupItem(t, depsReturning(collect.Result{Resources: sampleResources()}))

	m = send(m, keyMsg("/"))
	m = send(m, keyMsg("존재하지않는이름"))
	m = send(m, keyMsg("enter"))

	if view := m.View(); strings.Contains(view, "found nothing") {
		t.Errorf("필터 결과 없음을 리소스 없음으로 설명했다:\n%s", view)
	}
}

// TestEmptyResultHidesUnusableKeys는 결과가 없을 때 눌러도 아무 일이 없는 키를 안내에서
// 빼는지 확인한다. 반응하지 않는 키를 남겨두면 도구가 멈춘 것처럼 보인다.
func TestEmptyResultHidesUnusableKeys(t *testing.T) {
	t.Parallel()

	m := queryFirstGroupItem(t, depsReturning(collect.Result{}))

	view := m.View()
	for _, unusable := range []string{"details", "search"} {
		if strings.Contains(view, unusable) {
			t.Errorf("빈 결과 화면에 쓸 수 없는 키 안내 %q가 남아 있다:\n%s", unusable, view)
		}
	}

	// 다른 리전이나 프로필로 옮기는 길은 남아 있어야 한다.
	for _, want := range []string{"switch region", "switch profile", "esc/← back", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("빈 결과 화면에 %q 안내가 없다:\n%s", want, view)
		}
	}
}

// TestResultsKeepFullHelpBar는 결과가 있을 때는 상세·검색 안내가 그대로인지 확인한다.
func TestResultsKeepFullHelpBar(t *testing.T) {
	t.Parallel()

	m := queryFirstGroupItem(t, depsReturning(collect.Result{Resources: sampleResources()}))

	view := m.View()
	for _, want := range []string{"details", "search", "switch region"} {
		if !strings.Contains(view, want) {
			t.Errorf("결과가 있는 화면에 %q 안내가 없다:\n%s", want, view)
		}
	}
}
