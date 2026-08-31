package ec2

import (
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// 이 파일은 EC2 계열 수집기(instance/volume/networkInterface/address)가 함께 쓰는 작은
// 변환 헬퍼를 모은다. 같은 서비스의 수집기가 여럿이므로 한곳에 두어 중복을 없앤다.

// ec2Tags는 SDK 태그 슬라이스를 정렬된 도메인 필드로 바꾼다.
func ec2Tags(tags []ec2types.Tag) []model.Field {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return model.TagFields(m)
}

// tagValue는 SDK 태그 슬라이스에서 특정 키의 값을 찾는다.
func tagValue(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}

	return ""
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// itoa32는 int32를 문자열로 바꾼다. SDK 수치 필드는 대개 *int32라 aws.ToInt32로 값을
// 꺼낸 뒤 이 함수로 표시한다.
func itoa32(n int32) string {
	return strconv.Itoa(int(n))
}

// yesNo는 불리언을 사람이 읽는 한국어로 바꾼다.
func yesNo(b bool) string {
	if b {
		return "예"
	}

	return "아니오"
}
