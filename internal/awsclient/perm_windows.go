//go:build windows

package awsclient

import "io/fs"

// tooPermissive는 Windows에서 항상 false를 반환한다.
//
// Windows의 접근 제어는 ACL로 이루어지고 Go가 보고하는 Unix 스타일 권한 비트는 실제
// 권한을 반영하지 않는다. 그 값으로 경고하면 언제나 틀린 경고가 된다. ACL을 제대로 읽으려면
// golang.org/x/sys/windows 의 보안 API가 필요한데, 경고 하나를 위해 의존성을 늘릴 일은 아니다.
func tooPermissive(_ fs.FileMode) bool {
	return false
}
