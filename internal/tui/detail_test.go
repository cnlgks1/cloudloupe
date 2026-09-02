package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// TestDetailAlignsValuesByDisplayWidth는 라벨에 한글이 섞여도 값이 같은 칸에서 시작하는지
// 확인한다. fmt의 %-16s는 채울 칸을 문자 수로 세므로 한글 라벨에서 값이 밀렸다.
func TestDetailAlignsValuesByDisplayWidth(t *testing.T) {
	t.Parallel()

	res := model.Resource{
		Type:   model.TypeEC2InternetGateway,
		ID:     "igw-1",
		Name:   "web-igw",
		Region: "ap-northeast-2",
		Fields: []model.Field{
			{Key: "VPC", Value: "vpc-0212196b30296f40a"},
			{Key: "연결 상태", Value: "available"},
			{Key: "소유자 ID", Value: "123456789012"},
		},
	}

	view := renderDetail(New(true), res)

	starts := make(map[int][]string)
	for _, line := range strings.Split(view, "\n") {
		for _, field := range res.Fields {
			if !strings.HasPrefix(line, field.Key) {
				continue
			}

			start := lipgloss.Width(line) - lipgloss.Width(field.Value)
			starts[start] = append(starts[start], field.Key)
		}
	}

	if len(starts) != 1 {
		t.Errorf("값 시작 위치가 라벨마다 다르다: %v\n%s", starts, view)
	}
}

// TestDetailKeepsFieldOrder는 상세가 수집기가 정한 필드 순서를 그대로 쓰는지 확인한다.
// 순서가 흔들리면 같은 리소스를 다시 열 때마다 화면이 달라진다.
func TestDetailKeepsFieldOrder(t *testing.T) {
	t.Parallel()

	res := model.Resource{
		Type: model.TypeEC2NATGateway,
		ID:   "nat-1",
		Fields: []model.Field{
			{Key: "연결 유형", Value: "public"},
			{Key: "VPC", Value: "vpc-1"},
			{Key: "서브넷", Value: "subnet-1"},
		},
		Related: []model.Ref{
			{Type: model.TypeEC2VPC, ID: "vpc-1", Relation: model.RelationAssociatedWith},
			{Type: model.TypeEC2RouteTable, ID: "rtb-1", Relation: model.RelationRoutesTo, Via: "0.0.0.0/0"},
		},
	}

	view := renderDetail(New(true), res)

	if got := []int{
		strings.Index(view, "연결 유형"),
		strings.Index(view, "VPC"),
		strings.Index(view, "서브넷"),
	}; got[0] > got[1] || got[1] > got[2] {
		t.Errorf("필드 순서가 수집기 순서와 다르다: %v\n%s", got, view)
	}

	// 관계의 한정어(Via)는 어떤 규칙이 간선을 만들었는지 알려주므로 함께 보여야 한다.
	if !strings.Contains(view, "0.0.0.0/0") {
		t.Errorf("관계의 Via가 보이지 않는다:\n%s", view)
	}
}

// TestDetailShowsIdentityAndTags는 상세가 계정·생성 시각·태그까지 보여주는지 확인한다.
// 수집은 하는데 화면에 없으면 사용자는 그 값이 없다고 믿는다.
func TestDetailShowsIdentityAndTags(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 11, 14, 3, 22, 5, 0, time.UTC)
	res := model.Resource{
		Type:      model.TypeEC2NATGateway,
		ID:        "nat-0a1b",
		Name:      "web-nat-a",
		ARN:       "arn:aws:ec2:ap-northeast-2:123456789012:natgateway/nat-0a1b",
		Region:    "ap-northeast-2",
		Profile:   "prod",
		AccountID: "123456789012",
		Status:    "available",
		CreatedAt: &created,
		Fields:    []model.Field{{Key: "연결 유형", Value: "public"}},
		Tags: []model.Field{
			{Key: "Name", Value: "web-nat-a"},
			{Key: "env", Value: "prod"},
		},
	}

	view := renderDetail(New(true), res)

	for _, want := range []string{
		"123456789012",
		"prod",
		res.ARN,
		"available",
		"2025-11-14 03:22:05 UTC",
		"태그 (2)",
		"env",
		"prod",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("상세에 %q가 없다:\n%s", want, view)
		}
	}
}

// TestDetailOmitsEmptySections는 값이 없는 항목으로 화면을 채우지 않는지 확인한다.
// 라우팅 테이블은 상태가 없고 ENI는 생성 시각이 없다. "-"만 늘어놓으면 읽을 것이 줄어든다.
func TestDetailOmitsEmptySections(t *testing.T) {
	t.Parallel()

	res := model.Resource{
		Type:   model.TypeEC2RouteTable,
		ID:     "rtb-1",
		Region: "ap-northeast-2",
		Fields: []model.Field{{Key: "VPC", Value: "vpc-1"}},
	}

	view := renderDetail(New(true), res)

	for _, unwanted := range []string{"ARN", "상태", "생성", "태그", "관계"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("값이 없는 %q 항목이 표시된다:\n%s", unwanted, view)
		}
	}
}
