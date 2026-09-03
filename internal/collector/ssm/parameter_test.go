package ssm_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ssmcollector "github.com/cnlgks1/cloudloupe/internal/collector/ssm"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeSSM은 파라미터 수집기가 쓰는 DescribeParameters를 대신한다.
//
// pages는 DescribeParameters의 페이지들, pageErr는 마지막 페이지 뒤에 낼 오류다.
type fakeSSM struct {
	pages   [][]ssmtypes.ParameterMetadata
	pageErr error

	calls int
}

func (f *fakeSSM) DescribeParameters(
	_ context.Context,
	_ *awsssm.DescribeParametersInput,
	_ ...func(*awsssm.Options),
) (*awsssm.DescribeParametersOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awsssm.DescribeParametersOutput{}, nil
	}

	out := &awsssm.DescribeParametersOutput{Parameters: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestParameterCollectorType(t *testing.T) {
	t.Parallel()

	if got := ssmcollector.NewParameter(&fakeSSM{}).Type(); got != model.TypeSSMParameter {
		t.Errorf("Type() = %q, want %q", got, model.TypeSSMParameter)
	}
}

// TestParameterCollectConvertsFieldsAndKMS는 메타데이터 값을 그대로 담고 SecureString이
// 고객 키로 암호화되면 KMS 관계를 만드는지 확인한다.
func TestParameterCollectConvertsFieldsAndKMS(t *testing.T) {
	t.Parallel()

	modified := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	arn := "arn:aws:ssm:ap-northeast-2:123456789012:parameter/app/db-password"
	kmsKey := "arn:aws:kms:ap-northeast-2:123456789012:key/abc"

	api := &fakeSSM{
		pages: [][]ssmtypes.ParameterMetadata{{
			{
				Name:             aws.String("/app/db-password"),
				ARN:              aws.String(arn),
				Type:             ssmtypes.ParameterTypeSecureString,
				Tier:             ssmtypes.ParameterTierStandard,
				DataType:         aws.String("text"),
				Version:          3,
				Description:      aws.String("DB 비밀번호"),
				KeyId:            aws.String(kmsKey),
				LastModifiedDate: &modified,
			},
		}},
	}

	got, err := ssmcollector.NewParameter(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("파라미터 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "/app/db-password" || res.ARN != arn {
		t.Errorf("ID/ARN = %q/%q", res.ID, res.ARN)
	}
	// 값은 AWS가 준 그대로. SecureString/Standard를 번역하지 않는다.
	if got, want := res.FieldValue("Type"), "SecureString"; got != want {
		t.Errorf("Type = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("Version"), "3"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("LastModifiedDate"), "2025-06-01T00:00:00Z"; got != want {
		t.Errorf("LastModifiedDate = %q, want %q", got, want)
	}

	if len(res.Related) != 1 {
		t.Fatalf("관계 %d개, want 1", len(res.Related))
	}
	ref := res.Related[0]
	if ref.Type != model.TypeKMSKey || ref.Relation != "KeyId" || ref.ID != kmsKey {
		t.Errorf("관계 = %+v", ref)
	}
}

// TestParameterCollectWithoutKeyHasNoRelation은 KeyId가 없으면(평문 String) 관계를 만들지
// 않는지 확인한다.
func TestParameterCollectWithoutKeyHasNoRelation(t *testing.T) {
	t.Parallel()

	api := &fakeSSM{
		pages: [][]ssmtypes.ParameterMetadata{{
			{Name: aws.String("/app/flag"), Type: ssmtypes.ParameterTypeString},
		}},
	}

	got, err := ssmcollector.NewParameter(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got[0].Related) != 0 {
		t.Errorf("KeyId 없으면 관계 없음, got %+v", got[0].Related)
	}
	if v := got[0].FieldValue("LastModifiedDate"); v != "-" {
		t.Errorf("LastModifiedDate 없음 = %q, want -", v)
	}
}

// TestParameterCollectFollowsPages는 페이지네이션을 이어 받는지 확인한다.
func TestParameterCollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeSSM{
		pages: [][]ssmtypes.ParameterMetadata{
			{{Name: aws.String("/a")}},
			{{Name: aws.String("/b")}},
		},
	}

	got, err := ssmcollector.NewParameter(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"/a", "/b"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
	if api.calls != 2 {
		t.Errorf("DescribeParameters 호출 = %d회, want 2", api.calls)
	}
}

// TestParameterCollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestParameterCollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeSSM{
		pages:   [][]ssmtypes.ParameterMetadata{{{Name: aws.String("/a")}}},
		pageErr: denied,
	}

	got, err := ssmcollector.NewParameter(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "/a" {
		t.Errorf("수집 결과 = %+v, want /a 하나", got)
	}
}
