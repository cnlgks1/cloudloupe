package tui_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
	"github.com/cnlgks1/cloudloupe/internal/tui"
)

// 이 테스트들은 렌더링 문자열이 아니라 "메시지 입력 → 기대 화면"으로 상태 전이를
// 검증한다(원칙 11). 렌더링 비교는 스타일이 조금만 바뀌어도 깨진다.
//
// TUI는 awsclient/collect를 직접 부르지 않고 Deps 함수로 주입받으므로, 여기서는 AWS
// 없이 가짜 Deps를 넘긴다.

func sampleProfiles() []awsclient.Profile {
	return []awsclient.Profile{
		{Name: "prod", Kind: awsclient.KindSSO, Region: "ap-northeast-2"},
		{Name: "staging", Kind: awsclient.KindSSO, Region: "us-east-1"},
	}
}

func sampleResources() []model.Resource {
	return []model.Resource{
		{
			Type:   model.TypeELBv2LoadBalancer,
			ID:     "web-alb",
			Name:   "web-alb",
			Region: "ap-northeast-2",
			Fields: []model.Field{{Key: "스킴", Value: "internet-facing"}},
			Related: []model.Ref{
				{Type: model.TypeELBv2TargetGroup, ID: "web-tg", Relation: model.RelationForwardsTo},
			},
		},
		{Type: model.TypeEC2Instance, ID: "i-0a1b", Name: "web-01", Region: "ap-northeast-2"},
	}
}

// okDeps는 성공하는 가짜 의존성을 만든다. 프로필 로딩도 성공한다.
func okDeps(resources []model.Resource) tui.Deps {
	return tui.Deps{
		LoadProfiles: func(_ awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error) {
			return sampleProfiles(), awsclient.Locations{}, nil
		},
		ResourceGroups: []tui.ResourceGroup{
			{
				ID:    "ec2",
				Label: "EC2",
				Types: []tui.ResourceType{
					{ID: model.TypeEC2Instance, Label: "EC2 인스턴스"},
					{ID: model.TypeEC2Volume, Label: "Volumes"},
				},
			},
			{
				ID:    "elbv2",
				Label: "ELBv2",
				Types: []tui.ResourceType{
					{ID: model.TypeELBv2LoadBalancer, Label: "로드 밸런서"},
					{ID: model.TypeELBv2TargetGroup, Label: "타깃 그룹"},
				},
			},
		},
		Identify: func(_ context.Context, profile, _ string, _ awsclient.Locations) (awsclient.Identity, error) {
			return awsclient.Identity{AccountID: "123456789012", ARN: "arn:aws:sts::123456789012:user/" + profile}, nil
		},
		Collect: func(_ context.Context, _ string, _, _ []string, _ awsclient.Locations) collect.Result {
			return collect.Result{Resources: resources}
		},
		Explain: awsclient.Explain,
	}
}

func newTestModel(t *testing.T, deps tui.Deps) tui.Model {
	t.Helper()

	m := tui.NewModel(tui.New(true), deps, awsclient.Override{})

	return send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
}

// mkModel은 필터 흐름 테스트용으로 리소스를 담은 모델을 만든다(창 크기는 미적용).
func mkModel(t *testing.T, resources []model.Resource) tui.Model {
	t.Helper()

	return tui.NewModel(tui.New(true), okDeps(resources), awsclient.Override{})
}

func send(m tui.Model, msgs ...tea.Msg) tui.Model {
	var cur tea.Model = m
	for _, msg := range msgs {
		cur, _ = cur.Update(msg)
	}

	return cur.(tui.Model)
}

// step은 Update를 호출하고, 반환된 tea.Cmd가 만들어내는 메시지를 모두 흘려보낸다.
//
// 신원 확인·수집은 tea.Cmd로 나가서 tea.Msg로 돌아온다. 실제 런타임이 하는 일을 흉내
// 내어, Cmd를 실행하고 그 결과 메시지를 다시 Update에 넣는다. tea.Batch는 여러 Cmd를
// tea.BatchMsg(=[]tea.Cmd)로 묶으므로, 그 경우 각 Cmd를 재귀적으로 처리한다.
func step(m tui.Model, msg tea.Msg) tui.Model {
	next, cmd := m.Update(msg)

	return drain(next.(tui.Model), cmd)
}

