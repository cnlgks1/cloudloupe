package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// Screen은 현재 화면이다. 대화형 흐름은 이 enum과 switch로 명시한다.
//
// 흐름: 프로필 선택 → 신원 확인 → 리전 선택 → 수집 중 → 리소스 목록 → 상세.
// esc로 한 단계씩 뒤로 간다.
//
// 테스트가 화면 전환을 검증할 수 있도록 타입과 상수를 export한다.
type Screen int

// 화면 상수.
const (
	ScreenConfigPath Screen = iota // 설정을 못 찾았을 때 경로를 입력받는 화면
	ScreenProfile
	ScreenIdentity
	ScreenRegion
	ScreenResourceType // 조회할 리소스 그룹 선택
	ScreenResourceItem // 그룹 안의 세부 리소스 항목 선택
	ScreenCollecting
	ScreenList
	ScreenResourceKind // 수집 결과 안의 세부 종류 필터 선택
	ScreenDetail
	ScreenCollectErrors      // 부분 수집 오류 목록
	ScreenCollectErrorDetail // 부분 수집 오류 원본 상세
	ScreenError              // 프로필·신원 확인 단계의 치명적 오류
)

// Deps는 TUI가 바깥 세계와 상호작용하는 데 필요한 함수들이다.
//
// TUI가 awsclient/collect를 직접 부르지 않고 함수로 주입받는 이유: 테스트에서 AWS 없이
// 가짜 구현을 넘길 수 있고, 의존성 방향이 한쪽으로 유지된다(tui는 이 인터페이스만 안다).
type Deps struct {
	// LoadProfiles는 주어진 경로 지정으로 프로필을 읽는다. Override가 비면 기본 위치
	// (~/.aws)를 쓴다. 경로를 바꿔가며 다시 시도할 수 있도록 함수로 주입한다.
	LoadProfiles func(awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error)

	// ResourceGroups는 사용자가 선택할 AWS 서비스별 큰 리소스 목록이다.
	// 세부 타입은 그룹 안에 남아 실제 수집기 선택과 목록 종류 표시에 사용한다.
	ResourceGroups []ResourceGroup

	// Identify는 프로필의 신원을 확인한다(STS GetCallerIdentity).
	Identify func(ctx context.Context, profile, region string) (awsclient.Identity, error)

	// Collect는 선택한 프로필의 여러 리전에서 지정한 타입만 조회한다. types가 비면 전부.
	Collect func(ctx context.Context, profile string, regions, types []string) collect.Result

	// Explain은 에러를 사용자용 문장으로 바꾼다.
	Explain func(error) string
}

// ResourceGroup은 선택 화면에 보여줄 AWS 서비스별 큰 리소스 묶음이다.
type ResourceGroup struct {
	ID    string         // "ec2" 같은 안정적인 그룹 ID
	Label string         // "EC2" 같은 표시 이름
	Types []ResourceType // 그룹을 선택했을 때 함께 수집할 내부 세부 타입
}

// ResourceType은 그룹 안에서 수집되는 내부 리소스 타입의 표시 메타데이터다.
type ResourceType struct {
	ID             string   // "ec2:instance" 같은 타입 ID
	Label          string   // "EC2 인스턴스" 같은 표시 이름
	Columns        []string // 단일 타입 목록에 표시할 Resource.Fields 키 순서
	SummaryColumns []string // 혼합 그룹의 주요 정보에 합칠 필드 순서
}

// resourceKind는 실제 수집 결과에 존재하는 세부 종류와 개수다.
type resourceKind struct {
	ID    string
	Label string
	Count int
}

