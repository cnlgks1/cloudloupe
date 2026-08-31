package awsclient_test

import (
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

func TestRegionsPutsDefaultFirst(t *testing.T) {
	t.Parallel()

	regions := awsclient.Regions("ap-northeast-2")

	if len(regions) == 0 {
		t.Fatal("리전 목록이 비었다")
	}

	if regions[0].Code != "ap-northeast-2" {
		t.Errorf("첫 리전 = %q, want ap-northeast-2 (기본값이 맨 앞)", regions[0].Code)
	}

	// 기본 리전이 목록에 중복으로 나타나면 안 된다.
	count := 0

	for _, r := range regions {
		if r.Code == "ap-northeast-2" {
			count++
		}
	}

	if count != 1 {
		t.Errorf("ap-northeast-2가 %d번 나타난다, want 1", count)
	}
}

func TestRegionsWithoutDefault(t *testing.T) {
	t.Parallel()

	regions := awsclient.Regions("")

	if len(regions) == 0 {
		t.Fatal("리전 목록이 비었다")
	}

	// 서울이 목록에 있어야 한다. 없으면 목록이 잘못된 것이다.
	found := false

	for _, r := range regions {
		if r.Code == "ap-northeast-2" && r.Name != "" {
			found = true
		}
	}

	if !found {
		t.Error("ap-northeast-2가 이름과 함께 목록에 있어야 한다")
	}
}

func TestRegionsUnknownDefaultStillFirst(t *testing.T) {
	t.Parallel()

	// 커스텀 엔드포인트나 새 리전처럼 정적 목록에 없는 기본 리전도 맨 앞에 나와야 한다.
	// 사용자가 그 리전을 실제로 쓰고 있는데 목록에서 빠지면 혼란스럽다.
	regions := awsclient.Regions("xx-unknown-1")

	if regions[0].Code != "xx-unknown-1" {
		t.Errorf("알 수 없는 기본 리전이 맨 앞이어야 한다, got %q", regions[0].Code)
	}
}
