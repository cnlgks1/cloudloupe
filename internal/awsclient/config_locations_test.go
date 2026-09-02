package awsclient_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

// writeSharedConfig는 테스트용 공유 설정 파일을 만든다.
func writeSharedConfig(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("%s 작성: %v", path, err)
	}
}

// TestConfigWithLocationsUsesGivenFiles는 지정한 설정 파일이 실제 AWS SDK 설정까지
// 전달되는지 확인한다.
//
// 프로필 탐색만 지정 경로를 쓰고 STS와 수집기는 기본 경로를 쓰던 회귀가 있었다. 그때는
// 프로필 목록에 보이는 프로필로 조회하면 "설정을 찾을 수 없다"는 오류가 났다. 화면에
// 보이는 것과 실제 조회 대상이 갈리는 종류의 버그라 사용자가 원인을 알 수 없다.
//
// 환경 변수는 일부러 존재하지 않는 경로로 덮어써서, SDK가 기본 탐색으로 돌아가면 값을
// 읽지 못하고 실패하게 만든다. 네트워크 호출은 하지 않는다.
func TestConfigWithLocationsUsesGivenFiles(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	credentialsPath := filepath.Join(dir, "credentials")

	writeSharedConfig(t, configPath, "[profile investigate]\nregion = eu-west-1\n")
	writeSharedConfig(t, credentialsPath, "[investigate]\n"+
		"aws_access_key_id = AKIAIOSFODNN7EXAMPLE\n"+
		"aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n")

	missing := filepath.Join(dir, "does-not-exist")
	t.Setenv(awsclient.EnvConfigFile, missing)
	t.Setenv(awsclient.EnvCredentialsFile, missing)

	locations, err := awsclient.ResolveWith(awsclient.Override{
		ConfigPath:      configPath,
		CredentialsPath: credentialsPath,
	})
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}

	t.Run("프로필의 리전을 지정 파일에서 읽는다", func(t *testing.T) {
		cfg, err := awsclient.ConfigWithLocations(context.Background(), "investigate", "", locations)
		if err != nil {
			t.Fatalf("ConfigWithLocations: %v", err)
		}

		if got, want := cfg.Region, "eu-west-1"; got != want {
			t.Errorf("Region = %q, want %q", got, want)
		}

		credentials, err := cfg.Credentials.Retrieve(context.Background())
		if err != nil {
			t.Fatalf("자격증명 조회: %v", err)
		}

		if got, want := credentials.AccessKeyID, "AKIAIOSFODNN7EXAMPLE"; got != want {
			t.Errorf("AccessKeyID = %q, want %q", got, want)
		}
	})

	// 수집은 리전마다 설정을 새로 만든다. 선택한 리전이 프로필의 기본 리전을 덮어쓰면서도
	// 파일 경로는 그대로 유지되어야 한다.
	t.Run("선택 리전이 프로필 기본 리전을 덮어쓴다", func(t *testing.T) {
		cfg, err := awsclient.ConfigWithLocations(
			context.Background(), "investigate", "ap-northeast-2", locations)
		if err != nil {
			t.Fatalf("ConfigWithLocations: %v", err)
		}

		if got, want := cfg.Region, "ap-northeast-2"; got != want {
			t.Errorf("Region = %q, want %q", got, want)
		}

		if _, err := cfg.Credentials.Retrieve(context.Background()); err != nil {
			t.Errorf("리전을 바꾼 뒤 자격증명 조회 실패: %v", err)
		}
	})
}

// TestConfigIgnoresLocationsWhenUnset은 Locations 제로 값이 SDK 기본 탐색을 막지 않는지
// 확인한다.
//
// 헤드리스 경로와 기존 Config 호출자는 경로를 지정하지 않는다. 이때 빈 경로가 SDK에
// 전달되면 기본 탐색이 깨진다.
func TestConfigIgnoresLocationsWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	writeSharedConfig(t, configPath, "[profile from-env]\nregion = us-east-2\n")

	t.Setenv(awsclient.EnvConfigFile, configPath)
	t.Setenv(awsclient.EnvCredentialsFile, filepath.Join(dir, "does-not-exist"))

	cfg, err := awsclient.ConfigWithLocations(
		context.Background(), "from-env", "", awsclient.Locations{})
	if err != nil {
		t.Fatalf("ConfigWithLocations: %v", err)
	}

	if got, want := cfg.Region, "us-east-2"; got != want {
		t.Errorf("Region = %q, want %q", got, want)
	}
}
