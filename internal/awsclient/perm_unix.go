//go:build !windows

package awsclient

import "io/fs"

// tooPermissive는 파일이 소유자 외의 사용자에게 열려 있는지 판단한다.
//
// group과 other의 비트가 하나라도 있으면 참이다. 자격증명 파일에 기대하는 권한은 0600이다.
func tooPermissive(mode fs.FileMode) bool {
	return mode.Perm()&0o077 != 0
}
