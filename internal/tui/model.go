package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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
	"github.com/cnlgks1/cloudloupe/internal/graph"
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
	ScreenResource // 조회할 서비스와 resource type 선택 (한 화면 트리)
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

	// Identify는 프로필 탐색에 사용한 경로로 신원을 확인한다(STS GetCallerIdentity).
	Identify func(
		ctx context.Context,
		profile, region string,
		locations awsclient.Locations,
	) (awsclient.Identity, error)

	// Collect는 같은 설정 경로로 선택 프로필의 지정 타입을 조회한다. types가 비면 전부.
	Collect func(
		ctx context.Context,
		profile string,
		regions, types []string,
		locations awsclient.Locations,
	) collect.Result

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
	kindTable    table.Model
	errorTable   table.Model
	detail       viewport.Model
	spinner      spinner.Model

	resourceList resourceListModel
	listCaption  string

	// relations는 조회 결과의 관계 그래프다. 상세 화면이 대상 이름과 역방향을 여기서
	// 얻는다. 수집기가 남긴 원본 Ref는 대상 ID만 있고 이름도, 나를 가리키는 관계도 없다.
	//
	// 조회가 끝난 뒤 백그라운드에서 한 번 빌드한다. 상세를 열 때마다 만들지 않는다.
	relations *graph.Graph

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

	// resourceTree는 서비스와 resource type을 한 화면에서 고르는 트리다. 펼침과 선택
	// 상태를 소유한다.
	resourceTree resourceTree

	// treeFiltering은 선택 화면에서 검색어를 입력하는 중인지 나타낸다.
	treeFiltering bool

	// replaceRegionOnEnter는 목록 화면에서 리전을 바꾸러 들어왔을 때 기존 선택보다
	// 현재 커서 행을 우선하도록 한다. space로 다중 선택을 조작하면 false로 바뀐다.
	replaceRegionOnEnter bool

	// explicitRegionSelection은 space로 명시적으로 체크한 다중 선택인지 나타낸다.
	// false면 이전 Enter 선택이 남아 있어도 현재 커서 항목으로 교체한다.
	explicitRegionSelection bool

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
		Enter:         key.NewBinding(key.WithKeys("enter", "right"), key.WithHelp("enter/→", "select")),
		Back:          key.NewBinding(key.WithKeys("esc", "left"), key.WithHelp("esc/←", "back")),
		Toggle:        key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
		Quit:          key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		SwitchProfile: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "switch profile")),
		SwitchRegion:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "switch region")),
		FilterKind:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "Filter by kind")),
		ShowErrors:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "errors")),
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
	cfgTI.Placeholder = "~/.aws/config path (empty for default)"
	cfgTI.CharLimit = 512

	credsTI := textinput.New()
	credsTI.Placeholder = "~/.aws/credentials path (empty to match config)"
	credsTI.CharLimit = 512

	filterTI := textinput.New()
	filterTI.Prompt = "/ "
	filterTI.Placeholder = "search type, name, ID, region, status, fields"
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
	profiles, loc, err := m.deps.LoadProfiles(override)
	if err != nil {
		// 실패한 새 경로가 이미 성공한 프로필 목록의 실행 경로를 덮으면 안 된다.
		// 입력값은 남겨 사용자가 고칠 수 있게 하고, 확정된 override와 locations는 보존한다.
		m.errText = m.deps.Explain(err)

		return m.enterConfigPathWith(override)
	}

	// 프로필 목록과 실제 연결 경로는 성공했을 때만 함께 확정한다.
	m.override = override
	m.locations = loc
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
	return m.enterConfigPathWith(m.override)
}

