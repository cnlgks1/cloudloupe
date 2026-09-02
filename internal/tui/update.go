package tui

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// identityMsg는 신원 확인 결과를 Update로 전달한다.
type identityMsg struct {
	requestID uint64
	id        awsclient.Identity
	err       error
}

// collectDoneMsg는 수집과 표시 데이터 준비가 끝났을 때 Update로 전달되는 메시지다.
type collectDoneMsg struct {
	requestID  uint64
	result     collect.Result
	data       resourceTableData
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

	// q는 상세 화면에서는 이전 목록으로 돌아가고, 그 외 화면에서는 종료한다.
	if key.Matches(msg, m.keys.Quit) &&
		m.screen != ScreenConfigPath && m.screen != ScreenDetail && m.screen != ScreenCollectErrorDetail {
		return m, tea.Quit
	}

	switch m.screen {
	case ScreenConfigPath:
		return m.keyConfigPath(msg)
	case ScreenProfile:
		return m.keyProfile(msg)
	case ScreenRegion:
		return m.keyRegion(msg)
	case ScreenResourceType:
		return m.keyResourceType(msg)
	case ScreenResourceItem:
		return m.keyResourceItem(msg)
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
			m.loading = p.Name + " 자격증명 확인 중..."
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

	return func() tea.Msg {
		id, err := m.deps.Identify(ctx, profile, region)

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
	m.replaceTypeOnEnter = false
	m.explicitRegionSelection = false
	m.explicitTypeSelection = false
	m.collectFromItem = false
	m.itemTable = table.Model{}
	m.itemGroup = 0
	m.regions = awsclient.Regions(m.profileRegion())
	m.regionTable = buildRegionTable(m.theme, m.regions, nil, m.width, m.listHeight())
	m.typeTable = buildTypeTable(m.theme, m.deps.ResourceGroups, nil, m.width, m.listHeight())
	m.typeTable.SetCursor(0)
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
	if len(m.typeTable.Rows()) == 0 {
		m.typeTable = buildTypeTable(m.theme, m.deps.ResourceGroups, m.chosenTypes, m.width, m.listHeight())
	}

	m.screen = ScreenResourceType

	return m
}

func (m Model) keyResourceType(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.screen = ScreenRegion

		return m, nil

	case key.Matches(msg, m.keys.Toggle):
		i := m.typeTable.Cursor()
		if i >= 0 && i < len(m.deps.ResourceGroups) {
			m.toggleResourceGroup(m.deps.ResourceGroups[i])
			m.replaceTypeOnEnter = false
			m.explicitTypeSelection = len(m.chosenTypes) > 0
			m.typeTable = buildTypeTable(m.theme, m.deps.ResourceGroups, m.chosenTypes, m.width, m.listHeight())
			m.typeTable.SetCursor(i)
		}

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		// space로 그룹을 명시적으로 골랐으면 그 선택대로 바로 조회한다. 그러지 않았다면
		// 커서 그룹의 세부 항목으로 들어간다. 그룹 화면에 포함 항목을 나열하지 않는 대신
		// 한 단계 내려가서 무엇을 수집할지 온전히 보여주는 방식이다.
		if m.explicitTypeSelection && len(m.chosenTypes) > 0 && !m.replaceTypeOnEnter {
			m.replaceTypeOnEnter = false
			m.collectFromItem = false

			return m.startCollecting()
		}

		m.replaceTypeOnEnter = false

		return m.enterResourceGroup(m.typeTable.Cursor())
	}

	return m.delegateToActiveList(msg)
}

// --- 세부 리소스 항목 ---

// enterResourceGroup은 그룹을 골랐을 때 다음 화면을 정한다.
//
// 세부 항목이 하나뿐인 그룹은 그 화면을 건너뛰고 바로 조회한다. 고를 것이 없는 목록을 한 번 더
// 보여주고 enter를 다시 받는 것은 사용자에게 아무것도 알려주지 않는다. 그룹에 타입이 늘어나면
// 이 조건이 자연히 풀려 다시 세부 항목 화면으로 들어간다.
//
// 건너뛴 화면은 뒤로 가기에도 끼어들지 않는다. collectFromItem을 false로 두므로 목록에서
// 뒤로 가면 사용자가 실제로 지나온 그룹 화면으로 돌아간다.
func (m Model) enterResourceGroup(groupIndex int) (tea.Model, tea.Cmd) {
	if groupIndex < 0 || groupIndex >= len(m.deps.ResourceGroups) {
		return m, nil
	}

	group := m.deps.ResourceGroups[groupIndex]
	if len(group.Types) != 1 {
		return m.gotoResourceItem(groupIndex), nil
	}

	m.itemGroup = groupIndex
	m.itemTable = table.Model{}
	m.chosenTypes = resourceGroupTypeIDs(group)
	m.explicitTypeSelection = false
	m.collectFromItem = false

	return m.startCollecting()
}

// gotoResourceItem은 선택한 그룹의 세부 항목 화면으로 들어간다.
func (m Model) gotoResourceItem(groupIndex int) Model {
	if groupIndex < 0 || groupIndex >= len(m.deps.ResourceGroups) {
		return m
	}

	// 다른 그룹으로 들어가면 이전 그룹에서 체크한 항목은 유지하지 않는다. 화면에 보이지
	// 않는 선택이 조회 범위에 남아 있으면 사용자가 결과를 설명할 수 없다.
	if m.itemGroup != groupIndex || !m.explicitTypeSelection {
		m.chosenTypes = nil
		m.explicitTypeSelection = false
	}

	m.itemGroup = groupIndex
	m.itemTable = buildResourceItemTable(
		m.theme, m.deps.ResourceGroups[groupIndex], m.chosenTypes, m.width, m.listHeight())
	m.itemTable.SetCursor(0)
	m.screen = ScreenResourceItem

	return m
}

func (m Model) keyResourceItem(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	group := m.currentResourceGroup()

	switch {
	case key.Matches(msg, m.keys.Back):
		m.typeTable = buildTypeTable(m.theme, m.deps.ResourceGroups, m.chosenTypes, m.width, m.listHeight())
		m.typeTable.SetCursor(m.itemGroup)
		m.screen = ScreenResourceType

		return m, nil

	case key.Matches(msg, m.keys.Toggle):
		i := m.itemTable.Cursor()
		if i >= 0 && i < len(group.Types) {
			m.toggleResourceItem(group.Types[i].ID)
			m.explicitTypeSelection = len(m.chosenTypes) > 0
			m.itemTable = buildResourceItemTable(m.theme, group, m.chosenTypes, m.width, m.listHeight())
			m.itemTable.SetCursor(i)
		}

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		// space로 체크한 항목이 없으면 커서가 가리키는 항목 하나만 조회한다. 리전 선택과
		// 같은 규칙이다. 그룹 전체가 필요하면 그룹 화면에서 space로 고른다.
		if !m.explicitTypeSelection || len(m.chosenTypes) == 0 {
			i := m.itemTable.Cursor()
			if i < 0 || i >= len(group.Types) {
				return m, nil
			}
			m.chosenTypes = []string{group.Types[i].ID}
			m.explicitTypeSelection = false
		}
		m.replaceTypeOnEnter = false
		m.collectFromItem = true

		return m.startCollecting()
	}

	return m.delegateToActiveList(msg)
}

// backToResourceSelection은 조회를 시작한 선택 화면으로 한 단계만 되돌아간다.
//
// 세부 항목에서 조회했으면 그 화면으로 돌아가야 한다. 그룹 화면까지 두 단계 올라가면
// 방금 고른 항목을 다시 찾아 들어가야 한다.
func (m Model) backToResourceSelection() Model {
	if m.collectFromItem && len(m.itemTable.Rows()) > 0 {
		m.screen = ScreenResourceItem

		return m
	}

	m.replaceTypeOnEnter = true
	m.screen = ScreenResourceType

	return m
}

func (m *Model) toggleResourceItem(typeID string) {
	if i := slices.Index(m.chosenTypes, typeID); i >= 0 {
		m.chosenTypes = slices.Delete(m.chosenTypes, i, i+1)

		return
	}

	m.chosenTypes = append(m.chosenTypes, typeID)
}

func (m *Model) toggleResourceGroup(group ResourceGroup) {
	if resourceGroupSelected(group, m.chosenTypes) {
		remove := make(map[string]struct{}, len(group.Types))
		for _, resourceType := range group.Types {
			remove[resourceType.ID] = struct{}{}
		}

		kept := m.chosenTypes[:0]
		for _, typ := range m.chosenTypes {
			if _, exists := remove[typ]; !exists {
				kept = append(kept, typ)
			}
		}
		m.chosenTypes = kept

		return
	}

	selected := make(map[string]struct{}, len(m.chosenTypes))
	for _, typ := range m.chosenTypes {
		selected[typ] = struct{}{}
	}
	for _, resourceType := range group.Types {
		if _, exists := selected[resourceType.ID]; exists {
			continue
		}
		m.chosenTypes = append(m.chosenTypes, resourceType.ID)
	}
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

func (m *Model) resetResourceSelection() {
	m.chosenTypes = nil
	m.explicitTypeSelection = false
	m.replaceTypeOnEnter = false
	m.collectFromItem = false
	m.itemTable = table.Model{}
	m.itemGroup = 0
	m.typeTable = buildTypeTable(m.theme, m.deps.ResourceGroups, nil, m.width, m.listHeight())
	m.typeTable.SetCursor(0)
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
	m.loading = strings.Join(m.chosenRegions, ", ") + " 조회 중..."

	profile := m.chosenProfile
	regions := append([]string(nil), m.chosenRegions...)
	types := append([]string(nil), m.chosenTypes...)
	groups := append([]ResourceGroup(nil), m.deps.ResourceGroups...)
	showRegion := len(regions) > 1
	collectFn := m.deps.Collect

	cmd := func() tea.Msg {
		result := collectFn(ctx, profile, regions, types)
		if result.Canceled || errors.Is(ctx.Err(), context.Canceled) {
			return collectDoneMsg{requestID: requestID, result: result, canceled: true}
		}

		data, prepared := buildResourceData(ctx, result.Resources, groups, types, showRegion)

		return collectDoneMsg{
			requestID:  requestID,
			result:     result,
			data:       data,
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
	title := "리소스 " + strconv.Itoa(len(result.Resources)) + "개"
	if len(result.Errors) > 0 {
		title += "  (오류 " + strconv.Itoa(len(result.Errors)) + "건)"
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
		m.detail.SetContent(renderDetail(m.theme, resource))
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
	if key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
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
	if key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
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
