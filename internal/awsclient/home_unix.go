//go:build !windows

package awsclient

import "os"

// homeDir는 사용자 홈 디렉터리를 반환한다.
//
// Unix에서는 os.UserHomeDir가 $HOME을 읽으며, 그것이 AWS CLI의 동작과 같다.
//
// runtime.GOOS 분기 대신 파일 이름 접미사로 플랫폼을 나눈 이유는, 각 플랫폼에서 실제로
// 컴파일되는 코드만 남아 잘못된 분기가 조용히 살아 있을 수 없기 때문이다.
func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err //nolint:wrapcheck // 호출자가 문맥을 붙여 감싼다
	}

	return home, nil
}
