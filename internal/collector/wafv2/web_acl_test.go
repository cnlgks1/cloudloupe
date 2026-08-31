package wafv2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awswafv2 "github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/collector/wafv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeWAFv2API는 wafv2API(메서드 2개)를 만족하는 테스트 대역이다.
type fakeWAFv2API struct {
	listPages []*awswafv2.ListWebACLsOutput
	listCalls int
	detail    map[string]*awswafv2.GetWebACLOutput // ACL Id -> 상세
	listErr   error
	getErr    error
}

func (f *fakeWAFv2API) ListWebACLs(_ context.Context, _ *awswafv2.ListWebACLsInput, _ ...func(*awswafv2.Options)) (*awswafv2.ListWebACLsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}

	page := f.listPages[f.listCalls]
	f.listCalls++

	return page, nil
}

func (f *fakeWAFv2API) GetWebACL(_ context.Context, in *awswafv2.GetWebACLInput, _ ...func(*awswafv2.Options)) (*awswafv2.GetWebACLOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}

	if out, ok := f.detail[aws.ToString(in.Id)]; ok {
		return out, nil
	}

	return &awswafv2.GetWebACLOutput{}, nil
}

func TestWAFv2WebACLCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	api := &fakeWAFv2API{
		listPages: []*awswafv2.ListWebACLsOutput{{
			WebACLs: []wafv2types.WebACLSummary{{
				Id:          aws.String("acl-123"),
				Name:        aws.String("prod-waf"),
				ARN:         aws.String("arn:aws:wafv2:ap-northeast-2:123456789012:regional/webacl/prod-waf/acl-123"),
				Description: aws.String("production web acl"),
			}},
		}},
		detail: map[string]*awswafv2.GetWebACLOutput{
			"acl-123": {WebACL: &wafv2types.WebACL{Rules: []wafv2types.Rule{{}, {}, {}}}},
		},
	}

	c := wafv2.NewWebACL(api)

	got, err := c.Collect(context.Background(), collect.Request{
		Scope: collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	r := got[0]

	if r.Type != model.TypeWAFv2WebACL || r.ID != "prod-waf" {
		t.Errorf("Type/ID = %q/%q", r.Type, r.ID)
	}

	if r.ARN == "" {
		t.Error("ARN이 비었다")
	}

	if got := r.FieldValue("규칙 수"); got != "3" {
		t.Errorf("규칙 수 = %q, want 3 (GetWebACL 상세에서)", got)
	}

	if got := r.FieldValue("설명"); got != "production web acl" {
		t.Errorf("설명 = %q", got)
	}
}

func TestWAFv2WebACLCollectorSurvivesGetError(t *testing.T) {
	t.Parallel()

	// GetWebACL 실패해도 목록의 ACL은 살아야 한다. 규칙 수만 비운다.
	api := &fakeWAFv2API{
		listPages: []*awswafv2.ListWebACLsOutput{{
			WebACLs: []wafv2types.WebACLSummary{{Id: aws.String("acl-1"), Name: aws.String("waf-1")}},
		}},
		getErr: errors.New("AccessDenied"),
	}

	c := wafv2.NewWebACL(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("Get 실패가 부분 오류로 반환되어야 한다")
	}

	if len(got) != 1 {
		t.Fatalf("ACL은 살아야 한다, got %d", len(got))
	}

	if got := got[0].FieldValue("규칙 수"); got != "-" {
		t.Errorf("규칙 수 = %q, want - (상세 조회 실패)", got)
	}
}

func TestWAFv2WebACLCollectorFollowsMarker(t *testing.T) {
	t.Parallel()

	// ListWebACLs는 페이지네이터가 없다. NextMarker로 손수 페이지를 넘긴다.
	api := &fakeWAFv2API{listPages: []*awswafv2.ListWebACLsOutput{
		{
			WebACLs:    []wafv2types.WebACLSummary{{Id: aws.String("acl-1"), Name: aws.String("waf-1")}},
			NextMarker: aws.String("page2"),
		},
		{
			WebACLs: []wafv2types.WebACLSummary{{Id: aws.String("acl-2"), Name: aws.String("waf-2")}},
		},
	}}

	c := wafv2.NewWebACL(api)

	got, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(got) != 2 || api.listCalls != 2 {
		t.Errorf("두 페이지에서 ACL 2개(호출 2회)여야 한다, got %d개 호출 %d회", len(got), api.listCalls)
	}
}

func TestWAFv2WebACLCollectorWrapsListError(t *testing.T) {
	t.Parallel()

	api := &fakeWAFv2API{listErr: errors.New("AccessDenied")}
	c := wafv2.NewWebACL(api)

	_, err := c.Collect(context.Background(), collect.Request{Scope: collect.Scope{Region: "r"}})
	if err == nil {
		t.Fatal("목록 조회 실패는 에러여야 한다")
	}

	if got := err.Error(); got == "AccessDenied" {
		t.Errorf("에러에 문맥이 안 붙었다: %q", got)
	}
}

func TestWAFv2WebACLCollectorType(t *testing.T) {
	t.Parallel()

	c := wafv2.NewWebACL(&fakeWAFv2API{})
	if c.Type() != model.TypeWAFv2WebACL {
		t.Errorf("Type() = %q, want %q", c.Type(), model.TypeWAFv2WebACL)
	}
}
