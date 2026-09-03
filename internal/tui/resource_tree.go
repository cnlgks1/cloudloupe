package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

// 리소스 선택은 화면 하나에서 끝난다.
//
// 이전에는 서비스 화면과 resource type 화면 두 단계였고, 타입이 하나뿐인 서비스는 두 번째
// 화면을 건너뛰었다. 그래서 카탈로그에 타입을 하나 추가하면 그 서비스의 화면 흐름이 바뀌었다.
// 수집기를 넣었을 뿐인데 사용자 경험이 변하는 구조였고, 화면 사이 상태를 맞추는 필드
// (지나온 화면, 뒤로 온 뒤 enter의 의미)도 그만큼 필요했다.
//
// 트리는 서비스 줄을 제자리에서 펼친다. 타입이 1개든 20개든 같은 규칙이고, 카탈로그가
// 늘어나면 줄이 늘어날 뿐 구조는 그대로다.
//
// 규모는 접힌 상태의 줄 수가 서비스 수와 같다는 점으로 감당한다. resource type이 수백 개가
// 되어도 첫 화면은 서비스 수만큼이고, 표는 보이는 창만 렌더링한다. 특정 타입을 찾을 때는
// 트리를 펼치는 대신 검색으로 좁힌다. 검색 중에는 서비스 열이 붙은 평면 목록이 된다.

// treeRowKind는 트리 줄의 종류다.
type treeRowKind int

const (
	// treeRowService는 펼칠 수 있는 AWS 서비스 줄이다.
	treeRowService treeRowKind = iota
	// treeRowType은 서비스 안의 resource type 줄이다.
	treeRowType
)

// treeRow는 화면에 보이는 줄 하나가 카탈로그의 무엇을 가리키는지 담는다.
//
// 표는 문자열만 들고 있으므로, 커서 위치에서 무엇을 조회할지 알려면 이 대응이 필요하다.
// 인덱스로 들고 있어서 라벨이 바뀌어도 대응이 깨지지 않는다.
type treeRow struct {
	kind     treeRowKind
	groupIdx int
	typeIdx  int
}

// resourceTree는 서비스와 resource type 선택 상태를 담는다.
//
// 카탈로그는 읽기만 한다. 펼침과 선택은 이 구조체가 소유하고, 표는 그 상태로부터 매번
// 다시 만든다. 표를 진실의 원천으로 삼으면 필터가 걸린 뒤 커서를 원본으로 역매핑해야 하는데,
// 그 대응이 어긋나면 사용자가 고른 것과 조회한 것이 달라진다.
type resourceTree struct {
	groups   []ResourceGroup
	expanded map[string]bool // 서비스 ID → 펼침 여부
	selected map[string]bool // resource type ID → 선택 여부
	query    string          // 검색어. 비어 있으면 트리, 있으면 평면 목록
	rows     []treeRow
	table    table.Model
}

// newResourceTree는 카탈로그로 선택 트리를 만든다. 처음에는 모두 접힌 상태다.
func newResourceTree(theme Theme, groups []ResourceGroup, width, height int) resourceTree {
	t := resourceTree{
		groups:   groups,
		expanded: make(map[string]bool, len(groups)),
		selected: make(map[string]bool),
	}
	t.rebuild(theme, width, height)

	return t
}

// setSelection은 외부에서 정한 선택 상태를 반영한다.
//
// 리전을 바꿔 선택을 초기화할 때와, 조회한 뒤 이 화면으로 돌아올 때 쓴다.
func (t *resourceTree) setSelection(theme Theme, types []string, width, height int) {
	t.selected = make(map[string]bool, len(types))
	for _, typeID := range types {
		t.selected[typeID] = true
	}
	t.rebuild(theme, width, height)
}

// selectedTypes는 고른 resource type을 카탈로그 순서로 반환한다.
//
// 카탈로그 순서를 쓰는 이유는 결과 정렬과 리포트 출력이 그 순서를 기준으로 삼기 때문이다.
// 사용자가 고른 순서를 쓰면 같은 선택인데 결과 순서가 달라진다.
func (t resourceTree) selectedTypes() []string {
	var out []string
	for _, group := range t.groups {
		for _, resourceType := range group.Types {
			if t.selected[resourceType.ID] {
				out = append(out, resourceType.ID)
			}
		}
	}

	return out
}

