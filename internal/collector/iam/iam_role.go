// Package iam은 IAM 리소스를 조회해 도메인 모델로 바꾼다.
//
// IAM은 리전 개념이 없는 글로벌 서비스다. 그래서 이 패키지가 만드는 리소스의 Region은
// [model.RegionGlobal]로 고정하고, 카탈로그에서도 Global 범위로 등록해 선택한 리전 수와
// 무관하게 한 번만 조회한다.
//
// IAM 수집기를 더 추가할 때는 이 파일을 본떠 파일 하나에 수집기 하나를 둔다. 공용 변환
// 헬퍼가 필요해지면 ec2 패키지의 ec2_common.go처럼 iam_common.go로 옮긴다. 지금은 수집기가
// 하나뿐이라 여기에 함께 둔다.
package iam

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listRolesAPI는 역할 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 클라이언트 전체가 아니라 이 메서드 하나만 받으므로 자격증명 없이 단위 테스트를 할 수 있다.
type listRolesAPI interface {
	ListRoles(context.Context, *awsiam.ListRolesInput, ...func(*awsiam.Options)) (*awsiam.ListRolesOutput, error)
}

// roleCollector는 IAM 역할을 조회한다.
type roleCollector struct {
	api listRolesAPI
}

// NewRole은 IAM 역할 수집기를 만든다.
func NewRole(api listRolesAPI) collect.Collector {
	return roleCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c roleCollector) Type() string { return model.TypeIAMRole }

// Collect는 계정의 IAM 역할을 모두 조회해 도메인 리소스로 변환한다.
//
// 페이지 조회가 중간에 실패하면 그때까지 모은 역할과 오류를 함께 반환한다. Runner가 둘을
// 모두 보존하므로 권한이 부족해도 얻은 만큼은 화면에 남는다.
func (c roleCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsiam.NewListRolesPaginator(c.api, &awsiam.ListRolesInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list roles: %w", err)
		}

		for i := range page.Roles {
			out = append(out, roleToResource(req.Scope, page.Roles[i]))
		}
	}

	return out, nil
}

// roleToResource는 SDK 역할을 도메인 리소스로 변환한다.
//
// ID로 역할 이름을 쓴다. 계정 안에서 유일하고 사람이 읽을 수 있으며, 신뢰 정책과 콘솔에서도
// 이 이름으로 참조된다. RoleId(AROA...)는 표시 필드로만 남긴다.
//
// ListRoles 응답에는 태그, 마지막 사용 시각, 신뢰 정책 문서가 채워지지 않는다. 그 값은 역할
// 하나씩 GetRole·ListRoleTags를 부르는 N+1 조회가 필요하다. 역할이 수백 개인 계정에서
// 스로틀링을 부르므로 지금은 넣지 않는다. 넣을 때는 동시 실행 수를 제한해야 한다
// (설계 원칙 6). 그래서 아래 코드는 이 필드들이 비어 있어도 안전하게 동작한다.
func roleToResource(scope collect.Scope, role iamtypes.Role) model.Resource {
	r := model.Resource{
		Type:      model.TypeIAMRole,
		ID:        aws.ToString(role.RoleName),
		Name:      aws.ToString(role.RoleName),
		ARN:       aws.ToString(role.Arn),
		Region:    model.RegionGlobal,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Path", Value: orDash(aws.ToString(role.Path))},
			{Key: "Description", Value: orDash(aws.ToString(role.Description))},
			{Key: "MaxSessionDuration", Value: sessionSeconds(role.MaxSessionDuration)},
			{Key: "PermissionsBoundary", Value: permissionsBoundary(role.PermissionsBoundary)},
			{Key: "RoleLastUsed", Value: lastUsed(role.RoleLastUsed)},
			{Key: "RoleId", Value: orDash(aws.ToString(role.RoleId))},
		},
		Tags: iamTags(role.Tags),
	}

	if role.CreateDate != nil {
		createdAt := role.CreateDate.UTC()
		r.CreatedAt = &createdAt
	}

	// 역할은 인스턴스 프로필과 연결 정책으로 이어지지만, 그 대상은 아직 수집하지 않는다.
	// 관계를 만들면 존재하지 않는 노드를 가리키는 간선이 되므로 지금은 남기지 않는다.

	return r
}

// sessionSeconds는 최대 세션 시간을 API가 준 초 단위 그대로 표시한다.
//
// 시간으로 환산하지 않는 이유는 화면 값이 aws CLI 출력과 같아야 하기 때문이다. AWS는
// MaxSessionDuration을 초로 주고 CLI도 초로 찍는다.
func sessionSeconds(seconds *int32) string {
	value := aws.ToInt32(seconds)
	if value <= 0 {
		return "-"
	}

	return strconv.Itoa(int(value))
}

// permissionsBoundary는 권한 경계 정책 ARN을 표시 값으로 바꾼다.
func permissionsBoundary(boundary *iamtypes.AttachedPermissionsBoundary) string {
	if boundary == nil {
		return "-"
	}

	return orDash(aws.ToString(boundary.PermissionsBoundaryArn))
}

// lastUsed는 마지막 사용 시각과 그때의 리전을 한 값으로 만든다.
//
// 이 값은 로드맵 4단계의 미사용 후보 판정에 쓸 근거이기도 하다. 다만 IAM은 최근 400일만
// 추적하므로 "기록 없음"이 곧 "쓰지 않는 역할"은 아니다.
func lastUsed(used *iamtypes.RoleLastUsed) string {
	if used == nil || used.LastUsedDate == nil {
		return "-"
	}

	value := used.LastUsedDate.UTC().Format(time.RFC3339)
	if region := aws.ToString(used.Region); region != "" {
		value += " (" + region + ")"
	}

	return value
}

// iamTags는 SDK 태그를 키 순으로 정렬된 표시 필드로 바꾼다.
func iamTags(tags []iamtypes.Tag) []model.Field {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return model.TagFields(m)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
//
// 다른 수집기 패키지에도 같은 함수가 있다. 패키지 하나를 더 만들어 의존을 늘리기보다 세 줄을
// 복사하는 편이 낫다("약간의 복사가 약간의 의존보다 낫다").
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
