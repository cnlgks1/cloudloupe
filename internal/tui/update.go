package tui

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/graph"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// identityMsg는 신원 확인 결과를 Update로 전달한다.
type identityMsg struct {
	requestID uint64
	id        awsclient.Identity
	err       error
}

// collectDoneMsg는 수집과 표시 데이터 준비가 끝났을 때 Update로 전달되는 메시지다.
//
// relations는 백그라운드에서 미리 빌드한 관계 그래프다. 빌드가 실패하면 nil이고, 상세
// 화면은 수집기가 남긴 원본 Ref로 폴백한다. 그래프 빌드를 이 Cmd 안에서 하는 이유는
// 정렬·표 준비와 마찬가지로 UI 고루틴을 막지 않기 위해서다.
type collectDoneMsg struct {
	requestID  uint64
	result     collect.Result
	data       resourceTableData
	relations  *graph.Graph
	showRegion bool
	canceled   bool
}

// handleKey는 키 입력을 화면별로 처리한다.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c는 입력 중이어도 즉시 종료한다.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// 필터 입력 중에는 q, p, r 같은 문자도 검색어로 전달한다.
	if m.screen == ScreenList && m.filtering {
		return m.keyResourceFilter(msg)
	}

	// q는 어느 화면에서든 종료한다. 뒤로 가기는 esc와 ←가 담당한다.
	//
	// 한때 상세 화면에서만 q를 목록 복귀로 썼다. 그런데 하단 도움말은 모든 화면에서 "q quit"로
	// 안내하므로, 같은 키가 안내와 다르게 동작했다. 종료하려면 esc로 한 단계 나온 뒤 q를 다시
	// 눌러야 했다.
	//
	// 경로 입력 화면은 예외다. 거기서 q는 파일 경로에 들어갈 글자다.
	if key.Matches(msg, m.keys.Quit) && m.screen != ScreenConfigPath {
		return m, tea.Quit
	}

	switch m.screen {
	case ScreenConfigPath:
		return m.keyConfigPath(msg)
	case ScreenProfile:
		return m.keyProfile(msg)
	case ScreenRegion:
		return m.keyRegion(msg)
	case ScreenResource:
		return m.keyResource(msg)
	case ScreenCollecting:
		return m.keyCollecting(msg)
	case ScreenList:
		return m.keyList(msg)
	case ScreenResourceKind:
		return m.keyResourceKind(msg)
	case ScreenDetail:
		return m.keyDetail(msg)
	case ScreenCollectErrors:
		return m.keyCollectErrors(msg)
	case ScreenCollectErrorDetail:
		return m.keyCollectErrorDetail(msg)
	case ScreenError:
		return m.keyError(msg)
	case ScreenIdentity:
		if key.Matches(msg, m.keys.Back) {
			m.cancelWork()
			m.screen = ScreenProfile

			return m, nil
		}
	}

	return m, nil
}

// --- 경로 입력 ---

func (m Model) keyConfigPath(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		if len(m.profiles) > 0 {
			m.screen = ScreenProfile

			return m, nil
		}

		return m, tea.Quit
	case tea.KeyTab, tea.KeyShiftTab, tea.KeyDown, tea.KeyUp:
		return m.togglePathFocus(), nil
	case tea.KeyEnter:
		override := awsclient.Override{
			ConfigPath:      strings.TrimSpace(m.configInput.Value()),
			CredentialsPath: strings.TrimSpace(m.credsInput.Value()),
		}

		return m.loadProfiles(override), nil
	default:
		var cmd tea.Cmd
		if m.pathFocus == 0 {
			m.configInput, cmd = m.configInput.Update(msg)
		} else {
			m.credsInput, cmd = m.credsInput.Update(msg)
		}

		return m, cmd
	}
}

func (m Model) togglePathFocus() Model {
	if m.pathFocus == 0 {
		m.pathFocus = 1
		m.configInput.Blur()
		m.credsInput.Focus()
	} else {
		m.pathFocus = 0
		m.credsInput.Blur()
		m.configInput.Focus()
	}

	return m
}

// --- 프로필 ---

func (m Model) keyProfile(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Enter) {
		i := m.profileTable.Cursor()
		if i >= 0 && i < len(m.profiles) {
			p := m.profiles[i]
			m.chosenProfile = p.Name
			m.loading = p.Name + " checking credentials..."
			m.screen = ScreenIdentity

			cmd := m.identifyCmd(p)

			return m, cmd
		}

		return m, nil
	}

	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'c' {
		return m.enterConfigPath(), nil
	}

	return m.delegateToActiveList(msg)
}