// cursorTypes는 선택이 없을 때 커서 위치가 가리키는 resource type을 반환한다.
//
// 서비스 줄이면 그 서비스 전체, 타입 줄이면 그 타입 하나다. 리전 선택과 같은 규칙이라
// 사용자가 새로 배울 것이 없다.
func (t resourceTree) cursorTypes() []string {
	row, ok := t.currentRow()
	if !ok {
		return nil
	}

	group := t.groups[row.groupIdx]
	if row.kind == treeRowService {
		return resourceGroupTypeIDs(group)
	}

	return []string{group.Types[row.typeIdx].ID}
}

// queryTypes는 enter를 눌렀을 때 조회할 resource type을 정한다.
func (t resourceTree) queryTypes() []string {
	if types := t.selectedTypes(); len(types) > 0 {
		return types
	}

	return t.cursorTypes()
}

// currentRow는 커서가 가리키는 줄을 반환한다.
func (t resourceTree) currentRow() (treeRow, bool) {
	i := t.table.Cursor()
	if i < 0 || i >= len(t.rows) {
		return treeRow{}, false
	}

	return t.rows[i], true
}

// expandable은 서비스가 펼칠 만한지 알려준다.
//
// resource type이 하나뿐이면 펼쳐도 서비스 줄과 같은 것을 한 줄 더 보여줄 뿐이다. 그 하나의
// Type ID는 서비스 줄에 함께 표시한다.
func (t resourceTree) expandable(group ResourceGroup) bool {
	return len(group.Types) > 1
}

// toggleExpand는 커서의 서비스를 펼치거나 접는다.
//
// 펼칠 수 없는 줄에서는 false를 반환해, 호출자가 → 를 조회로 넘길 수 있게 한다.
func (t *resourceTree) toggleExpand(theme Theme, want bool, width, height int) bool {
	row, ok := t.currentRow()
	if !ok || row.kind != treeRowService || t.query != "" {
		return false
	}

	group := t.groups[row.groupIdx]
	if !t.expandable(group) {
		return false
	}

	id := group.ID
	if t.expanded[id] == want {
		return false
	}

	t.expanded[id] = want
	t.rebuildKeepingCursor(theme, width, height)

	return true
}

// collapseAll은 모든 서비스를 접는다. 여러 서비스를 펼쳐 화면이 길어졌을 때 쓴다.
func (t *resourceTree) collapseAll(theme Theme, width, height int) {
	t.expanded = make(map[string]bool, len(t.groups))
	t.rebuildKeepingCursor(theme, width, height)
}

// toggleSelect는 커서 줄의 선택을 뒤집는다.
//
// 서비스 줄에서는 그 서비스 전체를 켜거나 끈다. 일부만 선택된 상태에서는 전체 선택으로
// 올린다. 절반 선택된 것을 다시 눌러 전부 지우면 방금 고른 것이 사라진 것처럼 보인다.
func (t *resourceTree) toggleSelect(theme Theme, width, height int) {
	row, ok := t.currentRow()
	if !ok {
		return
	}

	group := t.groups[row.groupIdx]

	if row.kind == treeRowType {
		id := group.Types[row.typeIdx].ID
		if t.selected[id] {
			delete(t.selected, id)
		} else {
			t.selected[id] = true
		}
		t.rebuildKeepingCursor(theme, width, height)

		return
	}

	if t.serviceSelection(group) == selectionAll {
		for _, resourceType := range group.Types {
			delete(t.selected, resourceType.ID)
		}
	} else {
		for _, resourceType := range group.Types {
			t.selected[resourceType.ID] = true
		}
	}
	t.rebuildKeepingCursor(theme, width, height)
}

// selectVisible은 지금 보이는 줄의 resource type을 모두 선택한다.
//
// 검색으로 좁힌 뒤에만 동작한다. 필터 없이 전부 선택하면 리소스 타입 수 × 리전 수만큼
// 작업이 만들어져 사용자가 의도하지 않은 대량 조회가 된다.
func (t *resourceTree) selectVisible(theme Theme, width, height int) bool {
	if t.query == "" {
		return false
	}

	for _, row := range t.rows {
		if row.kind != treeRowType {
			continue
		}
		t.selected[t.groups[row.groupIdx].Types[row.typeIdx].ID] = true
	}
	t.rebuildKeepingCursor(theme, width, height)

	return true
}

