package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func testResourceGroups(types []ResourceType) []ResourceGroup {
	return []ResourceGroup{{ID: "test", Label: "테스트", Types: types}}
}

func buildTestTable(
	theme Theme,
	resources, schemaResources []model.Resource,
	groups []ResourceGroup,
	selectedTypes []string,
	showRegion bool,
	width, height int,
) table.Model {
	data, prepared := buildResourceData(context.Background(), schemaResources, groups, selectedTypes, showRegion)
	if !prepared {
		panic("테스트 리소스 데이터를 준비하지 못함")
	}
	rows := data.rows
	if len(resources) < len(rows) {
		rows = rows[:len(resources)]
	}

	return newResourceTable(theme, data, rows, width, height)
}

func TestBuildTableRegionColumnPolicy(t *testing.T) {
	t.Parallel()

	types := []ResourceType{{
		ID:      model.TypeEC2Volume,
		Label:   "EBS 볼륨",
		Columns: []string{"VolumeType"},
	}}

	t.Run("단일 리전은 숨김", func(t *testing.T) {
		t.Parallel()

		resources := []model.Resource{
			{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2", Fields: []model.Field{{Key: "VolumeType", Value: "gp3"}}},
			{Type: model.TypeRoute53RecordSet, ID: "example.com|A", Region: "global"},
		}

		showRegion := (Model{chosenRegions: []string{"ap-northeast-2"}}).shouldShowRegion()
		table := buildTestTable(New(true), resources, resources, testResourceGroups(types), []string{model.TypeEC2Volume}, showRegion, 120, 20)
		assertTableShape(t, table, false)
	})

	t.Run("다중 리전 선택은 한 리전만 결과가 있어도 표시", func(t *testing.T) {
		t.Parallel()

		resources := []model.Resource{
			{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2", Fields: []model.Field{{Key: "VolumeType", Value: "gp3"}}},
		}

		showRegion := (Model{chosenRegions: []string{"ap-northeast-2", "us-east-1"}}).shouldShowRegion()
		table := buildTestTable(New(true), resources, resources, testResourceGroups(types), []string{model.TypeEC2Volume}, showRegion, 120, 20)
		assertTableShape(t, table, true)
	})

	t.Run("필터 후에도 전체 결과 스키마 유지", func(t *testing.T) {
		t.Parallel()

		all := []model.Resource{
			{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2", Fields: []model.Field{{Key: "VolumeType", Value: "gp3"}}},
			{Type: model.TypeEC2Volume, ID: "vol-2", Region: "us-east-1", Fields: []model.Field{{Key: "VolumeType", Value: "gp2"}}},
		}
		visible := all[:1]

		showRegion := (Model{chosenRegions: []string{"ap-northeast-2", "us-east-1"}}).shouldShowRegion()
		table := buildTestTable(New(true), visible, all, testResourceGroups(types), []string{model.TypeEC2Volume}, showRegion, 120, 20)
		assertTableShape(t, table, true)

		regionColumn := columnIndex(table, "Region")
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
		Fields: []model.Field{{Key: "VolumeType", Value: "gp3"}},
	}}
	types := []ResourceType{{ID: model.TypeEC2Volume, Label: "EBS 볼륨", Columns: []string{"VolumeType"}}}

	table := buildTestTable(New(true), nil, all, testResourceGroups(types), []string{model.TypeEC2Volume}, false, 120, 20)
	if len(table.Rows()) != 0 {
		t.Errorf("Rows() = %d, want 0", len(table.Rows()))
	}

	want := []string{"Name", "VolumeType"}
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

	resources := []model.Resource{{ID: "one"}, {ID: "two"}}
	m := Model{
		theme:       New(true),
		listCaption: "리소스 2개",
	}
	data, prepared := buildResourceData(context.Background(), resources, nil, nil, false)
	if !prepared {
		t.Fatal("목록 데이터를 준비하지 못함")
	}
	m.resourceList.setResources(m.theme, resources, data, 100, 10)

	lines := strings.Split(m.resourceListView(), "\n")
	if len(lines) < 3 {
		t.Fatalf("렌더링 줄 수 = %d, want >= 3", len(lines))
	}
	if strings.Contains(lines[1], "/ filter") {
		t.Errorf("필터가 제목 줄에 있음: %q", lines[1])
	}
	if !strings.Contains(lines[2], "/ filter") {
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

	searchText := resourceSearchText(resources[0])
	if !searchTextMatches(searchText, strings.Fields(strings.ToLower("PRODUCTION t3.small platform"))) {
		t.Error("검색 문자열이 이름, 필드, 태그의 모든 토큰과 일치하지 않음")
	}
	if searchTextMatches(resourceSearchText(resources[1]), []string{"production"}) {
		t.Error("일치하지 않는 리소스가 검색됨")
	}
}

func assertTableShape(t *testing.T, tableModel table.Model, showRegion bool) {
	t.Helper()

	titles := columnTitles(tableModel)
	hasRegion := false
	for _, title := range titles {
		if title == "Region" {
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
		if len(m.resourceList.filteredIndexes) != 1 || m.resourceList.resources[m.resourceList.filteredIndexes[0]].ID != "i-web" {
			t.Errorf("실시간 필터 인덱스 = %v, want i-web", m.resourceList.filteredIndexes)
		}

		m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.filtering || m.filterQuery != "" || len(m.resourceList.filteredIndexes) != 2 {
			t.Errorf("취소 후 상태 filtering=%v query=%q rows=%d", m.filtering, m.filterQuery, len(m.resourceList.filteredIndexes))
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
		if m.screen != ScreenList || len(m.resourceList.filteredIndexes) != 0 {
			t.Errorf("0건 Enter 후 화면=%v rows=%d", m.screen, len(m.resourceList.filteredIndexes))
		}
	})
}

func newFilterFlowModel(resources []model.Resource) Model {
	input := textinput.New()
	input.Prompt = "/ "

	m := Model{
		theme:         New(true),
		keys:          defaultKeys(),
		screen:        ScreenList,
		width:         100,
		height:        24,
		filterInput:   input,
		detail:        viewport.New(100, 19),
		chosenRegions: []string{"ap-northeast-2"},
	}
	resources = append([]model.Resource(nil), resources...)
	data, prepared := buildResourceData(
		context.Background(), resources, nil, m.chosenTypes, m.shouldShowRegion())
	if !prepared {
		panic("테스트 리소스 데이터를 준비하지 못함")
	}
	m.resourceList.setResources(m.theme, resources, data, m.width, m.resourceListHeight())

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
				Label:          "Instances",
				Columns:        []string{"인스턴스 타입"},
				SummaryColumns: []string{"인스턴스 타입"},
			},
			{
				ID:             model.TypeEC2Volume,
				Label:          "Volumes",
				Columns:        []string{"VolumeType", "Size"},
				SummaryColumns: []string{"VolumeType", "Size"},
			},
		},
	}}
	resources := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-1", Name: "shared-web", Fields: []model.Field{{Key: "인스턴스 타입", Value: "t3.small"}}},
		{Type: model.TypeEC2Volume, ID: "vol-1", Name: "shared-data", Fields: []model.Field{{Key: "VolumeType", Value: "gp3"}, {Key: "Size", Value: "100"}}},
		{Type: model.TypeEC2Volume, ID: "vol-2", Name: "logs", Fields: []model.Field{{Key: "VolumeType", Value: "gp3"}, {Key: "Size", Value: "50"}}},
	}
	m := newResourceKindFilterModel(groups, resources)

	if !strings.Contains(m.View(), "Kind: All") || !strings.Contains(m.View(), "t: change") {
		t.Fatalf("혼합 목록에 종류 필터 줄이 없음:\n%s", m.View())
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if m.screen != ScreenResourceKind {
		t.Fatalf("t 입력 후 화면 = %v, want 종류 필터", m.screen)
	}
	if got := m.kindTable.Rows(); len(got) != 3 ||
		got[0][0] != "All" || got[0][1] != "3" ||
		got[1][0] != "Instances" || got[1][1] != "1" ||
		got[2][0] != "Volumes" || got[2][1] != "2" {
		t.Fatalf("종류 옵션 = %v", got)
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != ScreenList || m.resourceKindFilter != "" || len(m.resourceList.filteredIndexes) != 3 {
		t.Fatalf("종류 필터 취소 후 화면=%v 필터=%q rows=%d", m.screen, m.resourceKindFilter, len(m.resourceList.filteredIndexes))
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != ScreenList || m.resourceKindFilter != model.TypeEC2Volume || len(m.resourceList.filteredIndexes) != 2 {
		t.Fatalf("볼륨 적용 후 화면=%v 필터=%q rows=%d", m.screen, m.resourceKindFilter, len(m.resourceList.filteredIndexes))
	}
	for _, index := range m.resourceList.filteredIndexes {
		resource := m.resourceList.resources[index]
		if resource.Type != model.TypeEC2Volume {
			t.Errorf("종류 필터 결과에 다른 타입이 포함됨: %s", resource.Type)
		}
	}
	wantTitles := []string{"Kind", "Name", "ID", "Summary"}
	if got := columnTitles(m.resourceList.table); !slices.Equal(got, wantTitles) {
		t.Errorf("종류 필터 후 열 = %v, want %v", got, wantTitles)
	}

	// 종류=볼륨 상태에서 검색을 추가하면 두 조건을 모두 만족하는 행만 남는다.
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shared")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.resourceList.filteredIndexes) != 1 || m.resourceList.resources[m.resourceList.filteredIndexes[0]].ID != "vol-1" {
		t.Errorf("종류+검색 결과 인덱스 = %v, want vol-1", m.resourceList.filteredIndexes)
	}

	// 종류를 전체로 되돌려도 검색어는 유지되어 인스턴스와 볼륨 두 행이 보인다.
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.resourceKindFilter != "" || len(m.resourceList.filteredIndexes) != 2 {
		t.Errorf("전체 종류+검색 결과 필터=%q rows=%d", m.resourceKindFilter, len(m.resourceList.filteredIndexes))
	}
}

func TestResourceKindFilterIsHiddenForSingleKind(t *testing.T) {
	t.Parallel()

	groups := []ResourceGroup{{
		ID:    "ec2",
		Label: "EC2",
		Types: []ResourceType{{ID: model.TypeEC2Instance, Label: "Instances"}},
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
	data, prepared := buildResourceData(
		context.Background(), resources, groups, m.chosenTypes, m.shouldShowRegion())
	if !prepared {
		panic("종류 필터 테스트 데이터를 준비하지 못함")
	}
	m.resourceList.setResources(m.theme, resources, data, m.width, m.resourceListHeight())

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
				{Key: "Protocol", Value: "HTTP"},
				{Key: "Port", Value: "26656"},
				{Key: "TargetType", Value: "instance"},
				{Key: "Targets", Value: "1"},
			},
		},
		{
			Type: model.TypeELBv2TargetGroup,
			ID:   "web-monitoring-target-group",
			Name: "web-monitoring-target-group",
			Fields: []model.Field{
				{Key: "Protocol", Value: "HTTP"},
				{Key: "Port", Value: "3000"},
				{Key: "TargetType", Value: "instance"},
				{Key: "Targets", Value: "0"},
			},
		},
	}
	types := []ResourceType{{
		ID:      model.TypeELBv2TargetGroup,
		Label:   "타깃 그룹",
		Columns: []string{"Protocol", "Port", "TargetType", "Targets"},
	}}

	tableModel := buildTestTable(New(true), resources, resources, testResourceGroups(types),
		[]string{model.TypeELBv2TargetGroup}, false, 120, 20)
	wantTitles := []string{"Name", "Protocol", "Port", "TargetType", "Targets"}
	if got := columnTitles(tableModel); !slices.Equal(got, wantTitles) {
		t.Errorf("열 = %v, want %v", got, wantTitles)
	}

	nameWidth := tableModel.Columns()[columnIndex(tableModel, "Name")].Width
	portWidth := tableModel.Columns()[columnIndex(tableModel, "Port")].Width
	targetCountWidth := tableModel.Columns()[columnIndex(tableModel, "Targets")].Width
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
				Label:          "Instances",
				Columns:        []string{"인스턴스 타입", "사설 IP", "공인 IP"},
				SummaryColumns: []string{"인스턴스 타입", "사설 IP"},
			},
			{
				ID:             model.TypeEC2Volume,
				Label:          "Volumes",
				Columns:        []string{"VolumeType", "Size", "AvailabilityZone"},
				SummaryColumns: []string{"VolumeType", "Size"},
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
				{Key: "VolumeType", Value: "gp3"},
				{Key: "Size", Value: "100"},
			},
		},
	}

	tableModel := buildTestTable(New(true), resources, resources, groups,
		[]string{model.TypeEC2Instance, model.TypeEC2Volume}, false, 140, 20)
	wantTitles := []string{"Kind", "Name", "ID", "Status", "Summary"}
	if got := columnTitles(tableModel); !slices.Equal(got, wantTitles) {
		t.Errorf("열 = %v, want %v", got, wantTitles)
	}

	rows := tableModel.Rows()
	if got := rows[0][columnIndex(tableModel, "Kind")]; got != "Instances" {
		t.Errorf("인스턴스 종류 = %q, want %q", got, "Instances")
	}
	if got := rows[1][columnIndex(tableModel, "Kind")]; got != "Volumes" {
		t.Errorf("볼륨 종류 = %q, want %q", got, "Volumes")
	}
	if got := rows[0][columnIndex(tableModel, "Summary")]; got != "인스턴스 타입 t3.small · 사설 IP 10.0.0.10" {
		t.Errorf("인스턴스 주요 정보 = %q", got)
	}
	if got := rows[1][columnIndex(tableModel, "Summary")]; got != "VolumeType gp3 · Size 100" {
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

	tableModel := buildTestTable(New(true), resources, resources, nil,
		[]string{model.TypeELBv2TargetGroup, model.TypeEC2Instance}, false, 100, 20)
	wantTitles := []string{"Kind", "Name", "ID", "Status"}
	if got := columnTitles(tableModel); !slices.Equal(got, wantTitles) {
		t.Errorf("열 = %v, want %v", got, wantTitles)
	}
	assertTableShape(t, tableModel, false)
}

