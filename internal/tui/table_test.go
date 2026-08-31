package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

func testResourceGroups(types []ResourceType) []ResourceGroup {
	return []ResourceGroup{{ID: "test", Label: "테스트", Types: types}}
}

func TestBuildTableRegionColumnPolicy(t *testing.T) {
	t.Parallel()

	types := []ResourceType{{
		ID:      model.TypeEC2Volume,
		Label:   "EBS 볼륨",
		Columns: []string{"타입"},
	}}

	t.Run("단일 리전은 숨김", func(t *testing.T) {
		t.Parallel()

		resources := []model.Resource{
			{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2", Fields: []model.Field{{Key: "타입", Value: "gp3"}}},
			{Type: model.TypeRoute53RecordSet, ID: "example.com|A", Region: "global"},
		}

		showRegion := (Model{chosenRegions: []string{"ap-northeast-2"}}).shouldShowRegion()
		table := buildTable(New(true), resources, resources, testResourceGroups(types), []string{model.TypeEC2Volume}, showRegion, 120, 20)
		assertTableShape(t, table, false)
	})

	t.Run("다중 리전 선택은 한 리전만 결과가 있어도 표시", func(t *testing.T) {
		t.Parallel()

		resources := []model.Resource{
			{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2", Fields: []model.Field{{Key: "타입", Value: "gp3"}}},
		}

		showRegion := (Model{chosenRegions: []string{"ap-northeast-2", "us-east-1"}}).shouldShowRegion()
		table := buildTable(New(true), resources, resources, testResourceGroups(types), []string{model.TypeEC2Volume}, showRegion, 120, 20)
		assertTableShape(t, table, true)
	})

	t.Run("필터 후에도 전체 결과 스키마 유지", func(t *testing.T) {
		t.Parallel()

		all := []model.Resource{
			{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2", Fields: []model.Field{{Key: "타입", Value: "gp3"}}},
			{Type: model.TypeEC2Volume, ID: "vol-2", Region: "us-east-1", Fields: []model.Field{{Key: "타입", Value: "gp2"}}},
		}
		visible := all[:1]

		showRegion := (Model{chosenRegions: []string{"ap-northeast-2", "us-east-1"}}).shouldShowRegion()
		table := buildTable(New(true), visible, all, testResourceGroups(types), []string{model.TypeEC2Volume}, showRegion, 120, 20)
		assertTableShape(t, table, true)

		regionColumn := columnIndex(table, "리전")
		if regionColumn < 0 {
			t.Fatal("리전 열이 없음")
		}
		if got := table.Rows()[0][regionColumn]; got != "ap-northeast-2" {
			t.Errorf("리전 셀 = %q, want %q", got, "ap-northeast-2")
		}
	})
}

func TestBuildTableKeepsColumnsWhenFilterHasNoRows(t *testing.T) {
	t.Parallel()

	all := []model.Resource{{
		Type:   model.TypeEC2Volume,
		ID:     "vol-1",
		Region: "ap-northeast-2",
		Fields: []model.Field{{Key: "타입", Value: "gp3"}},
	}}
	types := []ResourceType{{ID: model.TypeEC2Volume, Label: "EBS 볼륨", Columns: []string{"타입"}}}

	table := buildTable(New(true), nil, all, testResourceGroups(types), []string{model.TypeEC2Volume}, false, 120, 20)
	if len(table.Rows()) != 0 {
		t.Errorf("Rows() = %d, want 0", len(table.Rows()))
	}

	want := []string{"이름", "타입"}
	got := columnTitles(table)
	if len(got) != len(want) {
		t.Fatalf("열 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("열 %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResourceListViewUsesDedicatedFilterLine(t *testing.T) {
	t.Parallel()

	m := Model{
		theme:           New(true),
		listCaption:     "리소스 2개",
		allResourceRows: []model.Resource{{ID: "one"}, {ID: "two"}},
		resourceRows:    []model.Resource{{ID: "one"}, {ID: "two"}},
	}
	m.resourceTable = buildTable(m.theme, m.resourceRows, m.allResourceRows, nil, nil, false, 100, 10)

	lines := strings.Split(m.resourceListView(), "\n")
	if len(lines) < 3 {
		t.Fatalf("렌더링 줄 수 = %d, want >= 3", len(lines))
	}
	if strings.Contains(lines[1], "/ 필터") {
		t.Errorf("필터가 제목 줄에 있음: %q", lines[1])
	}
	if !strings.Contains(lines[2], "/ 필터") {
		t.Errorf("독립 필터 줄이 없음: %q", lines[2])
	}

	if got, want := (Model{height: 24}).resourceListHeight(), 19; got != want {
		t.Errorf("resourceListHeight() = %d, want %d", got, want)
	}
}

func TestFilterResourcesMatchesAllTokensAndDetails(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{
			Type:   model.TypeEC2Instance,
			ID:     "i-web",
			Name:   "production-web",
			Region: "ap-northeast-2",
			Status: "running",
			Fields: []model.Field{{Key: "인스턴스 타입", Value: "t3.small"}},
			Tags:   []model.Field{{Key: "Team", Value: "platform"}},
		},
		{Type: model.TypeEC2Instance, ID: "i-dev", Name: "development", Region: "us-east-1", Status: "stopped"},
	}

	filtered := filterResources(resources, "PRODUCTION t3.small platform")
	if len(filtered) != 1 || filtered[0].ID != "i-web" {
		t.Errorf("필터 결과 = %+v, want i-web", filtered)
	}
}

func assertTableShape(t *testing.T, tableModel table.Model, showRegion bool) {
	t.Helper()

	titles := columnTitles(tableModel)
	hasRegion := false
	for _, title := range titles {
		if title == "리전" {
			hasRegion = true
			break
		}
	}
	if hasRegion != showRegion {
		t.Errorf("리전 열 표시 = %v, want %v (열: %v)", hasRegion, showRegion, titles)
	}

	for i, row := range tableModel.Rows() {
		if len(row) != len(tableModel.Columns()) {
			t.Errorf("행 %d 셀 수 = %d, 열 수 = %d", i, len(row), len(tableModel.Columns()))
		}
	}
}

func columnTitles(tableModel table.Model) []string {
	columns := tableModel.Columns()
	titles := make([]string, 0, len(columns))
	for _, column := range columns {
		titles = append(titles, column.Title)
	}
	return titles
}

func columnIndex(tableModel table.Model, title string) int {
	for i, column := range tableModel.Columns() {
		if column.Title == title {
			return i
		}
	}

	return -1
}

func TestResourceFilterUpdateFlow(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-web", Name: "production-web", Region: "ap-northeast-2"},
		{Type: model.TypeEC2Instance, ID: "i-dev", Name: "development", Region: "ap-northeast-2"},
	}

	t.Run("실시간 적용과 취소", func(t *testing.T) {
		t.Parallel()

		m := newFilterFlowModel(resources)
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("production")})
		if len(m.resourceRows) != 1 || m.resourceRows[0].ID != "i-web" {
			t.Errorf("실시간 필터 결과 = %+v, want i-web", m.resourceRows)
		}

		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.filtering || m.filterQuery != "" || len(m.resourceRows) != 2 {
			t.Errorf("취소 후 상태 filtering=%v query=%q rows=%d", m.filtering, m.filterQuery, len(m.resourceRows))
		}
	})

	t.Run("적용 후 상세 매핑", func(t *testing.T) {
		t.Parallel()

		m := newFilterFlowModel(resources)
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("development")})
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if m.filtering || m.filterQuery != "development" {
			t.Errorf("적용 후 filtering=%v query=%q", m.filtering, m.filterQuery)
		}

		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if m.screen != ScreenDetail || !strings.Contains(m.View(), "development") {
			t.Errorf("필터 행 상세 화면 = %v, 출력:\n%s", m.screen, m.View())
		}
	})

	t.Run("0건 결과에서 Enter 안전", func(t *testing.T) {
		t.Parallel()

		m := newFilterFlowModel(resources)
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("no-such-resource")})
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if m.screen != ScreenList || len(m.resourceRows) != 0 {
			t.Errorf("0건 Enter 후 화면=%v rows=%d", m.screen, len(m.resourceRows))
		}
	})
}

