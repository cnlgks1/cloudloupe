package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// pump는 실제 런타임처럼 Update가 낸 Cmd를 소진한다.
// pump는 실제 런타임처럼 Update가 낸 Cmd를 소진하되, 스피너 Tick 같은 반복 타이머
// Cmd는 따라가지 않는다. 타이머 Cmd는 실제 시간을 기다리므로 따라가면 테스트가 느려진다.
func pump(m tea.Model, cmd tea.Cmd) tea.Model {
	for range 10 {
		if cmd == nil {
			return m
		}

		msg := cmd()
		if msg == nil {
			return m
		}

		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				if c == nil {
					continue
				}

				if inner := c(); inner != nil {
					m, _ = m.Update(inner)
				}
			}

			return m
		}

		m, cmd = m.Update(msg)
	}

	return m
}

func key(m tea.Model, s string) tea.Model {
	var msg tea.KeyMsg
	switch s {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	next, cmd := m.Update(msg)
	return pump(next, cmd)
}

func TestResourceTableRendersRows(t *testing.T) {
	// 리소스 목록은 컬럼 테이블로 그려진다. 조회한 리소스가 테이블 행으로 나타나야 한다.
	res := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-web-01", Name: "web-01", Region: "ap-northeast-2", Status: "running"},
		{Type: model.TypeEC2Instance, ID: "i-db-99", Name: "db-99", Region: "ap-northeast-2", Status: "stopped"},
	}
	var m tea.Model = mkModel(t, res)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = key(m, "enter") // 리전
	m = key(m, "enter") // 타입
	m = key(m, "enter") // 목록

	v := m.View()
	if !strings.Contains(v, "web-01") || !strings.Contains(v, "db-99") {
		t.Errorf("테이블에 두 리소스가 모두 행으로 나와야 한다:\n%s", v)
	}

	// 공통 열 제목이 보여야 한다.
	if !strings.Contains(v, "이름") || !strings.Contains(v, "상태") {
		t.Errorf("테이블 헤더(이름/상태)가 보여야 한다:\n%s", v)
	}
}

func TestQQuitsNotFilters(t *testing.T) {
	// q는 필터를 시작하지 않고 종료해야 한다. 타이핑 즉시 필터를 넣으면서 q가 필터
	// 글자로 처리되어 종료가 막힌 회귀를 방어한다.
	res := []model.Resource{{Type: model.TypeEC2Instance, ID: "i-1", Name: "web"}}
	var m tea.Model = mkModel(t, res)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// 프로필 화면에서 q → 종료 Cmd(tea.Quit)가 나와야 한다.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q를 눌렀는데 아무 Cmd도 없다. 종료가 안 된다")
	}

	if msg := cmd(); !isQuit(msg) {
		t.Errorf("q가 종료(tea.Quit)를 내야 한다, got %T", msg)
	}
}

// isQuit은 메시지가 tea.Quit의 결과인지 확인한다. tea.QuitMsg는 내부 타입이라 타입
// 이름으로 판별한다.
func isQuit(msg tea.Msg) bool {
	_, ok := msg.(tea.QuitMsg)

	return ok
}

func TestBreadcrumbShowsPath(t *testing.T) {
	// 모든 목록 화면 상단에 현재 경로(프로필/리전/리소스)가 보여야 한다. 프로필을 고른
	// 뒤 리전 화면에서 그 프로필이 헤더에 나타나는지 확인한다.
	var m tea.Model = mkModel(t, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = key(m, "enter") // 프로필 선택 → 신원 → 리전

	v := m.View()
	if !strings.Contains(v, "프로필") || !strings.Contains(v, "리전") || !strings.Contains(v, "리소스") {
		t.Errorf("상단 경로 헤더(프로필/리전/리소스)가 보여야 한다:\n%s", v)
	}

	// 첫 프로필(prod)이 헤더에 나타나야 한다.
	if !strings.Contains(v, "prod") {
		t.Errorf("경로 헤더에 선택한 프로필(prod)이 보여야 한다:\n%s", v)
	}
}
