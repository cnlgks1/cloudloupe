package iam

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// listUsersAPI는 사용자 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type listUsersAPI interface {
	ListUsers(context.Context, *awsiam.ListUsersInput, ...func(*awsiam.Options)) (*awsiam.ListUsersOutput, error)
}

// userCollector는 IAM 사용자를 조회한다.
type userCollector struct {
	api listUsersAPI
}

// NewUser는 IAM 사용자 수집기를 만든다.
func NewUser(api listUsersAPI) collect.Collector {
	return userCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c userCollector) Type() string { return model.TypeIAMUser }

// Collect는 계정의 IAM 사용자를 모두 조회해 도메인 리소스로 변환한다.
//
// 페이지 조회가 중간에 실패하면 그때까지 모은 사용자와 오류를 함께 반환한다.
func (c userCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsiam.NewListUsersPaginator(c.api, &awsiam.ListUsersInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("list users: %w", err)
		}

		for i := range page.Users {
			out = append(out, userToResource(req.Scope, page.Users[i]))
		}
	}

	return out, nil
}

// userToResource는 SDK 사용자를 도메인 리소스로 변환한다.
//
// ID로 사용자 이름을 쓴다. PasswordLastUsed는 나중에 미사용 후보 판정의 근거가 된다.
// ListUsers 응답에는 태그가 채워지지 않을 수 있으나(계정 설정에 따라) 있으면 담는다.
func userToResource(scope collect.Scope, user iamtypes.User) model.Resource {
	r := model.Resource{
		Type:      model.TypeIAMUser,
		ID:        aws.ToString(user.UserName),
		Name:      aws.ToString(user.UserName),
		ARN:       aws.ToString(user.Arn),
		Region:    model.RegionGlobal,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "Path", Value: orDash(aws.ToString(user.Path))},
			{Key: "PasswordLastUsed", Value: dateValue(user.PasswordLastUsed)},
			{Key: "PermissionsBoundary", Value: permissionsBoundary(user.PermissionsBoundary)},
			{Key: "UserId", Value: orDash(aws.ToString(user.UserId))},
		},
		Tags: iamTags(user.Tags),
	}

	if user.CreateDate != nil {
		createdAt := user.CreateDate.UTC()
		r.CreatedAt = &createdAt
	}

	return r
}

// dateValue는 선택적인 시각을 RFC 3339로 표시한다. nil이면 "-"다.
func dateValue(t *time.Time) string {
	if t == nil {
		return "-"
	}

	return t.UTC().Format(time.RFC3339)
}
