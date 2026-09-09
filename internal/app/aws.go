// Package app은 AWS 설정, 수집기 카탈로그와 실행 코어를 연결하는 애플리케이션 계층이다.
//
// cmd는 플래그와 입출력만, collect는 실행 규칙만, collector는 서비스 API 변환만 맡는다.
// 프로필·리전별 AWS 설정과 글로벌 리소스 실행 정책은 이 패키지에서 조립한다.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"

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
// Identify는 SDK 기본 탐색 규칙으로 프로필의 신원을 확인한다.
func Identify(ctx context.Context, profile, region string) (awsclient.Identity, error) {
	return identifyWithConfig(ctx, profile, region, awsclient.Config)
}

// IdentifyWithLocations는 프로필 탐색에 사용한 경로로 신원을 확인한다.
func IdentifyWithLocations(
	ctx context.Context,
	profile, region string,
	locations awsclient.Locations,
) (awsclient.Identity, error) {
	return identifyWithConfig(ctx, profile, region, func(ctx context.Context, profile, region string) (aws.Config, error) {
		return awsclient.ConfigWithLocations(ctx, profile, region, locations)
	})
}

func identifyWithConfig(
	ctx context.Context,
	profile, region string,
	loadConfig func(context.Context, string, string) (aws.Config, error),
) (awsclient.Identity, error) {
	cfg, err := loadConfig(ctx, profile, region)
	if err != nil {
		return awsclient.Identity{}, fmt.Errorf("AWS 설정 로드 (%s/%s): %w", profile, region, err)
	}

	identity, err := awsclient.WhoAmI(ctx, awsclient.STSFromConfig(cfg))
	if err != nil {
		return awsclient.Identity{}, fmt.Errorf("호출 주체 확인 (%s/%s): %w", profile, region, err)
	}

	return identity, nil
}

type collectDeps struct {
	identify func(context.Context, string, string) (awsclient.Identity, error)
	config   func(context.Context, string, string) (aws.Config, error)
	registry func(aws.Config, bool, []string) (*collect.Registry, []string, error)
	run      func(context.Context, []collect.Job) collect.Result
}

// Collect는 선택한 프로필들의 여러 리전에서 지정한 리소스 타입을 조회한다.
//
// 프로필과 리전은 곱집합으로 순회한다. 프로필 하나 또는 리전 하나의 설정·권한이 실패해도
// 다른 조합은 계속 실행한다. Route 53 같은 글로벌 타입은 계정 단위이므로 프로필마다 처음
// 성공한 리전에서 한 번씩 실행한다. 모든 실패는 성공한 리소스와 함께 Result.Errors에 보존한다.
func Collect(ctx context.Context, profiles, regions, types []string) collect.Result {
	return collectWith(ctx, profiles, regions, types, collectDeps{
		identify: Identify,
		config:   awsclient.Config,
		registry: catalog.Registry,
		run:      (collect.Runner{Classify: classifyError}).Run,
	})
}

// CollectWithLocations는 프로필 탐색에 사용한 경로로 리소스를 조회한다.
func CollectWithLocations(
	ctx context.Context,
	profiles, regions, types []string,
	locations awsclient.Locations,
) collect.Result {
	loadConfig := func(ctx context.Context, profile, region string) (aws.Config, error) {
		return awsclient.ConfigWithLocations(ctx, profile, region, locations)
	}

	return collectWith(ctx, profiles, regions, types, collectDeps{
		identify: func(ctx context.Context, profile, region string) (awsclient.Identity, error) {
			return identifyWithConfig(ctx, profile, region, loadConfig)
		},
		config:   loadConfig,
		registry: catalog.Registry,
		run:      (collect.Runner{Classify: classifyError}).Run,
	})
}

func collectWith(
	ctx context.Context,
	profiles, regions, types []string,
	deps collectDeps,
) collect.Result {
	var result collect.Result

	jobs := make([]collect.Job, 0, len(profiles)*len(regions)*len(catalog.Definitions()))
	// reportedUnknown은 프로필·리전 전체에 걸쳐 같은 미지원 타입을 한 번만 보고하게 한다.
	reportedUnknown := make(map[string]struct{})
	loadConfig := deps.config
	registerCollectors := deps.registry
	runJobs := deps.run

	for _, profile := range profiles {
		accountID, canceled := identifyAccount(ctx, profile, regions, &result, deps.identify)
		if canceled {
			result.Canceled = true

			return result
		}

		// 글로벌 타입(IAM, Route 53 등)은 계정 단위라 프로필마다 한 번씩 실행해야 한다.
		// 프로필이 바뀔 때마다 다시 계획하도록 여기서 리셋한다. 리셋을 빠뜨리면 두 번째
		// 프로필부터 글로벌 리소스가 통째로 누락된다.
		includeGlobal := true

		for _, region := range regions {
			cfg, err := loadConfig(ctx, profile, region)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					result.Canceled = true

					return result
				}

				result.Errors = append(result.Errors, collectError(configErrorType, profile, region,
					fmt.Errorf("AWS 설정 로드: %w", err)))

				continue
			}

			registry, unknown, err := registerCollectors(cfg, includeGlobal, types)
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

			// 이 프로필의 글로벌 타입은 한 번 계획했다. 같은 프로필의 다음 리전부터 제외한다.
			includeGlobal = false

			scope := collect.Scope{Profile: profile, Region: region, AccountID: accountID}
			jobs = append(jobs, collect.Plan(registry, []collect.Scope{scope})...)
		}
	}

	runResult := runJobs(ctx, jobs)
	result.Resources = append(result.Resources, runResult.Resources...)
	result.Errors = append(result.Errors, runResult.Errors...)
	result.Canceled = result.Canceled || runResult.Canceled

	return result
}

func identifyAccount(
	ctx context.Context,
	profile string,
	regions []string,
	result *collect.Result,
	identify func(context.Context, string, string) (awsclient.Identity, error),
) (string, bool) {
	if len(regions) == 0 {
		return "", false
	}

	identity, err := identify(ctx, profile, regions[0])
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", true
		}

		result.Errors = append(result.Errors, collectError(identityErrorType, profile, regions[0],
			fmt.Errorf("계정 확인: %w", err)))

		return "", false
	}

	return identity.AccountID, false
}

func collectError(typ, profile, region string, err error) model.CollectError {
	details := awsclient.ClassifyError(err)

	return model.CollectError{
		Type:        typ,
		Profile:     profile,
		Region:      region,
		Code:        details.Code,
		Message:     err.Error(),
		Explanation: details.Explanation,
	}
}

func classifyError(err error) collect.ErrorDetails {
	details := awsclient.ClassifyError(err)

	return collect.ErrorDetails{Code: details.Code, Explanation: details.Explanation}
}
