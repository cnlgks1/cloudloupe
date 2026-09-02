// cloudloupe 명령은 조회 전용 AWS 인프라 조사 TUI다.
//
// AWS 공유 설정에 있는 프로필들을 넘나들며 리소스와 그 관계, 그리고 미사용 판정의 근거를
// 조사한다. AWS 리소스를 생성하거나 수정하거나 삭제하지 않는다.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"github.com/cnlgks1/cloudloupe/internal/app"
	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/catalog"
	"github.com/cnlgks1/cloudloupe/internal/tui"
)

// 빌드 정보. 릴리스 시 -ldflags로 주입된다. 이 프로젝트에서 유일하게 허용된 패키지 수준
// 변수이며, 나머지 의존성은 모두 명시적으로 주입한다.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "cloudloupe: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("cloudloupe", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		showVersion = fs.Bool("version", false, "버전 정보를 출력하고 종료한다")
		check       = fs.Bool("check", false, "설정 위치를 진단하고 문제가 있으면 0이 아닌 코드로 종료한다")
		listOnly    = fs.Bool("list-profiles", false, "TUI 없이 프로필 목록만 출력한다")
		output      = fs.String("output", "", "프로필 목록 출력 형식: text 또는 json (지정하면 TUI 대신 목록만 출력)")
		ascii       = fs.Bool("ascii", false, "유니코드 대신 ASCII 문자로 렌더링한다 (구형 Windows 콘솔용)")
		configPath  = fs.String("config", "", "AWS config 파일 경로 (기본: ~/.aws/config 또는 AWS_CONFIG_FILE)")
		credsPath   = fs.String("credentials", "", "AWS credentials 파일 경로 (기본: ~/.aws/credentials)")
	)

	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("플래그 해석: %w", err)
	}

	override := awsclient.Override{ConfigPath: *configPath, CredentialsPath: *credsPath}

	if *showVersion {
		fmt.Fprintf(stdout, "cloudloupe %s\n커밋:   %s\n빌드:   %s\nGo:     %s %s/%s\n",
			version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)

		return nil
	}

	if *check {
		return runCheck(stdout, override)
	}

	// --output이나 --list-profiles를 주면 헤드리스 모드(프로필 목록만). 그 외에는
	// 대화형 TUI를 띄운다. 파이프로 넘기거나 터미널이 아니면 자동으로 목록 출력으로
	// 폴백한다.
	if *output != "" || *listOnly {
		format := *output
		if format == "" {
			format = "text"
		}

		return listProfiles(stdout, format, override)
	}

	if !isInteractive() {
		// 터미널이 아니면(파이프, CI) TUI를 띄울 수 없다. 목록 출력으로 폴백한다.
		return listProfiles(stdout, "text", override)
	}

	return runTUI(ascii, override)
}

// isInteractive는 표준 출력이 터미널인지 확인한다.
//
// 파이프나 CI에서 실행하면 TUI를 띄울 수 없으므로 목록 출력으로 폴백한다.
func isInteractive() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// runTUI는 대화형 TUI를 띄운다.
//
// 여기가 배선의 핵심이다. TUI는 awsclient/collect를 직접 부르지 않고 tui.Deps 함수로
// 주입받는다. 실제 AWS 연결(Config, STS, 수집기)과 프로필 로딩을 이 함수들 안에 담아
// 주입한다. 덕분에 tui 패키지는 AWS를 모르고, 테스트에서는 가짜 Deps를 넘길 수 있다.
//
// 설정이 없어도 여기서 죽지 않는다. TUI가 경로 입력 화면을 띄워 사용자가 직접 위치를
// 지정할 수 있게 한다. LoadProfiles를 함수로 주입하는 이유가 이것이다. 경로를 바꿔가며
// 다시 시도할 수 있어야 한다.
func runTUI(ascii *bool, override awsclient.Override) error {
	groups, err := resourceGroups()
	if err != nil {
		return err
	}

	deps := tui.Deps{
		LoadProfiles: func(ov awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error) {
			loc, err := awsclient.ResolveWith(ov)
			if err != nil {
				return nil, loc, fmt.Errorf("AWS 설정 위치 해석: %w", err)
			}

			profiles, err := loc.LoadProfiles()
			if err != nil {
				return nil, loc, fmt.Errorf("AWS 프로필 로드: %w", err)
			}

			return profiles, loc, nil
		},
		ResourceGroups: groups,
		Identify:       app.IdentifyWithLocations,
		Collect:        app.CollectWithLocations,
		Explain:        awsclient.Explain,
	}

	theme := tui.New(tui.DetectASCII(*ascii))
	model := tui.NewModel(theme, deps, override)

	// AltScreen: TUI가 별도 화면 버퍼를 쓰고, 종료하면 원래 터미널 내용이 복원된다.
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("TUI 실행: %w", err)
	}

	return nil
}

