package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// identityMsg는 신원 확인 결과를 Update로 전달한다.
type identityMsg struct {
	id  awsclient.Identity
	err error
}

// collectDoneMsg는 수집이 끝났을 때 Update로 전달되는 메시지다.
type collectDoneMsg struct {
	result collect.Result
}

// handleKey는 키 입력을 화면별로 처리한다.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 경로 입력 중에는 q를 종료로 가로채지 않는다.
	if m.screen == ScreenConfigPath {
		return m.keyConfigPath(msg)
	}

	// ctrl+c는 입력 중이어도 즉시 종료한다.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// 필터 입력 중에는 q, p, R 같은 문자도 검색어로 전달한다.
	if m.screen == ScreenList && m.filtering {
		return m.keyResourceFilter(msg)
	}

	// q는 상세 외에서 종료한다.
	if key.Matches(msg, m.keys.Quit) && m.screen != ScreenDetail {
		return m, tea.Quit
	}

	switch m.screen {
	case ScreenProfile:
		return m.keyProfile(msg)
	case ScreenRegion:
		return m.keyRegion(msg)
	case ScreenResourceType:
		return m.keyResourceType(msg)
	case ScreenCollecting:
		return m.keyCollecting(msg)
	case ScreenList:
		return m.keyList(msg)
	case ScreenResourceKind:
		return m.keyResourceKind(msg)
	case ScreenDetail:
		return m.keyDetail(msg)
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

			return m, m.identifyCmd(p)
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

	profile := p.Name
	region := p.Region

	return func() tea.Msg {
		id, err := m.deps.Identify(ctx, profile, region)

		return identityMsg{id: id, err: err}
	}
}

// --- 신원 확인 결과 ---

func (m Model) onIdentity(msg identityMsg) (tea.Model, tea.Cmd) {
	if m.screen != ScreenIdentity {
		return m, nil
	}

	if msg.err != nil {
		m.errText = m.deps.Explain(msg.err)
		m.screen = ScreenError

		return m, nil
	}

	m.identity = msg.id
	m.chosenRegions = nil
	m.chosenTypes = nil
	m.replaceRegionOnEnter = false
	m.replaceTypeOnEnter = false
	m.explicitTypeSelection = false
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
			m.regionTable = buildRegionTable(m.theme, m.regions, m.chosenRegions, m.width, m.listHeight())
			m.regionTable.SetCursor(i)
		}

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		// 명시적인 리전 전환으로 들어왔거나 아무 체크가 없으면 현재 커서 하나로 교체한다.
		if m.replaceRegionOnEnter || len(m.chosenRegions) == 0 {
			i := m.regionTable.Cursor()
			if i >= 0 && i < len(m.regions) {
				m.chosenRegions = []string{m.regions[i].Code}
			}
		}
		m.replaceRegionOnEnter = false

		if len(m.chosenRegions) == 0 {
			return m, nil
		}

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
		// space로 만든 명시적 다중 선택이 없으면 현재 커서의 서비스 그룹만 조회한다.
		// chosenTypes에는 그룹 ID가 아니라 실제 수집기에 전달할 내부 타입 ID를 보관한다.
		if m.replaceTypeOnEnter || !m.explicitTypeSelection || len(m.chosenTypes) == 0 {
			i := m.typeTable.Cursor()
			if i >= 0 && i < len(m.deps.ResourceGroups) {
				m.chosenTypes = resourceGroupTypeIDs(m.deps.ResourceGroups[i])
			}
			m.explicitTypeSelection = false
		}
		m.replaceTypeOnEnter = false

		return m.startCollecting()
	}

	return m.delegateToActiveList(msg)
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
	m.screen = ScreenCollecting
	m.loading = strings.Join(m.chosenRegions, ", ") + " 조회 중..."

	profile := m.chosenProfile
	regions := append([]string(nil), m.chosenRegions...)
	types := append([]string(nil), m.chosenTypes...)
	collectFn := m.deps.Collect

	cmd := func() tea.Msg {
		return collectDoneMsg{result: collectFn(ctx, profile, regions, types)}
	}

	return m, tea.Batch(cmd, m.spinner.Tick)
}