func TestResourceSearchTextKeepsCompleteSearchScope(t *testing.T) {
	t.Parallel()

	resource := model.Resource{
		Type:      "type-token",
		ID:        "id-token",
		Name:      "name-token",
		ARN:       "arn-token",
		Region:    "region-token",
		Profile:   "profile-token",
		AccountID: "account-token",
		Status:    "status-token",
		Fields:    []model.Field{{Key: "field-key-token", Value: "field-value-token"}},
		Tags:      []model.Field{{Key: "tag-key-token", Value: "tag-value-token"}},
		Related: []model.Ref{{
			Type: "related-type-token", ID: "related-id-token",
			Relation: "relation-token", Via: "via-token",
		}},
	}
	searchText := resourceSearchText(resource)
	queries := []string{
		"TYPE-TOKEN", "id-token", "name-token", "arn-token", "region-token",
		"profile-token", "account-token", "status-token", "field-key-token",
		"field-value-token", "tag-key-token", "tag-value-token", "related-type-token",
		"related-id-token", "relation-token", "via-token",
	}
	for _, query := range queries {
		if !searchTextMatches(searchText, []string{strings.ToLower(query)}) {
			t.Errorf("검색 범위에서 %q을 찾지 못함", query)
		}
	}
}

func TestResourceCursorNavigation(t *testing.T) {
	resources := make([]model.Resource, 100)
	for i := range resources {
		resources[i] = model.Resource{ID: fmt.Sprintf("id-%03d", i), Name: fmt.Sprintf("resource-%03d", i)}
	}

	viewportHeight := newFilterFlowModel(resources).resourceList.tableHeight - 1
	tests := []struct {
		name  string
		start int
		msg   tea.KeyMsg
		want  int
	}{
		{name: "위 화살표", start: 1, msg: tea.KeyMsg{Type: tea.KeyUp}, want: 0},
		{name: "k", start: 1, msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}, want: 0},
		{name: "아래 화살표", msg: tea.KeyMsg{Type: tea.KeyDown}, want: 1},
		{name: "j", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}, want: 1},
		{name: "page up", start: 50, msg: tea.KeyMsg{Type: tea.KeyPgUp}, want: 50 - viewportHeight},
		{name: "b", start: 50, msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")}, want: 50 - viewportHeight},
		{name: "page down", msg: tea.KeyMsg{Type: tea.KeyPgDown}, want: viewportHeight},
		{name: "f", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")}, want: viewportHeight},
		{name: "half page up", start: 50, msg: tea.KeyMsg{Type: tea.KeyCtrlU}, want: 50 - viewportHeight/2},
		{name: "u", start: 50, msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")}, want: 50 - viewportHeight/2},
		{name: "half page down", msg: tea.KeyMsg{Type: tea.KeyCtrlD}, want: viewportHeight / 2},
		{name: "d", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, want: viewportHeight / 2},
		{name: "home", start: 50, msg: tea.KeyMsg{Type: tea.KeyHome}, want: 0},
		{name: "g", start: 50, msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}, want: 0},
		{name: "end", msg: tea.KeyMsg{Type: tea.KeyEnd}, want: 99},
		{name: "G", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")}, want: 99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newFilterFlowModel(resources)
			for range tt.start {
				m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
			}
			m = updateFilterModel(m, tt.msg)

			if m.resourceList.cursor != tt.want {
				t.Fatalf("전역 커서 = %d, want %d", m.resourceList.cursor, tt.want)
			}
			if got, want := m.resourceList.table.Cursor(), m.resourceList.cursor-m.resourceList.windowStart; got != want {
				t.Errorf("로컬 커서 = %d, want %d", got, want)
			}
			if got := len(m.resourceList.table.Rows()); got > viewportHeight {
				t.Errorf("캐시 행 수 = %d, want <= %d", got, viewportHeight)
			}
		})
	}
}

