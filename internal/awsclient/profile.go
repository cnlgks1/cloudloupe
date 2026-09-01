// Package awsclient는 AWS 공유 설정을 읽고 cloudloupe가 사용하는 조회 전용 서비스
// 클라이언트를 만든다.
package awsclient

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// ErrNoSharedConfig는 두 공유 설정 파일 중 어느 것도 존재하지 않음을 알린다.
//
// 이건 프로그램의 실패가 아니라 흔한 첫 실행 상황이다. 따라서 호출자는 errors.Is로
// 이 값을 잡아내고 프로필을 만드는 방법을 안내해야 한다.
var ErrNoSharedConfig = errors.New("AWS 공유 설정을 찾을 수 없습니다")

// Source는 프로필이 어느 공유 파일에 선언되었는지 기록하는 비트 집합이다.
type Source uint8

// 프로필이 선언될 수 있는 출처.
const (
	SourceConfig Source = 1 << iota
	SourceCredentials
)

// String은 프로필이 어느 파일에서 왔는지 이름으로 알려준다.
func (s Source) String() string {
	switch {
	case s&SourceConfig != 0 && s&SourceCredentials != 0:
		return "config+credentials"
	case s&SourceConfig != 0:
		return "config"
	case s&SourceCredentials != 0:
		return "credentials"
	default:
		return "unknown"
	}
}

// Kind는 프로필이 자격증명을 얻는 방식을 분류한다. 표시용 메타데이터이며, 실제 해석은
// AWS SDK의 자격증명 체인에 맡긴다.
type Kind string

// 인식하는 프로필 종류.
const (
	KindSSO        Kind = "sso"
	KindAssumeRole Kind = "assume-role"
	KindProcess    Kind = "process"
	KindStatic     Kind = "static"
	KindUnknown    Kind = "unknown"
)

// Profile은 공유 설정 파일에서 발견한 항목 하나다.
//
// 이 구조체의 모든 필드는 비밀이 아닌 메타데이터다. 액세스 키, 시크릿 키, 세션 토큰,
// SSO 토큰은 의도적으로 없다. cloudloupe가 이 파일들을 읽는 목적은 어떤 프로필이
// 존재하고 어떤 형태인지 파악하는 것뿐이다. 자격증명 값은 AWS SDK가 해석하며 이
// 구조체로 절대 복사되지 않는다. 따라서 Profile은 아무것도 유출하지 않고 로그에 남기거나
// 화면에 그릴 수 있다.
type Profile struct {
	Name          string
	Region        string
	Kind          Kind
	Source        Source
	RoleARN       string
	SourceProfile string
	SSOSession    string
	SSOAccountID  string
	SSORoleName   string
	MFASerial     string
}

// LoadProfilesFrom은 주어진 파일들에서 프로필을 찾아낸다.
//
// 파일이 없는 것은 에러가 아니다. config 파일만 있는 설정(SSO, assume-role)도,
// credentials 파일만 있는 설정도 흔하다. 둘 다 없을 때만 ErrNoSharedConfig를 반환한다.
//
// 결과는 "default"를 맨 앞에 두고 나머지를 이름순으로 정렬해 반환한다. 선택 목록의
// 순서가 파일에 어떤 순서로 적혀 있는지에 좌우되지 않게 하기 위함이다.
func LoadProfilesFrom(configPath, credentialsPath string) ([]Profile, error) {
	profiles := make(map[string]*Profile)

	configFound, err := readInto(profiles, configPath, SourceConfig)
	if err != nil {
		return nil, err
	}

	credentialsFound, err := readInto(profiles, credentialsPath, SourceCredentials)
	if err != nil {
		return nil, err
	}

	if !configFound && !credentialsFound {
		return nil, fmt.Errorf("%w (찾아본 위치: %s, %s)", ErrNoSharedConfig, configPath, credentialsPath)
	}

	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, *p)
	}

	slices.SortFunc(out, func(a, b Profile) int {
		if a.Name == "default" != (b.Name == "default") {
			if a.Name == "default" {
				return -1
			}

			return 1
		}

		return cmp.Compare(a.Name, b.Name)
	})

	return out, nil
}

// readInto는 파일 하나의 프로필을 acc에 병합하고, 그 파일이 존재했는지를 함께 알린다.
func readInto(acc map[string]*Profile, path string, source Source) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("%s 열기 실패: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sections, err := parseINI(f)
	if err != nil {
		return false, fmt.Errorf("%s 파싱 실패: %w", path, err)
	}

	for _, sec := range sections {
		name, ok := profileName(sec.name, source)
		if !ok {
			continue
		}

		p, exists := acc[name]
		if !exists {
			p = &Profile{Name: name}
			acc[name] = p
		}

		p.Source |= source
		applyProps(p, sec.props)
	}

	return true, nil
}