// Model은 대화형 TUI 전체의 상태다.
type Model struct {
	theme Theme
	keys  keyMap
	deps  Deps

	screen Screen
	width  int
	height int

	profiles  []awsclient.Profile
	override  awsclient.Override
	locations awsclient.Locations

	// 경로 입력 화면은 config와 credentials 두 경로를 각각 받는다. pathFocus가 지금
	// 어느 입력에 커서가 있는지 가리킨다(0=config, 1=credentials). tab으로 오간다.
	configInput textinput.Model
	credsInput  textinput.Model
	pathFocus   int

	// 일반 선택 테이블은 위젯 커서로 원본을 찾는다. 리소스 목록은 원본과 파생 인덱스,
	// 가상 viewport를 구체 서브모델 하나에서 함께 관리한다.
	profileTable table.Model
	regionTable  table.Model
	typeTable    table.Model
	itemTable    table.Model
	kindTable    table.Model
	errorTable   table.Model
	detail       viewport.Model
	spinner      spinner.Model

	resourceList       resourceListModel
	listCaption        string
	filterInput        textinput.Model
	filtering          bool
	filterQuery        string
	previousFilter     string
	resourceKinds      []resourceKind
	resourceKindFilter string
	collectErrors      []model.CollectError
	showRegion         bool

	chosenProfile    string
	identity         awsclient.Identity
	regions          []awsclient.Region
	chosenRegions    []string
	confirmedRegions []string
	chosenTypes      []string

	// itemGroup은 세부 항목 화면이 보여주는 그룹의 인덱스다. 그룹을 되짚어 올라가거나
	// 창 크기가 바뀔 때 어떤 그룹을 다시 그려야 하는지 알아야 한다.
	itemGroup int

	// replace...OnEnter는 목록 화면에서 리전/타입을 바꾸러 들어왔을 때 기존 선택보다
	// 현재 커서 행을 우선하도록 한다. space로 다중 선택을 조작하면 false로 바뀐다.
	replaceRegionOnEnter bool
	replaceTypeOnEnter   bool

	// collectFromItem은 조회를 세부 항목 화면에서 시작했는지 나타낸다. 뒤로 가기는 건너뛴
	// 화면이 아니라 실제로 지나온 화면으로 돌아가야 한다.
	collectFromItem bool

	// explicit...Selection은 space로 명시적으로 체크한 다중 선택인지 나타낸다.
	// false면 이전 Enter 선택이 남아 있어도 현재 커서 항목으로 교체한다.
	explicitRegionSelection bool
	explicitTypeSelection   bool

	errText string
	loading string

	// cancel은 진행 중인 백그라운드 작업(신원 확인, 수집)을 취소한다. esc로 호출된다.
	cancel context.CancelFunc

	// collectSequence와 identitySequence는 취소된 이전 작업의 늦은 완료 메시지를 구별한다.
	collectSequence  uint64
	identitySequence uint64
}

// keyMap은 키 바인딩을 한곳에 모은다. 키 문자열을 Update에 흩뿌리지 않기 위함이다.
//
// Enter/Back에 화살표(→/←)를 함께 묶는다. 마법사형 흐름(프로필→리전→타입→목록)에서
// →로 다음 단계, ←로 이전 단계로 가는 것이 화살표만 쓰는 사용자에게 자연스럽기 때문이다.
// 이렇게 하려면 list 위젯이 기본으로 →/←에 걸어 둔 "페이지 넘김"을 꺼야 한다(newList에서
// 처리). 페이지 넘김은 f/b(또는 pgup/pgdn)로 남으므로 잃는 기능은 없다.
type keyMap struct {
	Enter         key.Binding
	Back          key.Binding
	Toggle        key.Binding
	Quit          key.Binding
	SwitchProfile key.Binding
	SwitchRegion  key.Binding
	FilterKind    key.Binding
	ShowErrors    key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Enter:         key.NewBinding(key.WithKeys("enter", "right"), key.WithHelp("enter/→", "선택")),
		Back:          key.NewBinding(key.WithKeys("esc", "left"), key.WithHelp("esc/←", "뒤로")),
		Toggle:        key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "선택")),
		Quit:          key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "종료")),
		SwitchProfile: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "프로필 전환")),
		SwitchRegion:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "리전 전환")),
		FilterKind:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "종류 필터")),
		ShowErrors:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "오류 보기")),
	}
}

