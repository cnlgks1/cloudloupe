package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// 리소스 목록은 2줄 리스트가 아니라 컬럼 정렬 테이블로 보여준다. k9s·taws 같은 인프라
// TUI의 관례다. 인프라 조회는 여러 리소스의 같은 속성(상태, IP, 타입)을 세로로 훑어
// 비교하는 일이 많으므로, 속성이 열로 정렬돼 있어야 눈이 빠르게 읽는다.
//
// 컬럼은 타입마다 다르다(EC2는 IP, 로드밸런서는 스킴/DNS). 공통 열 뒤에 catalog가
// 제공한 타입별 Fields 키를 붙인다. 결과 첫 행에 스키마를 의존하지 않으므로 조건부 필드나
// 빈 결과에서도 타입 정의가 흔들리지 않는다.

// resourceColumn은 리소스 공통·타입별 열의 제목과 값 추출을 함께 정의한다.
// 제목과 행 생성을 같은 목록에서 처리해 열 수와 셀 수가 어긋나지 않게 한다.
type resourceColumn struct {
	title string
	value func(model.Resource) string
}

// resourceTableData는 한 번 계산한 리소스 목록의 표시·검색 데이터를 보관한다.
// rows의 각 위치는 원본 resources의 같은 위치와 대응한다.
type resourceTableData struct {
	titles          []string
	rows            []table.Row
	searchTexts     []string
	preferredWidths []int
}

// buildResourceData는 고정 스키마의 행, 검색 문자열, 선호 폭을 수집 완료 시 한 번 만든다.
func buildResourceData(
	ctx context.Context,
	resources []model.Resource,
	groups []ResourceGroup,
	selectedTypes []string,
	showRegion bool,
) (resourceTableData, bool) {
	select {
	case <-ctx.Done():
		return resourceTableData{}, false
	default:
	}

	columns := resourceColumns(resources, groups, selectedTypes, showRegion)
	data := resourceTableData{
		titles:          make([]string, len(columns)),
		rows:            make([]table.Row, len(resources)),
		searchTexts:     make([]string, len(resources)),
		preferredWidths: make([]int, len(columns)),
	}
	for i, column := range columns {
		data.titles[i] = column.title
		data.preferredWidths[i] = lipgloss.Width(column.title) + 2
	}

	for i, resource := range resources {
		if i%256 == 0 {
			select {
			case <-ctx.Done():
				return resourceTableData{}, false
			default:
			}
		}

		row := make(table.Row, len(columns))
		for columnIndex, column := range columns {
			row[columnIndex] = column.value(resource)
			data.preferredWidths[columnIndex] = max(
				data.preferredWidths[columnIndex], lipgloss.Width(row[columnIndex])+2)
		}
		data.rows[i] = row
		data.searchTexts[i] = resourceSearchText(resource)
	}

	select {
	case <-ctx.Done():
		return resourceTableData{}, false
	default:
		return data, true
	}
}

// resourceSearchText는 기존 검색 범위 전체를 소문자 문자열 하나로 고정한다.
func resourceSearchText(resource model.Resource) string {
	var text strings.Builder
	write := func(value string) {
		text.WriteString(value)
		text.WriteByte('\n')
	}

	write(resource.Type)
	write(resource.ID)
	write(resource.Name)
	write(resource.ARN)
	write(resource.Region)
	write(resource.Profile)
	write(resource.AccountID)
	write(resource.Status)
	for _, field := range resource.Fields {
		write(field.Key)
		write(field.Value)
	}
	for _, tag := range resource.Tags {
		write(tag.Key)
		write(tag.Value)
	}
	for _, ref := range resource.Related {
		write(ref.Type)
		write(ref.ID)
		write(ref.Relation)
		write(ref.Via)
	}

	return strings.ToLower(text.String())
}

// newResourceTable은 캐시된 행과 선호 폭으로 리소스 테이블을 만든다.
func newResourceTable(theme Theme, data resourceTableData, rows []table.Row, width, height int) table.Model {
	return table.New(
		table.WithColumns(layoutResourceColumns(data.titles, data.preferredWidths, width)),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithStyles(tableStyles(theme)),
	)
}

