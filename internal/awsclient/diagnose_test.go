package awsclient_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

// resolveIn은 주어진 홈 디렉터리를 기준으로 위치를 해석한다.
func resolveIn(t *testing.T, home string) awsclient.Locations {
	t.Helper()
	clearAWSEnv(t)
	setHome(t, home)

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	return loc
}

// writeAWSConfig는 홈 아래에 .aws 디렉터리와 파일을 만든다.
func writeAWSConfig(t *testing.T, home, filename, body string, mode os.FileMode) string {
	t.Helper()

	dir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

// findCheck는 이름으로 진단 항목을 찾는다.
func findCheck(t *testing.T, d awsclient.Diagnosis, namePrefix string) awsclient.Check {
	t.Helper()

	for _, c := range d.Checks {
		if strings.HasPrefix(c.Name, namePrefix) {
			return c
		}
	}

	names := make([]string, 0, len(d.Checks))
	for _, c := range d.Checks {
		names = append(names, c.Name)
	}

	t.Fatalf("진단 항목 %q를 찾을 수 없다. 있는 항목: %v", namePrefix, names)

	return awsclient.Check{}
}

func TestDiagnoseHealthySetup(t *testing.T) {
	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[default]\nregion = ap-northeast-2\n", 0o600)
	writeAWSConfig(t, home, "credentials", "[default]\naws_access_key_id = AKIAEXAMPLE\n", 0o600)

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "default"}})

	if diag.Worst() != awsclient.SeverityOK {
		for _, c := range diag.Problems() {
			t.Logf("  [%s] %s — %s", c.Status, c.Name, c.Detail)
		}

		t.Errorf("정상 설정인데 심각도 = %q", diag.Worst())
	}

	if len(diag.Problems()) != 0 {
		t.Errorf("정상 설정에 조치 항목이 %d개 있다", len(diag.Problems()))
	}
}

func TestDiagnoseMissingConfigIsWarningNotFailure(t *testing.T) {
	// 첫 실행 상황이다. 프로그램의 실패가 아니므로 문제가 아니라 주의여야 한다.
	loc := resolveIn(t, t.TempDir())
	diag := awsclient.Diagnose(loc, nil)

	c := findCheck(t, diag, "config 파일")
	if c.Status != awsclient.SeverityWarn {
		t.Errorf("config 파일 상태 = %q, want %q", c.Status, awsclient.SeverityWarn)
	}

	if c.Hint == "" {
		t.Error("설정이 없을 때는 만드는 방법을 알려줘야 한다")
	}

	if diag.Worst() == awsclient.SeverityFail {
		t.Error("설정이 없는 것만으로 문제 상태가 되어서는 안 된다")
	}
}

func TestDiagnoseMissingCredentialsIsNormal(t *testing.T) {
	// SSO나 assume-role만 쓰면 credentials 파일이 아예 없다. 이걸 경고하면 대부분의
	// 정상 사용자에게 잘못된 신호를 준다.
	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[profile prod]\nsso_session = org\n", 0o600)

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "prod"}})

	c := findCheck(t, diag, "credentials 파일")
	if c.Status != awsclient.SeverityOK {
		t.Errorf("credentials 없음 상태 = %q, want %q", c.Status, awsclient.SeverityOK)
	}
}

func TestDiagnoseUnreadableConfigIsFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root로 실행 중이라 권한 비트가 적용되지 않는다")
	}

	if runtime.GOOS == "windows" {
		t.Skip("Windows에서는 권한 비트로 읽기를 막을 수 없다")
	}

	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[default]\n", 0o000)

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, nil)

	c := findCheck(t, diag, "config 파일")
	if c.Status != awsclient.SeverityFail {
		t.Errorf("읽을 수 없는 config 상태 = %q, want %q", c.Status, awsclient.SeverityFail)
	}

	// 파일이 있는데 읽을 수 없는 것은 없는 것과 구별되어야 한다.
	if diag.Worst() != awsclient.SeverityFail {
		t.Error("읽을 수 없는 설정은 문제 상태여야 한다")
	}
}

func TestDiagnoseConfigDirectoryIsAFile(t *testing.T) {
	home := t.TempDir()

	// .aws 자리에 파일이 있으면 AWS CLI도 동작하지 않는다.
	if err := os.WriteFile(filepath.Join(home, ".aws"), []byte("oops"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, nil)

	c := findCheck(t, diag, "설정 디렉터리")
	if c.Status != awsclient.SeverityFail {
		t.Errorf("디렉터리 자리에 파일이 있을 때 상태 = %q, want %q", c.Status, awsclient.SeverityFail)
	}
}

func TestDiagnoseWarnsOnOpenCredentialsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows는 ACL을 쓰므로 Unix 권한 비트로 판단하지 않는다")
	}

	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[default]\n", 0o600)
	writeAWSConfig(t, home, "credentials", "[default]\naws_access_key_id = AKIAEXAMPLE\n", 0o644)

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "default"}})

	c := findCheck(t, diag, "credentials 권한")
	if c.Status != awsclient.SeverityWarn {
		t.Errorf("0644 권한 상태 = %q, want %q", c.Status, awsclient.SeverityWarn)
	}

	if !strings.Contains(c.Hint, "chmod 600") {
		t.Errorf("힌트에 고치는 명령이 있어야 한다: %q", c.Hint)
	}
}

