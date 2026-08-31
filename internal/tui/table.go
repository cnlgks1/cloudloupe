package tui

import (
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

// commonColumns는 모든 리소스 타입에 공통인 앞쪽 열이다.
var commonColumns = []string{"타입", "이름", "ID", "리전", "상태"}

// buildTable은 리소스 목록과 카탈로그 스키마로 테이블 열과 행을 만든다.
//
// 한 타입만 있으면 그 타입의 Columns를 공통 열 뒤에 붙인다. 여러 타입이 섞이면 타입마다
// 필드 의미가 다르므로 공통 열만 표시하고, 상세 화면에서 타입별 전체 필드를 보여준다.
func buildTable(theme Theme, resources []model.Resource, types []ResourceType, width, height int) table.Model {
	fieldKeys := fieldColumnKeys(resources, types)

	titles := append(append([]string{}, commonColumns...), fieldKeys...)
	columns := layoutColumns(titles, width)

	rows := make([]table.Row, 0, len(resources))
	for _, r := range resources {
		row := []string{r.Type, r.DisplayName(), r.ID, r.Region, r.Status}
		for _, key := range fieldKeys {
			row = append(row, r.FieldValue(key))
		}

		rows = append(rows, row)
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
	)

	t.SetStyles(tableStyles(theme))

	return t
}

// fieldColumnKeys는 단일 타입 목록에 사용할 안정적인 Fields 키 순서를 반환한다.
//
// 카탈로그에 정의된 Columns를 우선한다. 테스트나 외부 주입처럼 스키마가 없는 경우에만
// 같은 타입의 모든 리소스 Fields 합집합을 처음 나타난 순서로 사용한다. 첫 행 하나만 보고
// 스키마를 정하지 않으므로 조건부 필드도 사라지지 않는다.
func fieldColumnKeys(resources []model.Resource, types []ResourceType) []string {
	if len(resources) == 0 {
		return nil
	}

	resourceType := resources[0].Type
	for _, resource := range resources[1:] {
		if resource.Type != resourceType {
			return nil
		}
	}

	for _, typ := range types {
		if typ.ID == resourceType && len(typ.Columns) > 0 {
			return append([]string(nil), typ.Columns...)
		}
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

// layoutColumns는 열 제목들에 너비를 배분한다.
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

// buildTypeTable은 리소스 타입 선택 테이블을 만든다. 다중 선택이므로 선택 표시 열을 둔다.
// 열: 선택 / 타입 / 타입 ID.
func buildTypeTable(theme Theme, types []ResourceType, chosen []string, width, height int) table.Model {
	set := make(map[string]bool, len(chosen))
	for _, c := range chosen {
		set[c] = true
	}

	titles := []string{"", "타입", "타입 ID"}
	columns := layoutColumns(titles, width)
	if len(columns) > 0 {
		columns[0].Width = 3
	}

	rows := make([]table.Row, 0, len(types))
	for _, t := range types {
		mark := " "
		if set[t.ID] {
			mark = theme.Glyphs.Healthy
		}

		rows = append(rows, table.Row{mark, t.Label, t.ID})
	}

	return newDataTable(theme, columns, rows, height)
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