func resourceColumns(
	resources []model.Resource,
	groups []ResourceGroup,
	selectedTypes []string,
	showRegion bool,
) []resourceColumn {
	var columns []resourceColumn
	mixedTypes := multipleResourceTypes(resources, selectedTypes)

	if mixedTypes {
		columns = append(columns, resourceColumn{title: "종류", value: func(resource model.Resource) string {
			return resourceTypeLabel(groups, resource.Type)
		}})
	}

	columns = append(columns, resourceColumn{title: "이름", value: func(resource model.Resource) string {
		return resource.DisplayName()
	}})

	if hasDistinctResourceID(resources) {
		columns = append(columns, resourceColumn{title: "ID", value: func(resource model.Resource) string {
			return resource.ID
		}})
	}

	if showRegion {
		columns = append(columns, resourceColumn{title: "리전", value: func(resource model.Resource) string {
			return resource.Region
		}})
	}

	if hasResourceStatus(resources) {
		columns = append(columns, resourceColumn{title: "상태", value: func(resource model.Resource) string {
			return resource.Status
		}})
	}

	if mixedTypes {
		if hasSummaryColumns(groups, selectedTypes) {
			columns = append(columns, resourceColumn{title: "주요 정보", value: func(resource model.Resource) string {
				return resourceSummary(groups, resource)
			}})
		}

		return columns
	}

	for _, key := range fieldColumnKeys(resources, groups, selectedTypes) {
		columns = append(columns, resourceColumn{title: key, value: func(resource model.Resource) string {
			return resource.FieldValue(key)
		}})
	}

	return columns
}

func multipleResourceTypes(resources []model.Resource, selectedTypes []string) bool {
	if len(selectedTypes) > 1 {
		return true
	}
	if len(selectedTypes) == 1 || len(resources) < 2 {
		return false
	}

	first := resources[0].Type
	for _, resource := range resources[1:] {
		if resource.Type != first {
			return true
		}
	}

	return false
}

func hasDistinctResourceID(resources []model.Resource) bool {
	for _, resource := range resources {
		if resource.DisplayName() != resource.ID {
			return true
		}
	}

	return false
}

func hasResourceStatus(resources []model.Resource) bool {
	for _, resource := range resources {
		if strings.TrimSpace(resource.Status) != "" {
			return true
		}
	}

	return false
}

func resourceTypeMetadata(groups []ResourceGroup, typeID string) (ResourceType, bool) {
	for _, group := range groups {
		for _, resourceType := range group.Types {
			if resourceType.ID == typeID {
				return resourceType, true
			}
		}
	}

	return ResourceType{}, false
}

func resourceTypeLabel(groups []ResourceGroup, typeID string) string {
	resourceType, ok := resourceTypeMetadata(groups, typeID)
	if !ok || resourceType.Label == "" {
		return typeID
	}

	return resourceType.Label
}

func hasSummaryColumns(groups []ResourceGroup, selectedTypes []string) bool {
	selected := make(map[string]struct{}, len(selectedTypes))
	for _, typeID := range selectedTypes {
		selected[typeID] = struct{}{}
	}

	for _, group := range groups {
		for _, resourceType := range group.Types {
			if len(selected) > 0 {
				if _, exists := selected[resourceType.ID]; !exists {
					continue
				}
			}
			if len(resourceType.SummaryColumns) > 0 {
				return true
			}
		}
	}

	return false
}

func resourceSummary(groups []ResourceGroup, resource model.Resource) string {
	resourceType, ok := resourceTypeMetadata(groups, resource.Type)
	if !ok {
		return ""
	}

	parts := make([]string, 0, len(resourceType.SummaryColumns))
	for _, key := range resourceType.SummaryColumns {
		if value := strings.TrimSpace(resource.FieldValue(key)); value != "" {
			parts = append(parts, key+" "+value)
		}
	}

	return strings.Join(parts, " · ")
}

// fieldColumnKeys는 단일 타입 목록에 사용할 안정적인 Fields 키 순서를 반환한다.
//
// 카탈로그에 정의된 Columns를 우선한다. 테스트나 외부 주입처럼 스키마가 없는 경우에만
// 같은 타입의 모든 리소스 Fields 합집합을 처음 나타난 순서로 사용한다. 첫 행 하나만 보고
// 스키마를 정하지 않으므로 조건부 필드도 사라지지 않는다.
func fieldColumnKeys(resources []model.Resource, groups []ResourceGroup, selectedTypes []string) []string {
	resourceType := ""
	switch {
	case len(selectedTypes) == 1:
		resourceType = selectedTypes[0]
	case len(selectedTypes) > 1:
		return nil
	case len(resources) > 0:
		resourceType = resources[0].Type
		for _, resource := range resources[1:] {
			if resource.Type != resourceType {
				return nil
			}
		}
	default:
		return nil
	}

	if metadata, ok := resourceTypeMetadata(groups, resourceType); ok && len(metadata.Columns) > 0 {
		return append([]string(nil), metadata.Columns...)
	}

	seen := make(map[string]struct{})
	var keys []string

	for _, resource := range resources {
		for _, field := range resource.Fields {
			if _, exists := seen[field.Key]; exists {
				continue
			}

			seen[field.Key] = struct{}{}
			keys = append(keys, field.Key)
		}
	}

	return keys
}