// clearSelection은 선택을 모두 지운다.
func (t *resourceTree) clearSelection(theme Theme, width, height int) {
	t.selected = make(map[string]bool)
	t.rebuildKeepingCursor(theme, width, height)
}

// setQuery는 검색어를 바꾼다. 검색 중에는 트리를 접고 평면 목록으로 보여준다.
func (t *resourceTree) setQuery(theme Theme, query string, width, height int) {
	t.query = strings.TrimSpace(query)
	t.rebuild(theme, width, height)
}

// update는 커서 이동을 표에 위임한다.
func (t *resourceTree) update(msg tea.Msg) {
	t.table, _ = t.table.Update(msg)
}

// resize는 창 크기 변경을 반영한다.
func (t *resourceTree) resize(theme Theme, width, height int) {
	t.rebuildKeepingCursor(theme, width, height)
}

// View는 표를 렌더링한다.
func (t resourceTree) View() string {
	return t.table.View()
}

// selectionState는 서비스의 선택 상태다.
type selectionState int

const (
	selectionNone selectionState = iota
	selectionSome
	selectionAll
)

func (t resourceTree) serviceSelection(group ResourceGroup) selectionState {
	if len(group.Types) == 0 {
		return selectionNone
	}

	count := 0
	for _, resourceType := range group.Types {
		if t.selected[resourceType.ID] {
			count++
		}
	}

	switch count {
	case 0:
		return selectionNone
	case len(group.Types):
		return selectionAll
	default:
		return selectionSome
	}
}

// counts는 상단에 보여줄 규모를 반환한다.
//
// 조회 전에 몇 종을 고른 상태인지 보이지 않으면, 접힌 서비스 안의 선택을 잊고 의도보다 큰
// 조회를 시작하게 된다.
func (t resourceTree) counts() (services, types, selected, shown int) {
	for _, group := range t.groups {
		types += len(group.Types)
	}
	for _, row := range t.rows {
		if row.kind == treeRowType {
			shown++
		}
	}

	return len(t.groups), types, len(t.selected), shown
}

// rebuild는 현재 상태로 줄 목록과 표를 다시 만든다. 커서는 맨 위로 간다.
func (t *resourceTree) rebuild(theme Theme, width, height int) {
	t.rows = t.buildRows()
	t.table = t.buildTable(theme, width, height)
}

// rebuildKeepingCursor는 표를 다시 만들되 커서 위치를 유지한다.
//
// 펼침이나 선택 때문에 줄이 늘거나 줄면 커서가 튄다. 방금 조작한 줄에 머물러야 연속으로
// 여러 개를 고를 수 있다.
func (t *resourceTree) rebuildKeepingCursor(theme Theme, width, height int) {
	before, ok := t.currentRow()
	t.rebuild(theme, width, height)

	if !ok {
		return
	}

	for i, row := range t.rows {
		if row == before {
			t.table.SetCursor(i)

			return
		}
	}

	// 접어서 사라진 줄이면 그 서비스 줄로 올린다.
	for i, row := range t.rows {
		if row.kind == treeRowService && row.groupIdx == before.groupIdx {
			t.table.SetCursor(i)

			return
		}
	}
}

func (t resourceTree) buildRows() []treeRow {
	if t.query != "" {
		return t.buildFilteredRows()
	}

	rows := make([]treeRow, 0, len(t.groups)*2)
	for groupIdx, group := range t.groups {
		rows = append(rows, treeRow{kind: treeRowService, groupIdx: groupIdx})
		if !t.expandable(group) || !t.expanded[group.ID] {
			continue
		}
		for typeIdx := range group.Types {
			rows = append(rows, treeRow{kind: treeRowType, groupIdx: groupIdx, typeIdx: typeIdx})
		}
	}

	return rows
}