func drain(m tui.Model, cmd tea.Cmd) tui.Model {
	if cmd == nil {
		return m
	}

	switch msg := cmd().(type) {
	case nil:
		return m
	case tea.BatchMsg:
		// 배치의 각 Cmd를 한 번씩 실행해 결과 메시지를 흘려보낸다. 스피너 Tick은
		// 상태를 바꾸지 않으므로 결과 Cmd를 더 따라가지 않는다(무한 Tick 방지).
		for _, c := range msg {
			if c == nil {
				continue
			}

			if inner := c(); inner != nil {
				next, _ := m.Update(inner)
				m = next.(tui.Model)
			}
		}

		return m
	default:
		next, _ := m.Update(msg)

		return next.(tui.Model)
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestStartsOnProfileScreen(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(nil))
	if m.Screen() != tui.ScreenProfile {
		t.Errorf("초기 화면 = %v, want 프로필 선택", m.Screen())
	}
}

func TestStartsOnConfigPathWhenNoConfig(t *testing.T) {
	t.Parallel()

	// 설정을 못 찾으면 죽지 않고 경로 입력 화면으로 시작해야 한다. 사용자가 물어본 흐름이다.
	deps := okDeps(nil)
	deps.LoadProfiles = func(_ awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error) {
		return nil, awsclient.Locations{}, awsclient.ErrNoSharedConfig
	}

	m := newTestModel(t, deps)
	if m.Screen() != tui.ScreenConfigPath {
		t.Errorf("설정 없을 때 화면 = %v, want 경로 입력", m.Screen())
	}
}

func TestConfigPathEntryRetriesLoad(t *testing.T) {
	t.Parallel()

	// 경로 입력 화면에서 경로를 입력하고 enter를 누르면, 그 경로로 다시 로딩한다.
	// 이번엔 성공하도록 해서 프로필 선택으로 넘어가는지 본다.
	loaded := ""
	attempt := 0

	deps := okDeps(nil)
	deps.LoadProfiles = func(ov awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error) {
		attempt++
		if attempt == 1 {
			// 첫 로딩(NewModel 안)은 실패 → 경로 입력 화면.
			return nil, awsclient.Locations{}, awsclient.ErrNoSharedConfig
		}

		loaded = ov.ConfigPath

		return sampleProfiles(), awsclient.Locations{}, nil
	}

	m := newTestModel(t, deps)
	if m.Screen() != tui.ScreenConfigPath {
		t.Fatalf("경로 입력 화면으로 시작해야 한다, got %v", m.Screen())
	}

	// 경로를 타이핑하고 enter.
	m = send(m, keyMsg("/tmp/custom/config"))
	m = send(m, keyMsg("enter"))

	if m.Screen() != tui.ScreenProfile {
		t.Errorf("경로 입력 후 화면 = %v, want 프로필 선택", m.Screen())
	}

	if loaded != "/tmp/custom/config" {
		t.Errorf("입력한 경로로 재시도해야 한다, got %q", loaded)
	}
}

func TestFullFlowProfileToList(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(sampleResources()))

	// 프로필 선택 → 신원 확인 Cmd 실행 → 리전 선택.
	m = step(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenRegion {
		t.Fatalf("프로필 선택 후 화면 = %v, want 리전 선택", m.Screen())
	}

	// 리전 선택 → 리소스 선택. 무조건 조회로 직행하지 않는다.
	m = send(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenResource {
		t.Fatalf("리전 선택 후 화면 = %v, want 리소스 선택", m.Screen())
	}

	// 커서가 서비스 줄에 있으면 그 서비스 전체를 조회한다.
	m = step(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenList {
		t.Fatalf("리소스 선택 후 화면 = %v, want 리소스 목록", m.Screen())
	}

	// enter로 상세, esc로 목록 복귀.
	m = send(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenDetail {
		t.Fatalf("enter 후 화면 = %v, want 상세", m.Screen())
	}

	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenList {
		t.Errorf("esc 후 화면 = %v, want 리소스 목록", m.Screen())
	}

	// 선택 화면이 하나뿐이므로 목록에서 뒤로 가면 항상 그 화면이다. 어느 경로로 조회했든
	// 돌아갈 곳이 같아서 사용자가 규칙을 짐작할 필요가 없다.
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenResource {
		t.Errorf("목록에서 esc 후 화면 = %v, want 리소스 선택", m.Screen())
	}

	// 거기서 다시 뒤로 가면 리전이다.
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenRegion {
		t.Errorf("리소스 선택에서 esc 후 화면 = %v, want 리전 선택", m.Screen())
	}
}

// TestBreadcrumbRevealsOnlyConfirmedSteps는 경로 헤더가 확정된 단계까지만 보여주는지
// 확인한다.
//
// 뒤로 나온 화면에서 아직 고르지 않은 뒷단계 값을 계속 보여주면 지금 무엇을 고치고 있는지
// 헷갈린다. 선택 화면은 자체 헤더에 규모와 선택 수를 보여주므로 경로에 겹쳐 쓰지 않는다.
func TestBreadcrumbRevealsOnlyConfirmedSteps(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(sampleResources()))
	m = step(m, keyMsg("enter")) // 리전

	// 리전을 고르는 중에는 뒷단계인 서비스가 경로에 없어야 한다.
	if got := m.View(); strings.Contains(got, "Service: ") {
		t.Errorf("리전 화면 경로에 서비스가 보인다:\n%s", got)
	}

	// 선택 화면도 마찬가지다. 아직 조회하지 않았으므로 확정된 것은 리전까지다.
	m = send(m, keyMsg("enter"))
	if got := m.View(); strings.Contains(got, "Service: ") {
		t.Errorf("선택 화면 경로에 확정되지 않은 서비스가 보인다:\n%s", got)
	}

	// 조회한 뒤에는 무엇을 보고 있는지 경로에 드러난다.
	m = send(m, keyMsg("right"))                // 서비스 펼치기
	m = send(m, keyMsg("down"), keyMsg("down")) // Volumes
	m = step(m, keyMsg("enter"))                // 조회 → 목록

	got := m.View()
	if !strings.Contains(got, "Service: ") || !strings.Contains(got, "Resource type: ") ||
		!strings.Contains(got, "Volumes") {
		t.Errorf("조회 후 경로에 서비스와 resource type이 없다:\n%s", got)
	}

	// 리전 전환으로 돌아가면 뒷단계는 다시 감춰진다.
	m = send(m, keyMsg("r"))
	if got := m.View(); strings.Contains(got, "Service: ") ||
		strings.Contains(got, "Resource type: ") {
		t.Errorf("리전 전환 화면에 이전 리소스 선택이 남아 있다:\n%s", got)
	}
}

// TestHelpBarAlwaysEndsWithBackAndQuit은 모든 화면의 하단 도움말이 같은 꼬리로 끝나는지
// 확인한다. 같은 키가 화면마다 다르게 안내되면 사용자가 매번 다시 읽어야 한다.
func TestHelpBarAlwaysEndsWithBackAndQuit(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(sampleResources()))

	screens := []struct {
		name string
		keys []tea.KeyMsg
	}{
		{name: "프로필"},
		{name: "리전", keys: []tea.KeyMsg{keyMsg("enter")}},
		{name: "리소스 그룹", keys: []tea.KeyMsg{keyMsg("enter"), keyMsg("enter")}},
		{name: "세부 항목", keys: []tea.KeyMsg{keyMsg("enter"), keyMsg("enter"), keyMsg("enter")}},
		{name: "목록", keys: []tea.KeyMsg{keyMsg("enter"), keyMsg("enter"), keyMsg("enter"), keyMsg("enter")}},
	}

	for _, screen := range screens {
		current := m
		for _, k := range screen.keys {
			current = step(current, k)
		}

		view := current.View()
		if !strings.Contains(view, "esc/← back") || !strings.Contains(view, "q quit") {
			t.Errorf("%s 화면 도움말에 공통 뒤로·종료가 없다:\n%s", screen.name, view)
		}
	}
}

// TestServiceBehavesSameRegardlessOfTypeCount는 resource type 수가 화면 흐름을 바꾸지
// 않는지 확인한다.
//
// 이전에는 타입이 하나뿐인 서비스만 두 번째 화면을 건너뛰었다. 그래서 카탈로그에 타입을
// 추가하면 그 서비스의 흐름이 1단계에서 2단계로 변했다. 수집기를 넣었을 뿐인데 사용자
// 경험이 달라지는 구조였다. 이 테스트가 그 예외의 재발을 막는다.
func TestServiceBehavesSameRegardlessOfTypeCount(t *testing.T) {
	t.Parallel()

	var gotTypes []string
	deps := okDeps(sampleResources())
	deps.ResourceGroups = append(deps.ResourceGroups, tui.ResourceGroup{
		ID:    "kms",
		Label: "KMS",
		Types: []tui.ResourceType{{ID: model.TypeKMSKey, Label: "Keys"}},
	})
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = append([]string(nil), types...)

		return collect.Result{Resources: sampleResources()}
	}

	// 타입 1개 서비스(KMS)와 2개 서비스(EC2)가 같은 키 조작에 같은 방식으로 반응해야 한다.
	for _, tc := range []struct {
		name  string
		downs int
		want  []string
	}{
		{name: "타입 1개 서비스", downs: 2, want: []string{model.TypeKMSKey}},
		{name: "타입 여러 개 서비스", downs: 0, want: []string{model.TypeEC2Instance, model.TypeEC2Volume}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotTypes = nil

			m := newTestModel(t, deps)
			m = step(m, keyMsg("enter")) // 리전
			m = send(m, keyMsg("enter")) // 리소스 선택
			for range tc.downs {
				m = send(m, keyMsg("down"))
			}

			// enter 한 번으로 커서 서비스 전체를 조회한다. 중간 화면이 끼어들지 않는다.
			m = step(m, keyMsg("enter"))
			if m.Screen() != tui.ScreenList {
				t.Fatalf("enter 후 화면 = %v, want 목록", m.Screen())
			}
			if !slices.Equal(gotTypes, tc.want) {
				t.Errorf("조회 타입 = %v, want %v", gotTypes, tc.want)
			}

			// 뒤로 가는 곳도 같다.
			m = send(m, keyMsg("esc"))
			if m.Screen() != tui.ScreenResource {
				t.Errorf("목록에서 esc 후 화면 = %v, want 리소스 선택", m.Screen())
			}
		})
	}
}

