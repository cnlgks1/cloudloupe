package awsclient_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
)

// fakeSTS는 stsAPI를 만족하는 테스트 대역이다. 실제 AWS를 호출하지 않는다.
type fakeSTS struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f fakeSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return f.out, f.err
}

func TestWhoAmI(t *testing.T) {
	t.Parallel()

	api := fakeSTS{out: &sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
		Arn:     aws.String("arn:aws:sts::123456789012:assumed-role/ReadOnlyAccess/alice"),
		UserId:  aws.String("AROAEXAMPLE:alice"),
	}}

	id, err := awsclient.WhoAmI(context.Background(), api)
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}

	if id.AccountID != "123456789012" {
		t.Errorf("AccountID = %q", id.AccountID)
	}

	if id.ARN == "" || id.UserID == "" {
		t.Errorf("ARN/UserID가 비었다: %+v", id)
	}
}

func TestWhoAmIWrapsError(t *testing.T) {
	t.Parallel()

	api := fakeSTS{err: errors.New("boom")}

	_, err := awsclient.WhoAmI(context.Background(), api)
	if err == nil {
		t.Fatal("에러가 반환되어야 한다")
	}

	if err.Error() == "boom" {
		t.Errorf("문맥이 안 붙었다: %q", err)
	}
}

// apiError는 smithy.APIError를 만족하는 테스트용 에러다.
type apiError struct {
	code string
	msg  string
}

func (e apiError) Error() string        { return e.code + ": " + e.msg }
func (e apiError) ErrorCode() string    { return e.code }
func (e apiError) ErrorMessage() string { return e.msg }
func (e apiError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

func TestExplainMapsKnownErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name:     "만료된 토큰",
			err:      apiError{code: "ExpiredToken", msg: "token expired"},
			contains: "만료",
		},
		{
			name:     "권한 없음",
			err:      apiError{code: "AccessDenied", msg: "denied"},
			contains: "권한",
		},
		{
			name:     "리전 옵트인 필요",
			err:      apiError{code: "OptInRequired", msg: "opt in"},
			contains: "리전",
		},
		{
			name:     "유효하지 않은 자격증명",
			err:      apiError{code: "InvalidClientTokenId", msg: "bad"},
			contains: "유효하지 않",
		},
		{
			name:     "자격증명 못 찾음 (표준 에러 아님)",
			err:      errors.New("failed to refresh cached credentials, no EC2 IMDS role found"),
			contains: "자격증명을 찾을 수 없",
		},
		{
			name:     "리전 미지정 (표준 에러 아님)",
			err:      errors.New("could not find region configuration"),
			contains: "리전이 지정되지 않",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := awsclient.Explain(tc.err)
			if got == "" {
				t.Fatal("설명이 비었다")
			}

			if !strings.Contains(got, tc.contains) {
				t.Errorf("Explain() = %q, %q를 포함해야 한다", got, tc.contains)
			}
		})
	}
}

func TestExplainNilIsEmpty(t *testing.T) {
	t.Parallel()

	if got := awsclient.Explain(nil); got != "" {
		t.Errorf("Explain(nil) = %q, want 빈 문자열", got)
	}
}

func TestExplainUnknownErrorPassesThrough(t *testing.T) {
	t.Parallel()

	// 알 수 없는 에러는 원문을 그대로 보여준다. 정보를 삼키지 않는다.
	got := awsclient.Explain(errors.New("뭔가 예상 못한 에러"))
	if got != "뭔가 예상 못한 에러" {
		t.Errorf("Explain() = %q", got)
	}
}

func TestClassifyErrorExtractsWrappedThrottlingCode(t *testing.T) {
	t.Parallel()

	err := errors.Join(errors.New("describe resources"), apiError{code: "ThrottlingException", msg: "rate exceeded"})

	got := awsclient.ClassifyError(err)
	if got.Code != "ThrottlingException" {
		t.Errorf("Code = %q, want %q", got.Code, "ThrottlingException")
	}
	if !strings.Contains(got.Explanation, "요청 한도") {
		t.Errorf("Explanation = %q, want 요청 한도 설명", got.Explanation)
	}
}
