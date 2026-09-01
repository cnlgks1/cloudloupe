package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// resourceListModel은 리소스 목록의 원본, 파생 인덱스, 가상 viewport를 함께 관리한다.
//
// cursor는 필터 결과 전체에서의 위치이고 table.Cursor는 현재 window 안의 위치다.
// filteredIndexes와 data의 같은 인덱스는 resources의 같은 원본 리소스를 가리킨다.
type resourceListModel struct {
	table           table.Model
	resources       []model.Resource
	data            resourceTableData
	filteredIndexes []int
	visibleRows     []table.Row
	cursor          int
	windowStart     int
	tableHeight     int
}

// setResources는 수집 결과와 사전 계산 데이터를 한 번에 설치한다.
func (m *resourceListModel) setResources(
	theme Theme,
	resources []model.Resource,
	data resourceTableData,
	width int,
	height int,
) {
	m.resources = resources
	m.data = data
	m.filteredIndexes = make([]int, len(resources))
	for i := range m.filteredIndexes {
		m.filteredIndexes[i] = i
	}
	m.visibleRows = nil
	m.cursor = 0
	m.windowStart = 0
	m.tableHeight = height
	m.table = newResourceTable(theme, data, nil, width, height)
	m.syncWindow()
}

// totalCount는 필터 적용 전 원본 리소스 수를 반환한다.
func (m resourceListModel) totalCount() int {
	return len(m.resources)
}

// filteredCount는 현재 종류와 검색 조건에 맞는 리소스 수를 반환한다.
func (m resourceListModel) filteredCount() int {
	return len(m.filteredIndexes)
}

// applyFilter는 캐시된 검색 문자열에 종류와 검색 조건을 함께 적용한다.
func (m *resourceListModel) applyFilter(kind, query string) {
	tokens := strings.Fields(strings.ToLower(query))
	m.filteredIndexes = m.filteredIndexes[:0]

	for i, resource := range m.resources {
		if kind != "" && resource.Type != kind {
			continue
		}
		if i >= len(m.data.searchTexts) || !searchTextMatches(m.data.searchTexts[i], tokens) {
			continue
		}

		m.filteredIndexes = append(m.filteredIndexes, i)
	}

	m.cursor = 0
	m.windowStart = 0
	// 결과 수와 window 위치가 같아도 다른 원본 행일 수 있으므로 행 매핑을 무효화한다.
	m.visibleRows = m.visibleRows[:0]
	m.syncWindow()
}

// moveCursor는 필터 결과 전체 기준으로 커서를 이동하고 가상 window를 맞춘다.
func (m *resourceListModel) moveCursor(msg tea.KeyMsg) {
	if len(m.filteredIndexes) == 0 {
		return
	}

	viewportHeight := max(0, m.tableHeight-1)
	next := m.cursor
	switch {
	case key.Matches(msg, m.table.KeyMap.LineUp):
		next--
	case key.Matches(msg, m.table.KeyMap.LineDown):
		next++
	case key.Matches(msg, m.table.KeyMap.PageUp):
		next -= viewportHeight
	case key.Matches(msg, m.table.KeyMap.PageDown):
		next += viewportHeight
	case key.Matches(msg, m.table.KeyMap.HalfPageUp):
		next -= viewportHeight / 2
	case key.Matches(msg, m.table.KeyMap.HalfPageDown):
		next += viewportHeight / 2
	case key.Matches(msg, m.table.KeyMap.GotoTop):
		next = 0
	case key.Matches(msg, m.table.KeyMap.GotoBottom):
		next = len(m.filteredIndexes) - 1
	default:
		return
	}

	m.cursor = min(max(next, 0), len(m.filteredIndexes)-1)
	m.syncWindow()
}

// resize는 캐시된 선호 폭으로 열을 배치하고 현재 선택이 보이도록 window를 맞춘다.
func (m *resourceListModel) resize(width, height int) {
	if m.tableHeight == 0 {
		return
	}

	columns := layoutResourceColumns(m.data.titles, m.data.preferredWidths, width)
	if !sameTableColumns(m.table.Columns(), columns) {
		m.table.SetColumns(columns)
	}
	if m.tableHeight != height {
		m.table.SetHeight(height)
		m.tableHeight = height
		m.syncWindow()
	}
}

// selected는 현재 필터 커서를 원본 리소스로 역매핑한다.
func (m resourceListModel) selected() (model.Resource, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filteredIndexes) {
		return model.Resource{}, false
	}

	resourceIndex := m.filteredIndexes[m.cursor]
	if resourceIndex < 0 || resourceIndex >= len(m.resources) {
		return model.Resource{}, false
	}

	return m.resources[resourceIndex], true
}

// View는 현재 가상 window의 테이블을 렌더링한다.
func (m resourceListModel) View() string {
	return m.table.View()
}

// Columns는 현재 터미널 폭에 맞춘 테이블 열을 반환한다.
func (m resourceListModel) Columns() []table.Column {
	return m.table.Columns()
}

// Rows는 현재 가상 window에 설치된 행을 반환한다.
func (m resourceListModel) Rows() []table.Row {
	return m.table.Rows()
}

func (m *resourceListModel) syncWindow() {
	count := len(m.filteredIndexes)
	if count == 0 {
		m.cursor = 0
		m.windowStart = 0
		m.visibleRows = m.visibleRows[:0]
		m.table.SetRows(m.visibleRows)

		return
	}

	m.cursor = min(max(m.cursor, 0), count-1)
	windowSize := min(max(0, m.tableHeight-1), count)
	if windowSize == 0 {
		m.windowStart = m.cursor
		m.visibleRows = m.visibleRows[:0]
		m.table.SetRows(m.visibleRows)

		return
	}

	previousStart := m.windowStart
	m.windowStart = min(max(m.windowStart, 0), count-windowSize)
	if m.cursor < m.windowStart {
		m.windowStart = m.cursor
	} else if m.cursor >= m.windowStart+windowSize {
		m.windowStart = m.cursor - windowSize + 1
	}
	m.windowStart = min(max(m.windowStart, 0), count-windowSize)

	if previousStart != m.windowStart || len(m.visibleRows) != windowSize {
		m.visibleRows = m.visibleRows[:0]
		end := m.windowStart + windowSize
		for _, resourceIndex := range m.filteredIndexes[m.windowStart:end] {
			if resourceIndex >= 0 && resourceIndex < len(m.data.rows) {
				m.visibleRows = append(m.visibleRows, m.data.rows[resourceIndex])
			} else {
				m.visibleRows = append(m.visibleRows, nil)
			}
		}
		m.table.SetRows(m.visibleRows)
	}

	localCursor := m.cursor - m.windowStart
	if m.table.Cursor() != localCursor {
		m.table.SetCursor(localCursor)
	}
}