// resourceGroups는 카탈로그의 세부 타입을 서비스별 큰 선택 단위로 변환한다.
func resourceGroups() ([]tui.ResourceGroup, error) {
	groups, err := catalog.Groups()
	if err != nil {
		return nil, fmt.Errorf("리소스 그룹 구성: %w", err)
	}

	out := make([]tui.ResourceGroup, 0, len(groups))

	for _, group := range groups {
		resourceGroup := tui.ResourceGroup{ID: group.ID, Label: group.Label}
		for _, definition := range group.Types {
			resourceGroup.Types = append(resourceGroup.Types, tui.ResourceType{
				ID:             definition.Type,
				Label:          definition.Label,
				Columns:        definition.Columns,
				SummaryColumns: definition.SummaryColumns,
			})
		}
		out = append(out, resourceGroup)
	}

	return out, nil
}

// runCheck는 설정 위치를 진단한다.
//
// 경로를 보여주는 것과 그 경로가 쓸 수 있는지 확인하는 것은 다른 일이다. 설치 방식과 실행
// 방식에 따라 cloudloupe가 보는 파일과 `aws` 명령이 보는 파일이 갈릴 수 있으므로, 그것을
// 대조까지 해서 알려준다.
//
// 문제가 있으면 0이 아닌 코드로 끝난다. 스크립트에서 사전 점검으로 쓸 수 있다.
func runCheck(w io.Writer, override awsclient.Override) error {
	loc, err := awsclient.ResolveWith(override)
	if err != nil {
		return fmt.Errorf("AWS 설정 위치 해석: %w", err)
	}

	// 프로필 읽기 실패는 진단 대상이지 진단 실패가 아니다. 읽지 못했다는 사실 자체가
	// 보고할 내용이므로 에러를 삼키고 nil을 넘긴다.
	profiles, loadErr := loc.LoadProfiles()
	if loadErr != nil {
		profiles = nil
	}

	diag := awsclient.Diagnose(loc, profiles)

	rows := make([][]string, 0, len(diag.Checks)+1)
	rows = append(rows, []string{"상태", "항목", "내용"})

	for _, c := range diag.Checks {
		rows = append(rows, []string{string(c.Status), c.Name, c.Detail})
	}

	if _, err := io.WriteString(w, formatTable(rows)); err != nil {
		return fmt.Errorf("진단 표 출력 실패: %w", err)
	}

	if loadErr != nil {
		fmt.Fprintf(w, "\n프로필 읽기 실패: %v\n", loadErr)
	} else {
		fmt.Fprintf(w, "\n프로필 %d개를 읽었습니다.\n", len(profiles))
	}

	crossCheckAWSCLI(w, loc, profiles, loadErr)

	if problems := diag.Problems(); len(problems) > 0 {
		fmt.Fprintln(w, "\n조치할 것:")

		for _, c := range problems {
			fmt.Fprintf(w, "  [%s] %s — %s\n", c.Status, c.Name, c.Detail)

			if c.Hint != "" {
				fmt.Fprintf(w, "         %s\n", c.Hint)
			}
		}
	}

	if diag.Worst() == awsclient.SeverityFail {
		return errors.New("진단에서 문제를 발견했습니다")
	}

	return nil
}

