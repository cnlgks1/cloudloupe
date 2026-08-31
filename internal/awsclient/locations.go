package awsclient

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AWS 설정 위치와 기본값을 결정하는 환경 변수.
//
// 이름과 우선순위는 AWS CLI 및 SDK와 같다. 그래야 `aws` 명령이 보는 것과 cloudloupe가
// 보는 것이 어긋나지 않는다.
const (
	EnvConfigFile      = "AWS_CONFIG_FILE"
	EnvCredentialsFile = "AWS_SHARED_CREDENTIALS_FILE"
	EnvProfile         = "AWS_PROFILE"
	EnvDefaultProfile  = "AWS_DEFAULT_PROFILE"
	EnvRegion          = "AWS_REGION"
	EnvDefaultRegion   = "AWS_DEFAULT_REGION"
)

// PathSource는 경로가 어떻게 결정되었는지 나타낸다.
type PathSource string

// 경로가 결정되는 방식.
const (
	PathFromFlag PathSource = "직접 지정"
	PathFromEnv  PathSource = "환경 변수"
	PathFromHome PathSource = "홈 디렉터리"
)

// ResolvedPath는 파일 경로와 그 경로가 결정된 근거다.
//
// 근거를 함께 들고 다니는 이유가 있다. 사용자가 "왜 내 프로필이 안 보이냐"고 물을 때,
// 답은 거의 항상 "예상과 다른 파일을 읽고 있다"이며, 그건 대개 환경 변수 때문이다.
// 경로만 있고 근거가 없으면 그 진단을 할 수 없다.
type ResolvedPath struct {
	Path   string
	Source PathSource
	EnvVar string // Source가 PathFromEnv일 때 어떤 변수가 지정했는지
	Exists bool
}

// String은 경로와 그 근거를 한 줄로 표현한다.
func (p ResolvedPath) String() string {
	origin := string(p.Source)
	if p.EnvVar != "" {
		origin = p.EnvVar
	}

	state := "있음"
	if !p.Exists {
		state = "없음"
	}

	return fmt.Sprintf("%s (%s, %s)", p.Path, origin, state)
}

// EnvValue는 환경 변수에서 읽어낸 값과 그 값을 준 변수 이름이다.
type EnvValue struct {
	Value  string
	EnvVar string
}

// Set은 값이 지정되었는지 알려준다.
func (v EnvValue) Set() bool {
	return v.Value != ""
}

// Locations는 cloudloupe가 AWS 설정을 어디서 찾았고 환경에서 무엇을 읽어냈는지 담는다.
//
// 이 값은 전부 실행 시점에 결정된다. 빌드 시점에 굽지 않으므로, Homebrew로 설치했든
// go install로 받았든 아카이브를 직접 풀었든 동작이 같다. 바이너리는 자신을 실행한
// 사용자의 환경을 보고, 그 사용자의 홈 디렉터리를 쓴다.
type Locations struct {
	Home           string
	Config         ResolvedPath
	Credentials    ResolvedPath
	SSOCacheDir    string
	DefaultProfile EnvValue
	DefaultRegion  EnvValue
}

// Override는 명시적으로 지정한 경로다. --config/--credentials 플래그나 TUI에서 사용자가
// 직접 입력한 경로가 여기 담긴다.
//
// 우선순위가 가장 높다. 사용자가 "이 파일을 봐라"라고 명시했으면 환경 변수나 홈 디렉터리
// 기본값보다 그 지시를 따르는 것이 당연하다.
type Override struct {
	ConfigPath      string
	CredentialsPath string
}

// Resolve는 현재 환경에서 AWS 설정 위치와 기본값을 결정한다.
//
// ResolveWith(Override{})와 같다. 명시적 경로가 없는 일반적인 경우에 쓴다.
func Resolve() (Locations, error) {
	return ResolveWith(Override{})
}

