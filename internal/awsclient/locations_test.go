package awsclient_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

// clearAWSEnv는 AWS 관련 환경 변수를 모두 비운다.
//
// 개발자의 셸에 AWS_PROFILE 같은 변수가 걸려 있으면 테스트 결과가 사람마다 달라진다.
// t.Setenv를 쓰므로 각 테스트가 끝나면 원래 값으로 복원된다.
func clearAWSEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		awsclient.EnvConfigFile,
		awsclient.EnvCredentialsFile,
		awsclient.EnvProfile,
		awsclient.EnvDefaultProfile,
		awsclient.EnvRegion,
		awsclient.EnvDefaultRegion,
	} {
		t.Setenv(name, "")
	}
}

// setHome은 홈 디렉터리를 임시 경로로 바꾼다. 플랫폼마다 읽는 변수가 다르다.
func setHome(t *testing.T, dir string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)

		return
	}

	t.Setenv("HOME", dir)
}

func TestResolveDefaultsToHomeDirectory(t *testing.T) {
	clearAWSEnv(t)

	home := t.TempDir()
	setHome(t, home)

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// filepath.Join을 쓰므로 구분자가 다른 Windows에서도 올바르다. 경로를 문자열로
	// 이어붙이지 않았음을 이 단정이 지켜준다.
	if want := filepath.Join(home, ".aws", "config"); loc.Config.Path != want {
		t.Errorf("config 경로 = %q, want %q", loc.Config.Path, want)
	}

	if want := filepath.Join(home, ".aws", "credentials"); loc.Credentials.Path != want {
		t.Errorf("credentials 경로 = %q, want %q", loc.Credentials.Path, want)
	}

	if loc.Config.Source != awsclient.PathFromHome {
		t.Errorf("config 근거 = %q, want %q", loc.Config.Source, awsclient.PathFromHome)
	}

	if loc.Home != home {
		t.Errorf("홈 = %q, want %q", loc.Home, home)
	}

	// SSO 캐시 위치도 같은 홈에서 파생되어야 한다. 나중에 SSO 토큰을 읽을 때 쓴다.
	if want := filepath.Join(home, ".aws", "sso", "cache"); loc.SSOCacheDir != want {
		t.Errorf("SSO 캐시 = %q, want %q", loc.SSOCacheDir, want)
	}
}

func TestResolveHonoursFileEnvironmentOverrides(t *testing.T) {
	clearAWSEnv(t)
	setHome(t, t.TempDir())

	dir := t.TempDir()
	configPath := filepath.Join(dir, "custom-config")
	credsPath := filepath.Join(dir, "custom-credentials")

	t.Setenv(awsclient.EnvConfigFile, configPath)
	t.Setenv(awsclient.EnvCredentialsFile, credsPath)

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if loc.Config.Path != configPath {
		t.Errorf("config 경로 = %q, want %q", loc.Config.Path, configPath)
	}

	if loc.Config.Source != awsclient.PathFromEnv {
		t.Errorf("config 근거 = %q, want %q", loc.Config.Source, awsclient.PathFromEnv)
	}

	// 어느 변수가 경로를 정했는지 기록해야 한다. "왜 내 프로필이 안 보이냐"의 답이
	// 대개 이것이다.
	if loc.Config.EnvVar != awsclient.EnvConfigFile {
		t.Errorf("config EnvVar = %q, want %q", loc.Config.EnvVar, awsclient.EnvConfigFile)
	}

	if loc.Credentials.Path != credsPath {
		t.Errorf("credentials 경로 = %q, want %q", loc.Credentials.Path, credsPath)
	}
}

func TestResolveExpandsTildeInEnvironmentPath(t *testing.T) {
	clearAWSEnv(t)

	// Windows runner의 DOS 8.3 짧은 홈 경로에는 합법적인 ~ 문자가 들어갈 수 있다.
	home := filepath.Join(t.TempDir(), "RUNNER~1")
	setHome(t, home)

	// 환경 변수가 셸을 거치지 않고 오면 ~ 가 확장되지 않은 채 남는다. 그대로 열면
	// "~"라는 이름의 디렉터리를 찾게 되어 조용히 실패한다.
	t.Setenv(awsclient.EnvConfigFile, "~/custom/config")

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := filepath.Join(home, "custom", "config")
	if loc.Config.Path != want {
		t.Errorf("config 경로 = %q, want %q", loc.Config.Path, want)
	}
}

