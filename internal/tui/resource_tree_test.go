package tui_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
	"github.com/cnlgks1/cloudloupe/internal/tui"
)

// treeDeps는 타입 수가 다른 서비스 네 개를 가진 카탈로그를 만든다.
//
// 타입이 1개인 서비스와 여러 개인 서비스를 섞어야 화면 흐름이 타입 수에 따라 갈리지 않는지
// 확인할 수 있다.
func treeDeps() (tui.Deps, *[]string) {
	queried := new([]string)

	deps := okDeps(nil)
	deps.ResourceGroups = []tui.ResourceGroup{
		{ID: "ec2", Label: "EC2", Types: []tui.ResourceType{
			{ID: "ec2:instance", Label: "Instances"},
			{ID: "ec2:volume", Label: "Volumes"},
			{ID: "ec2:vpc", Label: "VPCs"},
		}},
		{ID: "autoscaling", Label: "Auto Scaling", Types: []tui.ResourceType{
			{ID: "autoscaling:autoScalingGroup", Label: "Auto Scaling groups"},
		}},
		{ID: "rds", Label: "RDS", Types: []tui.ResourceType{
			{ID: "rds:dbCluster", Label: "DB clusters"},
			{ID: "rds:dbInstance", Label: "DB instances"},
		}},
		{ID: "iam", Label: "IAM", Types: []tui.ResourceType{
			{ID: "iam:role", Label: "Roles"},
		}},
	}
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		*queried = append([]string(nil), types...)

		return collect.Result{Resources: []model.Resource{
			{Type: "ec2:instance", ID: "i-1", Name: "web-01", Region: "ap-northeast-2"},
		}}
	}

	return deps, queried
}

// treeModel은 리소스 선택 화면까지 진행한 모델을 만든다.
func treeModel(t *testing.T, deps tui.Deps) tui.Model {
	t.Helper()

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 프로필 → 리전
	m = send(m, keyMsg("enter")) // 리전 → 리소스 선택

	if m.Screen() != tui.ScreenResource {
		t.Fatalf("화면 = %v, want 리소스 선택", m.Screen())
	}

	return m
}

// TestTreeStartsCollapsedWithEveryService는 첫 화면이 서비스만 보여주는지 확인한다.
//
// 접힌 줄 수가 서비스 수와 같다는 것이 규모를 감당하는 근거다. resource type이 수백 개가
// 되어도 첫 화면 길이는 서비스 수만큼이다.
func TestTreeStartsCollapsedWithEveryService(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	view := treeModel(t, deps).View()

	for _, service := range []string{"EC2", "Auto Scaling", "RDS", "IAM"} {
		if !strings.Contains(view, service) {
			t.Errorf("서비스 %q가 첫 화면에 없다:\n%s", service, view)
		}
	}

	// 접힌 상태에서는 resource type이 보이지 않는다.
	for _, typeID := range []string{"ec2:instance", "rds:dbCluster"} {
		if strings.Contains(view, typeID) {
			t.Errorf("접힌 상태인데 %q가 보인다:\n%s", typeID, view)
		}
	}

	// 전체 규모를 상단에 알려준다. 무엇을 조회할 수 있는지 세어 보지 않아도 알 수 있어야 한다.
	if !strings.Contains(view, "4 services") || !strings.Contains(view, "7 resource types") {
		t.Errorf("상단에 규모가 없다:\n%s", view)
	}
}

// TestTreeExpandsInPlace는 펼침이 화면을 바꾸지 않고 제자리에서 일어나는지 확인한다.
//
// 화면이 넘어가면 다른 서비스가 시야에서 사라져 지금 어디인지 잃는다. 콘솔 왼쪽 메뉴가
// 목록을 보는 동안에도 계속 보이는 것과 같은 이유다.
func TestTreeExpandsInPlace(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)

	m = send(m, keyMsg("right"))
	if m.Screen() != tui.ScreenResource {
		t.Fatalf("펼친 뒤 화면 = %v, want 리소스 선택 유지", m.Screen())
	}

	view := m.View()
	for _, want := range []string{"ec2:instance", "ec2:volume", "ec2:vpc"} {
		if !strings.Contains(view, want) {
			t.Errorf("펼친 서비스의 %q가 없다:\n%s", want, view)
		}
	}
	// 펼치는 동안에도 다른 서비스는 계속 보인다.
	if !strings.Contains(view, "RDS") || !strings.Contains(view, "IAM") {
		t.Errorf("펼친 뒤 다른 서비스가 사라졌다:\n%s", view)
	}

	m = send(m, keyMsg("left"))
	if view := m.View(); strings.Contains(view, "ec2:instance") {
		t.Errorf("접었는데 resource type이 남아 있다:\n%s", view)
	}
}

