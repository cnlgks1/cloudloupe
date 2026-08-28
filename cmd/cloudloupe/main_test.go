package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

// clearEnv는 AWS 관련 환경 변수를 비운다.
//
// 이걸 빼먹으면 테스트가 개발자 셸에 의존한다. 실제로 그런 일이 있었다. 셸에
// AWS_PROFILE이 걸려 있는 기계에서만 기본 프로필 표시가 나타나서, 통과 여부가 사람마다
// 달라졌다.
func clearEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		awsclient.EnvProfile,
		awsclient.EnvDefaultProfile,
		awsclient.EnvRegion,
		awsclient.EnvDefaultRegion,
	} {
		t.Setenv(name, "")
	}
}

// useFixtures는 공유 설정 탐색이 테스트 픽스처를 보도록 한다.
//
// 모든 테스트가 이걸 호출한다. 그러지 않으면 테스트가 개발자의 실제 ~/.aws를 읽게 되고,
// 결과가 누구의 기계에서 돌리는지에 따라 달라진다.
func useFixtures(t *testing.T) {
	t.Helper()
	clearEnv(t)
	t.Setenv(awsclient.EnvConfigFile, filepath.Join("testdata", "config"))
	t.Setenv(awsclient.EnvCredentialsFile, filepath.Join("testdata", "credentials"))
}

// useEmptyHome은 존재하지 않는 경로를 보도록 한다.
func useEmptyHome(t *testing.T) {
	t.Helper()
	clearEnv(t)

	dir := t.TempDir()
	t.Setenv(awsclient.EnvConfigFile, filepath.Join(dir, "config"))
	t.Setenv(awsclient.EnvCredentialsFile, filepath.Join(dir, "credentials"))
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	err = run(args, &out, &errOut)

	return out.String(), errOut.String(), err
}

func TestVersionFlag(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t, "--version")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, want := range []string{"cloudloupe", "커밋:", "빌드:", "Go:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--version 출력에 %q가 없음:\n%s", want, stdout)
		}
	}
}

func TestListProfilesText(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, want := range []string{
		"프로필", "종류", "리전", "선언 위치",
		"default", "prod", "audit-a",
		"sso", "assume-role",
		"ap-northeast-2", "eu-west-1",
		"프로필 3개",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("출력에 %q가 없음:\n%s", want, stdout)
		}
	}

	// 조회 전용이라는 사실을 문서만이 아니라 출력에서도 밝힌다.
	if !strings.Contains(stdout, "변경하지 않습니다") {
		t.Error("출력은 cloudloupe가 아무것도 변경하지 않음을 밝혀야 한다")
	}
}

func TestListProfilesTextAlignsColumns(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// 한글 헤더는 터미널에서 두 칸을 차지한다. 룬 개수로 폭을 세면 열이 어긋나므로
	// 표시 폭 기준으로 정렬되는지 확인한다.
	//
	// 표 블록은 첫 빈 줄 앞까지다. 그 뒤에는 요약 문장이 붙으므로 행으로 취급하면 안 된다.
	table, _, found := strings.Cut(stdout, "\n\n")
	if !found {
		t.Fatalf("표 뒤에 요약 줄이 있어야 한다:\n%s", stdout)
	}

	lines := strings.Split(table, "\n")
	if len(lines) < 3 {
		t.Fatalf("표에 줄이 부족하다:\n%s", table)
	}

	// 각 줄에서 두 번째 열이 시작하는 표시 폭 위치를 구한다. 헤더가 한글이고 데이터가
	// ASCII이므로, 바이트 인덱스로 비교하면 통과해버리고 실제 어긋남을 놓친다.
	positions := make([]int, 0, len(lines))

	for _, line := range lines {
		gap := strings.Index(line, " ")
		if gap < 0 {
			t.Fatalf("줄에 열 구분이 없다: %q", line)
		}

		rest := strings.TrimLeft(line[gap:], " ")
		positions = append(positions, displayWidth(line[:len(line)-len(rest)]))
	}

	for i, p := range positions {
		if p != positions[0] {
			t.Errorf("%d번째 줄의 두 번째 열이 %d칸에서 시작한다. 첫 줄은 %d칸:\n%s",
				i, p, positions[0], table)
		}
	}

	// 줄 끝 공백은 복사해 붙일 때 지저분해진다.
	for i, line := range lines {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("%d번째 줄에 줄 끝 공백이 있다: %q", i, line)
		}
	}
}

func TestDisplayWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"prod", 4},
		{"프로필", 6},
		{"선언 위치", 9},
		{"ap-northeast-2", 14},
		{"종류", 4},
	}

	for _, tc := range tests {
		if got := displayWidth(tc.in); got != tc.want {
			t.Errorf("displayWidth(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestListProfilesJSON(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t, "--output", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var payload struct {
		Profiles []struct {
			Name       string `json:"name"`
			Kind       string `json:"kind"`
			Region     string `json:"region"`
			DeclaredIn string `json:"declaredIn"`
			RoleARN    string `json:"roleArn"`
		} `json:"profiles"`
	}

	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("출력이 올바른 JSON이 아니다: %v\n%s", err, stdout)
	}

	if len(payload.Profiles) != 3 {
		t.Fatalf("프로필 %d개, want 3", len(payload.Profiles))
	}

	// 파일에 적힌 순서와 무관하게 default가 맨 앞이다.
	if payload.Profiles[0].Name != "default" {
		t.Errorf("첫 프로필 = %q, want default", payload.Profiles[0].Name)
	}

	byName := make(map[string]string, len(payload.Profiles))
	for _, p := range payload.Profiles {
		byName[p.Name] = p.Kind
	}

	if byName["prod"] != "sso" {
		t.Errorf("prod kind = %q, want sso", byName["prod"])
	}

	if byName["audit-a"] != "assume-role" {
		t.Errorf("audit-a kind = %q, want assume-role", byName["audit-a"])
	}
}

func TestJSONKeysStayEnglish(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t, "--output", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// JSON 키는 출력 계약이다. 주석과 문서는 한국어로 쓰지만 키를 번역하면 이 출력을
	// 파싱하는 스크립트가 깨진다.
	for _, key := range []string{`"profiles"`, `"name"`, `"kind"`, `"declaredIn"`} {
		if !strings.Contains(stdout, key) {
			t.Errorf("JSON에 %s 키가 없음:\n%s", key, stdout)
		}
	}
}

func TestJSONOutputCarriesNoCredentialValues(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t, "--output", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// JSON 출력은 버그 리포트에 붙여넣을 가능성이 가장 높은 형태다.
	for _, secret := range []string{
		"AKIAIOSFODNN7EXAMPLE",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"aws_secret_access_key",
	} {
		if strings.Contains(stdout, secret) {
			t.Errorf("출력이 %q를 노출한다:\n%s", secret, stdout)
		}
	}
}

func TestUnknownOutputFormat(t *testing.T) {
	useFixtures(t)

	_, _, err := runCLI(t, "--output", "yaml")
	if err == nil {
		t.Fatal("지원하지 않는 형식에는 에러가 나야 한다")
	}

	if !strings.Contains(err.Error(), "text 또는 json") {
		t.Errorf("에러 %q는 지원하는 형식을 알려줘야 한다", err)
	}
}

func TestMissingSharedConfigExplainsWhatToDo(t *testing.T) {
	useEmptyHome(t)

	_, _, err := runCLI(t)
	if err == nil {
		t.Fatal("공유 설정이 없으면 에러가 나야 한다")
	}

	// ~/.aws가 없는 첫 사용자에게는 "파일 없음"만 던지지 말고 해결 방법을 알려줘야 한다.
	for _, want := range []string{"aws configure", "examples/aws/config.example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("에러는 %q를 안내해야 한다:\n%v", want, err)
		}
	}
}

func TestUsageMentionsReadOnly(t *testing.T) {
	useFixtures(t)

	_, stderr, err := runCLI(t, "--help")
	if err == nil {
		t.Log("--help가 에러를 반환하지 않았다")
	}

	if !strings.Contains(stderr, "조회 전용") {
		t.Errorf("사용법에 조회 전용이라는 설명이 있어야 한다:\n%s", stderr)
	}
}

func TestOutputShowsWhereConfigWasRead(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// 이걸 항상 보여주는 이유. Homebrew로 설치했든 바이너리를 직접 받았든 경로는 실행
	// 시점에 해석되는데, 환경 변수가 걸려 있거나 sudo로 홈이 바뀌면 사용자의 예상과
	// 달라진다. 읽은 위치를 출력하면 그 진단이 한 번에 끝난다.
	for _, want := range []string{
		"설정 위치",
		filepath.Join("testdata", "config"),
		filepath.Join("testdata", "credentials"),
		awsclient.EnvConfigFile,
		"있음",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("출력에 %q가 없음:\n%s", want, stdout)
		}
	}
}