func TestEscGoesBackOneStep(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(sampleResources()))
	m = step(m, keyMsg("enter")) // 리전 선택

	// 리전에서 esc → 프로필로.
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenProfile {
		t.Errorf("리전에서 esc 후 = %v, want 프로필", m.Screen())
	}
}

func TestIdentityFailureShowsError(t *testing.T) {
	t.Parallel()

	deps := okDeps(nil)
	deps.Identify = func(_ context.Context, _, _ string, _ awsclient.Locations) (awsclient.Identity, error) {
		return awsclient.Identity{}, errors.New("ExpiredToken")
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter"))

	if m.Screen() != tui.ScreenError {
		t.Fatalf("신원 확인 실패 후 화면 = %v, want 오류", m.Screen())
	}

	// 오류 화면에서 esc → 프로필로 복귀.
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenProfile {
		t.Errorf("오류에서 esc 후 = %v, want 프로필", m.Screen())
	}
}

func TestRegionMultiSelect(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, okDeps(sampleResources()))
	m = step(m, keyMsg("enter")) // 리전 선택

	// space로 현재 리전 체크 → enter로 타입 선택 화면.
	m = send(m, keyMsg("space"))
	m = send(m, keyMsg("enter"))

	if m.Screen() != tui.ScreenResource {
		t.Errorf("리전 체크 후 → 화면 = %v, want 타입 선택", m.Screen())
	}

	// 커서 서비스 전체를 조회 → 리소스 목록.
	m = step(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenList {
		t.Errorf("리소스 선택 후 조회 → 화면 = %v, want 리소스 목록", m.Screen())
	}
}

func TestResourceTypeSelectionFiltersCollect(t *testing.T) {
	t.Parallel()

	// 사용자가 특정 타입만 고르면 그 타입만 Collect에 전달되어야 한다. 무조건 전부
	// 조회하면 느리고 API 스로틀링 위험이다.
	var gotTypes []string

	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = types

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 리전
	m = send(m, keyMsg("enter")) // 타입 선택 화면

	// 첫 그룹(EC2)을 space로 체크하면 그룹의 내부 타입이 모두 전달되어야 한다.
	m = send(m, keyMsg("space"))
	_ = step(m, keyMsg("enter"))

	want := []string{model.TypeEC2Instance, model.TypeEC2Volume}
	if !slices.Equal(gotTypes, want) {
		t.Errorf("선택한 그룹의 타입이 모두 넘어가야 한다, got %v, want %v", gotTypes, want)
	}
}

func TestResourceTypeEnterSelectsCursorType(t *testing.T) {
	t.Parallel()

	// space 없이 enter하면 커서에 있는 첫 그룹(EC2)의 내부 타입을 모두 조회한다.
	var gotTypes []string

	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = types

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 리전
	m = send(m, keyMsg("enter")) // 리소스 선택 화면
	m = send(m, keyMsg("right")) // 커서 서비스를 펼친다
	m = send(m, keyMsg("down"))  // 첫 resource type으로 이동
	_ = step(m, keyMsg("enter")) // 체크 없이 조회 → 커서 타입 하나

	want := []string{model.TypeEC2Instance}
	if !slices.Equal(gotTypes, want) {
		t.Errorf("체크 없이 enter는 커서 타입 하나여야 한다, got %v, want %v", gotTypes, want)
	}
}

// TestTreeSpaceSelectsOnlyCheckedType은 체크한 resource type만 조회하는지 확인한다.
// 하나를 골랐는데 서비스 전체가 조회되면 화면과 결과가 어긋난다.
func TestTreeSpaceSelectsOnlyCheckedType(t *testing.T) {
	t.Parallel()

	var gotTypes []string
	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = append([]string(nil), types...)

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter"))                // 리전
	m = send(m, keyMsg("enter"))                // 리소스 선택
	m = send(m, keyMsg("right"))                // 서비스 펼치기
	m = send(m, keyMsg("down"), keyMsg("down")) // 두 번째 타입(Volumes)으로 이동
	m = send(m, keyMsg("space"))                // 그 타입만 체크
	_ = step(m, keyMsg("enter"))

	if want := []string{model.TypeEC2Volume}; !slices.Equal(gotTypes, want) {
		t.Errorf("체크한 resource type만 조회해야 한다, got %v, want %v", gotTypes, want)
	}
}

func TestViewIsPure(t *testing.T) {
	t.Parallel()

	// View를 여러 번 호출해도 같아야 한다. 상태를 바꾸면 안 된다(원칙 7).
	m := newTestModel(t, okDeps(sampleResources()))

	first := m.View()
	second := m.View()
	if first != second {
		t.Error("View가 호출마다 다르다 — 순수하지 않다")
	}
}

func TestViewRendersEveryScreenWithoutPanic(t *testing.T) {
	t.Parallel()

	// 두 테마 모두에서 모든 화면이 패닉 없이 렌더링되어야 한다.
	for _, ascii := range []bool{false, true} {
		m := tui.NewModel(tui.New(ascii), okDeps(sampleResources()), awsclient.Override{})
		m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})

		if m.View() == "" {
			t.Errorf("ascii=%v: 프로필 화면이 빈 문자열", ascii)
		}

		m = step(m, keyMsg("enter")) // 프로필 → 리전 (신원확인 Cmd)
		m = send(m, keyMsg("enter")) // 리전 → 리소스 선택 (Cmd 없음)
		m = step(m, keyMsg("enter")) // 리소스 선택 → 목록 (수집 Cmd)
		m = send(m, keyMsg("t"))
		if m.Screen() != tui.ScreenResourceKind || m.View() == "" {
			t.Errorf("ascii=%v: 종류 필터 화면을 렌더링하지 못함", ascii)
		}
		m = send(m, keyMsg("esc"))
		m = send(m, keyMsg("enter")) // 목록 → 상세

		if out := m.View(); !strings.Contains(out, "web-alb") {
			t.Errorf("ascii=%v: 상세 뷰에 리소스 이름이 없다:\n%s", ascii, out)
		}
	}
}

