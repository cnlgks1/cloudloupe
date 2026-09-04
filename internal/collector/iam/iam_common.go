package iam

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// iamTags는 SDK 태그를 키 순으로 정렬된 표시 필드로 바꾼다.
func iamTags(tags []iamtypes.Tag) []model.Field {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return model.TagFields(m)
}

// permissionsBoundary는 권한 경계 정책 ARN을 표시 값으로 바꾼다.
//
// 역할·사용자 모두 권한 경계를 가질 수 있어 공용으로 둔다.
func permissionsBoundary(boundary *iamtypes.AttachedPermissionsBoundary) string {
	if boundary == nil {
		return "-"
	}

	return orDash(aws.ToString(boundary.PermissionsBoundaryArn))
}
