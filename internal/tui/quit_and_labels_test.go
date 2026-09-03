package tui_test

import (
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/tui"
)

// listModel은 조회를 마친 리소스 목록 화면까지 진행한 모델을 만든다.
func listModel(t *testing.T) tui.Model {
	t.Helper()

	m := newTestModel(t, okDeps(sampleResources()))
	m = step(m, keyMsg("enter")) // 프로필 → 신원 확인 → 리전
	m = send(m, keyMsg("enter")) // 리전 → 리소스 선택
	m = step(m, keyMsg("enter")) // 커서 서비스 조회 → 목록

	if m.Screen() != tui.ScreenList {
		t.Fatalf("조회 후 화면 = %v, want 리소스 목록", m.Screen())
	}

	return m
}

// TestQuitWorksOnDetailScreen은 상세 화면에서도 q가 종료하는지 확인한다.
//
// 상세 화면에서만 q를 목록 복귀로 쓴 적이 있다. 하단 도움말은 모든 화면에서 "q quit"으로
// 안내하므로 같은 키가 안내와 다르게 동작했고, 종료하려면 esc로 나온 뒤 q를 다시 눌러야 했다.
func TestQuitWorksOnDetailScreen(t *testing.T) {
	t.Parallel()

	m := listModel(t)

	m = send(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenDetail {
		t.Fatalf("enter 후 화면 = %v, want 상세", m.Screen())
	}

	next, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("상세 화면의 q가 종료 Cmd를 내지 않았다")
	}
	if msg := cmd(); !isQuit(msg) {
		t.Errorf("상세 화면의 q = %T, want tea.QuitMsg", msg)
	}

	// 종료가 화면 전이로 바뀌어서는 안 된다.
	if got := next.(tui.Model).Screen(); got != tui.ScreenDetail {
		t.Errorf("q 이후 화면 = %v, want 상세 유지", got)
	}
}

// TestBackLeavesDetailWithoutQuitting은 상세 화면의 뒤로 가기가 esc와 ←인지 확인한다.
func TestBackLeavesDetailWithoutQuitting(t *testing.T) {
	t.Parallel()

	for _, keyName := range []string{"esc", "left"} {
		t.Run(keyName, func(t *testing.T) {
			t.Parallel()

			m := listModel(t)
			m = send(m, keyMsg("enter"))

			next, cmd := m.Update(keyMsg(keyName))
			if cmd != nil {
				if msg := cmd(); isQuit(msg) {
					t.Fatalf("%s가 종료를 냈다", keyName)
				}
			}
			if got := next.(tui.Model).Screen(); got != tui.ScreenList {
				t.Errorf("%s 이후 화면 = %v, want 리소스 목록", keyName, got)
			}
		})
	}
}

// TestSelectionScreenUsesAWSVocabulary는 선택 화면이 AWS 용어를 쓰는지 확인한다.
//
// 이 화면만 "Item", "Items"라는 자체 용어를 썼다. AWS 문서·콘솔·CLI 어디에도 없는 말이라
// 숫자가 무엇을 센 것인지 짐작할 근거가 없었고, 실제로 리전의 리소스 개수로 읽혔다.
func TestSelectionScreenUsesAWSVocabulary(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(nil))
	m = step(m, keyMsg("enter")) // 프로필 → 리전
	m = send(m, keyMsg("enter")) // 리전 → 리소스 선택

	if m.Screen() != tui.ScreenResource {
		t.Fatalf("화면 = %v, want 리소스 선택", m.Screen())
	}

	collapsed := m.View()
	for _, want := range []string{"Select resource type", "Service", "Resource types", "resource types"} {
		if !strings.Contains(collapsed, want) {
			t.Errorf("접힌 화면에 %q 표기가 없다:\n%s", want, collapsed)
		}
	}

	m = send(m, keyMsg("right")) // 첫 서비스 펼치기
	expanded := m.View()
	for _, want := range []string{"Type ID", "ec2:instance"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("펼친 화면에 %q 표기가 없다:\n%s", want, expanded)
		}
	}

	// AWS에 없는 자체 용어가 열 이름으로 돌아오면 안 된다.
	for _, view := range []string{collapsed, expanded} {
		for _, line := range strings.Split(view, "\n") {
			if !strings.Contains(line, "Type ID") {
				continue
			}
			if strings.Contains(line, "Item") {
				t.Errorf("열 이름에 자체 용어 Item이 남아 있다: %q", line)
			}
		}
	}
}
