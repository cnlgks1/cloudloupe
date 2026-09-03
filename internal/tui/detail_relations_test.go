package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/graph"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// relationGroups는 관계 테스트가 공유하는 타입 라벨 카탈로그다.
func relationGroups() []ResourceGroup {
	return []ResourceGroup{
		{ID: "ec2", Label: "EC2", Types: []ResourceType{
			{ID: model.TypeEC2Instance, Label: "Instances"},
			{ID: model.TypeEC2Subnet, Label: "Subnets"},
			{ID: model.TypeEC2VPC, Label: "VPCs"},
		}},
		{ID: "rds", Label: "RDS", Types: []ResourceType{
			{ID: model.TypeRDSDBCluster, Label: "DB clusters"},
			{ID: model.TypeRDSDBInstance, Label: "DB instances"},
		}},
	}
}

func relationResource(typ, id, name string, refs ...model.Ref) model.Resource {
	return model.Resource{
		Type: typ, ID: id, Name: name,
		Region: "ap-northeast-2", Profile: "prod", AccountID: "123456789012",
		Related: refs,
	}
}

// TestRelationShowsTargetNameNotJustID는 그래프가 대상 ID를 이름으로 바꿔 보여주는지
// 확인한다.
//
// 수집기가 남긴 원본 Ref는 대상 ID만 있다. vpc-0123이 무슨 VPC인지 알려면 그래프가
// 해석해야 한다. 콘솔 탭을 오가지 않고 한 화면에서 관계를 읽게 하는 것이 이 기능의 목적이다.
func TestRelationShowsTargetNameNotJustID(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		relationResource(model.TypeEC2VPC, "vpc-0123", "main-vpc"),
		relationResource(model.TypeEC2Subnet, "subnet-a", "private-a",
			model.Ref{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: "VpcId"}),
	}

	g, err := graph.Build(resources)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	view := renderDetail(New(true), relationGroups(), resources[1], g)

	// 관계 이름은 SDK 응답 필드 경로다.
	if !strings.Contains(view, "VpcId") {
		t.Errorf("관계 이름(SDK 필드 경로)이 없다:\n%s", view)
	}
	// 대상은 타입 라벨과 이름으로 보인다.
	if !strings.Contains(view, "VPCs") || !strings.Contains(view, "main-vpc") {
		t.Errorf("대상 타입 라벨과 이름이 없다:\n%s", view)
	}
}

