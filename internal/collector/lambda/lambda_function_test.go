package lambda_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	lambdacollector "github.com/cnlgks1/cloudloupe/internal/collector/lambda"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeListFunctions는 listFunctionsAPI를 만족하는 테스트 대역이다.
//
// errs는 호출 차수별 오류다. 두 번째 페이지만 실패시켜 부분 결과 보존을 확인하는 데 쓴다.
type fakeListFunctions struct {
	pages []*awslambda.ListFunctionsOutput
	errs  []error
	calls int
}

func (f *fakeListFunctions) ListFunctions(
	_ context.Context,
	_ *awslambda.ListFunctionsInput,
	_ ...func(*awslambda.Options),
) (*awslambda.ListFunctionsOutput, error) {
	call := f.calls
	f.calls++

	if call < len(f.errs) && f.errs[call] != nil {
		return nil, f.errs[call]
	}

	return f.pages[call], nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestFunctionCollectorType(t *testing.T) {
	t.Parallel()

	if got := lambdacollector.NewFunction(&fakeListFunctions{}).Type(); got != model.TypeLambdaFunction {
		t.Errorf("Type() = %q, want %q", got, model.TypeLambdaFunction)
	}
}

// TestFunctionCollectorConvertsFields는 SDK 응답이 표시 필드와 관계로 옮겨지는지 확인한다.
//
// 값은 API가 준 것을 그대로 쓴다. 화면과 aws CLI 출력을 대조할 수 있어야 하기 때문이다.
func TestFunctionCollectorConvertsFields(t *testing.T) {
	t.Parallel()

	api := &fakeListFunctions{pages: []*awslambda.ListFunctionsOutput{{
		Functions: []lambdatypes.FunctionConfiguration{{
			FunctionName:  aws.String("order-worker"),
			FunctionArn:   aws.String("arn:aws:lambda:ap-northeast-2:123456789012:function:order-worker"),
			State:         lambdatypes.StateActive,
			Runtime:       lambdatypes.RuntimeProvidedal2023,
			PackageType:   lambdatypes.PackageTypeZip,
			Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureArm64},
			MemorySize:    aws.Int32(512),
			Timeout:       aws.Int32(30),
			LastModified:  aws.String("2025-03-04T05:06:07.000+0000"),
			Version:       aws.String("$LATEST"),
			Role:          aws.String("arn:aws:iam::123456789012:role/order-worker"),
			Handler:       aws.String("bootstrap"),
			CodeSize:      1048576,
			Description:   aws.String("주문 처리"),
			KMSKeyArn:     aws.String("arn:aws:kms:ap-northeast-2:123456789012:key/key-1"),
			EphemeralStorage: &lambdatypes.EphemeralStorage{
				Size: aws.Int32(1024),
			},
			VpcConfig: &lambdatypes.VpcConfigResponse{
				VpcId:            aws.String("vpc-0123"),
				SubnetIds:        []string{"subnet-a", "subnet-c"},
				SecurityGroupIds: []string{"sg-0123"},
			},
		}},
	}}}

	got, err := lambdacollector.NewFunction(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("리소스 %d개, want 1", len(got))
	}

	function := got[0]
	if function.Type != model.TypeLambdaFunction || function.ID != "order-worker" ||
		function.Name != "order-worker" {
		t.Errorf("기본 식별 정보 = %+v", function)
	}
	if function.Status != "Active" || function.Region != "ap-northeast-2" ||
		function.Profile != "prod" || function.AccountID != "123456789012" {
		t.Errorf("범위 또는 상태 = %+v", function)
	}

	for _, want := range []model.Field{
		{Key: "Runtime", Value: "provided.al2023"},
		{Key: "PackageType", Value: "Zip"},
		{Key: "Architectures", Value: "arm64"},
		{Key: "MemorySize", Value: "512"},
		{Key: "Timeout", Value: "30"},
		{Key: "CodeSize", Value: "1048576"},
		{Key: "EphemeralStorage", Value: "1024"},
	} {
		if got := function.FieldValue(want.Key); got != want.Value {
			t.Errorf("%s = %q, want %q", want.Key, got, want.Value)
		}
	}

	wantRefs := []model.Ref{
		{Type: model.TypeEC2VPC, ID: "vpc-0123", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-a", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2Subnet, ID: "subnet-c", Relation: model.RelationAssociatedWith},
		{Type: model.TypeEC2SecurityGroup, ID: "sg-0123", Relation: model.RelationAssociatedWith},
		{
			Type:           model.TypeIAMRole,
			ID:             "arn:aws:iam::123456789012:role/order-worker",
			IdentifierKind: model.IdentifierARN,
			Relation:       model.RelationAssociatedWith,
		},
		{
			Type:           model.TypeKMSKey,
			ID:             "arn:aws:kms:ap-northeast-2:123456789012:key/key-1",
			IdentifierKind: model.IdentifierARN,
			Relation:       model.RelationAssociatedWith,
		},
	}
	if !slices.Equal(function.Related, wantRefs) {
		t.Errorf("Related = %+v, want %+v", function.Related, wantRefs)
	}
}

// TestFunctionCollectorKeepsLastModifiedOutOfCreatedAt은 LastModified를 생성 시각으로
// 오인하지 않는지 확인한다.
//
// ListFunctions에는 생성 시각이 없다. LastModified를 CreatedAt에 넣으면 목록의 Age 열이
// "만들어진 지 얼마"가 아니라 "마지막 배포 후 얼마"를 뜻하게 되어 조사 판단이 어긋난다.
func TestFunctionCollectorKeepsLastModifiedOutOfCreatedAt(t *testing.T) {
	t.Parallel()

	api := &fakeListFunctions{pages: []*awslambda.ListFunctionsOutput{{
		Functions: []lambdatypes.FunctionConfiguration{{
			FunctionName: aws.String("worker"),
			LastModified: aws.String("2025-03-04T05:06:07.000+0000"),
		}},
	}}}

	got, err := lambdacollector.NewFunction(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got[0].CreatedAt != nil {
		t.Errorf("CreatedAt = %v, want nil", got[0].CreatedAt)
	}
	if value := got[0].FieldValue("LastModified"); value != "2025-03-04T05:06:07.000+0000" {
		t.Errorf("LastModified = %q, API 값이 그대로 남아야 한다", value)
	}
}

// TestFunctionCollectorHandlesMissingOptionalValues는 선택적 값이 없어도 변환이 죽지 않고
// nil과 실제 0을 구분하는지 확인한다.
func TestFunctionCollectorHandlesMissingOptionalValues(t *testing.T) {
	t.Parallel()

	api := &fakeListFunctions{pages: []*awslambda.ListFunctionsOutput{{
		Functions: []lambdatypes.FunctionConfiguration{
			{FunctionName: aws.String("bare")},
			{FunctionName: aws.String("explicit-zero"), Timeout: aws.Int32(0)},
		},
	}}}

	got, err := lambdacollector.NewFunction(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// VpcConfig가 없으면 네트워크 관계를 만들지 않는다. VPC 밖에서 도는 함수가 기본이다.
	if len(got[0].Related) != 0 {
		t.Errorf("VpcConfig 없는 함수의 관계 = %+v, want 없음", got[0].Related)
	}
	for _, key := range []string{"Runtime", "MemorySize", "Timeout", "EphemeralStorage"} {
		if value := got[0].FieldValue(key); value != "-" {
			t.Errorf("값이 없는 %s = %q, want %q", key, value, "-")
		}
	}

	if value := got[1].FieldValue("Timeout"); value != "0" {
		t.Errorf("명시된 0의 Timeout = %q, want %q", value, "0")
	}
}

// TestFunctionCollectorDoesNotFetchTags는 태그를 얻으려고 함수마다 추가 호출을 하지
// 않는지 확인한다.
//
// ListFunctions 응답에는 태그가 없다. 함수마다 ListTags를 부르면 N+1 조회가 되어 함수가
// 많은 계정에서 스로틀링에 걸린다. 태그가 필요해지면 그때 팬아웃으로 넣되 이 선택을 뒤집는
// 것임을 알고 해야 한다.
func TestFunctionCollectorDoesNotFetchTags(t *testing.T) {
	t.Parallel()

	api := &fakeListFunctions{pages: []*awslambda.ListFunctionsOutput{{
		Functions: []lambdatypes.FunctionConfiguration{
			{FunctionName: aws.String("a")},
			{FunctionName: aws.String("b")},
			{FunctionName: aws.String("c")},
		},
	}}}

	got, err := lambdacollector.NewFunction(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if api.calls != 1 {
		t.Errorf("SDK 호출 = %d회, want 1 (함수 수와 무관해야 한다)", api.calls)
	}
	for _, function := range got {
		if len(function.Tags) != 0 {
			t.Errorf("%s에 태그가 있다: %+v", function.ID, function.Tags)
		}
	}
}

func TestFunctionCollectorFollowsPagination(t *testing.T) {
	t.Parallel()

	api := &fakeListFunctions{pages: []*awslambda.ListFunctionsOutput{
		{
			Functions:  []lambdatypes.FunctionConfiguration{{FunctionName: aws.String("fn-1")}},
			NextMarker: aws.String("page2"),
		},
		{Functions: []lambdatypes.FunctionConfiguration{{FunctionName: aws.String("fn-2")}}},
	}}

	got, err := lambdacollector.NewFunction(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 || api.calls != 2 {
		t.Errorf("함수 %d개(호출 %d회), want 2개(2회)", len(got), api.calls)
	}
}

// TestFunctionCollectorKeepsPartialResultsOnPaginationError는 페이지 중간 실패에도 앞
// 페이지 결과를 살리는지 확인한다. 절반이라도 보여주는 편이 낫다.
func TestFunctionCollectorKeepsPartialResultsOnPaginationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("TooManyRequestsException")
	api := &fakeListFunctions{
		pages: []*awslambda.ListFunctionsOutput{{
			Functions:  []lambdatypes.FunctionConfiguration{{FunctionName: aws.String("fn-1")}},
			NextMarker: aws.String("page2"),
		}},
		errs: []error{nil, wantErr},
	}

	got, err := lambdacollector.NewFunction(api).Collect(
		context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v로 감싼 오류", err, wantErr)
	}
	if len(got) != 1 || got[0].ID != "fn-1" {
		t.Errorf("부분 결과 = %+v, want fn-1", got)
	}
}

func TestFunctionCollectorStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	api := &fakeListFunctions{errs: []error{context.Canceled}}
	if _, err := lambdacollector.NewFunction(api).Collect(
		ctx, collect.Request{Scope: testScope()}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
