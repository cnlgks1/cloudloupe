package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/model"
	"github.com/cnlgks1/cloudloupe/internal/tui"
)

func sampleResources() []model.Resource {
	return []model.Resource{
		{
			Type:   model.TypeELBv2LoadBalancer,
			ID:     "web-alb",
			Name:   "web-alb",
			Region: "ap-northeast-2",
			Fields: []model.Field{{Key: "스킴", Value: "internet-facing"}},
			Related: []model.Ref{
				{Type: model.TypeELBv2TargetGroup, ID: "web-tg", Relation: model.RelationForwardsTo},
			},
		},
		{
			Type:   model.TypeEC2Instance,
			ID:     "i-0a1b",
			Name:   "web-01",
			Region: "ap-northeast-2",
		},
	}
}

// send는 Update를 직접 호출해 메시지를 흘려보낸다.
//
// TUI 테스트는 렌더링 문자열 비교가 아니라 "메시지 입력 → 기대 상태"로 한다. 렌더링
// 비교는 스타일이 조금만 바뀌어도 깨진다.
func send(m tui.Model, msgs ...tea.Msg) tui.Model {
	var current tea.Model = m
	for _, msg := range msgs {
		current, _ = current.Update(msg)
	}

	return current.(tui.Model)
}

func keyMsg(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}

	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}

	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestModelStartsOnList(t *testing.T) {
	t.Parallel()

	m := tui.NewModel(tui.New(false), sampleResources())
	if m.Screen() != 0 {
		t.Errorf("초기 화면이 리스트가 아니다: %v", m.Screen())
	}
}

func TestEnterOpensDetailEscGoesBack(t *testing.T) {
	t.Parallel()

	m := tui.NewModel(tui.New(false), sampleResources())
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	listScreen := m.Screen()

	// enter로 상세 진입.
	m = send(m, keyMsg("enter"))
	if m.Screen() == listScreen {
		t.Error("enter를 눌렀는데 상세로 전환되지 않았다")
	}

	// esc로 복귀.
	m = send(m, keyMsg("esc"))
	if m.Screen() != listScreen {
		t.Error("esc를 눌렀는데 리스트로 돌아오지 않았다")
	}
}

func TestViewRendersOnBothThemes(t *testing.T) {
	t.Parallel()

	// View는 순수해야 한다. 두 테마 모두에서 패닉 없이 문자열을 만들어야 한다.
	for _, ascii := range []bool{false, true} {
		m := tui.NewModel(tui.New(ascii), sampleResources())
		m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})

		if out := m.View(); out == "" {
			t.Errorf("ascii=%v: View가 빈 문자열을 반환했다", ascii)
		}

		// 상세 화면도 렌더링되는지.
		m = send(m, keyMsg("enter"))
		if out := m.View(); !strings.Contains(out, "web-alb") {
			t.Errorf("ascii=%v: 상세 뷰에 리소스 이름이 없다:\n%s", ascii, out)
		}
	}
}

func TestViewIsPure(t *testing.T) {
	t.Parallel()

	// View를 여러 번 호출해도 같은 결과여야 한다. 상태를 바꾸면 안 된다.
	m := tui.NewModel(tui.New(false), sampleResources())
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	first := m.View()
	second := m.View()

	if first != second {
		t.Error("View가 호출마다 다른 결과를 낸다 — 순수하지 않다")
	}
}

func TestDetailShowsOrderedFields(t *testing.T) {
	t.Parallel()

	// Fields는 순서 있는 슬라이스이므로 상세 뷰 순서가 결정적이어야 한다.
	res := []model.Resource{{
		Type:   model.TypeEC2Instance,
		ID:     "i-1",
		Name:   "test",
		Region: "ap-northeast-2",
		Fields: []model.Field{
			{Key: "첫째", Value: "1"},
			{Key: "둘째", Value: "2"},
			{Key: "셋째", Value: "3"},
		},
	}}

	m := tui.NewModel(tui.New(true), res)
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24}, keyMsg("enter"))

	out := m.View()

	first := strings.Index(out, "첫째")
	second := strings.Index(out, "둘째")
	third := strings.Index(out, "셋째")

	if !(first < second && second < third) {
		t.Errorf("필드 순서가 어긋났다: 첫째=%d 둘째=%d 셋째=%d\n%s", first, second, third, out)
	}
}