// buildFilteredRows는 검색어에 맞는 resource type만 평면으로 모은다.
//
// 서비스 줄을 넣지 않는 이유는, 걸러진 결과에서는 서비스가 열로 붙어 이미 보이기 때문이다.
// 서비스 100개를 펼쳐가며 찾는 대신 세 글자로 도달하게 하는 것이 이 모드의 목적이다.
func (t resourceTree) buildFilteredRows() []treeRow {
	tokens := strings.Fields(strings.ToLower(t.query))

	var rows []treeRow
	for groupIdx, group := range t.groups {
		for typeIdx, resourceType := range group.Types {
			haystack := strings.ToLower(
				group.Label + " " + group.ID + " " + resourceType.Label + " " + resourceType.ID)

			matched := true
			for _, token := range tokens {
				if !strings.Contains(haystack, token) {
					matched = false

					break
				}
			}
			if matched {
				rows = append(rows, treeRow{kind: treeRowType, groupIdx: groupIdx, typeIdx: typeIdx})
			}
		}
	}

	return rows
}

// 열 폭을 균등 배분하지 않고 직접 잡는다.
//
// 균등 배분은 선택 표시와 개수처럼 좁아도 되는 열에 과한 폭을 주고, 정작 잘리면 안 되는
// Type ID를 좁게 만든다. Type ID는 aws CLI와 리포트에서 그대로 쓰는 값이라 잘리면 쓸모가 없다.
const (
	treeMarkWidth  = 4
	treeCountWidth = 16 // "Resource types" 머리글이 들어가는 최소 폭
	treeLabelWidth = 26 // 들여쓴 resource type 이름까지 담는 폭
	treeIDMinWidth = 24
)

func (t resourceTree) buildTable(theme Theme, width, height int) table.Model {
	titles := []string{"", serviceColumn, resourceTypeColumn, typeIDColumn}
	if t.query == "" {
		// 트리 모드에서는 서비스와 타입 이름이 한 열에 들여쓰기로 들어간다.
		titles = []string{"", serviceColumn, typeCountColumn, typeIDColumn}
	}

	columns := layoutColumns(titles, width)
	if len(columns) == 4 {
		usable := max(width-2, treeMarkWidth+treeLabelWidth+treeCountWidth+treeIDMinWidth)

		columns[0].Width = treeMarkWidth
		columns[1].Width = treeLabelWidth
		columns[2].Width = treeCountWidth
		if t.query != "" {
			// 검색 모드에서는 세 번째 열이 개수가 아니라 이름이므로 좁히지 않는다.
			columns[1].Width = 18
			columns[2].Width = treeLabelWidth - 4
		}
		columns[3].Width = max(
			usable-columns[0].Width-columns[1].Width-columns[2].Width, treeIDMinWidth)
	}

	rows := make([]table.Row, 0, len(t.rows))
	for _, row := range t.rows {
		rows = append(rows, t.renderRow(theme, row))
	}

	return newDataTable(theme, columns, rows, height)
}

func (t resourceTree) renderRow(theme Theme, row treeRow) table.Row {
	group := t.groups[row.groupIdx]

	if row.kind == treeRowService {
		// 펼칠 것이 없는 서비스에는 펼침 표시를 두지 않는다. 트리에서 잎 노드에 펼침
		// 손잡이를 그리지 않는 것과 같다. 대신 하나뿐인 Type ID를 이 줄에 바로 보여주므로
		// 펼치지 않아도 숨는 정보가 없다.
		marker := "  "
		typeID := ""

		switch {
		case !t.expandable(group):
			if len(group.Types) == 1 {
				typeID = group.Types[0].ID
			}
		case t.expanded[group.ID]:
			marker = theme.Glyphs.Expanded + " "
		default:
			marker = theme.Glyphs.Collapsed + " "
		}

		return table.Row{
			t.selectionMark(theme, t.serviceSelection(group)),
			marker + group.Label,
			strconv.Itoa(len(group.Types)),
			typeID,
		}
	}

	resourceType := group.Types[row.typeIdx]
	mark := t.selectionMark(theme, selectionNone)
	if t.selected[resourceType.ID] {
		mark = t.selectionMark(theme, selectionAll)
	}

	if t.query != "" {
		// 평면 모드에서는 서비스를 열로 보여준다.
		return table.Row{mark, group.Label, resourceType.Label, resourceType.ID}
	}

	return table.Row{mark, "  " + resourceType.Label, "", resourceType.ID}
}

func (t resourceTree) selectionMark(theme Theme, state selectionState) string {
	switch state {
	case selectionAll:
		return theme.Glyphs.Healthy
	case selectionSome:
		return theme.Glyphs.Partial
	default:
		return " "
	}
}
