package tui

import (
	"context"
	"fmt"
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
	ScreenResourceType // 조회할 리소스 타입 선택
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

	// 일반 선택 테이블은 위젯 커서로 원본을 찾는다. 리소스 테이블은 대용량 결과 전체를
	// 위젯에 넣지 않고 filteredIndexes의 전역 커서와 화면 높이만큼의 행 캐시를 따로 둔다.
	profileTable table.Model
	regionTable  table.Model
	typeTable    table.Model
	kindTable    table.Model
	errorTable   table.Model
	detail       viewport.Model
	spinner      spinner.Model

	resourceTable       table.Model
	resources           []model.Resource
	resourceData        resourceTableData
	filteredIndexes     []int
	visibleResourceRows []table.Row
	resourceCursor      int
	resourceWindowStart int
	// resourceTableHeight는 마지막으로 위젯에 적용한 헤더 포함 전체 높이다.
	resourceTableHeight int
	listCaption         string
	filterInput         textinput.Model
	filtering           bool
	filterQuery         string
	previousFilter      string
	resourceKinds       []resourceKind
	resourceKindFilter  string
	collectErrors       []model.CollectError
	showRegion          bool

	chosenProfile string
	identity      awsclient.Identity
	regions       []awsclient.Region
	chosenRegions []string
	chosenTypes   []string

	// replace...OnEnter는 목록 화면에서 리전/타입을 바꾸러 들어왔을 때 기존 선택보다
	// 현재 커서 행을 우선하도록 한다. space로 다중 선택을 조작하면 false로 바뀐다.
	replaceRegionOnEnter bool
	replaceTypeOnEnter   bool

	// explicitTypeSelection은 chosenTypes가 space로 명시적으로 체크된 목록인지 나타낸다.
	// false면 chosenTypes에 이전 Enter 선택이 남아 있어도 현재 커서 타입으로 교체한다.
	explicitTypeSelection bool

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
		SwitchRegion:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "리전 전환")),
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
			[2]string{"q", "종료"},
		)
	case ScreenIdentity:
		return m.centered(m.spinner.View() + " " + m.loading)
	case ScreenRegion:
		return m.screenWithHelp("리전 선택", m.regionTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"space", "선택"},
			[2]string{"enter/→", "다음"},
			[2]string{"esc/←", "뒤로"},
		)
	case ScreenResourceType:
		return m.screenWithHelp("리소스 선택", m.typeTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"space", "여러 개 선택"},
			[2]string{"enter/→", "이 리소스 조회"},
			[2]string{"esc/←", "뒤로"},
		)
	case ScreenCollecting:
		return m.centered(m.spinner.View() + " " + m.loading + "\n\n" + m.theme.Faint.Render("esc: 취소"))
	case ScreenList:
		return m.resourceListView()
	case ScreenResourceKind:
		return m.screenWithHelp("종류 필터", m.kindTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "적용"},
			[2]string{"esc/←", "취소"},
		)
	case ScreenDetail:
		return m.detail.View() + m.helpBar(
			[2]string{"↑↓/jk", "스크롤"},
			[2]string{"esc/←/q", "목록으로"},
		)
	case ScreenCollectErrors:
		return m.screenWithHelp("수집 오류", m.errorTable,
			[2]string{"↑↓/jk", "이동"},
			[2]string{"enter/→", "원본 상세"},
			[2]string{"esc/←", "리소스 목록"},
		)
	case ScreenCollectErrorDetail:
		return m.detail.View() + m.helpBar(
			[2]string{"↑↓/jk", "스크롤"},
			[2]string{"esc/←/q", "오류 목록"},
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
		{"R", "리전 전환"},
		{"esc", "뒤로"},
		{"q", "종료"},
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
				fmt.Sprintf("결과 %d/%d개", len(m.filteredIndexes), len(m.resources)))
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
			m.theme.Faint.Render(fmt.Sprintf("결과 %d/%d개", len(m.filteredIndexes), len(m.resources))))
	}
	lines = append(lines, filterLine)

	return strings.Join(lines, "\n") + "\n" + m.resourceTable.View() + m.helpBar(help...)
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

	if len(m.resourceData.titles) > 0 || m.screen == ScreenList || m.screen == ScreenResourceKind {
		columns := layoutResourceColumns(
			m.resourceData.titles, m.resourceData.preferredWidths, msg.Width)
		if !sameTableColumns(m.resourceTable.Columns(), columns) {
			m.resourceTable.SetColumns(columns)
		}
		height := m.resourceListHeight()
		if m.resourceTableHeight != height {
			m.resourceTable.SetHeight(height)
			m.resourceTableHeight = height
			m.syncResourceTableWindow()
		}
	}

	m.detail.Width = msg.Width
	m.detail.Height = h

	return m
}