// profileName은 섹션 헤더를 프로필 이름으로 변환한다.
//
// 두 파일은 서로 다른 규칙을 쓰므로, 추측하지 않고 SDK의 규칙을 그대로 따른다.
// config에서는 "default"를 제외한 모든 프로필이 "profile " 접두사를 가져야 한다.
// 접두사 없는 섹션은 사용할 수 없는 프로필이므로, 인증되지 않을 프로필로 보여주는 대신
// 건너뛴다. "sso-session"이나 "services" 같은 섹션은 프로필이 아니라 설정 블록이다.
func profileName(section string, source Source) (string, bool) {
	if source == SourceCredentials {
		return section, true
	}

	if section == "default" {
		return section, true
	}

	rest, ok := cutPrefixWord(section, "profile")
	if !ok || rest == "" {
		return "", false
	}

	return rest, true
}

// cutPrefixWord는 앞 단어와 그 뒤의 공백을 잘라낸다. AWS가 복합 섹션 헤더를 쓰는 방식이
// 이것이다("profile prod", "sso-session org").
func cutPrefixWord(s, word string) (string, bool) {
	rest, ok := strings.CutPrefix(s, word)
	if !ok {
		return "", false
	}

	trimmed := strings.TrimLeft(rest, " \t")
	if trimmed == rest {
		// 단어 뒤에 공백이 없다면, 같은 글자로 시작하기만 하는 다른 헤더다.
		return "", false
	}

	return trimmed, true
}

func applyProps(p *Profile, props map[string]string) {
	setIfEmpty(&p.Region, props["region"])
	setIfEmpty(&p.RoleARN, props["role_arn"])
	setIfEmpty(&p.SourceProfile, props["source_profile"])
	setIfEmpty(&p.SSOSession, props["sso_session"])
	setIfEmpty(&p.SSOAccountID, props["sso_account_id"])
	setIfEmpty(&p.SSORoleName, props["sso_role_name"])
	setIfEmpty(&p.MFASerial, props["mfa_serial"])

	p.Kind = classify(p, props)
}

func setIfEmpty(dst *string, v string) {
	if *dst == "" && v != "" {
		*dst = v
	}
}

// classify는 프로필이 어떻게 인증하는지 판정한다.
//
// 순서가 중요하다. SSO 프로필도 역할 이름을 갖고 있고, assume-role 프로필은 정적 키를
// 가진 source_profile을 가리킬 수 있다. 따라서 가장 구체적인 신호가 이긴다.
func classify(p *Profile, props map[string]string) Kind {
	switch {
	case p.SSOSession != "" || (p.SSOAccountID != "" && p.SSORoleName != ""):
		return KindSSO
	case p.RoleARN != "":
		return KindAssumeRole
	case props["credential_process"] != "":
		return KindProcess
	case p.Source&SourceCredentials != 0:
		return KindStatic
	default:
		return KindUnknown
	}
}

// iniSection은 대괄호 블록 하나와 그 최상위 속성들이다.
type iniSection struct {
	name  string
	props map[string]string
}

// parseINI는 AWS 공유 설정이 사용하는 INI의 부분집합을 읽는다.
//
// 의존성을 가져오지 않고 직접 구현한 이유는 섹션 헤더와 비밀이 아닌 키 몇 개만 필요하기
// 때문이다. 특히 자격증명 값을 해석할 일이 전혀 없어서, 이 코드 경로에서 비밀을 완전히
// 배제할 수 있다.
//
// 들여쓴 줄은 중첩 하위 속성이다(`s3 =` 다음에 `  addressing_style = path`가 오는
// 형태). 그것들은 바로 위의 키에 속하므로 건너뛴다.
func parseINI(r io.Reader) ([]iniSection, error) {
	var sections []iniSection

	current := -1
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		raw := scanner.Text()

		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			name, ok := sectionName(line)
			if !ok {
				continue
			}

			sections = append(sections, iniSection{name: name, props: make(map[string]string)})
			current = len(sections) - 1

			continue
		}

		if current < 0 {
			// 섹션 헤더보다 앞에 나온 속성이다. 잘못된 형식이므로 무시한다.
			continue
		}

		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}

		sections[current].props[key] = strings.TrimSpace(value)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("읽기 실패: %w", err)
	}

	return sections, nil
}

// stripComment는 한 줄 전체가 주석인 경우를 제거한다. AWS는 값 뒤에 붙는 주석을
// 지원하지 않으므로 줄 앞의 표시만 인정하고, 값 안의 '#'은 그대로 보존한다.
func stripComment(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
		return ""
	}

	return raw
}

func sectionName(line string) (string, bool) {
	end := strings.Index(line, "]")
	if end < 0 {
		return "", false
	}

	name := strings.TrimSpace(line[1:end])
	if name == "" {
		return "", false
	}

	return name, true
}
