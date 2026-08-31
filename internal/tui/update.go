package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
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

	// ctrl+c는 어느 화면에서든 즉시 종료. q는 상세 외에서.
	if msg.String() == "ctrl+c" || (key.Matches(msg, m.keys.Quit) && m.screen != ScreenDetail) {
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
	m.regions = awsclient.Regions(m.profileRegion())
	m.regionTable = buildRegionTable(m.theme, m.regions, nil, m.width, m.listHeight())
	m.typeTable = buildTypeTable(m.theme, m.deps.ResourceTypes, nil, m.width, m.listHeight())
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
		m.typeTable = buildTypeTable(m.theme, m.deps.ResourceTypes, m.chosenTypes, m.width, m.listHeight())
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
		if i >= 0 && i < len(m.deps.ResourceTypes) {
			m.toggleType(m.deps.ResourceTypes[i].ID)
			m.replaceTypeOnEnter = false
			m.typeTable = buildTypeTable(m.theme, m.deps.ResourceTypes, m.chosenTypes, m.width, m.listHeight())
			m.typeTable.SetCursor(i)
		}

		return m, nil

	case key.Matches(msg, m.keys.Enter):
		// 목록에서 타입을 바꾸러 왔거나 체크가 없으면 현재 커서 타입 하나로 교체한다.
		if m.replaceTypeOnEnter || len(m.chosenTypes) == 0 {
			i := m.typeTable.Cursor()
			if i >= 0 && i < len(m.deps.ResourceTypes) {
				m.chosenTypes = []string{m.deps.ResourceTypes[i].ID}
			}
		}
		m.replaceTypeOnEnter = false

		return m.startCollecting()
	}

	return m.delegateToActiveList(msg)
}

func (m *Model) toggleType(id string) {
	for i, t := range m.chosenTypes {
		if t == id {
			m.chosenTypes = append(m.chosenTypes[:i], m.chosenTypes[i+1:]...)

			return
		}
	}

	m.chosenTypes = append(m.chosenTypes, id)
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
		m.screen = ScreenResourceType

		return m, nil
	}

	return m, nil
}

func (m Model) onCollectDone(msg collectDoneMsg) (tea.Model, tea.Cmd) {
	if m.screen != ScreenCollecting {
		return m, nil
	}

	m.resourceRows = msg.result.Resources
	m.listCaption = m.listTitle(msg.result)
	m.resourceTable = buildTable(m.theme, m.resourceRows, m.deps.ResourceTypes, m.width, m.listHeight())
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