func TestBackNavigationKeepsTypeSelection(t *testing.T) {
	t.Parallel()

	// 타입을 골라 두고 esc로 리전에 갔다가 다시 타입 화면으로 돌아와도 선택이 남아야
	// 한다. "뒤로 가면 선택이 날아간다"는 회귀를 방어한다.
	var gotTypes []string

	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = types

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 리전
	m = send(m, keyMsg("enter")) // 타입 선택 화면

	// 첫 그룹(EC2)을 체크한다.
	m = send(m, keyMsg("space"))

	// esc로 리전으로 갔다가 enter로 다시 타입 화면으로.
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenRegion {
		t.Fatalf("타입에서 esc 후 = %v, want 리전", m.Screen())
	}

	m = send(m, keyMsg("enter"))
	if m.Screen() != tui.ScreenResource {
		t.Fatalf("리전에서 enter 후 = %v, want 타입 선택", m.Screen())
	}

	// 여기서 바로 조회하면, 앞서 고른 그룹의 내부 타입이 그대로 살아 있어야 한다.
	_ = step(m, keyMsg("enter"))
	want := []string{model.TypeEC2Instance, model.TypeEC2Volume}
	if !slices.Equal(gotTypes, want) {
		t.Errorf("뒤로 갔다 와도 선택한 그룹이 유지되어야 한다, got %v, want %v", gotTypes, want)
	}
}