func (m *Model) identifyCmd(p awsclient.Profile) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.identitySequence++
	requestID := m.identitySequence

	profile := p.Name
	region := p.Region
	locations := m.locations

	return func() tea.Msg {
		id, err := m.deps.Identify(ctx, profile, region, locations)

		return identityMsg{requestID: requestID, id: id, err: err}
	}
}

// --- 신원 확인 결과 ---

func (m Model) onIdentity(msg identityMsg) (tea.Model, tea.Cmd) {
	if m.screen != ScreenIdentity || msg.requestID != m.identitySequence {
		return m, nil
	}

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	if errors.Is(msg.err, context.Canceled) {
		m.screen = ScreenProfile

		return m, nil
	}

	if msg.err != nil {
		m.errText = m.deps.Explain(msg.err)
		m.screen = ScreenError

		return m, nil
	}

	m.identity = msg.id
	m.chosenRegions = nil
	m.confirmedRegions = nil
	m.chosenTypes = nil
	m.replaceRegionOnEnter = false
	m.explicitRegionSelection = false
	m.treeFiltering = false
	m.regions = awsclient.Regions(m.profileRegion())
	m.regionTable = buildRegionTable(m.theme, m.regions, nil, m.width, m.listHeight())
	m.resourceTree = newResourceTree(m.theme, m.deps.ResourceGroups, m.width, m.listHeight())
	m.screen = ScreenRegion

	return m, nil
}

func (m Model) profileRegion() string {
	for _, p := range m.profiles {
		if p.Name == m.chosenProfile {
			return p.Region
		}
	}

	return ""
}

// --- 리전 ---

func (m Model) keyRegion(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = ScreenProfile

		return m, nil

	case key.Matches(msg, m.keys.Toggle):
		// 테이블 커서 위치의 리전을 토글한다. 재생성 뒤에도 같은 행에 머문다.
		i := m.regionTable.Cursor()
		if i >= 0 && i < len(m.regions) {
			m.toggleRegion(m.regions[i].Code)
			m.replaceRegionOnEnter = false
			m.explicitRegionSelection = len(m.chosenRegions) > 0
			m.regionTable = buildRegionTable(m.theme, m.regions, m.chosenRegions, m.width, m.listHeight())
			m.regionTable.SetCursor(i)
		}

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		// space로 만든 명시적 다중 선택이 없으면 현재 커서의 리전 하나로 교체한다.
		i := m.regionTable.Cursor()
		if m.replaceRegionOnEnter || !m.explicitRegionSelection || len(m.chosenRegions) == 0 {
			if i >= 0 && i < len(m.regions) {
				m.chosenRegions = []string{m.regions[i].Code}
				m.explicitRegionSelection = false
			}
		}
		m.replaceRegionOnEnter = false

		if len(m.chosenRegions) == 0 {
			return m, nil
		}

		// 선택 데이터와 체크 표시가 항상 같은 상태를 가리키게 한다.
		m.regionTable = buildRegionTable(m.theme, m.regions, m.chosenRegions, m.width, m.listHeight())
		m.regionTable.SetCursor(i)

		if !sameRegionSelection(m.confirmedRegions, m.chosenRegions) {
			m.resetResourceSelection()
		}
		m.confirmedRegions = append([]string(nil), m.chosenRegions...)

		return m.gotoResourceType(), nil
	}

	return m.delegateToActiveList(msg)
}

// --- 리소스 타입 ---

func (m Model) gotoResourceType() Model {
	m.screen = ScreenResource

	return m
}

// --- 리소스 선택 (서비스 + resource type 트리) ---