// NewModel은 모델을 만든다.
//
// 시작 화면은 프로필 로딩 결과에 달렸다. 기본 위치(또는 override)에서 프로필을 읽으면
// 프로필 선택 화면으로, 설정을 못 찾으면 경로 입력 화면으로 시작한다. 후자가 사용자가
// 물어본 흐름이다 — "기본 경로를 보고, 없으면 경로를 입력받는다".
func NewModel(theme Theme, deps Deps, override awsclient.Override) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: theme.Glyphs.SpinnerDots, FPS: 10}

	cfgTI := textinput.New()
	cfgTI.Placeholder = "~/.aws/config 경로 (비우면 기본 위치)"
	cfgTI.CharLimit = 512

	credsTI := textinput.New()
	credsTI.Placeholder = "~/.aws/credentials 경로 (비우면 config와 같은 위치)"
	credsTI.CharLimit = 512

	filterTI := textinput.New()
	filterTI.Prompt = "/ "
	filterTI.Placeholder = "타입, 이름, ID, 리전, 상태, 필드 검색"
	filterTI.CharLimit = 256

	m := Model{
		theme:       theme,
		keys:        defaultKeys(),
		deps:        deps,
		override:    override,
		configInput: cfgTI,
		credsInput:  credsTI,
		filterInput: filterTI,
		detail:      viewport.New(0, 0),
		spinner:     sp,
	}

	return m.loadProfiles(override)
}

// loadProfiles는 주어진 경로로 프로필을 읽고 그 결과에 따라 화면을 정한다.
//
// 성공하면 프로필 선택 화면, 설정을 못 찾으면 경로 입력 화면으로 간다. 경로 입력 화면에서
// 다시 이 함수를 호출하므로, 경로를 바꿔가며 재시도할 수 있다.
func (m Model) loadProfiles(override awsclient.Override) Model {
	m.override = override

	profiles, loc, err := m.deps.LoadProfiles(override)
	m.locations = loc

	if err != nil {
		// 설정을 못 찾았거나 읽지 못했다. 경로 입력 화면으로 보낸다.
		m.errText = m.deps.Explain(err)

		return m.enterConfigPath()
	}

	m.profiles = profiles
	m.profileTable = buildProfileTable(m.theme, profiles, m.width, m.listHeight())
	m.screen = ScreenProfile

	return m
}

// enterConfigPath는 경로 입력 화면으로 전환하고 config 입력에 커서를 둔다.
//
// 설정을 못 찾았을 때(loadProfiles 실패)와, 프로필 화면에서 사용자가 c로 다른 경로를
// 쓰려고 진입할 때 모두 이 함수를 쓴다. 두 입력 칸에는 지금 쓰고 있는 경로를 미리 채워
// 넣어, 한 쪽만 바꾸고 싶을 때 나머지를 다시 타이핑하지 않아도 되게 한다.
func (m Model) enterConfigPath() Model {
	m.configInput.SetValue(m.override.ConfigPath)
	m.credsInput.SetValue(m.override.CredentialsPath)
	m.pathFocus = 0
	m.configInput.Focus()
	m.credsInput.Blur()
	m.screen = ScreenConfigPath

	return m
}

// Init은 스피너를 시작한다.
func (m Model) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update는 상태 전이만 담당한다. 부수효과는 tea.Cmd로 내보낸다(원칙 7).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg), nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case identityMsg:
		return m.onIdentity(msg)

	case collectDoneMsg:
		return m.onCollectDone(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)

		return m, cmd
	}

	return m.delegateToActiveList(msg)
}

// View는 순수 렌더링이다. 상태를 바꾸지 않고 문자열만 만든다(원칙 7).
func (m Model) View() string {
	switch m.screen {
	case ScreenConfigPath:
		return m.configPathView()
	case ScreenProfile:
		return m.screenWithHelp("프로필 선택", m.profileTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "선택"},
			[2]string{"c", "AWS 설정 파일 경로 지정"},
		)
	case ScreenIdentity:
		return m.centered(m.spinner.View() + " " + m.loading)
	case ScreenRegion:
		return m.screenWithHelp("리전 선택", m.regionTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "이 리전 조회"},
			[2]string{"space", "여러 개 선택"},
		)
	case ScreenResourceType:
		return m.screenWithHelp("리소스 선택", m.typeTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "세부 항목"},
			[2]string{"space", "그룹 전체 조회"},
		)
	case ScreenResourceItem:
		return m.screenWithHelp(m.resourceItemTitle(), m.itemTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "이 항목 조회"},
			[2]string{"space", "여러 개 선택"},
		)
	case ScreenCollecting:
		return m.centered(m.spinner.View() + " " + m.loading + "\n\n" + m.theme.Faint.Render("esc: 취소"))
	case ScreenList:
		return m.resourceListView()
	case ScreenResourceKind:
		return m.screenWithHelp("종류 필터", m.kindTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "적용"},
		)
	case ScreenDetail:
		return m.detail.View() + m.helpBar(
			[2]string{"↑↓/jk", "스크롤"},
		)
	case ScreenCollectErrors:
		return m.screenWithHelp("수집 오류", m.errorTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "원본 상세"},
		)
	case ScreenCollectErrorDetail:
		return m.detail.View() + m.helpBar(
			[2]string{"↑↓/jk", "스크롤"},
		)
	case ScreenError:
		return m.errorView()
	default:
		return ""
	}
}