// layoutResourceColumns는 캐시한 선호 폭과 열 의미에 따라 실제 열 너비를 배분한다.
//
// 이름·ID·DNS·값은 남는 폭을 우선 사용하고, 포트·개수·IOPS·TTL 같은 짧은 값은 좁게
// 유지한다. 터미널이 좁으면 각 열의 최소 너비까지 긴 열부터 줄인다.
func layoutResourceColumns(titles []string, preferredWidths []int, width int) []table.Column {
	if len(titles) == 0 {
		return nil
	}

	widths := make([]int, len(titles))
	minimums := make([]int, len(titles))
	maximums := make([]int, len(titles))
	growable := make([]bool, len(titles))

	for i, title := range titles {
		minimums[i], maximums[i], growable[i] = resourceColumnBounds(title)
		preferred := lipgloss.Width(title) + 2
		if i < len(preferredWidths) {
			preferred = max(preferred, preferredWidths[i])
		}
		widths[i] = min(max(preferred, minimums[i]), maximums[i])
	}

	usable := max(1, width-2)
	total := sumWidths(widths)
	for total > usable {
		candidate := -1
		largestSlack := 0
		for i := range widths {
			slack := widths[i] - minimums[i]
			if slack > largestSlack {
				candidate = i
				largestSlack = slack
			}
		}
		if candidate < 0 {
			break
		}
		widths[candidate]--
		total--
	}

	for total < usable {
		grew := false
		for i := range widths {
			if !growable[i] || widths[i] >= maximums[i] || total >= usable {
				continue
			}
			widths[i]++
			total++
			grew = true
		}
		if !grew {
			break
		}
	}

	columns := make([]table.Column, 0, len(titles))
	for i, title := range titles {
		columns = append(columns, table.Column{Title: title, Width: widths[i]})
	}

	return columns
}

func resourceColumnBounds(title string) (minimum, maximum int, growable bool) {
	titleWidth := lipgloss.Width(title) + 2

	switch title {
	case "포트", "타깃 수", "IOPS", "TTL", "규칙 수":
		return max(6, titleWidth), max(10, titleWidth), false
	case "암호화":
		return max(8, titleWidth), max(12, titleWidth), false
	case "상태", "리전", "프로토콜", "스킴", "종류", "타깃 종류", "타입":
		return max(10, titleWidth), max(22, titleWidth), false
	case "이름", "ID", "DNS 이름", "값", "별칭 대상", "설명", "호스팅 영역", "주요 정보":
		return max(12, titleWidth), max(48, titleWidth), true
	default:
		return max(10, titleWidth), max(30, titleWidth), false
	}
}

func sumWidths(widths []int) int {
	var total int
	for _, width := range widths {
		total += width
	}

	return total
}

// sameTableColumns는 resize에서 실제 열 배치가 달라질 때만 테이블을 갱신하게 한다.
func sameTableColumns(left, right []table.Column) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

// layoutColumns는 일반 선택 테이블의 열 너비를 균등하게 배분한다.
//
// 터미널 너비를 열 개수로 고르게 나눈다. 최소 너비를 두어 좁은 터미널에서도 제목이
// 뭉개지지 않게 한다. 정교한 내용 기반 배분 대신 단순 균등 배분을 쓴다("영리함보다
// 명확함"). 필요해지면 그때 개선한다.
func layoutColumns(titles []string, width int) []table.Column {
	const minWidth = 10

	if len(titles) == 0 {
		return nil
	}

	// 테두리·여백으로 몇 칸 빠지므로 살짝 줄여 잡는다.
	usable := width - 2
	if usable < minWidth*len(titles) {
		usable = minWidth * len(titles)
	}

	per := usable / len(titles)
	if per < minWidth {
		per = minWidth
	}

	columns := make([]table.Column, 0, len(titles))
	for _, title := range titles {
		columns = append(columns, table.Column{Title: title, Width: per})
	}

	return columns
}

// buildProfileTable은 프로필 선택 테이블을 만든다. 열: 프로필 / 종류 / 리전.
func buildProfileTable(theme Theme, profiles []awsclient.Profile, width, height int) table.Model {
	titles := []string{"프로필", "종류", "리전"}
	columns := layoutColumns(titles, width)

	rows := make([]table.Row, 0, len(profiles))
	for _, p := range profiles {
		rows = append(rows, table.Row{p.Name, string(p.Kind), orDashUI(p.Region)})
	}

	return newDataTable(theme, columns, rows, height)
}

