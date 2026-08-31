// Package app은 AWS 설정, 수집기 카탈로그와 실행 코어를 연결하는 애플리케이션 계층이다.
//
// cmd는 플래그와 입출력만, collect는 실행 규칙만, collector는 서비스 API 변환만 맡는다.
// 프로필·리전별 AWS 설정과 글로벌 리소스 실행 정책은 이 패키지에서 조립한다.
package app

import (
	"context"
	"fmt"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/catalog"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

const (
	identityErrorType = "aws:identity"
	configErrorType   = "aws:config"
	catalogErrorType  = "cloudloupe:catalog"
)

// Identify는 프로필의 호출 주체를 STS로 확인한다.
func Identify(ctx context.Context, profile, region string) (awsclient.Identity, error) {
	cfg, err := awsclient.Config(ctx, profile, region)
	if err != nil {
		return awsclient.Identity{}, err
	}

	return awsclient.WhoAmI(ctx, awsclient.STSFromConfig(cfg))
}

// Collect는 선택 프로필의 여러 리전에서 지정한 리소스 타입을 조회한다.
//
// 리전 하나의 설정이나 권한이 실패해도 다른 리전은 계속 실행한다. Route 53 같은 글로벌
// 타입은 첫 번째 리전의 설정으로 한 번만 실행한다. 모든 실패는 성공한 리소스와 함께
// Result.Errors에 보존한다.
func Collect(ctx context.Context, profile string, regions, types []string) collect.Result {
	var result collect.Result

	accountID := identifyAccount(ctx, profile, regions, &result)
	jobs := make([]collect.Job, 0, len(regions)*len(catalog.Definitions()))
	reportedUnknown := make(map[string]struct{})

	for index, region := range regions {
		cfg, err := awsclient.Config(ctx, profile, region)
		if err != nil {
			result.Errors = append(result.Errors, collectError(configErrorType, profile, region,
				fmt.Errorf("AWS 설정 로드: %w", err)))

			continue
		}

		registry, unknown, err := catalog.Registry(cfg, index == 0, types)
		for _, typ := range unknown {
			if _, exists := reportedUnknown[typ]; exists {
				continue
			}

			reportedUnknown[typ] = struct{}{}
			result.Errors = append(result.Errors, collectError(typ, profile, region,
				fmt.Errorf("지원하지 않는 리소스 타입: %s", typ)))
		}

		if err != nil {
			result.Errors = append(result.Errors, collectError(catalogErrorType, profile, region, err))

			continue
		}

		scope := collect.Scope{Profile: profile, Region: region, AccountID: accountID}
		jobs = append(jobs, collect.Plan(registry, []collect.Scope{scope})...)
	}

	runResult := (collect.Runner{}).Run(ctx, jobs)
	result.Resources = append(result.Resources, runResult.Resources...)
	result.Errors = append(result.Errors, runResult.Errors...)

	return result
}

func identifyAccount(ctx context.Context, profile string, regions []string, result *collect.Result) string {
	if len(regions) == 0 {
		return ""
	}

	identity, err := Identify(ctx, profile, regions[0])
	if err != nil {
		result.Errors = append(result.Errors, collectError(identityErrorType, profile, regions[0],
			fmt.Errorf("계정 확인: %w", err)))

		return ""
	}

	return identity.AccountID
}

func collectError(typ, profile, region string, err error) model.CollectError {
	return model.CollectError{
		Type:    typ,
		Profile: profile,
		Region:  region,
		Message: err.Error(),
	}
}