// resourceListView는 리소스 목록과 독립된 필터 한 줄을 렌더링한다.
//
// 필터 줄을 항상 한 줄 예약해 입력 시작·적용·취소 때 테이블 높이가 흔들리지 않게 한다.
func (m Model) resourceListView() string {
	filterLine := m.theme.Faint.Render("/ 필터")
	help := [][2]string{
		{"↑↓/jk", "이동"},
		{"enter/→", "상세"},
		{"/", "검색"},
		{"p", "프로필 전환"},
		{"r", "리전 전환"},
	}

	if len(m.collectErrors) > 0 {
		help = append([][2]string{{"e", "오류 보기"}}, help...)
	}

	lines := []string{
		m.breadcrumb(),
		m.theme.Title.Render(m.listCaption),
	}
	if m.hasResourceKindFilter() {
		kind := m.resourceKindFilterLabel()
		kindLine := m.theme.Faint.Render("종류: ") + m.theme.Title.Render(kind) +
			m.theme.Faint.Render("  t: 변경")
		if m.resourceKindFilter != "" && m.filterQuery == "" {
			kindLine += "  " + m.theme.Faint.Render(
				fmt.Sprintf("결과 %d/%d개", m.resourceList.filteredCount(), m.resourceList.totalCount()))
		}
		lines = append(lines, kindLine)
		help = append([][2]string{{"t", "종류 필터"}}, help...)
	}

	if m.filtering {
		filterLine = m.filterInput.View()
		help = [][2]string{
			{"입력", "실시간 검색"},
			{"enter", "적용"},
			{"esc", "취소"},
		}
	} else if m.filterQuery != "" {
		filterLine = fmt.Sprintf("/ %s  %s", m.filterQuery,
			m.theme.Faint.Render(fmt.Sprintf("결과 %d/%d개",
				m.resourceList.filteredCount(), m.resourceList.totalCount())))
	}
	lines = append(lines, filterLine)

	return strings.Join(lines, "\n") + "\n" + m.resourceList.View() + m.helpBar(help...)
}

func (m Model) hasResourceKindFilter() bool {
	return len(m.resourceKinds) > 1
}

func (m Model) resourceKindFilterLabel() string {
	if m.resourceKindFilter == "" {
		return "전체"
	}
	for _, kind := range m.resourceKinds {
		if kind.ID == m.resourceKindFilter {
			return kind.Label
		}
	}

	return m.resourceKindFilter
}

// Screen은 현재 화면을 노출한다. 테스트에서 상태 전이를 확인하는 데 쓴다.
func (m Model) Screen() Screen {
	return m.screen
}