func newFilterFlowModel(resources []model.Resource) Model {
	input := textinput.New()
	input.Prompt = "/ "

	m := Model{
		theme:           New(true),
		keys:            defaultKeys(),
		screen:          ScreenList,
		width:           100,
		height:          24,
		allResourceRows: append([]model.Resource(nil), resources...),
		resourceRows:    append([]model.Resource(nil), resources...),
		filterInput:     input,
		detail:          viewport.New(100, 19),
		chosenRegions:   []string{"ap-northeast-2"},
	}
	m.resourceTable = buildTable(m.theme, m.resourceRows, m.allResourceRows, nil,
		m.chosenTypes, m.shouldShowRegion(), m.width, m.resourceListHeight())

	return m
}

func updateFilterModel(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)

	return next.(Model)
}

func TestResourceKindFilterFlowAndTextSearch(t *testing.T) {
	t.Parallel()

	groups := []ResourceGroup{{
		ID:    "ec2",
		Label: "EC2",
		Types: []ResourceType{
			{
				ID:             model.TypeEC2Instance,
				Label:          "인스턴스",
				Columns:        []string{"인스턴스 타입"},
				SummaryColumns: []string{"인스턴스 타입"},
			},
			{
				ID:             model.TypeEC2Volume,
				Label:          "볼륨",
				Columns:        []string{"타입", "크기(GiB)"},
				SummaryColumns: []string{"타입", "크기(GiB)"},
			},
		},
	}}
	resources := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-1", Name: "shared-web", Fields: []model.Field{{Key: "인스턴스 타입", Value: "t3.small"}}},
		{Type: model.TypeEC2Volume, ID: "vol-1", Name: "shared-data", Fields: []model.Field{{Key: "타입", Value: "gp3"}, {Key: "크기(GiB)", Value: "100"}}},
		{Type: model.TypeEC2Volume, ID: "vol-2", Name: "logs", Fields: []model.Field{{Key: "타입", Value: "gp3"}, {Key: "크기(GiB)", Value: "50"}}},
	}
	m := newResourceKindFilterModel(groups, resources)

	if !strings.Contains(m.View(), "종류: 전체") || !strings.Contains(m.View(), "t: 변경") {
		t.Fatalf("혼합 목록에 종류 필터 줄이 없음:\n%s", m.View())
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if m.screen != ScreenResourceKind {
		t.Fatalf("t 입력 후 화면 = %v, want 종류 필터", m.screen)
	}
	if got := m.kindTable.Rows(); len(got) != 3 ||
		got[0][0] != "전체" || got[0][1] != "3" ||
		got[1][0] != "인스턴스" || got[1][1] != "1" ||
		got[2][0] != "볼륨" || got[2][1] != "2" {
		t.Fatalf("종류 옵션 = %v", got)
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != ScreenList || m.resourceKindFilter != "" || len(m.resourceRows) != 3 {
		t.Fatalf("종류 필터 취소 후 화면=%v 필터=%q rows=%d", m.screen, m.resourceKindFilter, len(m.resourceRows))
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != ScreenList || m.resourceKindFilter != model.TypeEC2Volume || len(m.resourceRows) != 2 {
		t.Fatalf("볼륨 적용 후 화면=%v 필터=%q rows=%d", m.screen, m.resourceKindFilter, len(m.resourceRows))
	}
	for _, resource := range m.resourceRows {
		if resource.Type != model.TypeEC2Volume {
			t.Errorf("종류 필터 결과에 다른 타입이 포함됨: %s", resource.Type)
		}
	}
	wantTitles := []string{"종류", "이름", "ID", "주요 정보"}
	if got := columnTitles(m.resourceTable); !slices.Equal(got, wantTitles) {
		t.Errorf("종류 필터 후 열 = %v, want %v", got, wantTitles)
	}

	// 종류=볼륨 상태에서 검색을 추가하면 두 조건을 모두 만족하는 행만 남는다.
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shared")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.resourceRows) != 1 || m.resourceRows[0].ID != "vol-1" {
		t.Errorf("종류+검색 결과 = %+v, want vol-1", m.resourceRows)
	}

	// 종류를 전체로 되돌려도 검색어는 유지되어 인스턴스와 볼륨 두 행이 보인다.
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.resourceKindFilter != "" || len(m.resourceRows) != 2 {
		t.Errorf("전체 종류+검색 결과 필터=%q rows=%d", m.resourceKindFilter, len(m.resourceRows))
	}
}

func TestResourceKindFilterIsHiddenForSingleKind(t *testing.T) {
	t.Parallel()

	groups := []ResourceGroup{{
		ID:    "ec2",
		Label: "EC2",
		Types: []ResourceType{{ID: model.TypeEC2Instance, Label: "인스턴스"}},
	}}
	resources := []model.Resource{{Type: model.TypeEC2Instance, ID: "i-1", Name: "web"}}
	m := newResourceKindFilterModel(groups, resources)

	if strings.Contains(m.View(), "t: 변경") {
		t.Fatalf("단일 종류 목록에 종류 필터가 표시됨:\n%s", m.View())
	}
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if m.screen != ScreenList {
		t.Errorf("단일 종류에서 t 입력 후 화면 = %v, want 목록", m.screen)
	}
}

func newResourceKindFilterModel(groups []ResourceGroup, resources []model.Resource) Model {
	m := newFilterFlowModel(resources)
	m.deps.ResourceGroups = groups
	m.chosenTypes = resourceGroupTypeIDs(groups[0])
	m.resourceKinds = collectResourceKinds(groups, resources)
	m.resourceTable = buildTable(m.theme, m.resourceRows, m.allResourceRows, groups,
		m.chosenTypes, m.shouldShowRegion(), m.width, m.resourceListHeight())

	return m
}

func TestTargetGroupTableRemovesRedundantColumnsAndAdjustsWidths(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{
			Type: model.TypeELBv2TargetGroup,
			ID:   "season2-tgxecr-alb-tg-http-26656",
			Name: "season2-tgxecr-alb-tg-http-26656",
			Fields: []model.Field{
				{Key: "프로토콜", Value: "HTTP"},
				{Key: "포트", Value: "26656"},
				{Key: "타깃 종류", Value: "instance"},
				{Key: "타깃 수", Value: "1"},
			},
		},
		{
			Type: model.TypeELBv2TargetGroup,
			ID:   "web-monitoring-target-group",
			Name: "web-monitoring-target-group",
			Fields: []model.Field{
				{Key: "프로토콜", Value: "HTTP"},
				{Key: "포트", Value: "3000"},
				{Key: "타깃 종류", Value: "instance"},
				{Key: "타깃 수", Value: "0"},
			},
		},
	}
	types := []ResourceType{{
		ID:      model.TypeELBv2TargetGroup,
		Label:   "타깃 그룹",
		Columns: []string{"프로토콜", "포트", "타깃 종류", "타깃 수"},
	}}

	tableModel := buildTable(New(true), resources, resources, testResourceGroups(types),
		[]string{model.TypeELBv2TargetGroup}, false, 120, 20)
	wantTitles := []string{"이름", "프로토콜", "포트", "타깃 종류", "타깃 수"}
	if got := columnTitles(tableModel); !slices.Equal(got, wantTitles) {
		t.Errorf("열 = %v, want %v", got, wantTitles)
	}

	nameWidth := tableModel.Columns()[columnIndex(tableModel, "이름")].Width
	portWidth := tableModel.Columns()[columnIndex(tableModel, "포트")].Width
	targetCountWidth := tableModel.Columns()[columnIndex(tableModel, "타깃 수")].Width
	if nameWidth <= portWidth || nameWidth <= targetCountWidth {
		t.Errorf("열 너비 이름=%d 포트=%d 타깃 수=%d, 이름이 더 넓어야 함",
			nameWidth, portWidth, targetCountWidth)
	}
	assertTableShape(t, tableModel, false)
}