func (m Model) enterConfigPathWith(override awsclient.Override) Model {
	m.configInput.SetValue(override.ConfigPath)
	m.credsInput.SetValue(override.CredentialsPath)
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
		return m.screenWithHelp("Select profile", m.profileTable,
			[2]string{"↑↓/jk", "move"},
			[2]string{"enter/→", "select"},
			[2]string{"c", "Set AWS config paths"},
		)
	case ScreenIdentity:
		return m.centered(m.spinner.View() + " " + m.loading)
	case ScreenRegion:
		return m.screenWithHelp("Select region", m.regionTable,
			[2]string{"↑↓/jk", "move"},
			[2]string{"enter/→", "query this region"},
			[2]string{"space", "multi-select"},
		)
	case ScreenResource:
		return m.resourceTreeView()
	case ScreenCollecting:
		return m.centered(m.spinner.View() + " " + m.loading + "\n\n" + m.theme.Faint.Render("esc: cancel"))
	case ScreenList:
		return m.resourceListView()
	case ScreenResourceKind:
		return m.screenWithHelp("Filter by kind", m.kindTable,
			[2]string{"↑↓/jk", "move"},
			[2]string{"enter/→", "apply"},
		)
	case ScreenDetail:
		return m.detailView()
	case ScreenCollectErrors:
		return m.screenWithHelp("Collect errors", m.errorTable,
			[2]string{"↑↓/jk", "move"},
			[2]string{"enter/→", "raw detail"},
		)
	case ScreenCollectErrorDetail:
		return m.detailView()
	case ScreenError:
		return m.errorView()
	default:
		return ""
	}
}

// resourceTreeView는 서비스·resource type 선택 화면을 렌더링한다.
//
// 상단에 규모를 고정으로 보여준다. 접힌 서비스 안의 선택은 화면에 보이지 않으므로, 지금
// 몇 종을 고른 상태이고 조회가 몇 번의 요청이 되는지 숫자로 드러내야 한다. 이것이 없으면
// 리소스가 늘어날수록 의도보다 큰 조회를 실수로 시작하게 된다.
func (m Model) resourceTreeView() string {
	services, types, selected, shown := m.resourceTree.counts()

	scale := plural(services, "service") + "   " + plural(types, "resource type")
	if selected > 0 {
		scale += "   " + strconv.Itoa(selected) + " selected"

		// 리전이 여러 개면 요청 수가 곱해진다. 조회 규모를 누르기 전에 보여주는 것이
		// 실수로 시작한 대량 조회를 막는 유일한 장치다.
		if regions := len(m.chosenRegions); regions > 1 {
			scale += " × " + plural(regions, "region") +
				" = " + plural(selected*regions, "query")
		}
	}

	filterLine := m.theme.Faint.Render("/ search")
	help := [][2]string{
		{"↑↓/jk", "move"},
		{"→", "expand"},
		{"←", "collapse"},
		{"enter", "query"},
		{"space", "select"},
		{"z", "collapse all"},
		{"/", "search"},
	}

	switch {
	case m.treeFiltering:
		filterLine = m.filterInput.View()
		help = [][2]string{
			{"type", "live search"},
			{"enter", "apply"},
			{"esc", "cancel"},
		}
	case m.resourceTree.query != "":
		filterLine = "/ " + m.resourceTree.query + "  " +
			m.theme.Faint.Render(strconv.Itoa(shown)+"/"+strconv.Itoa(types)+" shown")
		help = [][2]string{
			{"↑↓/jk", "move"},
			{"enter", "query"},
			{"space", "select"},
			{"a", "select all shown"},
			{"/", "search"},
		}
	}

	if selected > 0 {
		help = append(help, [2]string{"x", "clear selection"})
	}

	return m.breadcrumb() + "\n" +
		m.theme.Title.Render("Select resource type") + "   " + m.theme.Faint.Render(scale) + "\n" +
		filterLine + "\n" +
		m.resourceTree.View() + m.helpBar(help...)
}

// plural은 개수와 단위를 붙인다. 영어 복수형은 s를 붙이는 규칙만 쓴다.
//
// "1 regions = 1 queries"처럼 보이면 도구가 성의 없어 보인다. query처럼 y로 끝나는 단어는
// ies가 되므로 그것만 따로 다룬다.
func plural(count int, unit string) string {
	if count == 1 {
		return strconv.Itoa(count) + " " + unit
	}

	if strings.HasSuffix(unit, "y") {
		return strconv.Itoa(count) + " " + strings.TrimSuffix(unit, "y") + "ies"
	}

	return strconv.Itoa(count) + " " + unit + "s"
}