func (m *Model) syncResourceTableWindow() {
	count := len(m.filteredIndexes)
	if count == 0 {
		m.resourceCursor = 0
		m.resourceWindowStart = 0
		m.visibleResourceRows = m.visibleResourceRows[:0]
		m.resourceTable.SetRows(m.visibleResourceRows)

		return
	}

	m.resourceCursor = min(max(m.resourceCursor, 0), count-1)
	windowSize := min(max(0, m.resourceTableHeight-1), count)
	if windowSize == 0 {
		m.resourceWindowStart = m.resourceCursor
		m.visibleResourceRows = m.visibleResourceRows[:0]
		m.resourceTable.SetRows(m.visibleResourceRows)

		return
	}

	previousStart := m.resourceWindowStart
	m.resourceWindowStart = min(max(m.resourceWindowStart, 0), count-windowSize)
	if m.resourceCursor < m.resourceWindowStart {
		m.resourceWindowStart = m.resourceCursor
	} else if m.resourceCursor >= m.resourceWindowStart+windowSize {
		m.resourceWindowStart = m.resourceCursor - windowSize + 1
	}
	m.resourceWindowStart = min(max(m.resourceWindowStart, 0), count-windowSize)

	if previousStart != m.resourceWindowStart || len(m.visibleResourceRows) != windowSize {
		m.visibleResourceRows = m.visibleResourceRows[:0]
		end := m.resourceWindowStart + windowSize
		for _, resourceIndex := range m.filteredIndexes[m.resourceWindowStart:end] {
			if resourceIndex >= 0 && resourceIndex < len(m.resourceData.rows) {
				m.visibleResourceRows = append(m.visibleResourceRows, m.resourceData.rows[resourceIndex])
			} else {
				m.visibleResourceRows = append(m.visibleResourceRows, nil)
			}
		}
		m.resourceTable.SetRows(m.visibleResourceRows)
	}

	localCursor := m.resourceCursor - m.resourceWindowStart
	if m.resourceTable.Cursor() != localCursor {
		m.resourceTable.SetCursor(localCursor)
	}
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
// 각 항목을 "키 = 뜻"으로 또렷하게 보여준다. 검색이 / 라는 것도 여기서 분명히 알린다.
func (m Model) helpBar(pairs ...[2]string) string {
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

	resource := "-"
	if labels := selectedResourceGroupLabels(m.deps.ResourceGroups, m.chosenTypes); len(labels) > 0 {
		resource = strings.Join(labels, ",")
	}

	label := func(k, v string) string {
		return m.theme.Faint.Render(k+": ") + m.theme.Title.Render(v)
	}

	parts := []string{
		label("프로필", profile),
		label("리전", region),
		label("리소스", resource),
	}

	return strings.Join(parts, m.theme.Faint.Render("   "))
}

func selectedResourceGroupLabels(groups []ResourceGroup, chosenTypes []string) []string {
	var labels []string
	for _, group := range groups {
		if resourceGroupSelected(group, chosenTypes) {
			labels = append(labels, group.Label)
		}
	}

	return labels
}

func renderCollectErrorDetail(
	theme Theme,
	groups []ResourceGroup,
	collectErr model.CollectError,
) string {
	explanation := orDashUI(collectErr.Explanation)

	return strings.Join([]string{
		theme.Error.Render("수집 오류"),
		"",
		fmt.Sprintf("%-16s %s", "리소스 종류", resourceTypeLabel(groups, collectErr.Type)),
		fmt.Sprintf("%-16s %s", "타입 ID", collectErr.Type),
		fmt.Sprintf("%-16s %s", "프로필", orDashUI(collectErr.Profile)),
		fmt.Sprintf("%-16s %s", "리전", orDashUI(collectErr.Region)),
		fmt.Sprintf("%-16s %s", "AWS 오류 코드", orDashUI(collectErr.Code)),
		"",
		theme.Title.Render("설명"),
		explanation,
		"",
		theme.Title.Render("원본 오류"),
		collectErr.Message,
	}, "\n")
}

// renderDetail은 리소스 상세를 문자열로 만든다. Fields는 순서 있는 슬라이스이므로
// 렌더링할 때마다 같은 순서로 나온다.
func renderDetail(theme Theme, res model.Resource) string {
	lines := []string{
		theme.Title.Render(res.DisplayName()),
		theme.Faint.Render(res.Type + "  " + res.Region),
		"",
	}

	for _, f := range res.Fields {
		lines = append(lines, fmt.Sprintf("%-16s %s", f.Key, f.Value))
	}

	if len(res.Related) > 0 {
		lines = append(lines, "", theme.Title.Render("관계"))

		for _, ref := range res.Related {
			via := ""
			if ref.Via != "" {
				via = "  (" + ref.Via + ")"
			}

			lines = append(lines, fmt.Sprintf("%s %s %s%s",
				theme.Glyphs.TreeBranch, ref.Relation, ref.ID, via))
		}
	}

	return strings.Join(lines, "\n")
}
