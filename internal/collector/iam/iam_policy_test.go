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

// fakeListPolicies는 ListPolicies를 대신한다. lastScope로 고객 정책만 요청하는지 확인한다.
type fakeListPolicies struct {
	pages     []*awsiam.ListPoliciesOutput
	errs      []error
	calls     int
	lastScope iamtypes.PolicyScopeType
}

func (f *fakeListPolicies) ListPolicies(
	_ context.Context,
	in *awsiam.ListPoliciesInput,
	_ ...func(*awsiam.Options),
) (*awsiam.ListPoliciesOutput, error) {
	f.lastScope = in.Scope

	i := f.calls
	f.calls++

	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i >= len(f.pages) {
		return &awsiam.ListPoliciesOutput{}, nil
	}

	return f.pages[i], nil
}

func TestPolicyCollectorType(t *testing.T) {
	t.Parallel()

	if got := iamcollector.NewPolicy(&fakeListPolicies{}).Type(); got != model.TypeIAMPolicy {
		t.Errorf("Type() = %q, want %q", got, model.TypeIAMPolicy)
	}
}

// TestPolicyCollectRequestsLocalScope는 고객 관리형(Local) 정책만 요청하는지 확인한다.
// AWS 관리형 정책은 수백 개라 목록을 도배하고 조사 가치가 낮다.
func TestPolicyCollectRequestsLocalScope(t *testing.T) {
	t.Parallel()

	api := &fakeListPolicies{}
	if _, err := iamcollector.NewPolicy(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if api.lastScope != iamtypes.PolicyScopeTypeLocal {
		t.Errorf("Scope = %q, want Local", api.lastScope)
	}
}

// TestPolicyCollectConvertsFields는 값을 그대로 담고 미사용(AttachmentCount 0)을 드러내는지
// 확인한다.
func TestPolicyCollectConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	api := &fakeListPolicies{
		pages: []*awsiam.ListPoliciesOutput{{
			Policies: []iamtypes.Policy{{
				PolicyName:       aws.String("app-read"),
				PolicyId:         aws.String("ANPAEXAMPLE"),
				Arn:              aws.String("arn:aws:iam::123456789012:policy/app-read"),
				Path:             aws.String("/"),
				Description:      aws.String("앱 읽기 권한"),
				AttachmentCount:  aws.Int32(0),
				IsAttachable:     true,
				DefaultVersionId: aws.String("v3"),
				CreateDate:       &created,
			}},
		}},
	}

	got, err := iamcollector.NewPolicy(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("정책 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "app-read" || res.ARN != "arn:aws:iam::123456789012:policy/app-read" {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	if res.Region != model.RegionGlobal {
		t.Errorf("Region = %q, want %q", res.Region, model.RegionGlobal)
	}
	// 어디에도 안 붙은 정책은 미사용 후보다.
	if got, want := res.FieldValue("AttachmentCount"), "0"; got != want {
		t.Errorf("AttachmentCount = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("IsAttachable"), "true"; got != want {
		t.Errorf("IsAttachable = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("DefaultVersionId"), "v3"; got != want {
		t.Errorf("DefaultVersionId = %q, want %q", got, want)
	}
}

// TestPolicyCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestPolicyCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeListPolicies{
		pages: []*awsiam.ListPoliciesOutput{{
			Policies: []iamtypes.Policy{{PolicyName: aws.String("a"), Arn: aws.String("arn:a")}},
		}},
		errs: []error{nil, denied},
	}
	api.pages[0].IsTruncated = true
	api.pages[0].Marker = aws.String("next")

	got, err := iamcollector.NewPolicy(api).Collect(context.Background(), collect.Request{Scope: testScope()})
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