// listProfiles는 공유 설정에서 발견한 프로필을 보고한다.
//
// 1단계 진입점이다. 프로필 탐색은 동작하고, 대화형 TUI가 다음에 붙는다. 여기서는 AWS에
// 접속하지 않으므로 자격증명 없이도 실행된다.
func listProfiles(w io.Writer, format string, override awsclient.Override) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("알 수 없는 출력 형식 %q: text 또는 json이어야 합니다", format)
	}

	// 위치 해석을 읽기보다 먼저 한다. 읽기가 실패해도 어디를 찾아봤는지는 보여줄 수 있어야
	// 한다. "내 프로필이 안 보인다"의 원인은 거의 항상 예상과 다른 파일을 읽은 것이다.
	loc, err := awsclient.ResolveWith(override)
	if err != nil {
		return fmt.Errorf("AWS 설정 위치 해석: %w", err)
	}

	profiles, err := loc.LoadProfiles()

	if errors.Is(err, awsclient.ErrNoSharedConfig) {
		return noConfigError(loc, err)
	}

	if err != nil {
		return fmt.Errorf("AWS 프로필 로드: %w", err)
	}

	if format == "json" {
		return writeProfilesJSON(w, loc, profiles)
	}

	return writeProfilesText(w, loc, profiles)
}

// crossCheckAWSCLI는 cloudloupe가 본 프로필 목록을 `aws` 명령이 보는 목록과 대조한다.
//
// 이게 "같은 파일을 보고 있나"에 대한 결정적인 답이다. 두 목록이 다르면 서로 다른 설정을
// 읽고 있다는 뜻이고, 원인은 대개 환경 변수나 snap 같은 격리된 설치다.
//
// aws 명령이 없거나 실패하면 조용히 넘어간다. AWS CLI는 cloudloupe의 의존성이 아니므로
// 없다는 것이 문제는 아니다.
func crossCheckAWSCLI(
	w io.Writer,
	locations awsclient.Locations,
	profiles []awsclient.Profile,
	loadErr error,
) {
	if loadErr != nil {
		return
	}

	theirs, err := awsCLIProfiles(locations)
	if err != nil {
		return
	}

	ours := make([]string, 0, len(profiles))
	for _, p := range profiles {
		ours = append(ours, p.Name)
	}

	missing, extra := diffProfiles(ours, theirs)

	if len(missing) == 0 && len(extra) == 0 {
		fmt.Fprintf(w, "aws 명령과 대조: 일치 (프로필 %d개)\n", len(theirs))

		return
	}

	fmt.Fprintln(w, "\naws 명령과 대조: 불일치 — 서로 다른 설정 파일을 읽고 있습니다")

	if len(missing) > 0 {
		fmt.Fprintf(w, "  aws 에만 있음:        %s\n", strings.Join(missing, ", "))
	}

	if len(extra) > 0 {
		fmt.Fprintf(w, "  cloudloupe 에만 있음: %s\n", strings.Join(extra, ", "))
	}

	fmt.Fprintf(w, "  %s / %s 환경 변수를 확인하세요.\n",
		awsclient.EnvConfigFile, awsclient.EnvCredentialsFile)
}

