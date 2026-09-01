package tui

import (
	"context"
	"fmt"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

func TestResourceListModelMaintainsDerivedState(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-web", Name: "web"},
		{Type: model.TypeEC2Volume, ID: "vol-data", Name: "data"},
		{Type: model.TypeEC2Instance, ID: "i-api", Name: "api"},
		{Type: model.TypeEC2Volume, ID: "vol-logs", Name: "logs"},
	}
	data, prepared := buildResourceData(context.Background(), resources, nil, nil, false)
	if !prepared {
		t.Fatal("리소스 목록 테스트 데이터를 준비하지 못함")
	}

	var list resourceListModel
	list.setResources(New(true), resources, data, 100, 4)
	assertResourceListInvariant(t, list)
	if got, want := list.totalCount(), 4; got != want {
		t.Fatalf("원본 개수 = %d, want %d", got, want)
	}
	if got, want := len(list.Rows()), 3; got != want {
		t.Fatalf("초기 viewport 행 수 = %d, want %d", got, want)
	}

	lastRowCell := &list.data.rows[3][0]
	preferredWidths := append([]int(nil), list.data.preferredWidths...)
	list.moveCursor(tea.KeyMsg{Type: tea.KeyEnd})
	list.resize(140, 3)
	assertResourceListInvariant(t, list)
	selected, ok := list.selected()
	if !ok || selected.ID != "vol-logs" {
		t.Fatalf("마지막 선택 = (%q, %v), want vol-logs", selected.ID, ok)
	}
	if &list.data.rows[3][0] != lastRowCell {
		t.Fatal("resize가 캐시된 행을 다시 만들었음")
	}
	if !slices.Equal(list.data.preferredWidths, preferredWidths) {
		t.Errorf("resize 후 선호 폭 = %v, want %v", list.data.preferredWidths, preferredWidths)
	}

	// 결과 수와 window 위치가 같은 필터끼리 바뀌어도 원본 행 매핑을 새로 만들어야 한다.
	list.applyFilter(model.TypeEC2Volume, "")
	assertResourceListInvariant(t, list)
	if got := list.filteredIndexes; !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("볼륨 필터 인덱스 = %v, want [1 3]", got)
	}
	list.applyFilter(model.TypeEC2Instance, "")
	assertResourceListInvariant(t, list)
	if got := list.filteredIndexes; !slices.Equal(got, []int{0, 2}) {
		t.Fatalf("인스턴스 필터 인덱스 = %v, want [0 2]", got)
	}
	if got := list.Rows(); len(got) != 2 || &got[0][0] != &list.data.rows[0][0] {
		t.Fatal("같은 크기의 새 필터 결과가 캐시된 원본 행으로 다시 매핑되지 않음")
	}
}

func TestResourceDetailReturnPreservesListSelection(t *testing.T) {
	t.Parallel()

	resources := make([]model.Resource, 100)
	for i := range resources {
		resources[i] = model.Resource{ID: fmt.Sprintf("id-%03d", i), Name: fmt.Sprintf("resource-%03d", i)}
	}
	m := newFilterFlowModel(resources)
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyPgDown})

	beforeCursor := m.resourceList.cursor
	beforeWindow := m.resourceList.windowStart
	beforeLocal := m.resourceList.table.Cursor()
	beforeResource, ok := m.resourceList.selected()
	if !ok {
		t.Fatal("상세 진입 전 선택 리소스가 없음")
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	afterResource, ok := m.resourceList.selected()
	if m.screen != ScreenList || !ok {
		t.Fatalf("상세 복귀 후 화면=%v 선택 존재=%v", m.screen, ok)
	}
	if m.resourceList.cursor != beforeCursor ||
		m.resourceList.windowStart != beforeWindow ||
		m.resourceList.table.Cursor() != beforeLocal ||
		afterResource.ID != beforeResource.ID {
		t.Errorf("상세 복귀 후 선택이 바뀜: cursor/window/local=%d/%d/%d, want %d/%d/%d",
			m.resourceList.cursor, m.resourceList.windowStart, m.resourceList.table.Cursor(),
			beforeCursor, beforeWindow, beforeLocal)
	}
}

func assertResourceListInvariant(t *testing.T, list resourceListModel) {
	t.Helper()

	for _, resourceIndex := range list.filteredIndexes {
		if resourceIndex < 0 || resourceIndex >= len(list.resources) || resourceIndex >= len(list.data.rows) {
			t.Fatalf("유효하지 않은 필터 인덱스 %d", resourceIndex)
		}
	}
	if list.filteredCount() == 0 {
		if list.cursor != 0 || list.windowStart != 0 || len(list.Rows()) != 0 {
			t.Fatalf("빈 결과 cursor/window/rows = %d/%d/%d", list.cursor, list.windowStart, len(list.Rows()))
		}

		return
	}
	if list.cursor < 0 || list.cursor >= list.filteredCount() {
		t.Fatalf("전역 커서 %d가 결과 범위 [0,%d)에 없음", list.cursor, list.filteredCount())
	}
	if got, want := list.table.Cursor(), list.cursor-list.windowStart; got != want {
		t.Fatalf("로컬 커서 = %d, want %d", got, want)
	}
	if got, limit := len(list.Rows()), max(0, list.tableHeight-1); got > limit {
		t.Fatalf("viewport 행 수 = %d, want <= %d", got, limit)
	}
	for i, row := range list.Rows() {
		resourceIndex := list.filteredIndexes[list.windowStart+i]
		if len(row) > 0 && &row[0] != &list.data.rows[resourceIndex][0] {
			t.Fatalf("viewport 행 %d가 원본 캐시 행 %d와 다름", i, resourceIndex)
		}
	}
}