func (m Model) resize(msg tea.WindowSizeMsg) Model {
	m.width = msg.Width
	m.height = msg.Height

	h := m.listHeight()

	// 각 리스트는 흐름이 진행되어야 생긴다. 아직 아이템이 없는 제로값 리스트에 SetSize를
	// 부르면 내부 페이지네이션 계산에서 패닉이 난다(설정을 못 찾아 경로 입력 화면으로
	// 시작하면 profileList조차 없다). 각 리스트는 만들어질 때 현재 창 크기로 초기화하고,
	// 여기서는 살아 있는 것만 갱신한다.
	m.configInput.Width = max(1, msg.Width-4)
	m.credsInput.Width = max(1, msg.Width-4)
	m.filterInput.Width = max(1, msg.Width-4)

	// 살아 있는 테이블만 현재 창 크기로 다시 만든다. 아직 안 만들어진 테이블은 제로값이라
	// Rows()가 비어 있다.
	if len(m.profiles) > 0 {
		m.profileTable = buildProfileTable(m.theme, m.profiles, msg.Width, h)
	}

	if len(m.regions) > 0 {
		cursor := m.regionTable.Cursor()
		m.regionTable = buildRegionTable(m.theme, m.regions, m.chosenRegions, msg.Width, h)
		m.regionTable.SetCursor(cursor)
	}

	if len(m.deps.ResourceGroups) > 0 && len(m.typeTable.Rows()) > 0 {
		cursor := m.typeTable.Cursor()
		m.typeTable = buildTypeTable(m.theme, m.deps.ResourceGroups, m.chosenTypes, msg.Width, h)
		m.typeTable.SetCursor(cursor)
	}

	if len(m.itemTable.Rows()) > 0 {
		cursor := m.itemTable.Cursor()
		m.itemTable = buildResourceItemTable(m.theme, m.currentResourceGroup(), m.chosenTypes, msg.Width, h)
		m.itemTable.SetCursor(cursor)
	}

	if m.hasResourceKindFilter() && len(m.kindTable.Rows()) > 0 {
		cursor := m.kindTable.Cursor()
		m.kindTable = buildResourceKindTable(m.theme, m.resourceKinds, msg.Width, h)
		m.kindTable.SetCursor(cursor)
	}

	if len(m.collectErrors) > 0 && len(m.errorTable.Rows()) > 0 {
		cursor := m.errorTable.Cursor()
		m.errorTable = buildCollectErrorTable(
			m.theme, m.collectErrors, m.deps.ResourceGroups, msg.Width, h)
		m.errorTable.SetCursor(cursor)
	}

	m.resourceList.resize(msg.Width, m.resourceListHeight())

	m.detail.Width = msg.Width
	m.detail.Height = h

	return m
}

func (m Model) delegateToActiveList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.screen {
	case ScreenProfile:
		m.profileTable, cmd = m.profileTable.Update(msg)
	case ScreenRegion:
		m.regionTable, cmd = m.regionTable.Update(msg)
	case ScreenResourceType:
		m.typeTable, cmd = m.typeTable.Update(msg)
	case ScreenResourceItem:
		m.itemTable, cmd = m.itemTable.Update(msg)
	case ScreenResourceKind:
		m.kindTable, cmd = m.kindTable.Update(msg)
	case ScreenCollectErrors:
		m.errorTable, cmd = m.errorTable.Update(msg)
	case ScreenList:
		// 리소스 목록 이동은 전역 커서와 캐시 window를 함께 갱신하므로 위젯에 위임하지 않는다.
	case ScreenDetail, ScreenCollectErrorDetail:
		m.detail, cmd = m.detail.Update(msg)
	case ScreenConfigPath, ScreenIdentity, ScreenCollecting, ScreenError:
		// 로딩·에러·경로입력 화면은 테이블에 위임할 것이 없다.
	}

	return m, cmd
}

func (m Model) centered(s string) string {
	if m.width == 0 || m.height == 0 {
		return s
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, s)
}

// configPathView는 config/credentials 경로를 각각 입력받는 화면이다.
//
// 설정을 못 찾아 여기서 시작했을 수도 있고, 프로필 화면에서 c로 들어왔을 수도 있다.
// 두 경우의 제목과 하단 안내를 다르게 보여준다(돌아갈 프로필이 있으면 esc가 종료가 아니라
// 뒤로다).
func (m Model) configPathView() string {
	title := "AWS 설정 파일 경로 지정"
	if len(m.profiles) == 0 {
		title = "AWS 설정을 찾을 수 없습니다"
	}

	lines := []string{m.theme.Title.Render(title), ""}

	if m.errText != "" {
		lines = append(lines, m.theme.Faint.Render(m.errText), "")
	}

	if m.locations.Config.Path != "" {
		lines = append(lines, m.theme.Faint.Render("현재 위치: "+m.locations.Config.Path), "")
	}

	// 커서가 있는 입력 칸의 라벨에 표시를 붙여 어디를 타이핑 중인지 보이게 한다.
	lines = append(lines,
		m.pathLabel("config 경로", 0),
		m.configInput.View(),
		"",
		m.pathLabel("credentials 경로", 1),
		m.credsInput.View(),
		"",
	)

	if len(m.profiles) > 0 {
		lines = append(lines, m.theme.Faint.Render("tab: 칸 이동   enter: 적용   esc: 뒤로"))
	} else {
		lines = append(lines, m.theme.Faint.Render("tab: 칸 이동   enter: 다시 시도   esc/q: 종료"))
	}

	return m.centered(strings.Join(lines, "\n"))
}