func TestResolveReportsWhetherFilesExist(t *testing.T) {
	clearAWSEnv(t)

	home := t.TempDir()
	setHome(t, home)

	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(filepath.Join(awsDir, "config"), []byte("[default]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !loc.Config.Exists {
		t.Error("config 파일이 있는데 Exists가 false다")
	}

	if loc.Credentials.Exists {
		t.Error("credentials 파일이 없는데 Exists가 true다")
	}
}

func TestResolveDirectoryIsNotAFile(t *testing.T) {
	clearAWSEnv(t)
	setHome(t, t.TempDir())

	// 실수로 ~/.aws/config를 디렉터리로 만들어둔 경우가 있다. 파일이 있다고 보고하면
	// 이후 열기 실패의 원인을 찾기 어려워진다.
	dir := t.TempDir()
	asDir := filepath.Join(dir, "config")

	if err := os.MkdirAll(asDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Setenv(awsclient.EnvConfigFile, asDir)

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if loc.Config.Exists {
		t.Error("디렉터리를 파일이 존재한다고 보고했다")
	}
}

func TestResolveReadsDefaultProfileFromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		want    string
		wantVar string
	}{
		{
			name:    "AWS_PROFILE",
			set:     map[string]string{awsclient.EnvProfile: "prod"},
			want:    "prod",
			wantVar: awsclient.EnvProfile,
		},
		{
			name:    "레거시 AWS_DEFAULT_PROFILE",
			set:     map[string]string{awsclient.EnvDefaultProfile: "legacy"},
			want:    "legacy",
			wantVar: awsclient.EnvDefaultProfile,
		},
		{
			// AWS CLI는 정식 변수를 먼저 본다. 둘 다 있으면 AWS_PROFILE이 이긴다.
			name: "둘 다 있으면 AWS_PROFILE이 우선",
			set: map[string]string{
				awsclient.EnvProfile:        "prod",
				awsclient.EnvDefaultProfile: "legacy",
			},
			want:    "prod",
			wantVar: awsclient.EnvProfile,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearAWSEnv(t)
			setHome(t, t.TempDir())

			for k, v := range tc.set {
				t.Setenv(k, v)
			}

			loc, err := awsclient.Resolve()
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if loc.DefaultProfile.Value != tc.want {
				t.Errorf("DefaultProfile.Value = %q, want %q", loc.DefaultProfile.Value, tc.want)
			}

			if loc.DefaultProfile.EnvVar != tc.wantVar {
				t.Errorf("DefaultProfile.EnvVar = %q, want %q", loc.DefaultProfile.EnvVar, tc.wantVar)
			}

			if !loc.DefaultProfile.Set() {
				t.Error("Set()이 false다")
			}
		})
	}
}

func TestResolveReadsDefaultRegionFromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		want    string
		wantVar string
	}{
		{
			name:    "AWS_REGION",
			set:     map[string]string{awsclient.EnvRegion: "ap-northeast-2"},
			want:    "ap-northeast-2",
			wantVar: awsclient.EnvRegion,
		},
		{
			name:    "레거시 AWS_DEFAULT_REGION",
			set:     map[string]string{awsclient.EnvDefaultRegion: "us-east-1"},
			want:    "us-east-1",
			wantVar: awsclient.EnvDefaultRegion,
		},
		{
			name: "둘 다 있으면 AWS_REGION이 우선",
			set: map[string]string{
				awsclient.EnvRegion:        "ap-northeast-2",
				awsclient.EnvDefaultRegion: "us-east-1",
			},
			want:    "ap-northeast-2",
			wantVar: awsclient.EnvRegion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearAWSEnv(t)
			setHome(t, t.TempDir())

			for k, v := range tc.set {
				t.Setenv(k, v)
			}

			loc, err := awsclient.Resolve()
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if loc.DefaultRegion.Value != tc.want {
				t.Errorf("DefaultRegion.Value = %q, want %q", loc.DefaultRegion.Value, tc.want)
			}

			if loc.DefaultRegion.EnvVar != tc.wantVar {
				t.Errorf("DefaultRegion.EnvVar = %q, want %q", loc.DefaultRegion.EnvVar, tc.wantVar)
			}
		})
	}
}

func TestResolveIgnoresBlankEnvironmentValues(t *testing.T) {
	clearAWSEnv(t)

	home := t.TempDir()
	setHome(t, home)

	// 공백만 있는 변수는 설정되지 않은 것으로 취급한다. `export AWS_PROFILE=` 같은
	// 실수가 흔하고, 이걸 프로필 이름으로 받으면 존재하지 않는 프로필을 찾게 된다.
	t.Setenv(awsclient.EnvProfile, "   ")
	t.Setenv(awsclient.EnvConfigFile, "  ")

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if loc.DefaultProfile.Set() {
		t.Errorf("공백 AWS_PROFILE을 값으로 받았다: %q", loc.DefaultProfile.Value)
	}

	if loc.Config.Source != awsclient.PathFromHome {
		t.Errorf("공백 AWS_CONFIG_FILE을 경로로 받았다: %+v", loc.Config)
	}
}

