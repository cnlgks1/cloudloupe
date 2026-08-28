package awsclient_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

const (
	testConfig      = "testdata/config"
	testCredentials = "testdata/credentials"
)

func load(t *testing.T) []awsclient.Profile {
	t.Helper()

	profiles, err := awsclient.LoadProfilesFrom(testConfig, testCredentials)
	if err != nil {
		t.Fatalf("LoadProfilesFrom: %v", err)
	}

	return profiles
}

func find(t *testing.T, profiles []awsclient.Profile, name string) awsclient.Profile {
	t.Helper()

	for _, p := range profiles {
		if p.Name == name {
			return p
		}
	}

	t.Fatalf("프로필 %q를 찾을 수 없다. 발견된 목록: %v", name, names(profiles))

	return awsclient.Profile{}
}

func names(profiles []awsclient.Profile) []string {
	out := make([]string, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p.Name)
	}

	return out
}

func TestLoadProfilesFromDiscoversBothFiles(t *testing.T) {
	t.Parallel()

	got := names(load(t))

	// "default"가 먼저, 나머지는 이름순. 파일에 섹션이 어떤 순서로 적혀 있든 결과 순서는
	// 달라지지 않아야 한다.
	want := []string{
		"default",
		"audit-a",
		"audit-b",
		"from-process",
		"legacy-keys",
		"nested-props",
		"prod",
		"staging",
		"temporary-session",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("프로필 이름 =\n  %v\nwant\n  %v", got, want)
	}
}

func TestLoadProfilesFromClassifiesKinds(t *testing.T) {
	t.Parallel()

	profiles := load(t)

	tests := map[string]awsclient.Kind{
		"prod":              awsclient.KindSSO,
		"staging":           awsclient.KindSSO,
		"audit-a":           awsclient.KindAssumeRole,
		"audit-b":           awsclient.KindAssumeRole,
		"from-process":      awsclient.KindProcess,
		"legacy-keys":       awsclient.KindStatic,
		"temporary-session": awsclient.KindStatic,
		"nested-props":      awsclient.KindUnknown,
	}

	for name, want := range tests {
		if got := find(t, profiles, name).Kind; got != want {
			t.Errorf("프로필 %q의 kind = %q, want %q", name, got, want)
		}
	}
}

func TestLoadProfilesFromMergesProfileDeclaredInBothFiles(t *testing.T) {
	t.Parallel()

	// "default"는 config에(리전과 함께) 그리고 credentials에(키와 함께) 모두 나온다.
	// 두 개가 아니라 하나의 프로필이다.
	def := find(t, load(t), "default")

	if def.Region != "ap-northeast-2" {
		t.Errorf("default의 리전 = %q, want ap-northeast-2", def.Region)
	}

	if def.Source != awsclient.SourceConfig|awsclient.SourceCredentials {
		t.Errorf("default의 출처 = %v, want config+credentials", def.Source)
	}

	if got := def.Source.String(); got != "config+credentials" {
		t.Errorf("Source.String() = %q, want config+credentials", got)
	}
}

func TestLoadProfilesFromReadsProfileDetails(t *testing.T) {
	t.Parallel()

	profiles := load(t)

	prod := find(t, profiles, "prod")
	if prod.SSOSession != "my-org" || prod.SSOAccountID != "123456789012" {
		t.Errorf("prod의 sso 필드 = %q/%q, want my-org/123456789012", prod.SSOSession, prod.SSOAccountID)
	}

	auditA := find(t, profiles, "audit-a")
	if auditA.RoleARN != "arn:aws:iam::555566667777:role/cloudloupe-readonly" {
		t.Errorf("audit-a의 role_arn = %q", auditA.RoleARN)
	}

	if auditA.SourceProfile != "default" {
		t.Errorf("audit-a의 source_profile = %q, want default", auditA.SourceProfile)
	}

	auditB := find(t, profiles, "audit-b")
	if auditB.MFASerial != "arn:aws:iam::111122223333:mfa/alice" {
		t.Errorf("audit-b의 mfa_serial = %q", auditB.MFASerial)
	}

	// 리전이 없는 프로필은 추측하지 않고 없는 그대로 보고한다. 호출자가 물어본다.
	if auditB.Region != "" {
		t.Errorf("audit-b의 리전 = %q, want 빈 문자열", auditB.Region)
	}
}

func TestLoadProfilesFromIgnoresNonProfileSections(t *testing.T) {
	t.Parallel()

	got := names(load(t))

	// sso-session과 services 블록은 프로필이 아니라 설정이다. config 파일의 접두사 없는
	// 섹션은 사용할 수 없는 프로필이다. 그리고 글자만 "profile"로 시작하는 헤더는 전혀
	// 다른 섹션이다.
	for _, unwanted := range []string{
		"my-org",
		"sso-session my-org",
		"my-services",
		"services my-services",
		"bare-section-should-be-ignored",
		"profiler-settings",
		"r-settings",
	} {
		for _, name := range got {
			if name == unwanted {
				t.Errorf("섹션 %q를 프로필로 취급해서는 안 된다. got %v", unwanted, got)
			}
		}
	}
}