// resourceListView는 리소스 목록과 독립된 필터 한 줄을 렌더링한다.
//
// 필터 줄을 항상 한 줄 예약해 입력 시작·적용·취소 때 테이블 높이가 흔들리지 않게 한다.
func (m Model) resourceListView() string {
	filterLine := m.theme.Faint.Render("/ filter")
	help := [][2]string{
		{"↑↓/jk", "move"},
		{"enter/→", "details"},
		{"/", "search"},
		{"p", "switch profile"},
		{"r", "switch region"},
	}

	// 조회 결과가 아예 없으면 이동·상세·검색은 눌러도 아무 일도 일어나지 않는다. 안내에
	// 남겨두면 도구가 반응하지 않는 것처럼 보인다. 필터 줄은 높이를 유지하려고 비워만 둔다.
	if m.resourceList.totalCount() == 0 {
		filterLine = ""
		help = [][2]string{
			{"p", "switch profile"},
			{"r", "switch region"},
		}
	}

	if len(m.collectErrors) > 0 {
		help = append([][2]string{{"e", "errors"}}, help...)
	}

	lines := []string{
		m.breadcrumb(),
		m.theme.Title.Render(m.listCaption),
	}
	if m.hasResourceKindFilter() {
		kind := m.resourceKindFilterLabel()
		kindLine := m.theme.Faint.Render("Kind: ") + m.theme.Title.Render(kind) +
			m.theme.Faint.Render("  t: change")
		if m.resourceKindFilter != "" && m.filterQuery == "" {
			kindLine += "  " + m.theme.Faint.Render(
				fmt.Sprintf("%d/%d shown", m.resourceList.filteredCount(), m.resourceList.totalCount()))
		}
		lines = append(lines, kindLine)
		help = append([][2]string{{"t", "Filter by kind"}}, help...)
	}

	if m.filtering {
		filterLine = m.filterInput.View()
		help = [][2]string{
			{"type", "live search"},
			{"enter", "apply"},
			{"esc", "cancel"},
		}
	} else if m.filterQuery != "" {
		filterLine = fmt.Sprintf("/ %s  %s", m.filterQuery,
			m.theme.Faint.Render(fmt.Sprintf("%d/%d shown",
				m.resourceList.filteredCount(), m.resourceList.totalCount())))
	}
	lines = append(lines, filterLine)

	body := m.resourceList.View()
	if notice := m.emptyResultNotice(); notice != "" {
		body = notice
	}

	return strings.Join(lines, "\n") + "\n" + body + m.helpBar(help...)
}

// emptyResultNotice는 결과가 없을 때 그 이유를 설명하는 문구를 만든다.
//
// 빈 표만 보여주면 조회가 실패한 것인지 정말 리소스가 없는 것인지 구분할 수 없다. 그룹
// 선택 화면의 타입 수를 리소스 개수로 오해한 뒤 이 화면을 보면 더 헷갈린다.
//
// 결과가 없는 경우는 두 가지이고 사용자가 할 일이 다르다. 조회가 성공했는데 리소스가 없으면
// 다른 리전이나 리소스를 고르면 되고, 권한이나 스로틀링으로 실패했으면 오류를 봐야 한다.
// 그래서 부분 오류가 있으면 그쪽을 먼저 가리킨다.
//
// 필터 때문에 행이 없는 경우는 여기서 다루지 않는다. 그때는 이미 "0/62 shown"이 이유를
// 말해주고, 필터를 지우면 결과가 돌아온다.
func (m Model) emptyResultNotice() string {
	if m.resourceList.totalCount() > 0 || m.filterQuery != "" || m.filtering {
		return ""
	}

	// 세부 항목을 골랐으면 그 이름이 가장 구체적이다. 그룹 전체를 골랐으면 그룹 이름
	// 뒤에 resources를 붙여 문장이 되게 한다("No Auto Scaling resources").
	subject := "No resources"
	if items := m.breadcrumbItems(); items != "" {
		subject = "No " + items
	} else if group := m.breadcrumbGroups(); group != "" && group != "-" {
		subject = "No " + group + " resources"
	}

	region := "the selected region"
	if len(m.chosenRegions) > 0 {
		region = strings.Join(m.chosenRegions, ", ")
	}

	lines := []string{m.theme.Title.Render(subject + " in " + region + ".")}

	if len(m.collectErrors) > 0 {
		lines = append(lines,
			m.theme.Faint.Render("Some queries failed, so this may be incomplete. Press e to see why."))
	} else {
		lines = append(lines,
			m.theme.Faint.Render("The query succeeded and found nothing."),
			m.theme.Faint.Render("Press r for another region, or esc to pick another resource."))
	}

	return "\n  " + strings.Join(lines, "\n  ") + "\n"
}

func (m Model) hasResourceKindFilter() bool {
	return len(m.resourceKinds) > 1
}