// TestTreeQueriesCursorRow는 선택 없이 enter를 눌렀을 때 커서 줄을 조회하는지 확인한다.
func TestTreeQueriesCursorRow(t *testing.T) {
	t.Parallel()

	t.Run("서비스 줄은 그 서비스 전체", func(t *testing.T) {
		t.Parallel()

		deps, queried := treeDeps()
		m := step(treeModel(t, deps), keyMsg("enter"))

		if m.Screen() != tui.ScreenList {
			t.Fatalf("화면 = %v, want 목록", m.Screen())
		}
		want := []string{"ec2:instance", "ec2:volume", "ec2:vpc"}
		if !slices.Equal(*queried, want) {
			t.Errorf("조회 타입 = %v, want %v", *queried, want)
		}
	})

	t.Run("타입 줄은 그 타입만", func(t *testing.T) {
		t.Parallel()

		deps, queried := treeDeps()
		m := treeModel(t, deps)
		m = send(m, keyMsg("right"), keyMsg("down"), keyMsg("down")) // Volumes
		step(m, keyMsg("enter"))

		if want := []string{"ec2:volume"}; !slices.Equal(*queried, want) {
			t.Errorf("조회 타입 = %v, want %v", *queried, want)
		}
	})
}

// TestTreeSelectionSurvivesCollapse는 접어도 선택이 유지되는지 확인한다.
//
// 서비스를 넘나들며 고르려면 접었다 펴는 동안 선택이 살아 있어야 한다. 접힌 서비스의
// 선택은 화면에 보이지 않으므로 부분 선택 표시와 상단 개수로 드러낸다.
func TestTreeSelectionSurvivesCollapse(t *testing.T) {
	t.Parallel()

	deps, queried := treeDeps()
	m := treeModel(t, deps)

	// EC2를 펼쳐 Instances를 체크하고 접는다.
	m = send(m, keyMsg("right"), keyMsg("down"), keyMsg("space"))
	m = send(m, keyMsg("up"), keyMsg("left"))

	view := m.View()
	if !strings.Contains(view, "1 selected") {
		t.Errorf("접은 뒤 선택 개수가 없다:\n%s", view)
	}
	if !strings.Contains(view, tui.New(true).Glyphs.Partial) {
		t.Errorf("부분 선택 표시가 없다:\n%s", view)
	}

	// RDS로 내려가 서비스 전체를 체크한다.
	m = send(m, keyMsg("down"), keyMsg("down"), keyMsg("space"))
	if view := m.View(); !strings.Contains(view, "3 selected") {
		t.Errorf("서비스 간 선택이 합쳐지지 않았다:\n%s", view)
	}

	step(m, keyMsg("enter"))
	want := []string{"ec2:instance", "rds:dbCluster", "rds:dbInstance"}
	if !slices.Equal(*queried, want) {
		t.Errorf("조회 타입 = %v, want %v", *queried, want)
	}
}

// TestTreeServiceToggleSelectsAllTypes는 서비스 줄의 space가 그 서비스 전체를 켜고 끄는지
// 확인한다. 이전 2단계 화면에서 space로 그룹 전체를 고르던 편의를 유지한다.
func TestTreeServiceToggleSelectsAllTypes(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)

	m = send(m, keyMsg("space"))
	if view := m.View(); !strings.Contains(view, "3 selected") {
		t.Errorf("서비스 전체 선택이 안 됐다:\n%s", view)
	}

	m = send(m, keyMsg("space"))
	if view := m.View(); strings.Contains(view, "selected") {
		t.Errorf("다시 눌렀을 때 선택이 해제되지 않았다:\n%s", view)
	}
}