func TestLoadProfilesFromSkipsNestedSubProperties(t *testing.T) {
	t.Parallel()

	// `s3 =` 다음에 오는 들여쓴 키들이 섹션 자신의 속성을 덮어쓰거나 엉뚱한 필드로
	// 새어 들어가면 안 된다.
	nested := find(t, load(t), "nested-props")

	if nested.Region != "ap-southeast-1" {
		t.Errorf("nested-props의 리전 = %q, want ap-southeast-1", nested.Region)
	}
}

func TestProfilesNeverCarryCredentialValues(t *testing.T) {
	t.Parallel()

	// 조회 전용·비밀 미보관 약속에 대한 회귀 방어선이다. credentials 파일을 파싱하면서
	// 어떤 비밀값도 반환 데이터로 복사되어서는 안 된다. 이 데이터는 TUI에 그려지고
	// 버그 리포트에 붙여넣어질 수 있다.
	secrets := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AKIAI44QH8DHBEXAMPLE",
		"je7MtGbClwBF/2Zp9Utk/h3yCo8nvbEXAMPLEKEY",
		"ASIAIOSFODNN7EXAMPLE",
		"IQoJb3JpZ2luX2VjEXAMPLETOKENDOESNOTWORK",
	}

	for _, p := range load(t) {
		rendered := renderAllFields(p)

		for _, secret := range secrets {
			if strings.Contains(rendered, secret) {
				t.Errorf("프로필 %q가 픽스처의 자격증명 값을 노출한다: %q", p.Name, secret)
			}
		}
	}
}

// renderAllFields는 리플렉션으로 모든 문자열 필드를 이어붙인다. 덕분에 필드를 새로
// 추가해도 테스트를 고치지 않고 비밀 검사에 자동으로 포함된다.
func renderAllFields(p awsclient.Profile) string {
	var sb strings.Builder

	v := reflect.ValueOf(p)
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.String {
			sb.WriteString(f.String())
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}

func TestLoadProfilesFromToleratesMissingFile(t *testing.T) {
	t.Parallel()

	// 두 파일 중 하나만 있는 설정이 흔하다.
	t.Run("config만 있음", func(t *testing.T) {
		t.Parallel()

		profiles, err := awsclient.LoadProfilesFrom(testConfig, filepath.Join(t.TempDir(), "absent"))
		if err != nil {
			t.Fatalf("LoadProfilesFrom: %v", err)
		}

		if len(profiles) == 0 {
			t.Fatal("config 파일만으로도 프로필이 나와야 한다")
		}

		if find(t, profiles, "default").Source != awsclient.SourceConfig {
			t.Error("default의 출처는 config뿐이어야 한다")
		}
	})

	t.Run("credentials만 있음", func(t *testing.T) {
		t.Parallel()

		profiles, err := awsclient.LoadProfilesFrom(filepath.Join(t.TempDir(), "absent"), testCredentials)
		if err != nil {
			t.Fatalf("LoadProfilesFrom: %v", err)
		}

		if find(t, profiles, "legacy-keys").Kind != awsclient.KindStatic {
			t.Error("legacy-keys는 static으로 분류되어야 한다")
		}
	})
}

func TestLoadProfilesFromReportsNoSharedConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := awsclient.LoadProfilesFrom(filepath.Join(dir, "config"), filepath.Join(dir, "credentials"))
	if !errors.Is(err, awsclient.ErrNoSharedConfig) {
		t.Fatalf("에러 = %v, want ErrNoSharedConfig", err)
	}

	// 처음 쓰는 사용자가 어디에 파일을 만들어야 하는지 알 수 있도록, 메시지가 찾아본
	// 경로를 밝혀야 한다.
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("에러 %q는 찾아본 경로를 알려줘야 한다", err)
	}
}

func TestLoadProfilesFromRejectsUnreadableFile(t *testing.T) {
	t.Parallel()

	// 존재하지만 읽을 수 없는 파일은 진짜 에러이고, 파일이 없는 경우와 구별되며 조용히
	// 삼켜져서는 안 된다.
	if os.Getuid() == 0 {
		t.Skip("root로 실행 중이라 권한 비트가 적용되지 않는다")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	if err := os.WriteFile(path, []byte("[default]\nregion = us-east-1\n"), 0o200); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := awsclient.LoadProfilesFrom(path, filepath.Join(dir, "credentials")); err == nil {
		t.Error("읽을 수 없는 config 파일에는 에러가 나야 한다")
	}
}
