package tui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// screen은 현재 화면을 나타낸다. 화면 전환은 이 enum과 switch로 명시한다.
type screen int

const (
	screenList screen = iota
	screenDetail
)

// keyMap은 키 바인딩을 한곳에 모은다. help 뷰와 공유하고, 키 문자열을 Update 안에
// 흩뿌리지 않기 위함이다.
type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	Enter  key.Binding
	Back   key.Binding
	Filter key.Binding
	Quit   key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:     key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("j/k", "이동")),
		Down:   key.NewBinding(key.WithKeys("j", "down")),
		Enter:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "상세")),
		Back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "뒤로")),
		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "필터")),
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "종료")),
	}
}

// resourceItem은 리스트에 표시되는 리소스 하나다. list.DefaultItem을 구현한다.
type resourceItem struct {
	res model.Resource
}

func (i resourceItem) Title() string       { return i.res.DisplayName() }
func (i resourceItem) Description() string { return i.res.Type + "  " + i.res.Region }
func (i resourceItem) FilterValue() string { return i.res.DisplayName() + " " + i.res.Type }

// Model은 리소스 목록 화면의 상태다.
//
// 이 파일은 phase-1 TUI가 실제로 Bubble Tea 라이브러리와 맞물리는지 확인하기 위한 최소
// 구현이다. 프로필 선택과 수집 연동은 이후 단계에서 붙는다.
type Model struct {
	theme   Theme
	keys    keyMap
	screen  screen
	list    list.Model
	detail  viewport.Model
	spinner spinner.Model
	width   int
	height  int
}

// NewModel은 리소스 목록으로 초기화된 모델을 만든다.
func NewModel(theme Theme, resources []model.Resource) Model {
	items := make([]list.Item, 0, len(resources))
	for _, r := range resources {
		items = append(items, resourceItem{res: r})
	}

	delegate := list.NewDefaultDelegate()
	lst := list.New(items, delegate, 0, 0)
	lst.Title = "리소스"
	lst.SetShowHelp(true)

	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: theme.Glyphs.SpinnerDots, FPS: 10}

	return Model{
		theme:   theme,
		keys:    defaultKeys(),
		screen:  screenList,
		list:    lst,
		detail:  viewport.New(0, 0),
		spinner: sp,
	}
}

// Init은 Bubble Tea 인터페이스를 만족한다.
func (m Model) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update는 상태 전이만 담당한다. 부수효과는 tea.Cmd로 내보낸다.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-1)
		m.detail.Width = msg.Width
		m.detail.Height = msg.Height - 1

		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd

	switch m.screen {
	case screenList:
		m.list, cmd = m.list.Update(msg)
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	}

	return m, cmd
}

// handleKey는 키 입력을 처리한다. 화면별로 나뉜다.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 필터 입력 중에는 리스트가 키를 먼저 소비하게 둔다. 그러지 않으면 q 같은 글자를
	// 필터에 입력할 수 없다.
	if m.screen == screenList && m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)

		return m, cmd
	}

	if key.Matches(msg, m.keys.Quit) {
		return m, tea.Quit
	}

	switch m.screen {
	case screenList:
		return m.handleListKey(msg)
	case screenDetail:
		if key.Matches(msg, m.keys.Back) {
			m.screen = screenList

			return m, nil
		}

		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Enter) {
		if it, ok := m.list.SelectedItem().(resourceItem); ok {
			m.detail.SetContent(renderDetail(m.theme, it.res))
			m.screen = screenDetail

			return m, nil
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)

	return m, cmd
}

// View는 순수 렌더링이다. 문자열만 만들고 상태를 바꾸지 않는다.
func (m Model) View() string {
	switch m.screen {
	case screenDetail:
		return m.detail.View()
	case screenList:
		return m.list.View()
	default:
		return m.list.View()
	}
}

// Screen은 현재 화면을 노출한다. 테스트에서 상태 전이를 확인하는 데 쓴다.
func (m Model) Screen() screen {
	return m.screen
}

// renderDetail은 리소스 상세를 문자열로 만든다.
//
// Fields는 순서 있는 슬라이스이므로 렌더링할 때마다 같은 순서로 나온다.
func renderDetail(theme Theme, res model.Resource) string {
	var sb []string

	sb = append(sb, theme.Title.Render(res.DisplayName()))
	sb = append(sb, theme.Faint.Render(res.Type+"  "+res.Region))

	for _, f := range res.Fields {
		sb = append(sb, f.Key+": "+f.Value)
	}

	for _, ref := range res.Related {
		sb = append(sb, theme.Glyphs.TreeBranch+" "+ref.Relation+" "+ref.ID)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sb...)
}