// TestTreeSearchFlattensAcrossServices는 검색이 서비스를 넘어 평면 목록으로 좁히는지
// 확인한다.
//
// 서비스가 많아지면 트리를 펼쳐가며 찾는 것이 비현실적이다. 이름을 아는 사용자는 검색으로
// 바로 도달해야 한다.
func TestTreeSearchFlattensAcrossServices(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)

	m = send(m, keyMsg("/"), keyMsg("db"), keyMsg("enter"))

	view := m.View()
	for _, want := range []string{"rds:dbCluster", "rds:dbInstance", "2/7 shown"} {
		if !strings.Contains(view, want) {
			t.Errorf("검색 결과에 %q가 없다:\n%s", want, view)
		}
	}
	if strings.Contains(view, "ec2:instance") {
		t.Errorf("검색과 무관한 타입이 남아 있다:\n%s", view)
	}

	// 검색 중에는 서비스 이름이 열로 붙어 어느 서비스인지 알 수 있다.
	if !strings.Contains(view, "RDS") {
		t.Errorf("검색 결과에 서비스 열이 없다:\n%s", view)
	}
}

// TestTreeSelectAllAppliesToSearchOnly는 a가 검색으로 좁힌 결과에만 동작하는지 확인한다.
//
// 필터 없이 전부 선택할 수 있으면 리소스 타입 수 × 리전 수만큼 작업이 만들어져, 사용자가
// 의도하지 않은 대량 조회를 한 번에 시작하게 된다.
func TestTreeSelectAllAppliesToSearchOnly(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)

	m = send(m, keyMsg("a"))
	if view := m.View(); strings.Contains(view, "selected") {
		t.Errorf("검색 없이 a로 전체 선택이 됐다:\n%s", view)
	}

	m = send(m, keyMsg("/"), keyMsg("db"), keyMsg("enter"), keyMsg("a"))
	if view := m.View(); !strings.Contains(view, "2 selected") {
		t.Errorf("검색 결과 전체 선택이 안 됐다:\n%s", view)
	}
}

// TestTreeBackUnwindsSearchBeforeLeaving은 검색이 걸린 상태의 뒤로 가기가 먼저 검색을
// 푸는지 확인한다. 걸러진 화면을 남긴 채 나가면 다시 들어왔을 때 목록이 왜 짧은지 알 수 없다.
func TestTreeBackUnwindsSearchBeforeLeaving(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)
	m = send(m, keyMsg("/"), keyMsg("db"), keyMsg("enter"))

	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenResource {
		t.Fatalf("검색 상태에서 esc 후 화면 = %v, want 리소스 선택 유지", m.Screen())
	}
	if view := m.View(); !strings.Contains(view, "EC2") || strings.Contains(view, "shown") {
		t.Errorf("검색이 풀리지 않았다:\n%s", view)
	}

	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenRegion {
		t.Errorf("두 번째 esc 후 화면 = %v, want 리전 선택", m.Screen())
	}
}

// TestTreeCollapseAllShortensScreen은 z가 모든 서비스를 접는지 확인한다.
func TestTreeCollapseAllShortensScreen(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)

	m = send(m, keyMsg("right"), keyMsg("down"), keyMsg("down"), keyMsg("down"), keyMsg("down"))
	m = send(m, keyMsg("right")) // Auto Scaling도 펼침
	m = send(m, keyMsg("z"))

	view := m.View()
	for _, typeID := range []string{"ec2:instance", "autoscaling:autoScalingGroup"} {
		if strings.Contains(view, typeID) {
			t.Errorf("z를 눌렀는데 %q가 남아 있다:\n%s", typeID, view)
		}
	}
}