func TestNewProfileResetsSelection(t *testing.T) {
	t.Parallel()

	// 프로필을 바꿔 신원 확인을 다시 하면 이전 프로필에서 고른 타입 선택이 비워져야
	// 한다. 프로필 A의 선택이 B로 새면 안 된다.
	var gotTypes []string

	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = types

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 리전
	m = send(m, keyMsg("enter")) // 리소스 선택 화면
	m = send(m, keyMsg("down"))  // 두 번째 그룹(ELBv2)으로 커서 이동
	m = send(m, keyMsg("space")) // ELBv2 그룹 체크

	// 프로필까지 뒤로: 리소스 선택 → 리전 → 프로필.
	m = send(m, keyMsg("esc"))
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenProfile {
		t.Fatalf("두 번 esc 후 = %v, want 프로필", m.Screen())
	}

	// 프로필에서 다시 enter → 리전 → 리소스 → 조회. 이전 ELBv2 선택은 초기화됐으므로
	// 커서의 첫 그룹 첫 항목만 넘어가야 한다.
	m = step(m, keyMsg("enter")) // 리전 (신원 확인 다시)
	m = send(m, keyMsg("enter")) // 리소스 선택 화면
	m = send(m, keyMsg("right")) // 커서 서비스를 펼친다
	m = send(m, keyMsg("down"))  // 첫 resource type
	_ = step(m, keyMsg("enter")) // 체크 없이 조회

	want := []string{model.TypeEC2Instance}
	if !slices.Equal(gotTypes, want) {
		t.Errorf("새 프로필에서는 이전 선택이 비워져야 한다, got %v, want %v", gotTypes, want)
	}
}

