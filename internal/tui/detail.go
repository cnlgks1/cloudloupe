package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/graph"
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
func renderDetail(theme Theme, groups []ResourceGroup, res model.Resource, g *graph.Graph) string {
	lines := []string{
		theme.Title.Render(res.DisplayName()),
		theme.Faint.Render(detailContext(res)),
	}

	lines = appendDetailSection(lines, theme, "Basics", detailBasics(res))
	lines = appendDetailSection(lines, theme, "Attributes", res.Fields)
	lines = appendDetailSection(lines, theme, "Tags", res.Tags)
	lines = append(lines, relationLines(theme, groups, res, g)...)

	return strings.Join(lines, "\n")
}

// relationLines는 관계 섹션을 만든다.
//
// 그래프가 있으면 대상 이름과 역방향(나를 가리키는 관계)까지 보여준다. 그래프가 없으면
// (빌드 실패) 수집기가 남긴 원본 Ref로 폴백해 최소한 관계 종류와 대상 ID는 보여준다.
//
// 관계는 남긴 필드 경로(DBClusterIdentifier, VpcConfig.SubnetIds 등)로 묶는다. 같은
// 필드에서 나온 대상이 함께 보이고, 각 줄이 어느 API 응답에서 왔는지 스스로 설명한다.
func relationLines(theme Theme, groups []ResourceGroup, res model.Resource, g *graph.Graph) []string {
	if g == nil {
		return fallbackRelationLines(theme, res)
	}

	key := res.Key()
	outgoing := g.Outgoing(key)
	incoming := g.Incoming(key)
	if len(outgoing) == 0 && len(incoming) == 0 {
		return nil
	}

	var lines []string
	if len(outgoing) > 0 {
		lines = append(lines, "", theme.Title.Render(detailSectionTitle("Relations", len(outgoing))))
		lines = append(lines, relationGroupLines(theme, groups, g, outgoing, false)...)
	}
	if len(incoming) > 0 {
		lines = append(lines, "", theme.Title.Render("Referenced by ("+strconv.Itoa(len(incoming))+")"))
		lines = append(lines, relationGroupLines(theme, groups, g, incoming, true)...)
	}

	return lines
}

// relationGroupLines는 간선들을 관계 이름(응답 필드 경로)으로 묶어 렌더링한다.
//
// byIncoming이 true면 이 리소스가 대상인 관계이므로, 대상이 아니라 출발 리소스를 보여준다.
func relationGroupLines(
	theme Theme,
	groups []ResourceGroup,
	g *graph.Graph,
	edges []graph.Edge,
	byIncoming bool,
) []string {
	var lines []string

	relation := ""
	for _, edge := range edges {
		if edge.Ref.Relation != relation {
			relation = edge.Ref.Relation
			lines = append(lines, "  "+theme.Faint.Render(relation))
		}

		for _, line := range edgeTargetLines(theme, groups, g, edge, byIncoming) {
			lines = append(lines, "  "+line)
		}
	}

	return lines
}

// edgeTargetLines는 한 간선의 대상을 "타입  이름  경유" 형태로 만든다.
//
// 역방향이면 출발 리소스가 대상이므로 SourceKey를 해석한다. 정방향이면 TargetKeys를
// 해석하되, 조회 범위에 없어 해석하지 못한 대상도 감추지 않는다. 그때는 대상 이름을 알 수
// 없으므로 ID만 보여준다. 관계가 통째로 사라진 것처럼 보이면 안 된다.
func edgeTargetLines(
	theme Theme,
	groups []ResourceGroup,
	g *graph.Graph,
	edge graph.Edge,
	byIncoming bool,
) []string {
	via := ""
	if edge.Ref.Via != "" {
		via = "  " + theme.Faint.Render("via "+edge.Ref.Via)
	}

	if byIncoming {
		source, ok := g.Resource(edge.SourceKey)
		if !ok {
			return []string{theme.Glyphs.TreeBranch + " " + edge.SourceKey}
		}

		return []string{relationTargetLine(theme, groups, source, via)}
	}

	if len(edge.TargetKeys) == 0 {
		// 대상을 같이 조회하지 않아 이름을 모른다. 타입과 ID만 보여준다. 그 타입도 함께
		// 조회하면 다음 조회에서 이름이 채워진다.
		typeLabel := resourceTypeLabel(groups, edge.Ref.Type)

		return []string{theme.Glyphs.TreeBranch + " " + typeLabel + "  " + edge.Ref.ID + via}
	}

	lines := make([]string, 0, len(edge.TargetKeys))
	for _, targetKey := range edge.TargetKeys {
		target, ok := g.Resource(targetKey)
		if !ok {
			lines = append(lines, theme.Glyphs.TreeBranch+" "+targetKey+via)

			continue
		}
		lines = append(lines, relationTargetLine(theme, groups, target, via))
	}

	return lines
}

// relationTargetLine은 대상 리소스 한 줄을 "타입  이름  경유"로 만든다.
func relationTargetLine(theme Theme, groups []ResourceGroup, target model.Resource, via string) string {
	typeLabel := resourceTypeLabel(groups, target.Type)
	name := target.DisplayName()

	return theme.Glyphs.TreeBranch + " " + typeLabel + "  " + name + via
}

// fallbackRelationLines는 그래프가 없을 때 수집기 원본 Ref를 그대로 보여준다.
func fallbackRelationLines(theme Theme, res model.Resource) []string {
	if len(res.Related) == 0 {
		return nil
	}

	lines := []string{"", theme.Title.Render(detailSectionTitle("Relations", len(res.Related)))}
	for _, ref := range res.Related {
		via := ""
		if ref.Via != "" {
			via = "  " + theme.Faint.Render("via "+ref.Via)
		}
		lines = append(lines, theme.Glyphs.TreeBranch+" "+ref.Relation+" "+ref.ID+via)
	}

	return lines
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
		fields = append(fields, model.Field{Key: "Namespace", Value: res.Namespace})
	}
	if res.ARN != "" {
		fields = append(fields, model.Field{Key: "ARN", Value: res.ARN})
	}
	if res.Status != "" {
		fields = append(fields, model.Field{Key: "Status", Value: res.Status})
	}
	if res.CreatedAt != nil {
		// 도메인 모델은 UTC로 들고 표시 직전에만 포맷한다(원칙 8). 화면에는 실행한 사람의
		// 지역 시간으로 보여준다. UTC로 찍으면 조사할 때마다 머릿속에서 시차를 더해야 한다.
		//
		// 오프셋을 남기는 RFC 3339를 쓰므로 어느 시간대인지 분명하고, 같은 순간을 가리키므로
		// aws CLI 출력과 대조할 수 있다. Go는 TZ 환경변수를 따르니 UTC로 보려면
		// TZ=UTC cloudloupe 로 실행한다.
		fields = append(fields, model.Field{
			Key:   "Created",
			Value: res.CreatedAt.Local().Format(time.RFC3339),
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
	case "Tags", "Relations":
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
		{Key: resourceTypeColumn, Value: resourceTypeLabel(groups, collectErr.Type)},
		{Key: typeIDColumn, Value: collectErr.Type},
		{Key: "Profile", Value: orDashUI(collectErr.Profile)},
		{Key: "Region", Value: orDashUI(collectErr.Region)},
		{Key: "Error code", Value: orDashUI(collectErr.Code)},
	}

	lines := []string{theme.Error.Render("Collect errors"), ""}
	lines = append(lines, alignedFieldLines(fields)...)
	lines = append(lines,
		"",
		theme.Title.Render("Explanation"),
		orDashUI(collectErr.Explanation),
		"",
		theme.Title.Render("Raw error"),
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