// keyResource는 선택 화면의 키를 처리한다.
//
// → 와 ← 를 Enter/Back보다 먼저 본다. 두 키는 전역 바인딩에서 다음 단계·뒤로에 묶여 있지만,
// 이 화면에서는 트리를 펼치고 접는 데 쓰는 편이 자연스럽다. 다른 화면의 동작은 그대로 둔다.
func (m Model) keyResource(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.treeFiltering {
		return m.keyResourceSearch(msg)
	}

	height := m.listHeight()

	switch msg.String() {
	case "right":
		// 서비스 줄이면 펼친다. 이미 펼쳐졌거나 타입 줄이면 조회로 넘어간다.
		if m.resourceTree.toggleExpand(m.theme, true, m.width, height) {
			return m, nil
		}

		return m.startCollectingSelection()

	case "left":
		// 펼쳐진 서비스면 접는다. 그 외에는 리전 선택으로 돌아간다.
		if m.resourceTree.toggleExpand(m.theme, false, m.width, height) {
			return m, nil
		}

		return m.backFromResourceSelection(), nil

	case "/":
		m.previousFilter = m.resourceTree.query
		m.filterInput.SetValue(m.resourceTree.query)
		m.filterInput.CursorEnd()
		m.treeFiltering = true

		return m, m.filterInput.Focus()

	case "z":
		m.resourceTree.collapseAll(m.theme, m.width, height)

		return m, nil

	case "a":
		// 검색으로 좁힌 결과에만 적용한다. 필터 없이 전부 선택하면 리소스 타입 수 × 리전 수
		// 만큼 작업이 생겨 의도하지 않은 대량 조회가 된다.
		m.resourceTree.selectVisible(m.theme, m.width, height)

		return m, nil

	case "x":
		m.resourceTree.clearSelection(m.theme, m.width, height)

		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Back):
		return m.backFromResourceSelection(), nil

	case key.Matches(msg, m.keys.Toggle):
		m.resourceTree.toggleSelect(m.theme, m.width, height)

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		return m.startCollectingSelection()
	}

	return m.delegateToActiveList(msg)
}

// keyResourceSearch는 선택 화면의 검색 입력을 처리한다.
//
// 입력 중에는 q, z, a 같은 글자도 검색어로 들어간다. 리소스 목록의 검색과 같은 규칙이다.
func (m Model) keyResourceSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	height := m.listHeight()

	switch msg.Type {
	case tea.KeyEnter:
		m.treeFiltering = false
		m.filterInput.Blur()

		return m, nil

	case tea.KeyEsc:
		m.treeFiltering = false
		m.filterInput.Blur()
		m.resourceTree.setQuery(m.theme, m.previousFilter, m.width, height)
		m.filterInput.SetValue(m.previousFilter)

		return m, nil

	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.resourceTree.setQuery(m.theme, m.filterInput.Value(), m.width, height)

		return m, cmd
	}
}

// startCollectingSelection은 트리에서 고른 것으로 조회를 시작한다.
//
// 체크한 것이 있으면 그것을, 없으면 커서 줄을 조회한다. 리전 선택과 같은 규칙이다.
func (m Model) startCollectingSelection() (tea.Model, tea.Cmd) {
	types := m.resourceTree.queryTypes()
	if len(types) == 0 {
		return m, nil
	}

	m.chosenTypes = types

	return m.startCollecting()
}

// backFromResourceSelection은 선택 화면에서 리전 선택으로 돌아간다.
func (m Model) backFromResourceSelection() Model {
	if m.treeFiltering || m.resourceTree.query != "" {
		// 검색이 걸린 상태에서 뒤로 가면 먼저 검색을 푼다. 걸러진 화면을 남긴 채 나가면
		// 다시 들어왔을 때 목록이 왜 짧은지 알 수 없다.
		m.treeFiltering = false
		m.filterInput.Blur()
		m.filterInput.SetValue("")
		m.resourceTree.setQuery(m.theme, "", m.width, m.listHeight())

		return m
	}

	m.screen = ScreenRegion

	return m
}

// backToResourceSelection은 조회 결과에서 선택 화면으로 돌아간다.
//
// 선택 화면이 하나뿐이라 되돌아갈 곳을 기억할 필요가 없다. 이전에는 세부 항목 화면을
// 건너뛴 경우와 지나온 경우를 구분하려고 별도 플래그를 들고 있었다.
func (m Model) backToResourceSelection() Model {
	m.screen = ScreenResource

	return m
}

func sameRegionSelection(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	leftSorted := slices.Clone(left)
	rightSorted := slices.Clone(right)
	slices.Sort(leftSorted)
	slices.Sort(rightSorted)

	return slices.Equal(leftSorted, rightSorted)
}

// resetResourceSelection은 리전 선택이 바뀌었을 때 리소스 선택을 비운다.
//
// 리전이 달라지면 이전 리전에서 고른 것을 그대로 조회하는 게 맞는지 알 수 없다. 화면에
// 보이지 않는 선택이 조회 범위에 남아 있으면 사용자가 결과를 설명할 수 없다.
func (m *Model) resetResourceSelection() {
	m.chosenTypes = nil
	m.treeFiltering = false
	m.resourceTree.setQuery(m.theme, "", m.width, m.listHeight())
	m.resourceTree.setSelection(m.theme, nil, m.width, m.listHeight())
}

func (m *Model) toggleRegion(code string) {
	for i, c := range m.chosenRegions {
		if c == code {
			m.chosenRegions = append(m.chosenRegions[:i], m.chosenRegions[i+1:]...)

			return
		}
	}

	m.chosenRegions = append(m.chosenRegions, code)
}