func TestArrowKeysNavigateList(t *testing.T) {
	t.Parallel()

	// 화살표(↑/↓)로 테이블 커서가 이동해야 한다. 비-터미널 테스트 환경에서는 선택 행
	// 강조가 렌더 문자열에 드러나지 않으므로, 커서 이동을 "실제 선택 결과"로 확인한다.
	// ↓ 두 번 → space로 세 번째 리전을 고르면, 첫 리전이 아닌 리전이 조회로 넘어가야 한다.
	// 화살표 이동이 실제 선택까지 반영되는지가 핵심이며, 이는 TestArrowSelectsRegionUnderCursor
	// 가 이미 한 칸 이동으로 검증한다. 여기서는 두 칸 이동도 동작하는지 함께 지킨다.
	var gotRegions []string

	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, regions, _ []string, _ awsclient.Locations) collect.Result {
		gotRegions = regions

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 리전 화면

	m = send(m, keyMsg("down"))
	m = send(m, keyMsg("down"))
	m = send(m, keyMsg("up")) // 순이동 확인: 아래 2 위 1 = 한 칸 아래
	m = send(m, keyMsg("space"))
	m = send(m, keyMsg("enter")) // 리소스 선택
	_ = step(m, keyMsg("enter")) // 조회

	if len(gotRegions) != 1 {
		t.Fatalf("화살표 이동 후 고른 리전 1개가 넘어가야 한다, got %v", gotRegions)
	}

	first := awsclient.Regions(sampleProfiles()[0].Region)[0].Code
	if gotRegions[0] == first {
		t.Errorf("화살표로 커서를 옮겼는데 여전히 첫 리전(%s)이 선택됐다", first)
	}
}

func TestArrowSelectsRegionUnderCursor(t *testing.T) {
	t.Parallel()

	// 화살표로 내려가 space로 고르면, 커서가 가리키던(첫 번째가 아닌) 리전이 선택되어
	// 조회로 넘어가야 한다. 화살표 이동이 실제 선택에까지 반영되는지 본다.
	var gotRegions []string

	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, regions, _ []string, _ awsclient.Locations) collect.Result {
		gotRegions = regions

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 리전 화면

	// 화살표로 한 칸 내려 두 번째 리전에 커서를 두고 space로 선택.
	m = send(m, keyMsg("down"))
	m = send(m, keyMsg("space"))
	m = send(m, keyMsg("enter")) // 리소스 선택 화면
	_ = step(m, keyMsg("enter")) // 조회

	if len(gotRegions) != 1 {
		t.Fatalf("화살표로 내려 고른 리전 1개가 조회로 넘어가야 한다, got %v", gotRegions)
	}

	// 첫 번째 리전이 아니어야 한다(커서가 내려갔으므로). 모델은 프로필의 기본 리전을
	// 목록 맨 위로 올리므로, 그 순서 기준의 첫 리전과 비교한다.
	first := awsclient.Regions(sampleProfiles()[0].Region)[0].Code
	if gotRegions[0] == first {
		t.Errorf("화살표로 내렸는데 여전히 첫 리전(%s)이 선택됐다", first)
	}
}

func TestArrowRightAdvancesLeftGoesBack(t *testing.T) {
	t.Parallel()

	// →는 다음 단계(enter와 동일), ←는 이전 단계(esc와 동일)로 가야 한다. 마법사형
	// 흐름을 화살표만으로 오갈 수 있어야 한다는 요구를 지킨다.
	m := newTestModel(t, okDeps(sampleResources()))

	// 프로필에서 → 로 신원 확인 → 리전.
	m = step(m, keyMsg("right"))
	if m.Screen() != tui.ScreenRegion {
		t.Fatalf("프로필에서 → 후 = %v, want 리전", m.Screen())
	}

	// 리전에서 → 로 타입.
	m = send(m, keyMsg("right"))
	if m.Screen() != tui.ScreenResource {
		t.Fatalf("리전에서 → 후 = %v, want 타입", m.Screen())
	}

	// 타입에서 ← 로 리전으로 되돌아온다.
	m = send(m, keyMsg("left"))
	if m.Screen() != tui.ScreenRegion {
		t.Fatalf("타입에서 ← 후 = %v, want 리전", m.Screen())
	}

	// 리전에서 ← 로 프로필로.
	m = send(m, keyMsg("left"))
	if m.Screen() != tui.ScreenProfile {
		t.Errorf("리전에서 ← 후 = %v, want 프로필", m.Screen())
	}
}