// awsCLIProfiles는 `aws configure list-profiles`의 결과를 읽는다.
//
// 조회 전용 원칙은 AWS API 호출에 대한 것이고, 이건 로컬 명령이 자신의 설정 파일을 읽어
// 이름만 출력하는 것이다. AWS를 변경하지 않는다.
func awsCLIProfiles(locations awsclient.Locations) ([]string, error) {
	path, err := exec.LookPath("aws")
	if err != nil {
		return nil, err //nolint:wrapcheck // 호출자가 조용히 무시한다
	}

	// 사용자 기계의 CLI가 느릴 수 있으므로 상한을 둔다. 진단이 멈춰 있으면 안 된다.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "configure", "list-profiles")
	cmd.Env = awsCLIEnv(locations)

	out, err := cmd.Output()
	if err != nil {
		return nil, err //nolint:wrapcheck // 호출자가 조용히 무시한다
	}

	var names []string

	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}

	return names, nil
}

// awsCLIEnv는 비교 대상 AWS CLI도 cloudloupe와 같은 공유 설정 파일을 보게 한다.
func awsCLIEnv(locations awsclient.Locations) []string {
	const (
		configPrefix      = awsclient.EnvConfigFile + "="
		credentialsPrefix = awsclient.EnvCredentialsFile + "="
	)

	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, configPrefix) || strings.HasPrefix(entry, credentialsPrefix) {
			continue
		}

		env = append(env, entry)
	}

	if locations.Config.Path != "" {
		env = append(env, configPrefix+locations.Config.Path)
	}
	if locations.Credentials.Path != "" {
		env = append(env, credentialsPrefix+locations.Credentials.Path)
	}

	return env
}

// diffProfiles는 두 프로필 이름 목록의 차이를 구한다.
//
// exec에서 분리해둔 순수 함수다. 덕분에 AWS CLI 설치 여부와 무관하게 비교 로직을 테스트할 수
// 있다.
func diffProfiles(ours, theirs []string) (missingFromOurs, extraInOurs []string) {
	inOurs := make(map[string]bool, len(ours))
	for _, n := range ours {
		inOurs[n] = true
	}

	inTheirs := make(map[string]bool, len(theirs))
	for _, n := range theirs {
		inTheirs[n] = true
	}

	for _, n := range theirs {
		if !inOurs[n] {
			missingFromOurs = append(missingFromOurs, n)
		}
	}

	for _, n := range ours {
		if !inTheirs[n] {
			extraInOurs = append(extraInOurs, n)
		}
	}

	slices.Sort(missingFromOurs)
	slices.Sort(extraInOurs)

	return missingFromOurs, extraInOurs
}

// noConfigError는 설정 파일이 없을 때 무엇을 어디서 찾았는지와 다음에 할 일을 알려준다.
//
// 안내 문구는 AWS CLI가 설치되어 있는지에 따라 달라진다. `aws` 명령이 없는 사람에게
// `aws configure`를 실행하라고 하는 것은 도움이 되지 않는다.
func noConfigError(loc awsclient.Locations, cause error) error {
	var sb strings.Builder

	fmt.Fprintf(&sb, "%v\n\n찾아본 위치:\n", cause)
	fmt.Fprintf(&sb, "  config       %s\n", loc.Config)
	fmt.Fprintf(&sb, "  credentials  %s\n", loc.Credentials)

	if loc.Home != "" {
		fmt.Fprintf(&sb, "  홈 디렉터리   %s\n", loc.Home)
	}

	sb.WriteString("\n")

	if _, err := exec.LookPath("aws"); err == nil {
		sb.WriteString("프로필을 만드세요:\n")
		sb.WriteString("    aws configure sso --profile prod\n")
		sb.WriteString("    aws configure --profile prod\n")
	} else {
		sb.WriteString("AWS CLI가 설치되어 있지 않습니다. 설정 파일을 직접 만들거나 CLI를 설치하세요:\n")
		sb.WriteString("    brew install awscli\n")
	}

	fmt.Fprintf(&sb, "\n다른 위치의 설정을 쓰려면 %s와 %s를 지정하세요.\n",
		awsclient.EnvConfigFile, awsclient.EnvCredentialsFile)
	sb.WriteString("cloudloupe --check 로 어디를 찾아보는지 확인할 수 있습니다.")

	return errors.New(sb.String())
}