// TestRelationShowsReverseDirection은 나를 가리키는 관계를 Referenced by로 보여주는지
// 확인한다.
//
// AWS는 역방향을 알려주는 API가 없다. 서브넷이 어느 리소스에 쓰이는지는 서브넷 응답에
// 없고, 관계를 모아야 나온다. 이것이 콘솔에 없는 정보다.
func TestRelationShowsReverseDirection(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		relationResource(model.TypeEC2Subnet, "subnet-a", "private-a"),
		relationResource(model.TypeRDSDBInstance, "orders-db", "orders-db",
			model.Ref{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"}),
	}

	g, err := graph.Build(resources)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 서브넷을 보면 그 서브넷을 쓰는 DB 인스턴스가 Referenced by에 나온다.
	view := renderDetail(New(true), relationGroups(), resources[0], g)

	if !strings.Contains(view, "Referenced by") {
		t.Errorf("역방향 섹션이 없다:\n%s", view)
	}
	if !strings.Contains(view, "DB instances") || !strings.Contains(view, "orders-db") {
		t.Errorf("역방향 대상이 없다:\n%s", view)
	}
}

// TestRelationMarksUnresolvedTargets는 조회 범위에 없어 해석하지 못한 대상을 감추지 않고
// ID로 보여주는지 확인한다.
//
// 감추면 관계가 통째로 사라진 것처럼 보인다. 같이 조회하지 않은 대상은 이름을 알 수 없으므로
// ID만 보여주고, 그 타입도 함께 조회하면 다음에 이름이 채워진다.
func TestRelationMarksUnresolvedTargets(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		relationResource(model.TypeEC2Subnet, "subnet-a", "private-a"),
		relationResource(model.TypeRDSDBInstance, "orders-db", "orders-db",
			model.Ref{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"},
			model.Ref{Type: model.TypeEC2Subnet, ID: "subnet-x", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"}),
	}

	g, err := graph.Build(resources)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	view := renderDetail(New(true), relationGroups(), resources[1], g)

	// 해석된 대상은 이름으로, 미조회 대상은 ID로.
	if !strings.Contains(view, "private-a") {
		t.Errorf("해석된 대상이 없다:\n%s", view)
	}
	if !strings.Contains(view, "subnet-x") {
		t.Errorf("미조회 대상 ID가 보이지 않는다:\n%s", view)
	}
}

// TestRelationFallsBackWithoutGraph는 그래프가 없을 때(빌드 실패) 원본 Ref로 폴백하는지
// 확인한다. 그래프가 nil이어도 최소한 관계 종류와 대상 ID는 보여야 한다.
func TestRelationFallsBackWithoutGraph(t *testing.T) {
	t.Parallel()

	res := relationResource(model.TypeEC2Subnet, "subnet-a", "private-a",
		model.Ref{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: "VpcId"})

	view := renderDetail(New(true), relationGroups(), res, nil)

	if !strings.Contains(view, "VpcId") || !strings.Contains(view, "vpc-0123") {
		t.Errorf("그래프 없는 폴백에 관계가 없다:\n%s", view)
	}
	// 폴백에는 역방향이 없다. 그래프가 있어야 만들 수 있다.
	if strings.Contains(view, "Referenced by") {
		t.Errorf("그래프 없이 역방향이 나왔다:\n%s", view)
	}
}

// TestRelationGroupsByFieldPath는 같은 필드에서 나온 대상이 하나의 관계 이름 아래
// 묶이는지 확인한다.
func TestRelationGroupsByFieldPath(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		relationResource(model.TypeEC2Subnet, "subnet-a", "private-a"),
		relationResource(model.TypeEC2Subnet, "subnet-c", "private-c"),
		relationResource(model.TypeRDSDBInstance, "orders-db", "orders-db",
			model.Ref{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"},
			model.Ref{Type: model.TypeEC2Subnet, ID: "subnet-c", Relation: "DBSubnetGroup.Subnets.SubnetIdentifier"}),
	}

	g, err := graph.Build(resources)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	view := renderDetail(New(true), relationGroups(), resources[2], g)

	// 관계 이름은 한 번만 나오고 그 아래 대상 둘이 묶인다.
	if strings.Count(view, "DBSubnetGroup.Subnets.SubnetIdentifier") != 1 {
		t.Errorf("같은 필드가 관계 이름으로 반복됐다:\n%s", view)
	}
	if !strings.Contains(view, "private-a") || !strings.Contains(view, "private-c") {
		t.Errorf("묶인 대상이 다 보이지 않는다:\n%s", view)
	}
}

// TestDetailHelpStaysAtBottomWhenScrolled는 상세 화면을 스크롤해도 도움말이 콘텐츠와
// 겹치지 않고 화면 맨 아래에 고정되는지 확인한다.
//
// 예전에는 viewport가 창을 꽉 채워, 콘텐츠가 스크롤 밖으로 잘리면 도움말이 잘린 줄 바로
// 아래에 붙어 상세 정보와 겹쳐 보였다. viewport 높이를 도움말 자리만큼 줄여 이를 없앤다.
func TestDetailHelpStaysAtBottomWhenScrolled(t *testing.T) {
	t.Parallel()

	// 창보다 긴 관계 목록을 만든다.
	var refs []model.Ref
	for i := range 30 {
		refs = append(refs, model.Ref{
			Type: model.TypeEC2Subnet, ID: "subnet-" + string(rune('a'+i%26)),
			Relation: "DBSubnetGroup.Subnets.SubnetIdentifier",
		})
	}
	res := relationResource(model.TypeRDSDBInstance, "big-db", "big-db", refs...)
	g, err := graph.Build([]model.Resource{res})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := Model{theme: New(true), width: 100, height: 12, deps: Deps{ResourceGroups: relationGroups()}}
	m = m.resize(tea.WindowSizeMsg{Width: 100, Height: 12})
	m.detail.SetContent(renderDetail(m.theme, relationGroups(), res, g))
	m.detail.GotoBottom()
	m.screen = ScreenDetail

	lines := strings.Split(m.View(), "\n")

	// 마지막 줄은 도움말이어야 한다.
	if last := lines[len(lines)-1]; !strings.Contains(last, "q quit") {
		t.Errorf("도움말이 마지막 줄이 아니다: %q", last)
	}
	// 도움말은 딱 한 번만 나와야 한다. 콘텐츠 중간에 섞이면 안 된다.
	if got := strings.Count(m.View(), "q quit"); got != 1 {
		t.Errorf("도움말이 %d번 나온다, want 1", got)
	}
	// 전체 높이가 창 높이를 넘지 않아야 한다.
	if len(lines) > 12 {
		t.Errorf("상세 화면이 %d줄로 창 높이 12를 넘었다", len(lines))
	}
}