func TestProfileScreenCEntersPathInput(t *testing.T) {
	t.Parallel()

	// 프로필 화면에서 c를 누르면 경로 입력 화면으로 가야 한다. 기본 위치를 쓰는 사용자는
	// 방해받지 않고, 다른 경로를 쓰려는 사람만 이 문으로 들어간다.
	m := newTestModel(t, okDeps(sampleResources()))
	if m.Screen() != tui.ScreenProfile {
		t.Fatalf("시작 화면 = %v, want 프로필", m.Screen())
	}

	m = send(m, keyMsg("c"))
	if m.Screen() != tui.ScreenConfigPath {
		t.Errorf("c 입력 후 = %v, want 경로 입력", m.Screen())
	}

	// 프로필이 이미 있으므로 esc는 종료가 아니라 프로필로 복귀여야 한다.
	m = send(m, keyMsg("esc"))
	if m.Screen() != tui.ScreenProfile {
		t.Errorf("경로 입력에서 esc 후 = %v, want 프로필(뒤로)", m.Screen())
	}
}

func TestConfigPathSubmitsBothPaths(t *testing.T) {
	t.Parallel()

	// 경로 입력 화면에서 config와 credentials 두 경로를 각각 입력하고 enter를 누르면,
	// 둘 다 LoadProfiles로 넘어가야 한다. tab으로 두 번째 칸으로 이동한다.
	var gotOverride awsclient.Override

	deps := okDeps(sampleResources())
	deps.LoadProfiles = func(ov awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error) {
		gotOverride = ov

		return sampleProfiles(), awsclient.Locations{}, nil
	}

	m := newTestModel(t, deps)
	m = send(m, keyMsg("c")) // 경로 입력 화면

	// 첫 칸(config)에 경로 타이핑.
	m = send(m, keyMsg("/etc/aws/config"))

	// tab으로 credentials 칸으로 이동해 타이핑.
	m = send(m, tea.KeyMsg{Type: tea.KeyTab})
	m = send(m, keyMsg("/etc/aws/creds"))

	// enter로 적용.
	_ = send(m, keyMsg("enter"))

	if gotOverride.ConfigPath != "/etc/aws/config" {
		t.Errorf("ConfigPath = %q, want /etc/aws/config", gotOverride.ConfigPath)
	}

	if gotOverride.CredentialsPath != "/etc/aws/creds" {
		t.Errorf("CredentialsPath = %q, want /etc/aws/creds", gotOverride.CredentialsPath)
	}
}

func TestRegionEnterReplacesPreviousBareSelectionAfterBack(t *testing.T) {
	t.Parallel()

	var gotRegions []string
	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, regions, _ []string, _ awsclient.Locations) collect.Result {
		gotRegions = append([]string(nil), regions...)

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter")) // 프로필 → 첫 리전 화면
	regions := awsclient.Regions(sampleProfiles()[0].Region)
	if len(regions) < 2 {
		t.Fatal("리전 재선택 테스트에 리전이 2개 이상 필요함")
	}

	m = send(m, keyMsg("enter")) // 첫 리전을 bare Enter로 선택 → 타입
	m = send(m, keyMsg("left"))  // 타입 → 리전
	m = send(m, keyMsg("down"))  // 두 번째 리전으로 이동
	m = send(m, keyMsg("enter")) // 두 번째 리전 선택 → 리소스 선택
	_ = step(m, keyMsg("enter")) // 조회

	if got, want := gotRegions, []string{regions[1].Code}; !slices.Equal(got, want) {
		t.Errorf("재선택 후 조회 리전 = %v, want %v", got, want)
	}
}

func TestRegionBackPreservesExplicitMultiSelection(t *testing.T) {
	t.Parallel()

	var gotRegions []string
	var gotTypes []string
	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, regions, types []string, _ awsclient.Locations) collect.Result {
		gotRegions = append([]string(nil), regions...)
		gotTypes = append([]string(nil), types...)

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter"))
	regions := awsclient.Regions(sampleProfiles()[0].Region)

	m = send(m, keyMsg("space"))
	m = send(m, keyMsg("down"), keyMsg("space"))
	m = send(m, keyMsg("enter"))                 // 명시적 두 리전 → 리소스 선택
	m = send(m, keyMsg("down"), keyMsg("space")) // ELB 서비스를 명시적으로 선택한다.
	m = send(m, keyMsg("left"))                  // 리소스 선택 → 리전
	m = send(m, keyMsg("space"))                 // 두 번째 리전을 해제한다.
	m = send(m, keyMsg("up"), keyMsg("space"))   // 첫 번째 리전을 해제한다.
	m = send(m, keyMsg("down"), keyMsg("space"))
	m = send(m, keyMsg("up"), keyMsg("space")) // 같은 집합을 역순으로 완성한다.
	m = send(m, keyMsg("enter"))
	_ = step(m, keyMsg("enter"))

	wantRegions := []string{regions[1].Code, regions[0].Code}
	if !slices.Equal(gotRegions, wantRegions) {
		t.Errorf("뒤로 이동 후 다중 선택 = %v, want %v", gotRegions, wantRegions)
	}
	wantTypes := []string{model.TypeELBv2LoadBalancer, model.TypeELBv2TargetGroup}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Errorf("동일 다중 리전 재확정 후 조회 타입 = %v, want %v", gotTypes, wantTypes)
	}
}

