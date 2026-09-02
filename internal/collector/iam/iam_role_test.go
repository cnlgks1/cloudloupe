package iam_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	iamcollector "github.com/cnlgks1/cloudloupe/internal/collector/iam"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeListRoles는 ListRoles를 대신한다. 테스트가 실제 AWS를 때리지 않게 하는 경계다.
//
// pages는 순서대로 돌려줄 응답이고 errs는 같은 순서의 오류다. 페이지네이터가 다음 요청을
// 보내는지 확인하려면 응답에 Marker를 채우고 IsTruncated를 true로 둔다.
type fakeListRoles struct {
	pages  []*awsiam.ListRolesOutput
	errs   []error
	calls  int
	marker []string
}

func (f *fakeListRoles) ListRoles(
	_ context.Context,
	in *awsiam.ListRolesInput,
	_ ...func(*awsiam.Options),
) (*awsiam.ListRolesOutput, error) {
	i := f.calls
	f.calls++
	f.marker = append(f.marker, aws.ToString(in.Marker))

	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i >= len(f.pages) {
		return &awsiam.ListRolesOutput{}, nil
	}

	return f.pages[i], nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestRoleCollectorType(t *testing.T) {
	t.Parallel()

	if got := iamcollector.NewRole(&fakeListRoles{}).Type(); got != model.TypeIAMRole {
		t.Errorf("Type() = %q, want %q", got, model.TypeIAMRole)
	}
}

// TestRoleCollectFollowsPages는 잘린 응답에서 다음 페이지를 이어 받는지 확인한다.
func TestRoleCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeListRoles{
		pages: []*awsiam.ListRolesOutput{
			{
				Roles:       []iamtypes.Role{{RoleName: aws.String("first"), Arn: aws.String("arn:aws:iam::123456789012:role/first")}},
				IsTruncated: true,
				Marker:      aws.String("page-2"),
			},
			{
				Roles: []iamtypes.Role{{RoleName: aws.String("second"), Arn: aws.String("arn:aws:iam::123456789012:role/second")}},
			},
		},
	}

	got, err := iamcollector.NewRole(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("역할 %d개 수집, want 2", len(got))
	}
	if api.calls != 2 {
		t.Errorf("ListRoles 호출 = %d회, want 2", api.calls)
	}
	if len(api.marker) < 2 || api.marker[1] != "page-2" {
		t.Errorf("두 번째 요청 Marker = %v, want page-2", api.marker)
	}
}

// TestRoleCollectKeepsPartialResults는 뒤 페이지가 실패해도 앞 페이지를 버리지 않는지
// 확인한다. 멀티 계정 조회에서 부분 실패는 전체 실패가 아니다.
func TestRoleCollectKeepsPartialResults(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("access denied")
	api := &fakeListRoles{
		pages: []*awsiam.ListRolesOutput{
			{
				Roles:       []iamtypes.Role{{RoleName: aws.String("first")}},
				IsTruncated: true,
				Marker:      aws.String("page-2"),
			},
			nil,
		},
		errs: []error{nil, wantErr},
	}

	got, err := iamcollector.NewRole(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "first" {
		t.Errorf("실패 전 수집분이 남아야 한다: %+v", got)
	}
	if !strings.Contains(err.Error(), "list roles") {
		t.Errorf("오류에 문맥이 없다: %v", err)
	}
}

// TestRoleToResourceMapsFields는 역할 하나가 도메인 리소스로 어떻게 옮겨지는지 고정한다.
func TestRoleToResourceMapsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	lastUsed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	api := &fakeListRoles{pages: []*awsiam.ListRolesOutput{{Roles: []iamtypes.Role{{
		RoleName:           aws.String("app-runtime"),
		RoleId:             aws.String("AROAEXAMPLE"),
		Arn:                aws.String("arn:aws:iam::123456789012:role/app-runtime"),
		Path:               aws.String("/service-role/"),
		Description:        aws.String("애플리케이션 런타임"),
		CreateDate:         &created,
		MaxSessionDuration: aws.Int32(3600),
		PermissionsBoundary: &iamtypes.AttachedPermissionsBoundary{
			PermissionsBoundaryArn: aws.String("arn:aws:iam::123456789012:policy/boundary"),
		},
		RoleLastUsed: &iamtypes.RoleLastUsed{LastUsedDate: &lastUsed, Region: aws.String("ap-northeast-2")},
		Tags:         []iamtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}}}}}

	got, err := iamcollector.NewRole(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("역할 %d개 수집, want 1", len(got))
	}

	role := got[0]
	if role.ID != "app-runtime" || role.Name != "app-runtime" {
		t.Errorf("ID/Name = %q/%q, want app-runtime", role.ID, role.Name)
	}
	// IAM은 글로벌 서비스이므로 선택한 리전이 아니라 global로 고정된다.
	if role.Region != model.RegionGlobal {
		t.Errorf("Region = %q, want %q", role.Region, model.RegionGlobal)
	}
	if role.CreatedAt == nil || !role.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", role.CreatedAt, created)
	}
	if got, want := role.Tag("env"), "prod"; got != want {
		t.Errorf("Tag(env) = %q, want %q", got, want)
	}

	for key, want := range map[string]string{
		"Path":                "/service-role/",
		"Description":         "애플리케이션 런타임",
		"MaxSessionDuration":  "3600",
		"PermissionsBoundary": "arn:aws:iam::123456789012:policy/boundary",
		"RoleLastUsed":        "2026-01-02T03:04:05Z (ap-northeast-2)",
		"RoleId":              "AROAEXAMPLE",
	} {
		if got := role.FieldValue(key); got != want {
			t.Errorf("FieldValue(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestRoleToResourceHandlesMissingOptionalValues는 ListRoles가 채우지 않는 값들을 안전하게
// 다루는지 확인한다. 태그·마지막 사용·권한 경계는 이 API 응답에 없을 수 있다.
func TestRoleToResourceHandlesMissingOptionalValues(t *testing.T) {
	t.Parallel()

	api := &fakeListRoles{pages: []*awsiam.ListRolesOutput{{Roles: []iamtypes.Role{{
		RoleName: aws.String("bare"),
	}}}}}

	got, err := iamcollector.NewRole(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}

	role := got[0]
	if role.CreatedAt != nil {
		t.Errorf("CreatedAt = %v, want nil", role.CreatedAt)
	}
	if len(role.Tags) != 0 {
		t.Errorf("Tags = %+v, want empty", role.Tags)
	}
	for _, key := range []string{"Path", "Description", "MaxSessionDuration", "PermissionsBoundary", "RoleLastUsed", "RoleId"} {
		if got := role.FieldValue(key); got != "-" {
			t.Errorf("FieldValue(%q) = %q, want %q", key, got, "-")
		}
	}
}

// TestRoleCollectStopsOnCanceledContext는 취소된 조회가 즉시 멈추는지 확인한다.
// TUI에서 esc로 수집을 끊을 수 있어야 한다.
func TestRoleCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeListRoles{errs: []error{context.Canceled}}
	if _, err := iamcollector.NewRole(api).Collect(ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