// buildRegionTable은 리전 선택 테이블을 만든다. 다중 선택이므로 첫 열에 선택 표시(●)를
// 둔다. 열: 선택 / 리전 / 이름.
func buildRegionTable(theme Theme, regions []awsclient.Region, chosen []string, width, height int) table.Model {
	set := make(map[string]bool, len(chosen))
	for _, c := range chosen {
		set[c] = true
	}

	titles := []string{"", "리전", "이름"}
	columns := layoutColumns(titles, width)
	// 첫 열(선택 표시)은 좁게 고정한다.
	if len(columns) > 0 {
		columns[0].Width = 3
	}

	rows := make([]table.Row, 0, len(regions))
	for _, r := range regions {
		mark := " "
		if set[r.Code] {
			mark = theme.Glyphs.Healthy
		}

		rows = append(rows, table.Row{mark, r.Code, r.Name})
	}

	return newDataTable(theme, columns, rows, height)
}

// buildTypeTable은 AWS 서비스별 큰 리소스 선택 테이블을 만든다.
//
// 세부 타입 ID를 노출하지 않고 그룹에 포함되는 리소스 이름만 보여준다. 선택 결과는
// resourceGroupTypeIDs로 펼쳐 실제 수집기에는 기존의 정밀한 타입 ID가 전달된다.
func buildTypeTable(theme Theme, groups []ResourceGroup, chosen []string, width, height int) table.Model {
	titles := []string{"", "리소스", "포함 항목"}
	columns := layoutColumns(titles, width)
	if len(columns) > 0 {
		columns[0].Width = 3
	}

	rows := make([]table.Row, 0, len(groups))
	for _, group := range groups {
		mark := " "
		if resourceGroupSelected(group, chosen) {
			mark = theme.Glyphs.Healthy
		}

		labels := make([]string, 0, len(group.Types))
		for _, resourceType := range group.Types {
			labels = append(labels, resourceType.Label)
		}
		rows = append(rows, table.Row{mark, group.Label, strings.Join(labels, ", ")})
	}

	return newDataTable(theme, columns, rows, height)
}

// buildResourceKindTable은 수집 결과 안의 세부 종류를 단일 선택 표로 만든다.
func buildResourceKindTable(theme Theme, kinds []resourceKind, width, height int) table.Model {
	titles := []string{"종류", "개수"}
	columns := layoutColumns(titles, width)

	total := 0
	for _, kind := range kinds {
		total += kind.Count
	}
	rows := make([]table.Row, 0, len(kinds)+1)
	rows = append(rows, table.Row{"전체", strconv.Itoa(total)})
	for _, kind := range kinds {
		rows = append(rows, table.Row{kind.Label, strconv.Itoa(kind.Count)})
	}

	return newDataTable(theme, columns, rows, height)
}

func resourceGroupTypeIDs(group ResourceGroup) []string {
	types := make([]string, 0, len(group.Types))
	for _, resourceType := range group.Types {
		types = append(types, resourceType.ID)
	}

	return types
}

func resourceGroupSelected(group ResourceGroup, chosen []string) bool {
	if len(group.Types) == 0 {
		return false
	}

	selected := make(map[string]struct{}, len(chosen))
	for _, typeID := range chosen {
		selected[typeID] = struct{}{}
	}
	for _, resourceType := range group.Types {
		if _, exists := selected[resourceType.ID]; !exists {
			return false
		}
	}

	return true
}

// newDataTable은 공통 설정을 적용한 table.Model을 만든다.
func newDataTable(theme Theme, columns []table.Column, rows []table.Row, height int) table.Model {
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
	)
	t.SetStyles(tableStyles(theme))

	return t
}

// orDashUI는 빈 문자열을 "-"로 바꾼다(표시용). collect의 orDash와 같은 역할이지만
// 패키지가 달라 여기 둔다("약간의 복사가 약간의 의존보다 낫다").
func orDashUI(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// tableStyles는 테마에 맞춘 테이블 스타일을 만든다.
//
// 선택된 행은 반전 강조, 헤더는 굵게. 상태 색(running 초록 등)은 셀 단위 스타일링이
// 필요해 지금 테이블 위젯으로는 어려우므로, 우선 행 강조만 적용한다. 색상은 후속 작업에서
// 셀 렌더러로 넣는다.
func tableStyles(theme Theme) table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).BorderBottom(true)
	s.Selected = s.Selected.Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))

	if theme.ASCII {
		// ASCII 테마에서는 색 대신 강조만.
		s.Selected = lipgloss.NewStyle().Bold(true).Reverse(true)
	}

	return s
}