func writeProfilesText(w io.Writer, loc awsclient.Locations, profiles []awsclient.Profile) error {
	if len(profiles) == 0 {
		fmt.Fprintln(w, "AWS 공유 설정에서 프로필을 찾지 못했습니다.")
		writeLocations(w, loc)

		return nil
	}

	// 기본 프로필 표시 열은 실제로 일치하는 프로필이 있을 때만 넣는다. 항상 넣으면
	// 대부분의 경우 빈 열이 표를 밀어낸다.
	marked := hasDefault(loc, profiles)

	header := []string{"프로필", "종류", "리전", "선언 위치", "상세"}
	if marked {
		header = append([]string{""}, header...)
	}

	rows := make([][]string, 0, len(profiles)+1)
	rows = append(rows, header)

	for _, p := range profiles {
		row := []string{p.Name, string(p.Kind), region(loc, p), p.Source.String(), detail(p)}
		if marked {
			row = append([]string{marker(loc, p)}, row...)
		}

		rows = append(rows, row)
	}

	if _, err := io.WriteString(w, formatTable(rows)); err != nil {
		return fmt.Errorf("프로필 표 출력 실패: %w", err)
	}

	fmt.Fprintf(w, "\n프로필 %d개. cloudloupe는 조회 전용이며 AWS 리소스나 자격증명을 변경하지 않습니다.\n",
		len(profiles))

	if marked {
		// 조사를 붙이면 영어 식별자의 끝 글자에 따라 "이/가"가 달라진다. 괄호로 피한다.
		fmt.Fprintf(w, "* 는 기본 프로필입니다 (%s).\n", loc.DefaultProfile.EnvVar)
	}

	writeDefaultProfileWarning(w, loc, profiles)
	writeLocations(w, loc)

	return nil
}

// writeDefaultProfileWarning은 환경이 지정한 기본 프로필이 존재하지 않을 때 알려준다.
//
// AWS_PROFILE이 오타이거나, 이미 지운 프로필을 가리키거나, 다른 config 파일을 읽고 있을 때
// 생긴다. 조용히 무시하면 사용자는 나중에 자격증명 해석이 실패하는 이유를 알 수 없다.
func writeDefaultProfileWarning(w io.Writer, loc awsclient.Locations, profiles []awsclient.Profile) {
	if !loc.DefaultProfile.Set() || hasDefault(loc, profiles) {
		return
	}

	fmt.Fprintf(w, "\n경고: %s=%q 인데 그 이름의 프로필이 없습니다.\n",
		loc.DefaultProfile.EnvVar, loc.DefaultProfile.Value)
	fmt.Fprintln(w, "      오타이거나, 지워진 프로필이거나, 다른 config 파일을 읽고 있을 수 있습니다.")
}

func hasDefault(loc awsclient.Locations, profiles []awsclient.Profile) bool {
	if !loc.DefaultProfile.Set() {
		return false
	}

	for _, p := range profiles {
		if p.Name == loc.DefaultProfile.Value {
			return true
		}
	}

	return false
}

// writeLocations는 설정을 어디서 읽었는지 보여준다.
//
// 이걸 항상 출력하는 이유가 있다. Homebrew로 설치했든 아카이브를 직접 풀었든 바이너리는
// 실행한 사용자의 환경에서 경로를 해석하는데, 그 결과가 사용자의 예상과 다를 때가 있다.
// 환경 변수가 걸려 있거나 sudo로 실행해 홈이 바뀐 경우다. 읽은 위치를 보여주면 그 진단이
// 한 번에 끝난다.
func writeLocations(w io.Writer, loc awsclient.Locations) {
	fmt.Fprintf(w, "\n설정 위치\n")
	fmt.Fprintf(w, "  config       %s\n", loc.Config)
	fmt.Fprintf(w, "  credentials  %s\n", loc.Credentials)

	if loc.DefaultRegion.Set() {
		fmt.Fprintf(w, "  기본 리전     %s (%s)\n", loc.DefaultRegion.Value, loc.DefaultRegion.EnvVar)
	}
}