// pathLabel은 입력 칸 라벨을 만든다. 커서가 있는 칸이면 앞에 화살표를 붙여 강조한다.
func (m Model) pathLabel(text string, focus int) string {
	if m.pathFocus == focus {
		return m.theme.Selected.Render(m.theme.Glyphs.Selected + " " + text)
	}

	return "  " + text
}

func (m Model) errorView() string {
	body := m.theme.Error.Render("오류") + "\n\n" + m.errText +
		"\n\n" + m.theme.Faint.Render("esc: 뒤로  q: 종료")

	return m.centered(body)
}

// listHeight는 하단 도움말 자리를 뺀 리스트 높이를 반환한다.
//
// helpBar가 빈 줄 1개 + 안내 1줄을 차지하므로 그만큼 리스트를 줄인다. 계산을 한곳에
// 모아, 리스트를 만드는 여러 지점(리사이즈, 리전·타입·리소스 생성)이 같은 값을 쓰게 한다.
func (m Model) listHeight() int {
	// 경로 헤더 1줄 + 화면 제목 1줄 + 도움말(빈 줄 1 + 안내 1)2줄 = 4줄을 뺀다.
	const chromeLines = 4

	h := m.height - chromeLines
	if h < 1 {
		h = 1
	}

	return h
}

// resourceListHeight는 독립된 필터 한 줄을 제외한 리소스 테이블 높이를 반환한다.
func (m Model) resourceListHeight() int {
	height := m.listHeight() - 1 // 검색 줄
	if m.hasResourceKindFilter() {
		height-- // 종류 필터 줄
	}

	return max(1, height)
}

// shouldShowRegion은 여러 리전을 함께 조회할 때만 행별 리전 열을 표시한다.
func (m Model) shouldShowRegion() bool {
	return len(m.chosenRegions) > 1
}

// helpBar는 화면 하단에 그릴 한국어 키 안내를 만든다.
//
// 각 항목을 "키 = 뜻"으로 또렷하게 보여준다. 화면마다 다른 것은 앞쪽 동작 키뿐이고,
// 뒤로·종료는 모든 화면에서 같은 자리에 같은 문구로 붙는다. 같은 키가 화면마다 다른
// 이름으로 보이면 사용자가 매 화면에서 도움말을 다시 읽어야 한다.
func (m Model) helpBar(pairs ...[2]string) string {
	pairs = append(pairs,
		[2]string{"esc/←", "뒤로"},
		[2]string{"q", "종료"},
	)

	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, m.theme.Title.Render(p[0])+" "+m.theme.Faint.Render(p[1]))
	}

	return "\n" + strings.Join(parts, m.theme.Faint.Render("   "))
}

// screenWithHelp는 한 화면을 조립한다: 상단 경로 헤더 + 화면 제목 + 테이블 + 하단 도움말.
//
// 경로 헤더는 taws처럼 지금 어디를 보고 있는지(프로필/리전/리소스)를 항상 위에 고정해
// 보여준다. 흐름이 깊어져도 사용자가 맥락을 잃지 않게 한다.
func (m Model) screenWithHelp(caption string, t table.Model, pairs ...[2]string) string {
	header := m.breadcrumb()
	title := m.theme.Title.Render(caption)

	return header + "\n" + title + "\n" + t.View() + m.helpBar(pairs...)
}

