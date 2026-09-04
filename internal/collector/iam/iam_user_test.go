package iam_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	iamcollector "github.com/cnlgks1/cloudloupe/internal/collector/iam"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeListUsers는 ListUsers를 대신한다.
type fakeListUsers struct {
	pages []*awsiam.ListUsersOutput
	errs  []error
	calls int
}

func (f *fakeListUsers) ListUsers(
	_ context.Context,
	_ *awsiam.ListUsersInput,
	_ ...func(*awsiam.Options),
) (*awsiam.ListUsersOutput, error) {
	i := f.calls
	f.calls++

	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i >= len(f.pages) {
		return &awsiam.ListUsersOutput{}, nil
	}

	return f.pages[i], nil
}

func TestUserCollectorType(t *testing.T) {
	t.Parallel()

	if got := iamcollector.NewUser(&fakeListUsers{}).Type(); got != model.TypeIAMUser {
		t.Errorf("Type() = %q, want %q", got, model.TypeIAMUser)
	}
}

// TestUserCollectConvertsFields는 값을 그대로 담고 글로벌 리전으로 고정하며 태그를
// 정렬하는지 확인한다.
func TestUserCollectConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	lastUsed := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	api := &fakeListUsers{
		pages: []*awsiam.ListUsersOutput{{
			Users: []iamtypes.User{{
				UserName:         aws.String("deploy"),
				UserId:           aws.String("AIDAEXAMPLE"),
				Arn:              aws.String("arn:aws:iam::123456789012:user/deploy"),
				Path:             aws.String("/"),
				CreateDate:       &created,
				PasswordLastUsed: &lastUsed,
				Tags: []iamtypes.Tag{
					{Key: aws.String("team"), Value: aws.String("platform")},
				},
			}},
		}},
	}

	got, err := iamcollector.NewUser(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("사용자 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "deploy" || res.Name != "deploy" {
		t.Errorf("ID/Name = %q/%q", res.ID, res.Name)
	}
	if res.Region != model.RegionGlobal {
		t.Errorf("Region = %q, want %q", res.Region, model.RegionGlobal)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	if got, want := res.FieldValue("PasswordLastUsed"), "2025-06-01T00:00:00Z"; got != want {
		t.Errorf("PasswordLastUsed = %q, want %q", got, want)
	}
	if got, want := res.Tag("team"), "platform"; got != want {
		t.Errorf("team 태그 = %q, want %q", got, want)
	}
}

// TestUserCollectDistinguishesNeverUsedPassword는 콘솔 로그인 기록이 없으면 "-"로 두는지
// 확인한다. 미사용 사용자 판정의 근거가 된다.
func TestUserCollectDistinguishesNeverUsedPassword(t *testing.T) {
	t.Parallel()

	api := &fakeListUsers{
		pages: []*awsiam.ListUsersOutput{{
			Users: []iamtypes.User{{UserName: aws.String("svc"), Arn: aws.String("arn:x")}},
		}},
	}

	got, err := iamcollector.NewUser(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if v := got[0].FieldValue("PasswordLastUsed"); v != "-" {
		t.Errorf("로그인 기록 없음 = %q, want -", v)
	}
}

// TestUserCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestUserCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeListUsers{
		pages: []*awsiam.ListUsersOutput{{
			Users: []iamtypes.User{{UserName: aws.String("a"), Arn: aws.String("arn:a")}},
		}},
		errs: []error{nil, denied},
	}
	// 첫 페이지가 잘렸다고 알려 두 번째 호출을 유도한다.
	api.pages[0].IsTruncated = true
	api.pages[0].Marker = aws.String("next")

	got, err := iamcollector.NewUser(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}

	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
}