// TestTreeScalesToManyServices는 서비스가 많아도 첫 화면이 서비스 수만큼만 나오고 검색이
// 그대로 동작하는지 확인한다.
func TestTreeScalesToManyServices(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	deps.ResourceGroups = nil
	for i := range 100 {
		id := "svc" + strconv.Itoa(i)
		group := tui.ResourceGroup{ID: id, Label: "Service " + strconv.Itoa(i)}
		for j := range 5 {
			group.Types = append(group.Types, tui.ResourceType{
				ID:    id + ":type" + strconv.Itoa(j),
				Label: "Type " + strconv.Itoa(j),
			})
		}
		deps.ResourceGroups = append(deps.ResourceGroups, group)
	}

	m := treeModel(t, deps)

	view := m.View()
	if !strings.Contains(view, "100 services") || !strings.Contains(view, "500 resource types") {
		t.Errorf("규모 표시가 없다:\n%s", view)
	}

	// 특정 타입을 찾을 때 100개를 펼치지 않고 검색으로 좁힌다.
	m = send(m, keyMsg("/"), keyMsg("svc42:type3"), keyMsg("enter"))
	if view := m.View(); !strings.Contains(view, "1/500 shown") {
		t.Errorf("검색으로 좁혀지지 않았다:\n%s", view)
	}
}

// TestTreeHidesExpanderOnSingleTypeService는 펼칠 것이 없는 서비스에 펼침 표시를 두지
// 않는지 확인한다.
//
// 트리에서 잎 노드에 펼침 손잡이를 그리지 않는 것과 같다. 대신 하나뿐인 Type ID를 서비스
// 줄에 바로 보여주므로 펼치지 않아도 숨는 정보가 없다.
func TestTreeHidesExpanderOnSingleTypeService(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)
	glyphs := tui.New(true).Glyphs

	for _, line := range strings.Split(m.View(), "\n") {
		switch {
		case strings.Contains(line, "Auto Scaling"), strings.Contains(line, "IAM"):
			// 타입이 1개인 서비스: 펼침 표시가 없고 Type ID가 그 줄에 있다.
			if strings.Contains(line, glyphs.Collapsed) {
				t.Errorf("타입 1개 서비스에 펼침 표시가 있다: %q", line)
			}
			if !strings.Contains(line, ":") {
				t.Errorf("타입 1개 서비스 줄에 Type ID가 없다: %q", line)
			}
		case strings.Contains(line, "EC2"), strings.Contains(line, "RDS"):
			// 타입이 여러 개인 서비스: 펼침 표시가 있고 Type ID는 펼쳐야 나온다.
			if !strings.Contains(line, glyphs.Collapsed) {
				t.Errorf("타입 여러 개 서비스에 펼침 표시가 없다: %q", line)
			}
		}
	}
}

// TestTreeSingleTypeServiceQueriesOnRightArrow는 펼칠 수 없는 줄에서 →가 조회로
// 넘어가는지 확인한다. 눌러도 아무 일이 없으면 도구가 멈춘 것처럼 보인다.
func TestTreeSingleTypeServiceQueriesOnRightArrow(t *testing.T) {
	t.Parallel()

	deps, queried := treeDeps()
	m := treeModel(t, deps)

	m = send(m, keyMsg("down")) // Auto Scaling (타입 1개)
	m = step(m, keyMsg("right"))

	if m.Screen() != tui.ScreenList {
		t.Fatalf("타입 1개 서비스에서 → 후 화면 = %v, want 목록", m.Screen())
	}
	if want := []string{"autoscaling:autoScalingGroup"}; !slices.Equal(*queried, want) {
		t.Errorf("조회 타입 = %v, want %v", *queried, want)
	}
}

// TestTreeKeepsTypeIDReadableAtNarrowWidth는 좁은 터미널에서도 Type ID가 잘리지 않는지
// 확인한다. aws CLI 출력이나 리포트와 대조하는 값이라 잘리면 쓸모가 없다.
func TestTreeKeepsTypeIDReadableAtNarrowWidth(t *testing.T) {
	t.Parallel()

	deps, _ := treeDeps()
	m := treeModel(t, deps)

	// 80칸은 지원하는 가장 좁은 터미널이다.
	if view := m.View(); !strings.Contains(view, "autoscaling:autoScalingGroup") {
		t.Errorf("폭 80에서 Type ID가 잘렸다:\n%s", view)
	}
}
