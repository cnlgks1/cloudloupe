package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// 이 파일은 스크롤 가능한 상세 화면의 렌더링만 담는다.
//
// 상세 화면은 자체 상태가 없다. 스크롤 위치는 viewport가, 키 처리는 Update가 갖는다.
// 그래서 서브모델이 아니라 순수 렌더 함수로 둔다. 모델 상태와 화면 문자열 조립을 한 파일에
// 섞지 않으려고 model.go에서 갈라냈다.

// detailLabelGap은 라벨과 값 사이에 두는 최소 간격이다.
const detailLabelGap = 2

// renderDetail은 리소스 상세를 문자열로 만든다.
//
// 기본 / 속성 / 태그 / 관계로 나눈다. 수집기가 만든 표시 필드만 늘어놓으면 이 리소스가 어느
// 계정의 무엇인지, 언제 만들어졌는지가 빠진다. 멀티 계정·멀티 리전 조회에서는 그 값이 필드
// 자체보다 먼저 필요하다. Fields와 Tags는 순서 있는 슬라이스이므로 렌더링할 때마다 같은
// 순서로 나온다.
func renderDetail(theme Theme, res model.Resource) string {
	lines := []string{
		theme.Title.Render(res.DisplayName()),
		theme.Faint.Render(detailContext(res)),
	}

	lines = appendDetailSection(lines, theme, "기본", detailBasics(res))
	lines = appendDetailSection(lines, theme, "속성", res.Fields)
	lines = appendDetailSection(lines, theme, "태그", res.Tags)

	if len(res.Related) > 0 {
		lines = append(lines, "", theme.Title.Render(detailSectionTitle("관계", len(res.Related))))

		for _, ref := range res.Related {
			via := ""
			if ref.Via != "" {
				via = "  (" + ref.Via + ")"
			}

			lines = append(lines, theme.Glyphs.TreeBranch+" "+ref.Relation+" "+ref.ID+via)
		}
	}

	return strings.Join(lines, "\n")
}

// detailContext는 제목 아래에 둘 위치 정보를 만든다. 비어 있는 값은 자리를 차지하지 않는다.
func detailContext(res model.Resource) string {
	parts := make([]string, 0, 4)
	for _, part := range []string{res.Type, res.Region, res.AccountID, res.Profile} {
		if part != "" {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, "  ")
}

// detailBasics는 리소스를 특정하는 값과 상태·생성 시각을 만든다.
//
// 값이 없는 항목은 넣지 않는다. ENI처럼 생성 시각이 없거나 라우팅 테이블처럼 상태가 없는
// 타입에 "-"만 남기면 화면이 길어지고 읽을 것이 줄어든다.
func detailBasics(res model.Resource) []model.Field {
	fields := []model.Field{{Key: "ID", Value: res.ID}}

	if res.Namespace != "" {
		fields = append(fields, model.Field{Key: "상위 범위", Value: res.Namespace})
	}
	if res.ARN != "" {
		fields = append(fields, model.Field{Key: "ARN", Value: res.ARN})
	}
	if res.Status != "" {
		fields = append(fields, model.Field{Key: "상태", Value: res.Status})
	}
	if res.CreatedAt != nil {
		// 도메인 모델은 UTC로 들고, 표시 직전에만 포맷한다.
		fields = append(fields, model.Field{
			Key:   "생성",
			Value: res.CreatedAt.UTC().Format("2006-01-02 15:04:05") + " UTC",
		})
	}

	return fields
}

func appendDetailSection(lines []string, theme Theme, title string, fields []model.Field) []string {
	if len(fields) == 0 {
		return lines
	}

	lines = append(lines, "", theme.Title.Render(detailSectionTitle(title, len(fields))))

	return append(lines, alignedFieldLines(fields)...)
}

// detailSectionTitle은 항목 수가 의미 있는 섹션에만 개수를 붙인다.
//
// 태그와 관계는 몇 개인지가 판단에 쓰인다. 태그가 하나도 없는 리소스와 스무 개인 리소스는
// 다르게 다뤄야 한다.
func detailSectionTitle(title string, count int) string {
	switch title {
	case "태그", "관계":
		return title + " (" + strconv.Itoa(count) + ")"
	default:
		return title
	}
}

// renderCollectErrorDetail은 부분 수집 오류 하나의 원본 상세를 만든다.
//
// 사용자 대면 설명과 원본 오류 메시지를 분리해 보여준다. 진단에는 원본이 필요하고,
// 판단에는 설명이 필요하다.
func renderCollectErrorDetail(
	theme Theme,
	groups []ResourceGroup,
	collectErr model.CollectError,
) string {
	fields := []model.Field{
		{Key: "리소스 종류", Value: resourceTypeLabel(groups, collectErr.Type)},
		{Key: "타입 ID", Value: collectErr.Type},
		{Key: "프로필", Value: orDashUI(collectErr.Profile)},
		{Key: "리전", Value: orDashUI(collectErr.Region)},
		{Key: "AWS 오류 코드", Value: orDashUI(collectErr.Code)},
	}

	lines := []string{theme.Error.Render("수집 오류"), ""}
	lines = append(lines, alignedFieldLines(fields)...)
	lines = append(lines,
		"",
		theme.Title.Render("설명"),
		orDashUI(collectErr.Explanation),
		"",
		theme.Title.Render("원본 오류"),
		collectErr.Message,
	)

	return strings.Join(lines, "\n")
}

// alignedFieldLines는 "라벨  값" 줄을 값 시작 위치가 같아지게 만든다.
//
// fmt의 %-16s는 채울 칸을 문자 수로 센다. 한글은 터미널에서 두 칸을 차지하므로 라벨에 한글이
// 섞이면 값이 줄마다 다른 위치에서 시작한다. 폭은 표 렌더링과 같은 방식으로 lipgloss가
// 계산하게 맡긴다.
func alignedFieldLines(fields []model.Field) []string {
	width := 0
	for _, field := range fields {
		width = max(width, lipgloss.Width(field.Key))
	}

	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		padding := width - lipgloss.Width(field.Key) + detailLabelGap
		lines = append(lines, field.Key+strings.Repeat(" ", padding)+field.Value)
	}

	return lines
}