// marker는 환경이 지정한 기본 프로필에 표시를 붙인다.
func marker(loc awsclient.Locations, p awsclient.Profile) string {
	if loc.DefaultProfile.Value == p.Name {
		return "*"
	}

	return " "
}

// region은 프로필의 리전을 반환하되, 없으면 환경 변수가 준 값으로 대체한다.
//
// AWS_REGION은 프로필에 리전이 없을 때 실제로 사용되는 값이므로, 비어 있다고 표시하면
// 사실과 다르다.
func region(loc awsclient.Locations, p awsclient.Profile) string {
	if p.Region != "" {
		return p.Region
	}

	if loc.DefaultRegion.Set() {
		return loc.DefaultRegion.Value + " (환경)"
	}

	return "-"
}

func writeProfilesJSON(w io.Writer, loc awsclient.Locations, profiles []awsclient.Profile) error {
	// JSON 키는 출력 계약이므로 영어로 유지한다. 번역하면 이 출력을 파싱하는 쪽이 깨진다.
	type pathJSON struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		EnvVar string `json:"envVar,omitempty"`
		Exists bool   `json:"exists"`
	}

	type envJSON struct {
		Value  string `json:"value"`
		EnvVar string `json:"envVar"`
	}

	type locationsJSON struct {
		Home           string   `json:"home,omitempty"`
		Config         pathJSON `json:"config"`
		Credentials    pathJSON `json:"credentials"`
		SSOCacheDir    string   `json:"ssoCacheDir,omitempty"`
		DefaultProfile *envJSON `json:"defaultProfile,omitempty"`
		DefaultRegion  *envJSON `json:"defaultRegion,omitempty"`
	}

	type profileJSON struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		Region        string `json:"region,omitempty"`
		DeclaredIn    string `json:"declaredIn"`
		IsDefault     bool   `json:"isDefault"`
		RoleARN       string `json:"roleArn,omitempty"`
		SourceProfile string `json:"sourceProfile,omitempty"`
		SSOSession    string `json:"ssoSession,omitempty"`
		SSOAccountID  string `json:"ssoAccountId,omitempty"`
		SSORoleName   string `json:"ssoRoleName,omitempty"`
		MFASerial     string `json:"mfaSerial,omitempty"`
	}

	toPath := func(p awsclient.ResolvedPath) pathJSON {
		return pathJSON{
			Path:   p.Path,
			Source: string(p.Source),
			EnvVar: p.EnvVar,
			Exists: p.Exists,
		}
	}

	toEnv := func(v awsclient.EnvValue) *envJSON {
		if !v.Set() {
			return nil
		}

		return &envJSON{Value: v.Value, EnvVar: v.EnvVar}
	}

	out := make([]profileJSON, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, profileJSON{
			Name:          p.Name,
			Kind:          string(p.Kind),
			Region:        p.Region,
			DeclaredIn:    p.Source.String(),
			IsDefault:     loc.DefaultProfile.Value == p.Name,
			RoleARN:       p.RoleARN,
			SourceProfile: p.SourceProfile,
			SSOSession:    p.SSOSession,
			SSOAccountID:  p.SSOAccountID,
			SSORoleName:   p.SSORoleName,
			MFASerial:     p.MFASerial,
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	payload := map[string]any{
		"locations": locationsJSON{
			Home:           loc.Home,
			Config:         toPath(loc.Config),
			Credentials:    toPath(loc.Credentials),
			SSOCacheDir:    loc.SSOCacheDir,
			DefaultProfile: toEnv(loc.DefaultProfile),
			DefaultRegion:  toEnv(loc.DefaultRegion),
		},
		"profiles": out,
	}

	if err := enc.Encode(payload); err != nil {
		return fmt.Errorf("프로필 JSON 인코딩 실패: %w", err)
	}

	return nil
}

func detail(p awsclient.Profile) string {
	switch p.Kind {
	case awsclient.KindSSO:
		if p.SSOAccountID != "" {
			return "계정 " + p.SSOAccountID
		}

		return "sso-session " + p.SSOSession
	case awsclient.KindAssumeRole:
		return p.RoleARN
	case awsclient.KindProcess, awsclient.KindStatic, awsclient.KindUnknown:
		return "-"
	default:
		return "-"
	}
}

// formatTable은 열을 표시 폭에 맞춰 정렬한 표를 만든다.
//
// text/tabwriter를 쓰지 않는 이유는 tabwriter가 룬 개수로 폭을 세기 때문이다. 한글은
// 터미널에서 두 칸을 차지하므로 한국어 헤더를 쓰면 열이 어긋난다. displayWidth로 직접
// 계산해서 맞춘다.
func formatTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	widths := make([]int, len(rows[0]))

	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && displayWidth(cell) > widths[i] {
				widths[i] = displayWidth(cell)
			}
		}
	}

	const gap = 2

	var sb strings.Builder

	for _, row := range rows {
		for i, cell := range row {
			sb.WriteString(cell)

			// 마지막 열 뒤에는 여백을 넣지 않는다. 줄 끝 공백이 남으면 복사해 붙일 때
			// 지저분해진다.
			if i < len(row)-1 {
				sb.WriteString(strings.Repeat(" ", widths[i]-displayWidth(cell)+gap))
			}
		}

		sb.WriteByte('\n')
	}

	return sb.String()
}