// breadcrumb은 상단에 고정으로 그리는 경로 헤더다. 현재 프로필·리전·리소스 그룹을 보여준다.
//
// 아직 안 고른 항목은 "-"로 둔다. 계정 ID가 확인됐으면 프로필 옆에 함께 보여준다.
func (m Model) breadcrumb() string {
	profile := orDashUI(m.chosenProfile)
	if m.identity.AccountID != "" {
		profile += " (" + m.identity.AccountID + ")"
	}

	region := "-"
	if len(m.chosenRegions) > 0 {
		region = strings.Join(m.chosenRegions, ",")
	}

	label := func(k, v string) string {
		return m.theme.Faint.Render(k+": ") + m.theme.Title.Render(v)
	}

	depth := m.breadcrumbDepth()
	parts := []string{label("프로필", profile)}

	if depth >= breadcrumbRegion {
		parts = append(parts, label("리전", region))
	}
	if depth >= breadcrumbGroup {
		parts = append(parts, label("리소스", orDashUI(m.breadcrumbGroups())))
	}

	// 세부 항목은 고른 것이 있을 때만 붙인다. 항상 "-"로 자리를 차지하면 좁은 터미널에서
	// 프로필·리전이 먼저 밀린다.
	if items := m.breadcrumbItems(); depth >= breadcrumbItem && items != "" {
		parts = append(parts, label("세부 항목", items))
	}

	return strings.Join(parts, m.theme.Faint.Render("   "))
}

// 경로 헤더에 보여줄 단계. 뒤 단계는 앞 단계를 모두 포함한다.
const (
	breadcrumbProfile = iota + 1
	breadcrumbRegion
	breadcrumbGroup
	breadcrumbItem
)

// breadcrumbDepth는 현재 화면까지 확정된 경로 단계를 반환한다.
//
// 뒤로 나온 화면에서 아직 고르지 않은 뒷단계 값을 계속 보여주면, 지금 어디를 고치고 있는지
// 헷갈린다. 리전 화면에서는 리전까지만 보여준다.
func (m Model) breadcrumbDepth() int {
	switch m.screen {
	case ScreenConfigPath, ScreenProfile, ScreenIdentity, ScreenError:
		return breadcrumbProfile
	case ScreenRegion:
		return breadcrumbRegion
	case ScreenResourceType:
		return breadcrumbGroup
	default:
		// 수집 중과 그 이후 화면은 선택이 모두 확정된 상태다.
		return breadcrumbItem
	}
}

// breadcrumbGroups는 경로 헤더에 보여줄 리소스 그룹 이름을 만든다.
//
// 그룹 전체를 고른 경우만이 아니라 세부 항목 하나만 고른 경우에도 그룹을 보여준다.
// 아직 아무것도 고르지 않았으면 지금 보고 있는 그룹을 쓴다.
func (m Model) breadcrumbGroups() string {
	var labels []string

	for _, group := range m.deps.ResourceGroups {
		for _, resourceType := range group.Types {
			if slices.Contains(m.chosenTypes, resourceType.ID) {
				labels = append(labels, group.Label)

				break
			}
		}
	}

	if len(labels) == 0 && m.screen == ScreenResourceItem {
		return m.currentResourceGroup().Label
	}

	return strings.Join(labels, ",")
}

// breadcrumbItems는 경로 헤더에 보여줄 세부 항목 이름을 만든다.
//
// 그룹의 모든 항목을 고른 경우에는 항목을 늘어놓지 않는다. 그룹 이름이 이미 같은 정보를
// 담고 있어 줄만 길어진다.
func (m Model) breadcrumbItems() string {
	var labels []string

	for _, group := range m.deps.ResourceGroups {
		chosen := make([]string, 0, len(group.Types))
		for _, resourceType := range group.Types {
			if slices.Contains(m.chosenTypes, resourceType.ID) {
				chosen = append(chosen, resourceType.Label)
			}
		}
		if len(chosen) > 0 && len(chosen) < len(group.Types) {
			labels = append(labels, chosen...)
		}
	}

	return strings.Join(labels, ",")
}

// currentResourceGroup은 세부 항목 화면이 보여주는 그룹을 반환한다.
//
// 인덱스가 범위를 벗어나면 빈 그룹을 반환한다. 그룹 목록이 비어 있어도 렌더링이 깨지지
// 않아야 한다.
func (m Model) currentResourceGroup() ResourceGroup {
	if m.itemGroup < 0 || m.itemGroup >= len(m.deps.ResourceGroups) {
		return ResourceGroup{}
	}

	return m.deps.ResourceGroups[m.itemGroup]
}

// resourceItemTitle은 세부 항목 화면의 제목을 만든다.
func (m Model) resourceItemTitle() string {
	group := m.currentResourceGroup()
	if group.Label == "" {
		return "세부 항목 선택"
	}

	return group.Label + " 세부 항목"
}

// 상세 화면 렌더링은 detail.go에 있다.