func TestDiagnoseNoPermissionWarningWhenLockedDown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows는 ACL을 쓴다")
	}

	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[default]\n", 0o600)
	writeAWSConfig(t, home, "credentials", "[default]\n", 0o600)

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "default"}})

	for _, c := range diag.Checks {
		if strings.HasPrefix(c.Name, "credentials 권한") {
			t.Errorf("0600인데 권한 항목이 나왔다: %+v", c)
		}
	}
}

func TestDiagnoseReportsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows에서 심볼릭 링크 생성은 권한이 필요하다")
	}

	// dotfile 관리 도구를 쓰면 흔한 구성이다. 문제는 아니지만 어디를 가리키는지 보여야 한다.
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "config")

	if err := os.WriteFile(real, []byte("[default]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.Symlink(real, filepath.Join(dir, "config")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "default"}})

	c := findCheck(t, diag, "심볼릭 링크")
	if c.Status != awsclient.SeverityOK {
		t.Errorf("정상 링크 상태 = %q, want %q", c.Status, awsclient.SeverityOK)
	}

	if !strings.Contains(c.Detail, "→") {
		t.Errorf("링크 대상을 보여줘야 한다: %q", c.Detail)
	}
}

func TestDiagnoseDetectsBrokenSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows에서 심볼릭 링크 생성은 권한이 필요하다")
	}

	home := t.TempDir()

	dir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.Symlink(filepath.Join(t.TempDir(), "없는파일"), filepath.Join(dir, "config")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, nil)

	c := findCheck(t, diag, "심볼릭 링크")
	if c.Status != awsclient.SeverityFail {
		t.Errorf("끊어진 링크 상태 = %q, want %q", c.Status, awsclient.SeverityFail)
	}
}

func TestDiagnoseDefaultProfile(t *testing.T) {
	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[profile prod]\nregion = ap-northeast-2\n", 0o600)

	t.Run("존재하는 프로필", func(t *testing.T) {
		clearAWSEnv(t)
		setHome(t, home)
		t.Setenv(awsclient.EnvProfile, "prod")

		loc, err := awsclient.Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "prod"}})

		c := findCheck(t, diag, "기본 프로필")
		if c.Status != awsclient.SeverityOK {
			t.Errorf("상태 = %q, want %q", c.Status, awsclient.SeverityOK)
		}
	})

	t.Run("없는 프로필", func(t *testing.T) {
		clearAWSEnv(t)
		setHome(t, home)
		t.Setenv(awsclient.EnvProfile, "오타난이름")

		loc, err := awsclient.Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "prod"}})

		c := findCheck(t, diag, "기본 프로필")
		if c.Status != awsclient.SeverityWarn {
			t.Errorf("상태 = %q, want %q", c.Status, awsclient.SeverityWarn)
		}

		if !strings.Contains(c.Hint, awsclient.EnvProfile) {
			t.Errorf("힌트에 변수 이름이 있어야 한다: %q", c.Hint)
		}
	})

	t.Run("프로필 목록을 읽지 못한 경우", func(t *testing.T) {
		clearAWSEnv(t)
		setHome(t, home)
		t.Setenv(awsclient.EnvProfile, "prod")

		loc, err := awsclient.Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		// 모르는 것을 안다고 하지 않는다. 목록이 없으면 존재 여부를 단정하지 않는다.
		diag := awsclient.Diagnose(loc, nil)

		c := findCheck(t, diag, "기본 프로필")
		if c.Status != awsclient.SeverityOK {
			t.Errorf("상태 = %q, want %q", c.Status, awsclient.SeverityOK)
		}

		if !strings.Contains(c.Detail, "확인 못 함") {
			t.Errorf("확인하지 못했음을 밝혀야 한다: %q", c.Detail)
		}
	})

	t.Run("설정되지 않으면 항목이 없다", func(t *testing.T) {
		loc := resolveIn(t, home)
		diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "prod"}})

		for _, c := range diag.Checks {
			if strings.HasPrefix(c.Name, "기본 프로필") {
				t.Errorf("AWS_PROFILE이 없는데 항목이 나왔다: %+v", c)
			}
		}
	})
}

