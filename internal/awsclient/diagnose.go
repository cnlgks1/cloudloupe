package awsclient

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Severity는 진단 항목의 심각도다.
type Severity string

// 진단 결과의 심각도.
const (
	SeverityOK   Severity = "정상"
	SeverityWarn Severity = "주의"
	SeverityFail Severity = "문제"
)

func (s Severity) rank() int {
	switch s {
	case SeverityFail:
		return 2
	case SeverityWarn:
		return 1
	case SeverityOK:
		return 0
	default:
		return 0
	}
}

// Check는 진단 항목 하나의 결과다.
//
// Hint는 사용자가 실제로 할 수 있는 행동을 담는다. 문제를 알려주고 해결책을 주지 않는
// 진단은 절반만 한 것이다.
type Check struct {
	Name   string
	Status Severity
	Detail string
	Hint   string
}

// Diagnosis는 설정 위치 진단 결과 전체다.
type Diagnosis struct {
	Locations Locations
	Checks    []Check
}

// Worst는 가장 심각한 항목의 심각도를 반환한다.
func (d Diagnosis) Worst() Severity {
	worst := SeverityOK

	for _, c := range d.Checks {
		if c.Status.rank() > worst.rank() {
			worst = c.Status
		}
	}

	return worst
}

// Problems는 주의 이상인 항목만 반환한다.
func (d Diagnosis) Problems() []Check {
	var out []Check

	for _, c := range d.Checks {
		if c.Status != SeverityOK {
			out = append(out, c)
		}
	}

	return out
}

// Diagnose는 해석된 설정 위치가 실제로 쓸 수 있는 상태인지 검증한다.
//
// 경로를 보여주는 것만으로는 부족하다. 같은 사용자, 같은 기계에서도 다음 이유로 cloudloupe가
// 보는 파일과 `aws` 명령이 보는 파일이 갈릴 수 있다.
//
//   - sudo로 실행하면 홈이 root의 것으로 바뀌어 프로필이 사라진 것처럼 보인다
//   - Linux에서 snap으로 설치한 AWS CLI는 격리된 홈(~/snap/aws-cli/...)에 설정을 둔다
//   - 환경 변수가 다른 파일을 가리킨다
//   - dotfile 관리 도구가 심볼릭 링크를 걸어두었다
//   - 파일은 있지만 권한 때문에 읽을 수 없다
//
// profiles는 읽기에 성공했을 때의 프로필 목록이다. 실패했으면 nil을 넘긴다. AWS_PROFILE이
// 실제 존재하는 프로필을 가리키는지 확인하는 데 쓴다.
//
// 이 함수는 파일 시스템과 환경 변수만 본다. AWS를 호출하지 않고 외부 프로세스를 실행하지
// 않으므로, 자격증명 없이 테스트할 수 있다.
func Diagnose(loc Locations, profiles []Profile) Diagnosis {
	d := Diagnosis{Locations: loc}

	d.Checks = append(d.Checks,
		checkHome(loc),
		checkAWSDir(loc),
		checkConfigFile(loc),
		checkCredentialsFile(loc),
	)

	if c, ok := checkCredentialsPermissions(loc); ok {
		d.Checks = append(d.Checks, c)
	}

	if c, ok := checkSymlink(loc); ok {
		d.Checks = append(d.Checks, c)
	}

	if c, ok := checkDefaultProfile(loc, profiles); ok {
		d.Checks = append(d.Checks, c)
	}

	d.Checks = append(d.Checks, checkAlternativeLocations(loc))

	return d
}

func checkHome(loc Locations) Check {
	const name = "홈 디렉터리"

	if loc.Home == "" {
		return Check{
			Name:   name,
			Status: SeverityFail,
			Detail: "확인할 수 없습니다",
			Hint:   fmt.Sprintf("%s와 %s로 파일 경로를 직접 지정하세요", EnvConfigFile, EnvCredentialsFile),
		}
	}

	// sudo로 실행하면 홈이 root의 것이 되어 프로필이 전부 사라진 것처럼 보인다.
	// cloudloupe는 조회 전용이라 관리자 권한이 필요할 이유가 없다.
	if user := strings.TrimSpace(os.Getenv("SUDO_USER")); user != "" {
		return Check{
			Name:   name,
			Status: SeverityWarn,
			Detail: fmt.Sprintf("%s (sudo로 실행 중, 원래 사용자는 %s)", loc.Home, user),
			Hint:   "cloudloupe는 조회 전용이라 sudo가 필요하지 않습니다. sudo 없이 실행하세요",
		}
	}

	return Check{Name: name, Status: SeverityOK, Detail: loc.Home}
}