func (m Model) keyCollecting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		m.cancelWork()
		m.replaceTypeOnEnter = true
		m.screen = ScreenResourceType

		return m, nil
	}

	return m, nil
}

func (m Model) onCollectDone(msg collectDoneMsg) (tea.Model, tea.Cmd) {
	if m.screen != ScreenCollecting {
		return m, nil
	}

	m.allResourceRows = append([]model.Resource(nil), msg.result.Resources...)
	m.resourceRows = append([]model.Resource(nil), msg.result.Resources...)
	m.resourceKinds = collectResourceKinds(m.deps.ResourceGroups, m.allResourceRows)
	m.resourceKindFilter = ""
	m.showRegion = m.shouldShowRegion()
	m.filtering = false
	m.filterQuery = ""
	m.previousFilter = ""
	m.filterInput.SetValue("")
	m.filterInput.Blur()
	m.listCaption = m.listTitle(msg.result)
	m.resourceTable = buildTable(m.theme, m.resourceRows, m.allResourceRows,
		m.deps.ResourceGroups, m.chosenTypes, m.showRegion, m.width, m.resourceListHeight())
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

	case key.Matches(msg, m.keys.Back):
		m.replaceTypeOnEnter = true
		m.screen = ScreenResourceType

		return m, nil

	case key.Matches(msg, m.keys.SwitchProfile):
		m.screen = ScreenProfile

		return m, nil

	case key.Matches(msg, m.keys.SwitchRegion):
		m.replaceRegionOnEnter = true
		m.screen = ScreenRegion

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		i := m.resourceTable.Cursor()
		if i >= 0 && i < len(m.resourceRows) {
			m.detail.SetContent(renderDetail(m.theme, m.resourceRows[i]))
			m.detail.GotoTop()
			m.screen = ScreenDetail

			return m, nil
		}
	}

	return m.delegateToActiveList(msg)
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

// applyResourceFilter는 현재 종류와 검색어를 모두 만족하는 행만 테이블에 반영한다.
func (m Model) applyResourceFilter() Model {
	resources := filterResourcesByKind(m.allResourceRows, m.resourceKindFilter)
	m.resourceRows = filterResources(resources, m.filterQuery)
	m.resourceTable = buildTable(m.theme, m.resourceRows, m.allResourceRows,
		m.deps.ResourceGroups, m.chosenTypes, m.showRegion, m.width, m.resourceListHeight())
	m.resourceTable.SetCursor(0)

	return m
}

func filterResourcesByKind(resources []model.Resource, typeID string) []model.Resource {
	if typeID == "" {
		return append([]model.Resource(nil), resources...)
	}

	filtered := make([]model.Resource, 0, len(resources))
	for _, resource := range resources {
		if resource.Type == typeID {
			filtered = append(filtered, resource)
		}
	}

	return filtered
}

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

// filterResources는 공백으로 나눈 모든 검색어가 포함된 리소스만 반환한다.
func filterResources(resources []model.Resource, query string) []model.Resource {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return append([]model.Resource(nil), resources...)
	}

	filtered := make([]model.Resource, 0, len(resources))
	for _, resource := range resources {
		if resourceMatches(resource, tokens) {
			filtered = append(filtered, resource)
		}
	}

	return filtered
}

func resourceMatches(resource model.Resource, tokens []string) bool {
	parts := []string{
		resource.Type,
		resource.ID,
		resource.Name,
		resource.ARN,
		resource.Region,
		resource.Profile,
		resource.AccountID,
		resource.Status,
	}

	for _, field := range resource.Fields {
		parts = append(parts, field.Key, field.Value)
	}
	for _, tag := range resource.Tags {
		parts = append(parts, tag.Key, tag.Value)
	}
	for _, ref := range resource.Related {
		parts = append(parts, ref.Type, ref.ID, ref.Relation, ref.Via)
	}

	haystack := strings.ToLower(strings.Join(parts, "\n"))
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}

	return true
}

// --- 상세 ---

func (m Model) keyDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
		m.screen = ScreenList

		return m, nil
	}

	return m.delegateToActiveList(msg)
}

// --- 에러 ---

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