func TestResourceTableHandles100KWithViewportCacheAndGlobalIndexes(t *testing.T) {
	resources := resourceSet100K()
	m := newFilterFlowModel(resources)

	rowCell := &m.resourceList.data.rows[99999][0]
	preferredWidths := append([]int(nil), m.resourceList.data.preferredWidths...)
	viewportHeight := m.resourceList.tableHeight - 1
	if got := len(m.resourceList.table.Rows()); got != viewportHeight {
		t.Fatalf("초기 캐시 행 수 = %d, want %d", got, viewportHeight)
	}
	if got := len(m.resourceList.filteredIndexes); got != len(resources) {
		t.Fatalf("초기 필터 인덱스 수 = %d, want %d", got, len(resources))
	}

	m.filterQuery = "resource-"
	m = m.applyResourceFilter()
	if got := len(m.resourceList.filteredIndexes); got != len(resources) {
		t.Fatalf("광범위 필터 인덱스 수 = %d, want %d", got, len(resources))
	}
	if got := len(m.resourceList.table.Rows()); got > viewportHeight {
		t.Fatalf("광범위 필터 캐시 행 수 = %d, want <= %d", got, viewportHeight)
	}
	if m.resourceList.cursor != 0 || m.resourceList.windowStart != 0 {
		t.Fatalf("필터 후 cursor/window = %d/%d, want 0/0", m.resourceList.cursor, m.resourceList.windowStart)
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.resourceList.cursor != viewportHeight {
		t.Fatalf("page down 후 전역 커서 = %d, want %d", m.resourceList.cursor, viewportHeight)
	}
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.resourceList.cursor != len(resources)-1 {
		t.Fatalf("end 후 전역 커서 = %d, want %d", m.resourceList.cursor, len(resources)-1)
	}

	m = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	resizedViewportHeight := m.resourceList.tableHeight - 1
	if m.resourceList.cursor != len(resources)-1 {
		t.Errorf("resize 후 전역 커서 = %d, want %d", m.resourceList.cursor, len(resources)-1)
	}
	if got := len(m.resourceList.table.Rows()); got > resizedViewportHeight {
		t.Errorf("resize 후 캐시 행 수 = %d, want <= %d", got, resizedViewportHeight)
	}
	if got, want := m.resourceList.table.Cursor(), m.resourceList.cursor-m.resourceList.windowStart; got != want {
		t.Errorf("resize 후 로컬 커서 = %d, want %d", got, want)
	}
	if &m.resourceList.data.rows[99999][0] != rowCell {
		t.Fatal("resize가 캐시된 행을 재생성함")
	}
	if !slices.Equal(m.resourceList.data.preferredWidths, preferredWidths) {
		t.Errorf("resize 후 선호 폭 = %v, want %v", m.resourceList.data.preferredWidths, preferredWidths)
	}

	m.resourceKindFilter = model.TypeEC2Volume
	m.filterQuery = "needle-99999"
	m = m.applyResourceFilter()
	if len(m.resourceList.filteredIndexes) != 1 || m.resourceList.filteredIndexes[0] != 99999 {
		t.Fatalf("희소 필터 인덱스 = %v, want [99999]", m.resourceList.filteredIndexes)
	}
	if got := m.resourceList.table.Rows(); len(got) != 1 || &got[0][0] != rowCell {
		t.Fatal("희소 필터가 캐시된 table.Row를 재사용하지 않음")
	}
	if m.resourceList.cursor != 0 || m.resourceList.windowStart != 0 || m.resourceList.table.Cursor() != 0 {
		t.Errorf("희소 필터 후 cursor/window/local = %d/%d/%d, want 0/0/0",
			m.resourceList.cursor, m.resourceList.windowStart, m.resourceList.table.Cursor())
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != ScreenDetail || !strings.Contains(m.View(), "resource-099999") {
		t.Fatalf("필터 인덱스를 원본 상세로 역매핑하지 못함: 화면=%v", m.screen)
	}
}

func BenchmarkResourceTable100K(b *testing.B) {
	resources := resourceSet100K()

	b.Run("prepare", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = newFilterFlowModel(resources)
		}
	})

	b.Run("broad-filter", func(b *testing.B) {
		m := newFilterFlowModel(resources)
		m.filterQuery = "resource-"
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			m = m.applyResourceFilter()
		}
	})

	b.Run("sparse-filter", func(b *testing.B) {
		m := newFilterFlowModel(resources)
		m.filterQuery = "needle-99999"
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			m = m.applyResourceFilter()
		}
	})

	b.Run("cursor-movement", func(b *testing.B) {
		m := newFilterFlowModel(resources)
		pageDown := tea.KeyMsg{Type: tea.KeyPgDown}
		pageUp := tea.KeyMsg{Type: tea.KeyPgUp}
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			if i%2 == 0 {
				m.resourceList.moveCursor(pageDown)
			} else {
				m.resourceList.moveCursor(pageUp)
			}
		}
	})

	b.Run("resize", func(b *testing.B) {
		m := newFilterFlowModel(resources)
		m.resourceList.moveCursor(tea.KeyMsg{Type: tea.KeyEnd})
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			m = m.resize(tea.WindowSizeMsg{Width: 120 + i%40, Height: 40})
		}
	})
}