func checkAWSDir(loc Locations) Check {
	const name = "설정 디렉터리"

	dir := filepath.Dir(loc.Config.Path)

	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return Check{
			Name:   name,
			Status: SeverityWarn,
			Detail: fmt.Sprintf("%s 없음", dir),
			Hint:   "AWS 설정을 아직 만들지 않은 상태입니다",
		}
	}

	if err != nil {
		return Check{
			Name:   name,
			Status: SeverityFail,
			Detail: fmt.Sprintf("%s 확인 실패: %v", dir, err),
		}
	}

	if !info.IsDir() {
		return Check{
			Name:   name,
			Status: SeverityFail,
			Detail: fmt.Sprintf("%s 가 디렉터리가 아닙니다", dir),
			Hint:   "같은 이름의 파일이 있으면 AWS CLI도 동작하지 않습니다",
		}
	}

	return Check{Name: name, Status: SeverityOK, Detail: dir}
}

func checkConfigFile(loc Locations) Check {
	const name = "config 파일"

	if !loc.Config.Exists {
		return Check{
			Name:   name,
			Status: SeverityWarn,
			Detail: fmt.Sprintf("%s 없음", loc.Config.Path),
			Hint:   "aws configure sso 또는 aws configure 로 프로필을 만드세요",
		}
	}

	if err := readable(loc.Config.Path); err != nil {
		return Check{
			Name:   name,
			Status: SeverityFail,
			Detail: fmt.Sprintf("%s 읽을 수 없음: %v", loc.Config.Path, err),
			Hint:   "파일 소유자와 권한을 확인하세요",
		}
	}

	return Check{Name: name, Status: SeverityOK, Detail: loc.Config.String()}
}

func checkCredentialsFile(loc Locations) Check {
	const name = "credentials 파일"

	// 없는 것이 정상인 경우가 많다. SSO나 assume-role만 쓰면 이 파일이 아예 없다.
	if !loc.Credentials.Exists {
		return Check{
			Name:   name,
			Status: SeverityOK,
			Detail: fmt.Sprintf("%s 없음 (SSO나 assume-role만 쓰면 정상)", loc.Credentials.Path),
		}
	}

	if err := readable(loc.Credentials.Path); err != nil {
		return Check{
			Name:   name,
			Status: SeverityFail,
			Detail: fmt.Sprintf("%s 읽을 수 없음: %v", loc.Credentials.Path, err),
			Hint:   "파일 소유자와 권한을 확인하세요",
		}
	}

	return Check{Name: name, Status: SeverityOK, Detail: loc.Credentials.String()}
}

// checkCredentialsPermissions는 자격증명 파일이 다른 사용자에게 열려 있는지 본다.
//
// AWS CLI는 권한을 검사하지 않으므로 동작에는 문제가 없다. 하지만 장기 액세스 키가 담긴
// 파일이 같은 기계의 다른 사용자에게 읽힌다면 알려줄 가치가 있다.
func checkCredentialsPermissions(loc Locations) (Check, bool) {
	if !loc.Credentials.Exists {
		return Check{}, false
	}

	info, err := os.Stat(loc.Credentials.Path)
	if err != nil {
		return Check{}, false
	}

	if !tooPermissive(info.Mode()) {
		return Check{}, false
	}

	return Check{
		Name:   "credentials 권한",
		Status: SeverityWarn,
		Detail: fmt.Sprintf("%04o — 다른 사용자가 읽을 수 있습니다", info.Mode().Perm()),
		Hint:   fmt.Sprintf("chmod 600 %s", loc.Credentials.Path),
	}, true
}

// checkSymlink는 설정 파일이 심볼릭 링크인지 확인하고 대상을 보고한다.
//
// dotfile 관리 도구(stow, chezmoi 등)를 쓰면 흔한 구성이다. 문제는 아니지만, 링크가 끊겨
// 있거나 예상과 다른 곳을 가리키는 경우를 찾을 때 이 정보가 필요하다.
func checkSymlink(loc Locations) (Check, bool) {
	info, err := os.Lstat(loc.Config.Path)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return Check{}, false
	}

	target, err := filepath.EvalSymlinks(loc.Config.Path)
	if err != nil {
		return Check{
			Name:   "심볼릭 링크",
			Status: SeverityFail,
			Detail: fmt.Sprintf("%s 링크가 끊어졌습니다: %v", loc.Config.Path, err),
			Hint:   "링크 대상이 존재하는지 확인하세요",
		}, true
	}

	return Check{
		Name:   "심볼릭 링크",
		Status: SeverityOK,
		Detail: fmt.Sprintf("%s → %s", loc.Config.Path, target),
	}, true
}