func TestServiceGroupTableUsesFriendlyTypeAndSummary(t *testing.T) {
	t.Parallel()

	groups := []ResourceGroup{{
		ID:    "ec2",
		Label: "EC2",
		Types: []ResourceType{
			{
				ID:             model.TypeEC2Instance,
				Label:          "인스턴스",
				Columns:        []string{"인스턴스 타입", "사설 IP", "공인 IP"},
				SummaryColumns: []string{"인스턴스 타입", "사설 IP"},
			},
			{
				ID:             model.TypeEC2Volume,
				Label:          "볼륨",
				Columns:        []string{"타입", "크기(GiB)", "가용 영역"},
				SummaryColumns: []string{"타입", "크기(GiB)"},
			},
		},
	}}
	resources := []model.Resource{
		{
			Type:   model.TypeEC2Instance,
			ID:     "i-123",
			Name:   "web",
			Status: "running",
			Fields: []model.Field{
				{Key: "인스턴스 타입", Value: "t3.small"},
				{Key: "사설 IP", Value: "10.0.0.10"},
			},
		},
		{
			Type:   model.TypeEC2Volume,
			ID:     "vol-123",
			Name:   "data",
			Status: "available",
			Fields: []model.Field{
				{Key: "타입", Value: "gp3"},
				{Key: "크기(GiB)", Value: "100"},
			},
		},
	}

	tableModel := buildTable(New(true), resources, resources, groups,
		[]string{model.TypeEC2Instance, model.TypeEC2Volume}, false, 140, 20)
	wantTitles := []string{"종류", "이름", "ID", "상태", "주요 정보"}
	if got := columnTitles(tableModel); !slices.Equal(got, wantTitles) {
		t.Errorf("열 = %v, want %v", got, wantTitles)
	}

	rows := tableModel.Rows()
	if got := rows[0][columnIndex(tableModel, "종류")]; got != "인스턴스" {
		t.Errorf("인스턴스 종류 = %q, want %q", got, "인스턴스")
	}
	if got := rows[1][columnIndex(tableModel, "종류")]; got != "볼륨" {
		t.Errorf("볼륨 종류 = %q, want %q", got, "볼륨")
	}
	if got := rows[0][columnIndex(tableModel, "주요 정보")]; got != "인스턴스 타입 t3.small · 사설 IP 10.0.0.10" {
		t.Errorf("인스턴스 주요 정보 = %q", got)
	}
	if got := rows[1][columnIndex(tableModel, "주요 정보")]; got != "타입 gp3 · 크기(GiB) 100" {
		t.Errorf("볼륨 주요 정보 = %q", got)
	}
	assertTableShape(t, tableModel, false)
}

func TestMixedResourceTableKeepsDisambiguatingColumns(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{Type: model.TypeELBv2TargetGroup, ID: "target-group", Name: "target-group"},
		{Type: model.TypeEC2Instance, ID: "i-123", Name: "web", Status: "running"},
	}

	tableModel := buildTable(New(true), resources, resources, nil,
		[]string{model.TypeELBv2TargetGroup, model.TypeEC2Instance}, false, 100, 20)
	wantTitles := []string{"종류", "이름", "ID", "상태"}
	if got := columnTitles(tableModel); !slices.Equal(got, wantTitles) {
		t.Errorf("열 = %v, want %v", got, wantTitles)
	}
	assertTableShape(t, tableModel, false)
}
