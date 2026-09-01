package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

type fakeAppCollector struct {
	typ string
}

func (c fakeAppCollector) Type() string { return c.typ }

func (c fakeAppCollector) Collect(context.Context, collect.Request) ([]model.Resource, error) {
	return nil, nil
}

func TestCollectWithPlansGlobalAfterFirstConfigFailure(t *testing.T) {
	t.Parallel()

	var includeGlobalCalls []bool
	var gotJobs []collect.Job

	deps := collectDeps{
		identify: successfulIdentity,
		config: func(_ context.Context, _, region string) (aws.Config, error) {
			if region == "failed-region" {
				return aws.Config{}, errors.New("설정 실패")
			}

			return aws.Config{}, nil
		},
		registry: func(_ aws.Config, includeGlobal bool, _ []string) (*collect.Registry, []string, error) {
			includeGlobalCalls = append(includeGlobalCalls, includeGlobal)

			return registryForTest(t, includeGlobal), nil, nil
		},
		run: func(_ context.Context, jobs []collect.Job) collect.Result {
			gotJobs = append([]collect.Job(nil), jobs...)

			return collect.Result{}
		},
	}

	result := collectWith(context.Background(), "prod",
		[]string{"failed-region", "ap-northeast-2", "us-east-1"}, nil, deps)

	if want := []bool{true, false}; !slices.Equal(includeGlobalCalls, want) {
		t.Errorf("includeGlobal 호출 = %v, want %v", includeGlobalCalls, want)
	}
	assertJobCounts(t, gotJobs, 2, 1)

	if len(result.Errors) != 1 || result.Errors[0].Type != configErrorType {
		t.Errorf("Errors = %+v, want 설정 오류 1건", result.Errors)
	}
}

func TestCollectWithPlansGlobalAfterFirstCatalogFailure(t *testing.T) {
	t.Parallel()

	var includeGlobalCalls []bool
	var gotJobs []collect.Job
	registryCalls := 0

	deps := collectDeps{
		identify: successfulIdentity,
		config: func(context.Context, string, string) (aws.Config, error) {
			return aws.Config{}, nil
		},
		registry: func(_ aws.Config, includeGlobal bool, _ []string) (*collect.Registry, []string, error) {
			includeGlobalCalls = append(includeGlobalCalls, includeGlobal)
			registryCalls++
			if registryCalls == 1 {
				return nil, nil, errors.New("카탈로그 실패")
			}

			return registryForTest(t, includeGlobal), nil, nil
		},
		run: func(_ context.Context, jobs []collect.Job) collect.Result {
			gotJobs = append([]collect.Job(nil), jobs...)

			return collect.Result{}
		},
	}

	result := collectWith(context.Background(), "prod",
		[]string{"ap-northeast-2", "us-east-1", "eu-west-1"}, nil, deps)

	if want := []bool{true, true, false}; !slices.Equal(includeGlobalCalls, want) {
		t.Errorf("includeGlobal 호출 = %v, want %v", includeGlobalCalls, want)
	}
	assertJobCounts(t, gotJobs, 2, 1)

	if len(result.Errors) != 1 || result.Errors[0].Type != catalogErrorType {
		t.Errorf("Errors = %+v, want 카탈로그 오류 1건", result.Errors)
	}
}

func successfulIdentity(context.Context, string, string) (awsclient.Identity, error) {
	return awsclient.Identity{AccountID: "123456789012"}, nil
}

func registryForTest(t *testing.T, includeGlobal bool) *collect.Registry {
	t.Helper()

	registry := collect.NewRegistry()
	if err := registry.Add(fakeAppCollector{typ: model.TypeEC2Instance}); err != nil {
		t.Fatalf("지역 수집기 등록 실패: %v", err)
	}
	if includeGlobal {
		if err := registry.Add(fakeAppCollector{typ: model.TypeRoute53RecordSet}); err != nil {
			t.Fatalf("글로벌 수집기 등록 실패: %v", err)
		}
	}

	return registry
}

func assertJobCounts(t *testing.T, jobs []collect.Job, wantRegional, wantGlobal int) {
	t.Helper()

	var regional, global int
	for _, job := range jobs {
		switch job.Collector.Type() {
		case model.TypeEC2Instance:
			regional++
		case model.TypeRoute53RecordSet:
			global++
		}
	}

	if regional != wantRegional || global != wantGlobal {
		t.Errorf("작업 수 지역=%d 글로벌=%d, want 지역=%d 글로벌=%d",
			regional, global, wantRegional, wantGlobal)
	}
}

func TestCollectWithSeparatesCancellation(t *testing.T) {
	t.Run("신원 확인 취소", func(t *testing.T) {
		deps := collectDeps{
			identify: func(context.Context, string, string) (awsclient.Identity, error) {
				return awsclient.Identity{}, context.Canceled
			},
		}

		result := collectWith(context.Background(), "prod", []string{"ap-northeast-2"}, nil, deps)
		if !result.Canceled || len(result.Errors) != 0 {
			t.Errorf("Result = %+v, want 취소 상태와 오류 0건", result)
		}
	})

	t.Run("설정 로드 취소", func(t *testing.T) {
		deps := collectDeps{
			identify: successfulIdentity,
			config: func(context.Context, string, string) (aws.Config, error) {
				return aws.Config{}, context.Canceled
			},
			run: func(_ context.Context, jobs []collect.Job) collect.Result {
				if len(jobs) != 0 {
					t.Errorf("취소 후 Jobs = %d, want 0", len(jobs))
				}

				return collect.Result{}
			},
		}

		result := collectWith(context.Background(), "prod", []string{"ap-northeast-2"}, nil, deps)
		if !result.Canceled || len(result.Errors) != 0 {
			t.Errorf("Result = %+v, want 취소 상태와 오류 0건", result)
		}
	})
}
