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

// fakeListGroups는 ListGroups를 대신한다.
type fakeListGroups struct {
	pages []*awsiam.ListGroupsOutput
	errs  []error
	calls int
}

func (f *fakeListGroups) ListGroups(
	_ context.Context,
	_ *awsiam.ListGroupsInput,
	_ ...func(*awsiam.Options),
) (*awsiam.ListGroupsOutput, error) {
	i := f.calls
	f.calls++

	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i >= len(f.pages) {
		return &awsiam.ListGroupsOutput{}, nil
	}

	return f.pages[i], nil
}

func TestGroupCollectorType(t *testing.T) {
	t.Parallel()

	if got := iamcollector.NewGroup(&fakeListGroups{}).Type(); got != model.TypeIAMGroup {
		t.Errorf("Type() = %q, want %q", got, model.TypeIAMGroup)
	}
}

// TestGroupCollectConvertsFields는 값을 그대로 담고 글로벌 리전으로 고정하는지 확인한다.
func TestGroupCollectConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	api := &fakeListGroups{
		pages: []*awsiam.ListGroupsOutput{{
			Groups: []iamtypes.Group{{
				GroupName:  aws.String("developers"),
				GroupId:    aws.String("AGPAEXAMPLE"),
				Arn:        aws.String("arn:aws:iam::123456789012:group/developers"),
				Path:       aws.String("/"),
				CreateDate: &created,
			}},
		}},
	}

	got, err := iamcollector.NewGroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("그룹 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "developers" || res.ARN != "arn:aws:iam::123456789012:group/developers" {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Region != model.RegionGlobal {
		t.Errorf("Region = %q, want %q", res.Region, model.RegionGlobal)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	if got, want := res.FieldValue("GroupId"), "AGPAEXAMPLE"; got != want {
		t.Errorf("GroupId = %q, want %q", got, want)
	}
}

// TestGroupCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestGroupCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeListGroups{
		pages: []*awsiam.ListGroupsOutput{{
			Groups: []iamtypes.Group{{GroupName: aws.String("a"), Arn: aws.String("arn:a")}},
		}},
		errs: []error{nil, denied},
	}
	api.pages[0].IsTruncated = true
	api.pages[0].Marker = aws.String("next")

	got, err := iamcollector.NewGroup(api).Collect(context.Background(), collect.Request{Scope: testScope()})
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