// --- 수집 ---

func (m Model) startCollecting() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.collectSequence++
	requestID := m.collectSequence
	m.screen = ScreenCollecting
	m.loading = strings.Join(m.chosenRegions, ", ") + " querying..."

	profile := m.chosenProfile
	regions := append([]string(nil), m.chosenRegions...)
	types := append([]string(nil), m.chosenTypes...)
	groups := append([]ResourceGroup(nil), m.deps.ResourceGroups...)
	locations := m.locations
	showRegion := len(regions) > 1
	collectFn := m.deps.Collect

	cmd := func() tea.Msg {
		result := collectFn(ctx, profile, regions, types, locations)
		if result.Canceled || errors.Is(ctx.Err(), context.Canceled) {
			return collectDoneMsg{requestID: requestID, result: result, canceled: true}
		}

		data, prepared := buildResourceData(ctx, result.Resources, groups, types, showRegion)

		// 관계 그래프도 이 백그라운드 Cmd 안에서 만든다. 빌드 실패(중복 키 같은 입력 문제)는
		// 조회 결과 표시를 막지 않으므로 nil로 두고 계속 진행한다.
		relations, _ := graph.Build(result.Resources)

		return collectDoneMsg{
			requestID:  requestID,
			result:     result,
			data:       data,
			relations:  relations,
			showRegion: showRegion,
			canceled:   !prepared,
		}
	}

	return m, tea.Batch(cmd, m.spinner.Tick)
}

func (m Model) keyCollecting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.cancelWork()

		return m.backToResourceSelection(), nil
	}

	return m, nil
}

func (m Model) onCollectDone(msg collectDoneMsg) (tea.Model, tea.Cmd) {
	if m.screen != ScreenCollecting || msg.requestID != m.collectSequence {
		return m, nil
	}

	if msg.canceled {
		m.cancelWork()

		return m.backToResourceSelection(), nil
	}

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.resourceKinds = collectResourceKinds(m.deps.ResourceGroups, msg.result.Resources)
	m.resourceKindFilter = ""
	m.resourceList.setResources(
		m.theme, msg.result.Resources, msg.data, m.width, m.resourceListHeight())
	m.relations = msg.relations
	m.collectErrors = append([]model.CollectError(nil), msg.result.Errors...)
	m.errorTable = buildCollectErrorTable(
		m.theme, m.collectErrors, m.deps.ResourceGroups, m.width, m.listHeight())
	m.showRegion = msg.showRegion
	m.filtering = false
	m.filterQuery = ""
	m.previousFilter = ""
	m.filterInput.SetValue("")
	m.filterInput.Blur()
	m.listCaption = m.listTitle(msg.result)
	m.screen = ScreenList

	return m, nil
}

func (m Model) listTitle(result collect.Result) string {
	title := strconv.Itoa(len(result.Resources)) + " resources"
	if len(result.Errors) > 0 {
		title += "  (" + strconv.Itoa(len(result.Errors)) + " errors)"
	}

	return title
}

// --- 리소스 목록 ---

func (m Model) keyList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "/":
		m.previousFilter = m.filterQuery
		m.filterInput.SetValue(m.filterQuery)
		m.filterInput.CursorEnd()
		m.filtering = true

		return m, m.filterInput.Focus()

	case key.Matches(msg, m.keys.FilterKind) && m.hasResourceKindFilter():
		m.kindTable = buildResourceKindTable(m.theme, m.resourceKinds, m.width, m.listHeight())
		m.kindTable.SetCursor(m.resourceKindFilterCursor())
		m.screen = ScreenResourceKind

		return m, nil

	case key.Matches(msg, m.keys.ShowErrors) && len(m.collectErrors) > 0:
		m.errorTable = buildCollectErrorTable(
			m.theme, m.collectErrors, m.deps.ResourceGroups, m.width, m.listHeight())
		m.errorTable.SetCursor(0)
		m.screen = ScreenCollectErrors

		return m, nil

	case key.Matches(msg, m.keys.Back):
		return m.backToResourceSelection(), nil

	case key.Matches(msg, m.keys.SwitchProfile):
		m.screen = ScreenProfile

		return m, nil

	case key.Matches(msg, m.keys.SwitchRegion):
		m.replaceRegionOnEnter = true
		m.screen = ScreenRegion

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		resource, ok := m.resourceList.selected()
		if !ok {
			return m, nil
		}
		m.detail.SetContent(renderDetail(m.theme, m.deps.ResourceGroups, resource, m.relations))
		m.detail.GotoTop()
		m.screen = ScreenDetail

		return m, nil
	}

	m.resourceList.moveCursor(msg)

	return m, nil
}