func (m Model) resourceKindFilterLabel() string {
	if m.resourceKindFilter == "" {
		return "All"
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

	if len(m.deps.ResourceGroups) > 0 {
		m.resourceTree.resize(m.theme, msg.Width, h)
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
	m.detail.Height = m.detailHeight()

	return m
}

// detailHeight는 상세 viewport의 높이를 반환한다.
//
// 도움말을 viewport 밖 화면 맨 아래에 고정하려면 그 자리(빈 줄 1 + 안내 1)를 빼야 한다.
// 이 값을 빼지 않으면 viewport가 창을 꽉 채워, 스크롤 위치에 따라 도움말이 잘린 콘텐츠
// 바로 아래에 붙어 상세 정보와 겹쳐 보인다.
func (m Model) detailHeight() int {
	const helpLines = 2

	h := m.height - helpLines
	if h < 1 {
		h = 1
	}

	return h
}

func (m Model) delegateToActiveList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.screen {
	case ScreenProfile:
		m.profileTable, cmd = m.profileTable.Update(msg)
	case ScreenRegion:
		m.regionTable, cmd = m.regionTable.Update(msg)
	case ScreenResource:
		m.resourceTree.update(msg)
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
	title := "Set AWS config paths"
	if len(m.profiles) == 0 {
		title = "AWS config not found"
	}

	lines := []string{m.theme.Title.Render(title), ""}

	if m.errText != "" {
		lines = append(lines, m.theme.Faint.Render(m.errText), "")
	}

	if m.locations.Config.Path != "" {
		lines = append(lines, m.theme.Faint.Render("Current: "+m.locations.Config.Path), "")
	}

	// 커서가 있는 입력 칸의 라벨에 표시를 붙여 어디를 타이핑 중인지 보이게 한다.
	lines = append(lines,
		m.pathLabel("config path", 0),
		m.configInput.View(),
		"",
		m.pathLabel("credentials path", 1),
		m.credsInput.View(),
		"",
	)

	if len(m.profiles) > 0 {
		lines = append(lines, m.theme.Faint.Render("tab: next field   enter: apply   esc: back"))
	} else {
		lines = append(lines, m.theme.Faint.Render("tab: next field   enter: retry   esc/q: quit"))
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

// detailView는 상세·오류 상세 화면을 조립한다.
//
// viewport를 detailHeight()만큼 정확히 채우고 도움말을 그 아래 화면 맨 하단에 붙인다.
// viewport가 창을 꽉 채우면 스크롤 위치에 따라 도움말이 잘린 콘텐츠 바로 아래에 붙어
// 상세 정보와 겹쳐 보인다. 높이를 도움말 자리만큼 줄여 그 겹침을 없앤다.
//
// 콘텐츠가 짧아 viewport를 다 못 채우면 viewport가 남는 줄을 빈 줄로 메우므로, 도움말은
// 언제나 같은 자리(맨 아래)에 고정된다.
func (m Model) detailView() string {
	body := m.detail.View()

	// viewport가 아직 높이를 못 받은 경우(테스트 등) 최소한 도움말이 겹치지 않게 채운다.
	if lines := strings.Count(body, "\n") + 1; lines < m.detailHeight() {
		body += strings.Repeat("\n", m.detailHeight()-lines)
	}

	return body + m.helpBar([2]string{"↑↓/jk", "scroll"})
}

func (m Model) errorView() string {
	body := m.theme.Error.Render("Error") + "\n\n" + m.errText +
		"\n\n" + m.theme.Faint.Render("esc: back  q: quit")

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
		[2]string{"esc/←", "back"},
		[2]string{"q", "quit"},
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
	parts := []string{label("Profile", profile)}

	if depth >= breadcrumbRegion {
		parts = append(parts, label("Region", region))
	}
	if depth >= breadcrumbGroup {
		parts = append(parts, label("Service", orDashUI(m.breadcrumbGroups())))
	}

	// 세부 항목은 고른 것이 있을 때만 붙인다. 항상 "-"로 자리를 차지하면 좁은 터미널에서
	// 프로필·리전이 먼저 밀린다.
	if items := m.breadcrumbItems(); depth >= breadcrumbItem && items != "" {
		parts = append(parts, label("Resource type", items))
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
	case ScreenResource:
		// 선택 화면은 자체 헤더에 규모와 선택 수를 보여준다. 경로에 같은 정보를 겹쳐
		// 쓰면 아직 확정하지 않은 선택이 확정된 것처럼 보인다.
		return breadcrumbRegion
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

// 상세 화면 렌더링은 detail.go에 있다.
