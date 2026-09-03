package ecs_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ecscollector "github.com/cnlgks1/cloudloupe/internal/collector/ecs"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeTaskDefinitionAPI는 태스크 정의 수집기가 쓰는 두 메서드를 대신한다.
//
// listPages는 ListTaskDefinitions의 페이지들, describe는 ARN별 상세다. describeErr는
// 특정 ARN만 실패시킨다. lastStatus·lastSort는 목록 조회에 활성·내림차순이 넘어가는지 본다.
type fakeTaskDefinitionAPI struct {
	listPages   [][]string
	describe    map[string]ecstypes.TaskDefinition
	describeErr map[string]error

	listCalls  int
	lastStatus ecstypes.TaskDefinitionStatus
	lastSort   ecstypes.SortOrder
}

func (f *fakeTaskDefinitionAPI) ListTaskDefinitions(
	_ context.Context,
	in *awsecs.ListTaskDefinitionsInput,
	_ ...func(*awsecs.Options),
) (*awsecs.ListTaskDefinitionsOutput, error) {
	f.lastStatus = in.Status
	f.lastSort = in.Sort

	i := f.listCalls
	f.listCalls++

	if i >= len(f.listPages) {
		return &awsecs.ListTaskDefinitionsOutput{}, nil
	}

	out := &awsecs.ListTaskDefinitionsOutput{TaskDefinitionArns: f.listPages[i]}
	if i+1 < len(f.listPages) {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func (f *fakeTaskDefinitionAPI) DescribeTaskDefinition(
	_ context.Context,
	in *awsecs.DescribeTaskDefinitionInput,
	_ ...func(*awsecs.Options),
) (*awsecs.DescribeTaskDefinitionOutput, error) {
	arn := aws.ToString(in.TaskDefinition)
	if err, ok := f.describeErr[arn]; ok {
		return nil, err
	}

	td := f.describe[arn]

	return &awsecs.DescribeTaskDefinitionOutput{TaskDefinition: &td}, nil
}

// TestTaskDefinitionCollectorType은 타입 ID를 확인한다.
func TestTaskDefinitionCollectorType(t *testing.T) {
	t.Parallel()

	if got := ecscollector.NewTaskDefinition(&fakeTaskDefinitionAPI{}).Type(); got != model.TypeECSTaskDefinition {
		t.Errorf("Type() = %q, want %q", got, model.TypeECSTaskDefinition)
	}
}

// TestTaskDefinitionCollectBuildsNameAndRoles는 이름을 Family:Revision으로 만들고 실행·태스크
// 역할을 iam:role 관계로 잇는지 확인한다.
func TestTaskDefinitionCollectBuildsNameAndRoles(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:ecs:ap-northeast-2:123456789012:task-definition/web:7"
	execRole := "arn:aws:iam::123456789012:role/ecsTaskExecutionRole"
	taskRole := "arn:aws:iam::123456789012:role/ecsTaskRole"

	api := &fakeTaskDefinitionAPI{
		listPages: [][]string{{arn}},
		describe: map[string]ecstypes.TaskDefinition{
			arn: {
				Family:                  aws.String("web"),
				TaskDefinitionArn:       aws.String(arn),
				Revision:                7,
				Cpu:                     aws.String("256"),
				Memory:                  aws.String("512"),
				NetworkMode:             ecstypes.NetworkModeAwsvpc,
				RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
				ExecutionRoleArn:        aws.String(execRole),
				TaskRoleArn:             aws.String(taskRole),
				Status:                  ecstypes.TaskDefinitionStatusActive,
			},
		},
	}

	got, err := ecscollector.NewTaskDefinition(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("태스크 정의 %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "web:7" || res.Name != "web:7" {
		t.Errorf("ID/Name = %q/%q, want web:7", res.ID, res.Name)
	}
	if res.Status != "ACTIVE" {
		t.Errorf("Status = %q, want ACTIVE", res.Status)
	}
	// 값은 AWS가 준 그대로. awsvpc를 번역하지 않는다.
	if got, want := res.FieldValue("NetworkMode"), "awsvpc"; got != want {
		t.Errorf("NetworkMode = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("RequiresCompatibilities"), "FARGATE"; got != want {
		t.Errorf("RequiresCompatibilities = %q, want %q", got, want)
	}

	type rel struct {
		relation string
		id       string
	}
	gotRels := make([]rel, 0, len(res.Related))
	for _, r := range res.Related {
		if r.Type != model.TypeIAMRole {
			t.Errorf("관계 대상 타입 = %q, want %q", r.Type, model.TypeIAMRole)
		}
		gotRels = append(gotRels, rel{r.Relation, r.ID})
	}
	want := []rel{
		{"ExecutionRoleArn", execRole},
		{"TaskRoleArn", taskRole},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 = %+v, want %+v", gotRels, want)
	}
}

// TestTaskDefinitionCollectRequestsActiveDesc는 목록 조회에 활성 상태와 내림차순 정렬이
// 넘어가는지 확인한다. 최신 리비전이 먼저 와야 화면에서 유용하다.
func TestTaskDefinitionCollectRequestsActiveDesc(t *testing.T) {
	t.Parallel()

	arn := "arn/td:1"
	api := &fakeTaskDefinitionAPI{
		listPages: [][]string{{arn}},
		describe: map[string]ecstypes.TaskDefinition{
			arn: {Family: aws.String("td"), TaskDefinitionArn: aws.String(arn), Revision: 1},
		},
	}

	if _, err := ecscollector.NewTaskDefinition(api).Collect(
		context.Background(), collect.Request{Scope: testScope()}); err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if api.lastStatus != ecstypes.TaskDefinitionStatusActive {
		t.Errorf("Status = %q, want ACTIVE", api.lastStatus)
	}
	if api.lastSort != ecstypes.SortOrderDesc {
		t.Errorf("Sort = %q, want DESC", api.lastSort)
	}
}

// TestTaskDefinitionCollectFallsBackToArnName은 Family가 비면 ARN의 이름 부분을 쓰는지
// 확인한다.
func TestTaskDefinitionCollectFallsBackToArnName(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:ecs:ap-northeast-2:123456789012:task-definition/legacy:3"
	api := &fakeTaskDefinitionAPI{
		listPages: [][]string{{arn}},
		describe: map[string]ecstypes.TaskDefinition{
			arn: {TaskDefinitionArn: aws.String(arn)},
		},
	}

	got, err := ecscollector.NewTaskDefinition(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if got[0].ID != "legacy:3" {
		t.Errorf("ID = %q, want legacy:3", got[0].ID)
	}
}

// TestTaskDefinitionCollectKeepsPartialFailures는 상세 조회 하나가 실패해도 나머지를
// 살리는지 확인한다.
func TestTaskDefinitionCollectKeepsPartialFailures(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	a, b := "arn/a:1", "arn/b:1"
	api := &fakeTaskDefinitionAPI{
		listPages: [][]string{{a, b}},
		describe: map[string]ecstypes.TaskDefinition{
			a: {Family: aws.String("a"), TaskDefinitionArn: aws.String(a), Revision: 1},
		},
		describeErr: map[string]error{b: denied},
	}

	got, err := ecscollector.NewTaskDefinition(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a:1" {
		t.Errorf("수집 결과 = %+v, want a:1 하나", got)
	}
}

// TestTaskDefinitionCollectStopsOnCanceledContext는 취소된 조회가 멈추는지 확인한다.
func TestTaskDefinitionCollectStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeTaskDefinitionAPI{listPages: [][]string{{"arn/td:1"}}}
	got, err := ecscollector.NewTaskDefinition(api).Collect(ctx, collect.Request{Scope: testScope()})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(got) != 0 {
		t.Errorf("취소 시 결과 = %+v, want 없음", got)
	}
}
