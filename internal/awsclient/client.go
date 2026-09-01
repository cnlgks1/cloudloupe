package awsclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

// Identity는 자격증명이 실제로 누구인지 나타낸다.
//
// STS GetCallerIdentity의 결과다. 프로필을 선택한 뒤 "이 자격증명이 정말 이 계정의
// 것인가"를 사용자에게 보여주는 데 쓴다. 여기까지 와야 자격증명이 유효하다는 것이
// 확인되므로, 리전 선택 전에 한 번 호출한다.
type Identity struct {
	AccountID string
	ARN       string
	UserID    string
}

// stsAPI는 신원 확인에 필요한 STS 메서드만 담은 좁은 인터페이스다.
//
// GetCallerIdentity는 이름 그대로 조회이며 조회 전용 가드를 통과한다.
type stsAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Config는 선택한 프로필과 리전으로 AWS 설정을 로드한다.
//
// 이 함수가 프로필 "탐색"과 실제 AWS "연결"의 경계다. 여기서부터 자격증명 체인이
// 동작한다(SSO 토큰, assume-role, 정적 키 등은 전부 SDK가 처리한다).
//
// region이 빈 문자열이면 프로필의 기본 리전을 쓴다. 프로필에도 리전이 없으면 이후
// AWS 호출에서 리전 미지정 에러가 나고, Explain이 그것을 사람이 읽을 수 있게 바꾼다.
func Config(ctx context.Context, profile, region string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithSharedConfigProfile(profile),
	}

	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("프로필 %q 설정 로드: %w", profile, err)
	}

	return cfg, nil
}

// WhoAmI는 STS로 자격증명의 신원을 확인한다.
//
// 프로필을 골랐을 때 실제로 유효한지, 어느 계정인지 알려주는 첫 실제 AWS 호출이다.
// 여기서 실패하면 자격증명 문제(만료, 권한 없음, 잘못된 프로필)이므로, 호출부는
// Explain으로 감싸 사용자에게 보여준다.
func WhoAmI(ctx context.Context, api stsAPI) (Identity, error) {
	out, err := api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, fmt.Errorf("신원 확인: %w", err)
	}

	return Identity{
		AccountID: aws.ToString(out.Account),
		ARN:       aws.ToString(out.Arn),
		UserID:    aws.ToString(out.UserId),
	}, nil
}

// STSFromConfig는 설정으로부터 STS 클라이언트를 만든다.
//
// WhoAmI가 좁은 인터페이스를 받으므로 테스트에서는 이 함수를 거치지 않고 fake를 넘긴다.
// 실제 실행에서만 이 어댑터를 쓴다.
func STSFromConfig(cfg aws.Config) *sts.Client {
	return sts.NewFromConfig(cfg)
}

// ErrorInfo는 원본 오류에서 추출한 공급자 코드와 사용자 설명이다.
type ErrorInfo struct {
	Code        string
	Explanation string
}

// ClassifyError는 AWS 오류를 공급자 코드와 사용자 설명으로 분류한다.
//
// 호출부가 붙인 %w 문맥을 errors.As로 따라가므로 모든 서비스 수집기가 같은 분류 경로를
// 사용한다. smithy.APIError가 아닌 설정·자격증명 오류는 코드 없이 설명만 반환한다.
func ClassifyError(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return ErrorInfo{
			Code:        apiErr.ErrorCode(),
			Explanation: explainAPIError(apiErr),
		}
	}

	return ErrorInfo{Explanation: explainNonAPIError(err)}
}

// Explain은 AWS 에러를 사용자가 읽을 수 있는 한국어 문장으로 바꾼다.
func Explain(err error) string {
	return ClassifyError(err).Explanation
}

func explainAPIError(apiErr smithy.APIError) string {
	switch apiErr.ErrorCode() {
	case "ExpiredToken", "ExpiredTokenException", "RequestExpired":
		return "자격증명이 만료되었습니다. 다시 로그인하세요 (예: aws sso login --profile <프로필>)."
	case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation":
		return "이 작업을 수행할 권한이 없습니다. 프로필의 IAM 권한을 확인하세요. cloudloupe는 조회 권한만 필요합니다."
	case "InvalidClientTokenId", "SignatureDoesNotMatch", "AuthFailure":
		return "자격증명이 유효하지 않습니다. 액세스 키나 프로필 설정을 확인하세요."
	case "OptInRequired":
		return "이 리전은 계정에서 활성화되어 있지 않습니다. AWS 콘솔에서 리전을 옵트인하거나 다른 리전을 선택하세요."
	case "Throttling", "ThrottlingException", "RequestLimitExceeded", "TooManyRequestsException":
		return "AWS API 요청 한도를 초과했습니다. 잠시 후 다시 시도하세요."
	default:
		return fmt.Sprintf("AWS 오류 %s: %s", apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
}

func explainNonAPIError(err error) string {
	msg := err.Error()

	switch {
	case contains(msg, "no EC2 IMDS role found", "failed to refresh cached credentials", "no valid providers"):
		return "자격증명을 찾을 수 없습니다. aws configure 로 프로필을 설정하거나 aws sso login 으로 로그인하세요."
	case contains(msg, "could not find region", "missing region", "no region"):
		return "리전이 지정되지 않았습니다. 프로필에 region을 설정하거나 리전을 선택하세요."
	default:
		return msg
	}
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}

	return false
}