// displayWidth는 문자열이 터미널에서 차지하는 칸 수를 센다.
//
// 동아시아 전각 문자를 두 칸으로 계산한다. 여기 나열한 범위는 한글과 CJK를 다루기에
// 충분하며, 이 프로그램이 출력하는 나머지 문자(AWS 식별자, ARN, 리전 이름)는 모두
// ASCII다. 결합 문자나 이모지까지 정확히 다뤄야 할 때가 오면 TUI에서 쓰는 lipgloss의
// 폭 계산으로 넘긴다.
func displayWidth(s string) int {
	width := 0

	for _, r := range s {
		if isWide(r) {
			width += 2

			continue
		}

		width++
	}

	return width
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F: // 한글 자모
		return true
	case r >= 0x2E80 && r <= 0x303E: // CJK 부수, 기호 및 문장부호
		return true
	case r >= 0x3041 && r <= 0x33FF: // 히라가나, 가타카나, 한글 호환 자모, CJK 기호
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK 확장 A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 통합 한자
		return true
	case r >= 0xA000 && r <= 0xA4CF: // 이족 음절
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // 한글 음절
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK 호환 한자
		return true
	case r >= 0xFE30 && r <= 0xFE6F: // CJK 호환 형태
		return true
	case r >= 0xFF00 && r <= 0xFF60: // 전각 형태
		return true
	case r >= 0xFFE0 && r <= 0xFFE6: // 전각 기호
		return true
	default:
		return false
	}
}

// usage는 --help 출력의 머리말이다.
//
// 사용자가 이 도구를 처음 보는 문자열이므로 실제로 되는 것만 적는다. 구현 범위가 늘면 이
// 문구도 함께 고쳐야 한다.
const usage = `cloudloupe — 조회 전용 AWS 인프라 조사 TUI.

여러 프로필과 리전의 AWS 리소스를 조회합니다. 리소스를 생성하거나 수정하거나 삭제하지
않습니다. SDK 호출은 조회 계열 API로 제한되며 CI가 이를 검사합니다.

사용법:
  cloudloupe [플래그]

플래그 없이 실행하면 프로필 → 리전 → 리소스 선택을 거쳐 조회하는 TUI가 열립니다.
지원 리소스와 필요한 IAM 권한은 README.md에 있습니다.

플래그:
`