func TestChangingRegionResetsResourceSelection(t *testing.T) {
	t.Parallel()

	type collectCall struct {
		regions []string
		types   []string
	}
	var calls []collectCall
	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, regions, types []string, _ awsclient.Locations) collect.Result {
		calls = append(calls, collectCall{
			regions: append([]string(nil), regions...),
			types:   append([]string(nil), types...),
		})

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter"))
	m = send(m, keyMsg("enter")) // 첫 리전 → 타입
	m = send(m, keyMsg("down"), keyMsg("space"))
	m = step(m, keyMsg("enter")) // ELB 명시 선택 → 첫 조회
	if m.Screen() != tui.ScreenList {
		t.Fatalf("첫 조회 후 화면 = %v, want 목록", m.Screen())
	}

	m = send(m, keyMsg("r"))
	m = send(m, keyMsg("down"), keyMsg("enter")) // 다른 리전 → 리소스 선택
	m = send(m, keyMsg("right"), keyMsg("down")) // 초기화된 첫 서비스의 첫 타입
	_ = step(m, keyMsg("enter"))                 // 두 번째 조회

	if len(calls) != 2 {
		t.Fatalf("Collect 호출 = %d, want 2", len(calls))
	}
	regions := awsclient.Regions(sampleProfiles()[0].Region)
	if got, want := calls[1].regions, []string{regions[1].Code}; !slices.Equal(got, want) {
		t.Errorf("두 번째 조회 리전 = %v, want %v", got, want)
	}
	wantTypes := []string{model.TypeEC2Instance}
	if !slices.Equal(calls[1].types, wantTypes) {
		t.Errorf("리전 변경 후 조회 타입 = %v, want 초기 타입 %v", calls[1].types, wantTypes)
	}
}

func TestConfirmingSameRegionPreservesResourceSelection(t *testing.T) {
	t.Parallel()

	var gotTypes []string
	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, _ []string, types []string, _ awsclient.Locations) collect.Result {
		gotTypes = append([]string(nil), types...)

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter"))
	m = send(m, keyMsg("enter"))
	m = send(m, keyMsg("down"), keyMsg("space")) // ELB를 명시적으로 선택한다.
	m = send(m, keyMsg("left"), keyMsg("enter")) // 같은 리전을 다시 확정한다.
	_ = step(m, keyMsg("enter"))

	want := []string{model.TypeELBv2LoadBalancer, model.TypeELBv2TargetGroup}
	if !slices.Equal(gotTypes, want) {
		t.Errorf("동일 리전 재확정 후 조회 타입 = %v, want %v", gotTypes, want)
	}
}

func TestChangingExplicitMultiRegionSelectionResetsResourceSelection(t *testing.T) {
	t.Parallel()

	var gotRegions []string
	var gotTypes []string
	deps := okDeps(sampleResources())
	deps.Collect = func(_ context.Context, _ string, regions, types []string, _ awsclient.Locations) collect.Result {
		gotRegions = append([]string(nil), regions...)
		gotTypes = append([]string(nil), types...)

		return collect.Result{Resources: sampleResources()}
	}

	m := newTestModel(t, deps)
	m = step(m, keyMsg("enter"))
	regions := awsclient.Regions(sampleProfiles()[0].Region)
	m = send(m, keyMsg("space"), keyMsg("down"), keyMsg("space"), keyMsg("enter"))
	m = send(m, keyMsg("down"), keyMsg("space"))                  // ELB를 명시적으로 선택한다.
	m = send(m, keyMsg("left"), keyMsg("space"), keyMsg("enter")) // 두 번째 리전을 해제한다.
	m = send(m, keyMsg("right"), keyMsg("down"))                  // 초기화된 첫 서비스의 첫 타입
	_ = step(m, keyMsg("enter"))

	if got, want := gotRegions, []string{regions[0].Code}; !slices.Equal(got, want) {
		t.Errorf("다중 선택 변경 후 조회 리전 = %v, want %v", got, want)
	}
	wantTypes := []string{model.TypeEC2Instance}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Errorf("다중 선택 변경 후 조회 타입 = %v, want 초기 타입 %v", gotTypes, wantTypes)
	}
}