func TestDiagnoseFindsSnapConfinedConfig(t *testing.T) {
	// Linux에서 snap으로 AWS CLI를 설치하면 격리된 홈에 설정이 만들어진다. 사용자는
	// aws configure 를 실행했는데 ~/.aws 에는 아무것도 없는 상황이 되고, 이게 "설정이
	// 있는데 왜 못 찾냐"의 실제 원인이다.
	home := t.TempDir()

	snapDir := filepath.Join(home, "snap", "aws-cli", "current", ".aws")
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	snapConfig := filepath.Join(snapDir, "config")
	if err := os.WriteFile(snapConfig, []byte("[default]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, nil)

	c := findCheck(t, diag, "다른 위치의 설정")

	if !strings.Contains(c.Detail, snapConfig) {
		t.Errorf("snap 경로를 찾아야 한다: %q", c.Detail)
	}

	// 우리 파일이 없는데 저쪽에 있으면 그것이 원인이므로 주의여야 한다.
	if c.Status != awsclient.SeverityWarn {
		t.Errorf("상태 = %q, want %q", c.Status, awsclient.SeverityWarn)
	}

	if !strings.Contains(c.Hint, awsclient.EnvConfigFile) {
		t.Errorf("힌트에 해결 방법이 있어야 한다: %q", c.Hint)
	}
}

func TestDiagnoseAlternativeIsInformationalWhenOursWorks(t *testing.T) {
	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[default]\n", 0o600)

	snapDir := filepath.Join(home, "snap", "aws-cli", "current", ".aws")
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(filepath.Join(snapDir, "config"), []byte("[default]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loc := resolveIn(t, home)
	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "default"}})

	// 우리 파일이 정상이면 다른 위치의 존재는 참고 정보일 뿐이다. 이걸 경고로 올리면
	// 정상 사용자에게 잘못된 신호를 준다.
	c := findCheck(t, diag, "다른 위치의 설정")
	if c.Status != awsclient.SeverityOK {
		t.Errorf("상태 = %q, want %q", c.Status, awsclient.SeverityOK)
	}
}

func TestDiagnoseWarnsUnderSudo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows에는 sudo가 없다")
	}

	home := t.TempDir()
	writeAWSConfig(t, home, "config", "[default]\n", 0o600)

	clearAWSEnv(t)
	setHome(t, home)
	t.Setenv("SUDO_USER", "someone")

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	diag := awsclient.Diagnose(loc, []awsclient.Profile{{Name: "default"}})

	// sudo로 실행하면 홈이 root의 것이 되어 프로필이 사라진 것처럼 보인다. 조회 전용
	// 도구에 관리자 권한이 필요할 이유도 없다.
	c := findCheck(t, diag, "홈 디렉터리")
	if c.Status != awsclient.SeverityWarn {
		t.Errorf("상태 = %q, want %q", c.Status, awsclient.SeverityWarn)
	}

	if !strings.Contains(c.Detail, "someone") {
		t.Errorf("원래 사용자를 밝혀야 한다: %q", c.Detail)
	}
}

func TestSeverityOrdering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		checks []awsclient.Check
		want   awsclient.Severity
	}{
		{
			name:   "빈 목록은 정상",
			checks: nil,
			want:   awsclient.SeverityOK,
		},
		{
			name: "주의가 정상을 이긴다",
			checks: []awsclient.Check{
				{Status: awsclient.SeverityOK},
				{Status: awsclient.SeverityWarn},
			},
			want: awsclient.SeverityWarn,
		},
		{
			name: "문제가 주의를 이긴다",
			checks: []awsclient.Check{
				{Status: awsclient.SeverityWarn},
				{Status: awsclient.SeverityFail},
				{Status: awsclient.SeverityOK},
			},
			want: awsclient.SeverityFail,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := awsclient.Diagnosis{Checks: tc.checks}
			if got := d.Worst(); got != tc.want {
				t.Errorf("Worst() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProblemsExcludesHealthyChecks(t *testing.T) {
	t.Parallel()

	d := awsclient.Diagnosis{Checks: []awsclient.Check{
		{Name: "a", Status: awsclient.SeverityOK},
		{Name: "b", Status: awsclient.SeverityWarn},
		{Name: "c", Status: awsclient.SeverityOK},
		{Name: "d", Status: awsclient.SeverityFail},
	}}

	problems := d.Problems()
	if len(problems) != 2 {
		t.Fatalf("조치 항목 %d개, want 2", len(problems))
	}

	if problems[0].Name != "b" || problems[1].Name != "d" {
		t.Errorf("순서가 유지되어야 한다: %q, %q", problems[0].Name, problems[1].Name)
	}
}
