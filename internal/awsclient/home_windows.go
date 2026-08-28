//go:build windows

package awsclient

import (
	"errors"
	"os"
	"strings"
)

// homeDir는 사용자 홈 디렉터리를 반환한다.
//
// Windows에서 os.UserHomeDir는 %USERPROFILE%을 읽는다. 그 변수가 비어 있는 환경도
// 있으므로, AWS SDK와 마찬가지로 %HOMEDRIVE%%HOMEPATH% 조합으로 한 번 더 시도한다.
// 이 대체 경로가 없으면 일부 서비스 계정이나 축소된 셸 환경에서 프로필을 못 찾는다.
func homeDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home, nil
	}

	drive := strings.TrimSpace(os.Getenv("HOMEDRIVE"))
	path := strings.TrimSpace(os.Getenv("HOMEPATH"))

	if drive != "" && path != "" {
		return drive + path, nil
	}

	return "", errors.New("USERPROFILE도 HOMEDRIVE+HOMEPATH도 설정되지 않았습니다")
}