func TestDefaultProfileFromEnvironmentIsMarked(t *testing.T) {
	useFixtures(t)
	t.Setenv(awsclient.EnvProfile, "prod")

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(stdout, "기본 프로필입니다") {
		t.Errorf("기본 프로필 설명이 없음:\n%s", stdout)
	}

	// 표시가 실제로 그 줄에 붙어야 한다.
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "prod") && strings.Contains(line, "sso") {
			if !strings.HasPrefix(line, "*") {
				t.Errorf("prod 줄에 * 표시가 없다: %q", line)
			}

			return
		}
	}

	t.Errorf("prod 줄을 찾을 수 없다:\n%s", stdout)
}

func TestNoMarkerColumnWithoutDefaultProfile(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// 기본 프로필이 없으면 표시 열을 넣지 않는다. 항상 넣으면 대부분의 경우 빈 열이
	// 표 전체를 오른쪽으로 밀어낸다.
	if !strings.HasPrefix(stdout, "프로필") {
		t.Errorf("표가 프로필 열에서 시작해야 한다:\n%s", stdout)
	}

	if strings.Contains(stdout, "기본 프로필입니다") {
		t.Errorf("기본 프로필이 없는데 설명이 나왔다:\n%s", stdout)
	}
}

func TestDanglingDefaultProfileWarns(t *testing.T) {
	useFixtures(t)
	t.Setenv(awsclient.EnvProfile, "존재하지-않는-프로필")

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// AWS_PROFILE이 오타이거나 지워진 프로필을 가리키면 조용히 넘어가면 안 된다.
	// 나중에 자격증명 해석이 실패했을 때 원인을 찾을 수 없게 된다.
	for _, want := range []string{"경고", awsclient.EnvProfile, "존재하지-않는-프로필"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("경고에 %q가 없음:\n%s", want, stdout)
		}
	}
}

func TestRegionFallsBackToEnvironment(t *testing.T) {
	useFixtures(t)
	t.Setenv(awsclient.EnvRegion, "us-west-2")

	stdout, _, err := runCLI(t)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// AWS_REGION은 프로필에 리전이 없을 때 실제로 쓰이는 값이므로 표에 반영되어야 한다.
	if !strings.Contains(stdout, "기본 리전") || !strings.Contains(stdout, "us-west-2") {
		t.Errorf("환경 기본 리전이 표시되지 않음:\n%s", stdout)
	}
}

func TestJSONIncludesResolvedLocations(t *testing.T) {
	useFixtures(t)
	t.Setenv(awsclient.EnvProfile, "prod")

	stdout, _, err := runCLI(t, "--output", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var payload struct {
		Locations struct {
			Config struct {
				Path   string `json:"path"`
				Source string `json:"source"`
				EnvVar string `json:"envVar"`
				Exists bool   `json:"exists"`
			} `json:"config"`
			DefaultProfile *struct {
				Value  string `json:"value"`
				EnvVar string `json:"envVar"`
			} `json:"defaultProfile"`
		} `json:"locations"`
		Profiles []struct {
			Name      string `json:"name"`
			IsDefault bool   `json:"isDefault"`
		} `json:"profiles"`
	}

	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("출력이 올바른 JSON이 아니다: %v\n%s", err, stdout)
	}

	if payload.Locations.Config.EnvVar != awsclient.EnvConfigFile {
		t.Errorf("config envVar = %q, want %q",
			payload.Locations.Config.EnvVar, awsclient.EnvConfigFile)
	}

	if !payload.Locations.Config.Exists {
		t.Error("픽스처 config가 있는데 exists가 false다")
	}

	if payload.Locations.DefaultProfile == nil {
		t.Fatal("defaultProfile이 없다")
	}

	if payload.Locations.DefaultProfile.Value != "prod" {
		t.Errorf("defaultProfile.value = %q, want prod", payload.Locations.DefaultProfile.Value)
	}

	var found bool

	for _, p := range payload.Profiles {
		if p.Name == "prod" {
			found = p.IsDefault
		}
	}

	if !found {
		t.Error("prod의 isDefault가 true여야 한다")
	}
}

func TestJSONOmitsUnsetEnvironmentDefaults(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t, "--output", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// 설정되지 않은 값을 빈 객체로 내보내면 설정된 것과 구별할 수 없다.
	for _, unwanted := range []string{`"defaultProfile"`, `"defaultRegion"`} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("설정되지 않은 %s가 출력에 있다:\n%s", unwanted, stdout)
		}
	}
}

func TestMissingConfigErrorShowsSearchedPaths(t *testing.T) {
	useEmptyHome(t)

	_, _, err := runCLI(t)
	if err == nil {
		t.Fatal("공유 설정이 없으면 에러가 나야 한다")
	}

	// 어디를 찾아봤는지 알려주지 않으면 사용자는 경로를 추측해야 한다.
	for _, want := range []string{"찾아본 위치", "config", "credentials", awsclient.EnvConfigFile} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("에러에 %q가 없음:\n%v", want, err)
		}
	}
}

func TestDiffProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ours        []string
		theirs      []string
		wantMissing []string
		wantExtra   []string
	}{
		{
			name:   "같은 목록",
			ours:   []string{"default", "prod"},
			theirs: []string{"prod", "default"},
		},
		{
			// 우리 파서가 프로필을 놓친 경우. AWS CLI가 보는데 우리가 못 보면 파서 버그다.
			name:        "aws 에만 있음",
			ours:        []string{"default"},
			theirs:      []string{"default", "prod", "staging"},
			wantMissing: []string{"prod", "staging"},
		},
		{
			// 우리가 프로필로 잘못 인식한 경우. 접두사 없는 섹션을 프로필로 취급하면 이렇게 된다.
			name:      "cloudloupe 에만 있음",
			ours:      []string{"default", "sso-session org"},
			theirs:    []string{"default"},
			wantExtra: []string{"sso-session org"},
		},
		{
			name:        "양쪽 모두 다름",
			ours:        []string{"a", "b"},
			theirs:      []string{"b", "c"},
			wantMissing: []string{"c"},
			wantExtra:   []string{"a"},
		},
		{
			name:      "빈 목록 대조",
			ours:      []string{"default"},
			theirs:    nil,
			wantExtra: []string{"default"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			missing, extra := diffProfiles(tc.ours, tc.theirs)

			if !slices.Equal(missing, tc.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tc.wantMissing)
			}

			if !slices.Equal(extra, tc.wantExtra) {
				t.Errorf("extra = %v, want %v", extra, tc.wantExtra)
			}
		})
	}
}

func TestCheckReportsHealthySetup(t *testing.T) {
	useFixtures(t)

	stdout, _, err := runCLI(t, "--check")
	if err != nil {
		t.Fatalf("정상 픽스처에서 실패했다: %v\n%s", err, stdout)
	}

	for _, want := range []string{"상태", "항목", "홈 디렉터리", "config 파일", "프로필"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("진단 출력에 %q가 없음:\n%s", want, stdout)
		}
	}
}

func TestCheckFailsOnUnreadableConfig(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root로 실행 중이라 권한 비트가 적용되지 않는다")
	}

	if runtime.GOOS == "windows" {
		t.Skip("Windows에서는 권한 비트로 읽기를 막을 수 없다")
	}

	clearEnv(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	if err := os.WriteFile(path, []byte("[default]\n"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv(awsclient.EnvConfigFile, path)
	t.Setenv(awsclient.EnvCredentialsFile, filepath.Join(dir, "credentials"))

	// 스크립트에서 사전 점검으로 쓸 수 있으려면 문제가 있을 때 0이 아닌 코드로 끝나야 한다.
	stdout, _, err := runCLI(t, "--check")
	if err == nil {
		t.Errorf("읽을 수 없는 설정인데 성공했다:\n%s", stdout)
	}
}

func TestCheckWarnsButSucceedsOnDanglingProfile(t *testing.T) {
	useFixtures(t)
	t.Setenv(awsclient.EnvProfile, "없는프로필")

	// 주의는 종료 코드를 바꾸지 않는다. 경고마다 스크립트가 멈추면 쓸 수 없게 된다.
	stdout, _, err := runCLI(t, "--check")
	if err != nil {
		t.Errorf("주의 항목만 있을 때는 성공해야 한다: %v", err)
	}

	if !strings.Contains(stdout, "조치할 것") {
		t.Errorf("조치 목록이 있어야 한다:\n%s", stdout)
	}

	if !strings.Contains(stdout, "없는프로필") {
		t.Errorf("문제가 된 값을 밝혀야 한다:\n%s", stdout)
	}
}

func TestCheckStillDiagnosesWhenConfigMissing(t *testing.T) {
	useEmptyHome(t)

	// 설정이 없는 상태도 진단 대상이다. 여기서 에러로 끝나버리면 진단이 무의미하다.
	stdout, _, err := runCLI(t, "--check")
	if err != nil {
		t.Logf("종료 상태: %v", err)
	}

	if !strings.Contains(stdout, "config 파일") {
		t.Errorf("설정이 없어도 진단 항목은 나와야 한다:\n%s", stdout)
	}

	if !strings.Contains(stdout, "조치할 것") {
		t.Errorf("설정이 없으면 조치 방법을 알려줘야 한다:\n%s", stdout)
	}
}