func resourceSet100K() []model.Resource {
	const count = 100_000

	resources := make([]model.Resource, count)
	for i := range resources {
		typeID := model.TypeEC2Instance
		if i%2 == 1 {
			typeID = model.TypeEC2Volume
		}
		resources[i] = model.Resource{
			Type: typeID,
			ID:   fmt.Sprintf("id-%06d", i),
			Name: fmt.Sprintf("resource-%06d", i),
		}
	}
	resources[count-1].Related = []model.Ref{{Via: "needle-99999"}}

	return resources
}

func TestCollectDoneRejectsCanceledAndStalePreparation(t *testing.T) {
	resources := []model.Resource{{Type: model.TypeEC2Instance, ID: "i-current", Name: "current"}}
	data, prepared := buildResourceData(context.Background(), resources, nil, nil, false)
	if !prepared {
		t.Fatal("현재 수집 데이터를 준비하지 못함")
	}

	m := newFilterFlowModel(nil)
	m.screen = ScreenCollecting
	m.collectSequence = 2

	stale := collectDoneMsg{requestID: 1, result: collect.Result{Resources: resources}, data: data}
	next, _ := m.onCollectDone(stale)
	m = next.(Model)
	if m.screen != ScreenCollecting || len(m.resourceList.resources) != 0 {
		t.Fatal("이전 수집의 늦은 완료 메시지를 수락함")
	}

	canceled := collectDoneMsg{requestID: 2, result: collect.Result{Resources: resources, Canceled: true}, data: data, canceled: true}
	next, _ = m.onCollectDone(canceled)
	m = next.(Model)
	if m.screen != ScreenResource || len(m.resourceList.resources) != 0 {
		t.Fatal("취소된 현재 수집에서 타입 선택 화면으로 복귀하지 않음")
	}

	m.screen = ScreenCollecting
	current := collectDoneMsg{requestID: 2, result: collect.Result{Resources: resources}, data: data}
	next, _ = m.onCollectDone(current)
	m = next.(Model)
	if m.screen != ScreenList || len(m.resourceList.resources) != 1 || m.resourceList.resources[0].ID != "i-current" {
		t.Fatalf("현재 수집 완료를 적용하지 못함: 화면=%v resources=%v", m.screen, m.resourceList.resources)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := buildResourceData(ctx, resources, nil, nil, false); ok {
		t.Fatal("취소된 context에서 리소스 데이터를 준비함")
	}
}

func TestCollectErrorListAndDetailFlow(t *testing.T) {
	t.Parallel()

	groups := []ResourceGroup{{
		ID:    "ec2",
		Label: "EC2",
		Types: []ResourceType{{ID: model.TypeEC2Instance, Label: "EC2 인스턴스"}},
	}}
	resources := []model.Resource{{Type: model.TypeEC2Instance, ID: "i-1", Name: "web"}}
	errs := []model.CollectError{
		{
			Type:        model.TypeEC2Instance,
			Profile:     "prod",
			Region:      "ap-northeast-2",
			Code:        "AccessDeniedException",
			Explanation: "조회 권한이 없습니다.",
			Message:     "raw access denied",
		},
		{
			Type:        "future:resource",
			Profile:     "prod",
			Region:      "us-east-1",
			Code:        "ThrottlingException",
			Explanation: "AWS API 요청 한도를 초과했습니다.",
			Message:     "raw throttle failure",
		},
	}
	data, prepared := buildResourceData(context.Background(), resources, groups, nil, true)
	if !prepared {
		t.Fatal("오류 흐름 테스트 데이터를 준비하지 못함")
	}

	m := newFilterFlowModel(nil)
	m.deps.ResourceGroups = groups
	m.screen = ScreenCollecting
	m.collectSequence = 7
	next, _ := m.Update(collectDoneMsg{
		requestID:  7,
		result:     collect.Result{Resources: resources, Errors: errs},
		data:       data,
		showRegion: true,
	})
	m = next.(Model)

	errs[0].Message = "변경된 외부 슬라이스"
	if m.screen != ScreenList || len(m.collectErrors) != 2 || m.collectErrors[0].Message != "raw access denied" {
		t.Fatalf("수집 오류 보존 결과 = 화면 %v, 오류 %+v", m.screen, m.collectErrors)
	}
	if !strings.Contains(m.View(), "e errors") || !strings.Contains(m.listCaption, "2 errors") {
		t.Fatalf("리소스 목록에 오류 진입점이 없음:\n%s", m.View())
	}
	if got, want := columnTitles(m.errorTable), []string{"Resource", "Profile", "Region", "Error code", "Explanation"}; !slices.Equal(got, want) {
		t.Errorf("오류 테이블 열 = %v, want %v", got, want)
	}
	if rows := m.errorTable.Rows(); len(rows) != 2 || rows[0][0] != "EC2 인스턴스" || rows[1][0] != "future:resource" {
		t.Fatalf("오류 테이블 행 = %v", rows)
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if m.screen != ScreenCollectErrors {
		t.Fatalf("e 입력 후 화면 = %v, want 오류 목록", m.screen)
	}
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.errorTable.Cursor() != 1 {
		t.Fatalf("오류 커서 = %d, want 1", m.errorTable.Cursor())
	}
	m = updateFilterModel(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if m.errorTable.Cursor() != 1 {
		t.Fatalf("크기 변경 후 오류 커서 = %d, want 1", m.errorTable.Cursor())
	}

	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != ScreenCollectErrorDetail || !strings.Contains(m.detail.View(), "raw throttle failure") {
		t.Fatalf("오류 상세 화면=%v 내용:\n%s", m.screen, m.detail.View())
	}
	// 뒤로 가기는 esc가 담당한다. q는 어느 화면에서든 종료이므로 여기서 쓰지 않는다.
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != ScreenCollectErrors {
		t.Fatalf("오류 상세에서 esc 후 화면 = %v, want 오류 목록", m.screen)
	}
	m = updateFilterModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != ScreenList {
		t.Fatalf("esc 입력 후 화면 = %v, want 리소스 목록", m.screen)
	}
}

func TestIdentityRejectsStaleResponseAndSeparatesCancellation(t *testing.T) {
	t.Parallel()

	m := newFilterFlowModel(nil)
	m.screen = ScreenIdentity
	m.identitySequence = 2

	next, _ := m.Update(identityMsg{
		requestID: 1,
		id:        awsclient.Identity{AccountID: "old-account"},
	})
	m = next.(Model)
	if m.screen != ScreenIdentity || m.identity.AccountID != "" {
		t.Fatalf("이전 신원 응답을 수락함: 화면=%v identity=%+v", m.screen, m.identity)
	}

	next, _ = m.Update(identityMsg{requestID: 2, err: context.Canceled})
	m = next.(Model)
	if m.screen != ScreenProfile || m.errText != "" {
		t.Fatalf("신원 확인 취소 결과: 화면=%v err=%q", m.screen, m.errText)
	}
}

func TestRegionEnterSynchronizesChosenRowsAndCursor(t *testing.T) {
	t.Parallel()

	m := newFilterFlowModel(nil)
	m.screen = ScreenRegion
	m.regions = []awsclient.Region{
		{Code: "ap-northeast-2", Name: "서울"},
		{Code: "us-east-1", Name: "버지니아 북부"},
	}
	m.regionTable = buildRegionTable(m.theme, m.regions, nil, m.width, m.listHeight())
	m.regionTable.SetCursor(1)

	next, _ := m.keyRegion(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if got, want := m.chosenRegions, []string{"us-east-1"}; !slices.Equal(got, want) {
		t.Fatalf("chosenRegions = %v, want %v", got, want)
	}
	rows := m.regionTable.Rows()
	if rows[1][0] != m.theme.Glyphs.Healthy || rows[0][0] == m.theme.Glyphs.Healthy {
		t.Errorf("리전 체크 표시가 선택과 다름: %v", rows)
	}
	if m.regionTable.Cursor() != 1 {
		t.Errorf("리전 테이블 커서 = %d, want 1", m.regionTable.Cursor())
	}
}

// TestFormatAgeUsesSingleUnit은 경과 시간이 큰 단위 하나로 압축되는지 확인한다.
//
// 목록 열은 좁아야 하고, 정확한 시각은 상세 화면에 있다. kubectl과 k9s의 AGE 열과 같은 규칙이다.
func TestFormatAgeUsesSingleUnit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		from time.Time
		want string
	}{
		{name: "초", from: now.Add(-45 * time.Second), want: "45s"},
		{name: "분", from: now.Add(-12 * time.Minute), want: "12m"},
		{name: "시간", from: now.Add(-5 * time.Hour), want: "5h"},
		{name: "이틀 미만은 시간", from: now.Add(-47 * time.Hour), want: "47h"},
		{name: "일", from: now.Add(-30 * 24 * time.Hour), want: "30d"},
		{name: "해를 넘기면 년과 일", from: now.Add(-400 * 24 * time.Hour), want: "1y35d"},
		// 시계 오차로 생성 시각이 미래로 오는 경우가 있다. 음수를 그대로 찍지 않는다.
		{name: "미래 시각", from: now.Add(time.Hour), want: "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := formatAge(tt.from, now); got != tt.want {
				t.Errorf("formatAge() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResourceTableShowsAgeColumn은 생성 시각이 있는 리소스에만 Age 열이 붙는지 확인한다.
func TestResourceTableShowsAgeColumn(t *testing.T) {
	t.Parallel()

	created := time.Now().Add(-72 * time.Hour)
	withTime := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-1", Name: "web", CreatedAt: &created},
	}
	withoutTime := []model.Resource{
		{Type: model.TypeEC2RouteTable, ID: "rtb-1", Name: "main"},
	}

	withAge := buildTestTable(New(true), withTime, withTime, nil, nil, false, 120, 10)
	if got := columnTitles(withAge); !slices.Contains(got, "Age") {
		t.Errorf("생성 시각이 있는데 Age 열이 없다: %v", got)
	}

	noAge := buildTestTable(New(true), withoutTime, withoutTime, nil, nil, false, 120, 10)
	if got := columnTitles(noAge); slices.Contains(got, "Age") {
		t.Errorf("생성 시각이 없는데 Age 열이 있다: %v", got)
	}
}