func TestResolveWorksWithoutHomeWhenBothFilesAreSet(t *testing.T) {
	clearAWSEnv(t)

	// 컨테이너나 CI에서는 홈이 없고 두 파일을 환경 변수로 지정하는 경우가 있다.
	// 그 상황에서 홈이 없다는 이유로 실패해서는 안 된다.
	setHome(t, "")

	dir := t.TempDir()
	t.Setenv(awsclient.EnvConfigFile, filepath.Join(dir, "config"))
	t.Setenv(awsclient.EnvCredentialsFile, filepath.Join(dir, "credentials"))

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("두 파일이 모두 지정되었으면 홈 없이도 성공해야 한다: %v", err)
	}

	if loc.Config.Source != awsclient.PathFromEnv {
		t.Errorf("config 근거 = %q, want %q", loc.Config.Source, awsclient.PathFromEnv)
	}
}

func TestResolveFailsHelpfullyWithoutHomeOrEnv(t *testing.T) {
	clearAWSEnv(t)
	setHome(t, "")

	if runtime.GOOS == "windows" {
		// Windows에서는 HOMEDRIVE+HOMEPATH 대체 경로가 남아 있어 홈을 비울 수 없다.
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	}

	_, err := awsclient.Resolve()
	if err == nil {
		t.Fatal("홈도 환경 변수도 없으면 에러가 나야 한다")
	}

	// 막힌 사용자에게 해결책을 알려줘야 한다.
	if !strings.Contains(err.Error(), awsclient.EnvConfigFile) {
		t.Errorf("에러는 %s로 지정하는 방법을 알려줘야 한다: %v", awsclient.EnvConfigFile, err)
	}
}

func TestResolvedPathString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     awsclient.ResolvedPath
		contains []string
	}{
		{
			name: "환경 변수에서 온 경로는 변수 이름을 밝힌다",
			path: awsclient.ResolvedPath{
				Path:   "/tmp/config",
				Source: awsclient.PathFromEnv,
				EnvVar: awsclient.EnvConfigFile,
				Exists: true,
			},
			contains: []string{"/tmp/config", awsclient.EnvConfigFile, "있음"},
		},
		{
			name: "홈에서 온 경로는 근거와 없음을 밝힌다",
			path: awsclient.ResolvedPath{
				Path:   "/home/u/.aws/config",
				Source: awsclient.PathFromHome,
				Exists: false,
			},
			contains: []string{"/home/u/.aws/config", "홈 디렉터리", "없음"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := tc.path.String()
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("String() = %q, %q가 없다", got, want)
				}
			}
		})
	}
}

func TestLocationsLoadProfiles(t *testing.T) {
	clearAWSEnv(t)
	setHome(t, t.TempDir())

	t.Setenv(awsclient.EnvConfigFile, testConfig)
	t.Setenv(awsclient.EnvCredentialsFile, testCredentials)

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	profiles, err := loc.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	if len(profiles) == 0 {
		t.Fatal("해석된 위치에서 프로필을 읽어야 한다")
	}

	if profiles[0].Name != "default" {
		t.Errorf("첫 프로필 = %q, want default", profiles[0].Name)
	}
}

func TestResolveMakesEnvironmentPathAbsolute(t *testing.T) {
	clearAWSEnv(t)
	setHome(t, t.TempDir())

	// AWS_CONFIG_FILE에 상대 경로를 넣는 사람이 있고, AWS CLI처럼 작업 디렉터리 기준으로
	// 해석된다. 상대 경로를 그대로 보고하면 진단 출력만 봐서는 어느 파일을 봤는지 알 수
	// 없고, 작업 디렉터리가 바뀌면 같은 값이 다른 파일을 가리킨다.
	t.Setenv(awsclient.EnvConfigFile, filepath.Join("testdata", "config"))

	loc, err := awsclient.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !filepath.IsAbs(loc.Config.Path) {
		t.Errorf("config 경로가 절대 경로가 아니다: %q", loc.Config.Path)
	}

	// 절대 경로로 바꿔도 가리키는 파일은 같아야 한다.
	if !loc.Config.Exists {
		t.Errorf("절대 경로로 바꾼 뒤 픽스처를 찾지 못했다: %q", loc.Config.Path)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	if want := filepath.Join(wd, "testdata", "config"); loc.Config.Path != want {
		t.Errorf("config 경로 = %q, want %q", loc.Config.Path, want)
	}
}