func checkDefaultProfile(loc Locations, profiles []Profile) (Check, bool) {
	if !loc.DefaultProfile.Set() {
		return Check{}, false
	}

	name := fmt.Sprintf("기본 프로필 (%s)", loc.DefaultProfile.EnvVar)

	// 프로필을 읽지 못했으면 존재 여부를 판단할 수 없다. 모르는 것을 안다고 하지 않는다.
	if profiles == nil {
		return Check{
			Name:   name,
			Status: SeverityOK,
			Detail: loc.DefaultProfile.Value + " (프로필 목록을 읽지 못해 확인 못 함)",
		}, true
	}

	for _, p := range profiles {
		if p.Name == loc.DefaultProfile.Value {
			return Check{Name: name, Status: SeverityOK, Detail: p.Name}, true
		}
	}

	return Check{
		Name:   name,
		Status: SeverityWarn,
		Detail: fmt.Sprintf("%q 프로필이 설정에 없습니다", loc.DefaultProfile.Value),
		Hint: fmt.Sprintf("오타이거나 지워진 프로필입니다. unset %s 하거나 이름을 고치세요",
			loc.DefaultProfile.EnvVar),
	}, true
}

// checkAlternativeLocations는 사용 중이 아닌 다른 위치에 설정이 있는지 찾는다.
//
// 사용자가 "설정이 있는데 왜 못 찾냐"고 할 때의 실제 원인이 대개 여기 있다. 설치 방식과
// 실행 방식에 따라 AWS 설정이 다른 곳에 놓이기 때문이다. 우리가 쓰는 경로에 파일이 없는데
// 다른 알려진 위치에 있다면, 그것을 알려주는 것이 진단의 핵심이다.
func checkAlternativeLocations(loc Locations) Check {
	const name = "다른 위치의 설정"

	found := make([]string, 0, 2)

	for _, candidate := range alternativeConfigPaths(loc) {
		if candidate == loc.Config.Path {
			continue
		}

		if fileExists(candidate) {
			found = append(found, candidate)
		}
	}

	if len(found) == 0 {
		return Check{Name: name, Status: SeverityOK, Detail: "없음"}
	}

	// 우리가 쓰는 파일이 정상이면 참고 정보다. 우리 파일이 없는데 저쪽에 있으면 그게 원인이다.
	status := SeverityOK
	hint := "참고용입니다. 현재 사용 중인 파일은 위에 표시된 경로입니다"

	if !loc.Config.Exists {
		status = SeverityWarn
		hint = fmt.Sprintf("이 파일을 쓰려면 %s=%s 로 지정하세요", EnvConfigFile, found[0])
	}

	return Check{
		Name:   name,
		Status: status,
		Detail: strings.Join(found, ", "),
		Hint:   hint,
	}
}

// alternativeConfigPaths는 AWS 설정이 놓일 수 있는 다른 위치 후보를 만든다.
//
// 목록을 짧게 유지한다. 실제로 관측되는 경우만 넣는다. 추측으로 늘리면 존재하지 않는 경로를
// 확인하는 데 시간만 쓰고 진단 출력이 지저분해진다.
func alternativeConfigPaths(loc Locations) []string {
	var out []string

	if loc.Home != "" {
		// Linux에서 snap으로 설치한 AWS CLI는 격리된 홈에 설정을 만든다. 사용자는
		// aws configure 를 실행했는데 ~/.aws 에는 아무것도 없는 상황이 된다.
		out = append(out, filepath.Join(loc.Home, "snap", "aws-cli", "current", ".aws", "config"))
	}

	// sudo로 실행 중이면 원래 사용자의 홈에 설정이 있다. 그게 사용자가 기대하는 파일이다.
	if user := strings.TrimSpace(os.Getenv("SUDO_USER")); user != "" {
		for _, base := range []string{"/home", "/Users"} {
			out = append(out, filepath.Join(base, user, ".aws", "config"))
		}
	}

	return out
}

func readable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err //nolint:wrapcheck // 호출자가 문맥을 붙인다
	}

	return f.Close() //nolint:wrapcheck // 호출자가 문맥을 붙인다
}