// ResolveWith는 명시적 경로 지정을 반영해 위치를 결정한다.
//
// 우선순위(높은 것부터):
//  1. Override — 플래그나 TUI에서 사용자가 직접 지정한 경로
//  2. 전용 환경 변수(AWS_CONFIG_FILE 등)
//  3. 홈 디렉터리의 ~/.aws
//
// 이 순서는 AWS CLI의 관례를 따르되, 사용자가 실행 시점에 명시한 경로를 맨 위에 둔다.
//
// 홈 디렉터리를 알 수 없어도 두 경로가 모두 명시(또는 환경 변수)되어 있으면 실패하지
// 않는다. 홈이 필요한데 없을 때만 에러다.
func ResolveWith(ov Override) (Locations, error) {
	loc := Locations{
		DefaultProfile: firstEnv(EnvProfile, EnvDefaultProfile),
		DefaultRegion:  firstEnv(EnvRegion, EnvDefaultRegion),
	}

	home, homeErr := homeDir()
	loc.Home = home

	if home != "" {
		loc.SSOCacheDir = filepath.Join(home, ".aws", "sso", "cache")
	}

	var err error

	loc.Config, err = resolvePathWith(ov.ConfigPath, EnvConfigFile, home, homeErr, "config")
	if err != nil {
		return loc, err
	}

	loc.Credentials, err = resolvePathWith(ov.CredentialsPath, EnvCredentialsFile, home, homeErr, "credentials")
	if err != nil {
		return loc, err
	}

	return loc, nil
}

func resolvePathWith(override, envVar, home string, homeErr error, filename string) (ResolvedPath, error) {
	// 1순위: 명시적으로 지정한 경로(플래그, TUI 입력).
	if v := strings.TrimSpace(override); v != "" {
		expanded, err := expandHome(v, home, homeErr)
		if err != nil {
			return ResolvedPath{}, err
		}

		expanded = absolute(expanded)

		return ResolvedPath{
			Path:   expanded,
			Source: PathFromFlag,
			Exists: fileExists(expanded),
		}, nil
	}

	// 2순위: 전용 환경 변수.
	if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
		// 환경 변수의 ~ 는 셸이 확장해주지 않는 경우가 있어서 직접 처리한다.
		expanded, err := expandHome(v, home, homeErr)
		if err != nil {
			return ResolvedPath{}, err
		}

		expanded = absolute(expanded)

		return ResolvedPath{
			Path:   expanded,
			Source: PathFromEnv,
			EnvVar: envVar,
			Exists: fileExists(expanded),
		}, nil
	}

	if homeErr != nil {
		return ResolvedPath{}, fmt.Errorf(
			"홈 디렉터리를 찾을 수 없어 %s 위치를 결정할 수 없습니다. %s로 직접 지정하세요: %w",
			filename, envVar, homeErr)
	}

	path := filepath.Join(home, ".aws", filename)

	return ResolvedPath{
		Path:   path,
		Source: PathFromHome,
		Exists: fileExists(path),
	}, nil
}

// expandHome은 선행 ~ 를 홈 디렉터리로 바꾼다.
//
// 환경 변수가 설정 파일이나 CI 설정에서 넘어온 경우 셸을 거치지 않아 ~ 가 그대로 남는다.
// 그 상태로 열면 "~/.aws/config"라는 이름의 디렉터리를 찾게 되어 조용히 실패한다.
func expandHome(path, home string, homeErr error) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}

	if homeErr != nil {
		return "", fmt.Errorf("경로 %q의 ~ 를 확장할 수 없습니다: %w", path, homeErr)
	}

	if path == "~" {
		return home, nil
	}

	return filepath.Join(home, path[2:]), nil
}

// absolute는 경로를 절대 경로로 만든다.
//
// AWS_CONFIG_FILE에 상대 경로를 넣는 경우가 있고, AWS CLI와 마찬가지로 그건 현재 작업
// 디렉터리 기준으로 해석된다. 상대 경로를 그대로 들고 다니면 두 가지가 문제다. 진단
// 출력에 "cmd/testdata/config"라고만 찍혀서 실제로 어느 파일을 봤는지 알 수 없고,
// 작업 디렉터리가 바뀌면 같은 값이 다른 파일을 가리킨다.
//
// 변환에 실패하면 원본을 그대로 쓴다. 진단 품질보다 동작이 우선이다.
func absolute(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}

	return abs
}

func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && !info.IsDir()
}

// firstEnv는 나열된 변수 중 값이 있는 첫 번째를 반환한다.
func firstEnv(names ...string) EnvValue {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return EnvValue{Value: v, EnvVar: name}
		}
	}

	return EnvValue{}
}

// LoadProfiles는 이 위치들에서 프로필을 읽어온다.
//
// Resolve와 분리한 이유는, 읽기가 실패했을 때도 호출자가 어디를 찾아봤는지 사용자에게
// 보여줄 수 있어야 하기 때문이다.
func (l Locations) LoadProfiles() ([]Profile, error) {
	return LoadProfilesFrom(l.Config.Path, l.Credentials.Path)
}