func (m Model) keyResourceKind(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = ScreenList

		return m, nil
	case key.Matches(msg, m.keys.Enter):
		cursor := m.kindTable.Cursor()
		m.resourceKindFilter = ""
		if cursor > 0 && cursor <= len(m.resourceKinds) {
			m.resourceKindFilter = m.resourceKinds[cursor-1].ID
		}
		m.screen = ScreenList

		return m.applyResourceFilter(), nil
	}

	return m.delegateToActiveList(msg)
}

func (m Model) resourceKindFilterCursor() int {
	if m.resourceKindFilter == "" {
		return 0
	}
	for i, kind := range m.resourceKinds {
		if kind.ID == m.resourceKindFilter {
			return i + 1 // 첫 행은 전체다.
		}
	}

	return 0
}

// keyResourceFilter는 목록 필터 입력을 처리한다.
func (m Model) keyResourceFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filterQuery = m.previousFilter
		m.filterInput.SetValue(m.previousFilter)
		m.filterInput.Blur()
		m.filtering = false

		return m.applyResourceFilter(), nil
	case tea.KeyEnter:
		m.filterQuery = strings.TrimSpace(m.filterInput.Value())
		m.filterInput.SetValue(m.filterQuery)
		m.filterInput.Blur()
		m.filtering = false

		return m.applyResourceFilter(), nil
	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.filterQuery = m.filterInput.Value()

		return m.applyResourceFilter(), cmd
	}
}

// applyResourceFilter는 화면의 종류·검색 조건을 목록 서브모델에 적용한다.
func (m Model) applyResourceFilter() Model {
	m.resourceList.applyFilter(m.resourceKindFilter, m.filterQuery)

	return m
}

func searchTextMatches(searchText string, tokens []string) bool {
	for _, token := range tokens {
		if !strings.Contains(searchText, token) {
			return false
		}
	}

	return true
}

// collectResourceKinds는 전체 원본 리소스에서 종류별 개수를 계산한다.
func collectResourceKinds(groups []ResourceGroup, resources []model.Resource) []resourceKind {
	counts := make(map[string]int)
	for _, resource := range resources {
		counts[resource.Type]++
	}

	seen := make(map[string]struct{}, len(counts))
	kinds := make([]resourceKind, 0, len(counts))
	for _, group := range groups {
		for _, resourceType := range group.Types {
			count := counts[resourceType.ID]
			if count == 0 {
				continue
			}
			if _, exists := seen[resourceType.ID]; exists {
				continue
			}
			seen[resourceType.ID] = struct{}{}
			kinds = append(kinds, resourceKind{ID: resourceType.ID, Label: resourceType.Label, Count: count})
		}
	}

	// 외부 주입이나 새 타입 메타데이터 누락이 있어도 결과를 필터링할 수 있게 보존한다.
	for _, resource := range resources {
		if _, exists := seen[resource.Type]; exists {
			continue
		}
		seen[resource.Type] = struct{}{}
		kinds = append(kinds, resourceKind{
			ID:    resource.Type,
			Label: resourceTypeLabel(groups, resource.Type),
			Count: counts[resource.Type],
		})
	}

	return kinds
}

// --- 상세 ---

func (m Model) keyDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 종료는 handleKey가 먼저 처리한다. 여기서 Quit을 뒤로 가기로 다시 잡으면 도움말의
	// "q quit"과 어긋난다.
	if key.Matches(msg, m.keys.Back) {
		m.screen = ScreenList

		return m, nil
	}

	return m.delegateToActiveList(msg)
}

// --- 수집 오류 ---

func (m Model) keyCollectErrors(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = ScreenList

		return m, nil
	case key.Matches(msg, m.keys.Enter):
		cursor := m.errorTable.Cursor()
		if cursor < 0 || cursor >= len(m.collectErrors) {
			return m, nil
		}

		m.detail.SetContent(renderCollectErrorDetail(
			m.theme, m.deps.ResourceGroups, m.collectErrors[cursor]))
		m.detail.GotoTop()
		m.screen = ScreenCollectErrorDetail

		return m, nil
	}

	return m.delegateToActiveList(msg)
}

func (m Model) keyCollectErrorDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.screen = ScreenCollectErrors

		return m, nil
	}

	return m.delegateToActiveList(msg)
}

// --- 치명적 오류 ---

func (m Model) keyError(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.errText = ""
		m.screen = ScreenProfile

		return m, nil
	}

	return m, nil
}

func (m *Model) cancelWork() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}
