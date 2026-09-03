package apigateway_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsapigwv2 "github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	apigwcollector "github.com/cnlgks1/cloudloupe/internal/collector/apigateway"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeV2API는 v2 API 수집기가 쓰는 GetApis를 대신한다.
//
// pages는 GetApis의 페이지들, pageErr는 마지막 페이지 뒤에 낼 오류다. 수집기가 NextToken을
// 직접 잇는 루프를 도는지 확인하려고 페이지마다 NextToken을 채운다.
type fakeV2API struct {
	pages   [][]apigwv2types.Api
	pageErr error

	calls int
}

func (f *fakeV2API) GetApis(
	_ context.Context,
	in *awsapigwv2.GetApisInput,
	_ ...func(*awsapigwv2.Options),
) (*awsapigwv2.GetApisOutput, error) {
	// 첫 호출은 토큰이 없어야 하고, 이후 호출은 앞 페이지가 준 토큰을 그대로 되보내야 한다.
	if f.calls == 0 && in.NextToken != nil {
		return nil, errors.New("첫 호출에 NextToken이 들어왔다")
	}

	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awsapigwv2.GetApisOutput{}, nil
	}

	out := &awsapigwv2.GetApisOutput{Items: f.pages[i]}
	// 다음 페이지가 남았거나, 남은 페이지는 없지만 그 뒤에 낼 오류가 있으면 토큰을 이어
	// 수집기가 한 번 더 호출하도록 만든다.
	if i+1 < len(f.pages) || (f.pageErr != nil && i+1 == len(f.pages)) {
		out.NextToken = aws.String("next")
	}

	return out, nil
}

func TestV2APICollectorType(t *testing.T) {
	t.Parallel()

	if got := apigwcollector.NewV2API(&fakeV2API{}).Type(); got != model.TypeAPIGatewayV2API {
		t.Errorf("Type() = %q, want %q", got, model.TypeAPIGatewayV2API)
	}
}

// TestV2APICollectConvertsFields는 SDK 값을 그대로 담고 프로토콜 종류를 구분하는지
// 확인한다.
func TestV2APICollectConvertsFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	api := &fakeV2API{
		pages: [][]apigwv2types.Api{{
			{
				ApiId:                     aws.String("h1"),
				Name:                      aws.String("http-api"),
				ProtocolType:              apigwv2types.ProtocolTypeHttp,
				ApiEndpoint:               aws.String("https://h1.execute-api.ap-northeast-2.amazonaws.com"),
				Version:                   aws.String("1.0"),
				CreatedDate:               &created,
				DisableExecuteApiEndpoint: aws.Bool(false),
			},
		}},
	}

	got, err := apigwcollector.NewV2API(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("v2 API %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "h1" || res.Name != "http-api" {
		t.Errorf("ID/Name = %q/%q", res.ID, res.Name)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	// 값은 AWS가 준 그대로. HTTP를 번역하지 않는다.
	if got, want := res.FieldValue("ProtocolType"), "HTTP"; got != want {
		t.Errorf("ProtocolType = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("DisableExecuteApiEndpoint"), "false"; got != want {
		t.Errorf("DisableExecuteApiEndpoint = %q, want %q", got, want)
	}
	if len(res.Related) != 0 {
		t.Errorf("v2 API는 관계 없음, got %+v", res.Related)
	}
}

// TestV2APICollectDistinguishesMissingBool은 선택적 불리언이 없을 때 "-"로 두는지 확인한다.
func TestV2APICollectDistinguishesMissingBool(t *testing.T) {
	t.Parallel()

	api := &fakeV2API{
		pages: [][]apigwv2types.Api{{
			{ApiId: aws.String("w1"), Name: aws.String("ws-api"), ProtocolType: apigwv2types.ProtocolTypeWebsocket},
		}},
	}

	got, err := apigwcollector.NewV2API(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if v := got[0].FieldValue("DisableExecuteApiEndpoint"); v != "-" {
		t.Errorf("없는 불리언 = %q, want -", v)
	}
	if got, want := got[0].FieldValue("ProtocolType"), "WEBSOCKET"; got != want {
		t.Errorf("ProtocolType = %q, want %q", got, want)
	}
}

// TestV2APICollectFollowsNextToken은 NextToken을 직접 이어 다음 페이지를 받는지 확인한다.
func TestV2APICollectFollowsNextToken(t *testing.T) {
	t.Parallel()

	api := &fakeV2API{
		pages: [][]apigwv2types.Api{
			{{ApiId: aws.String("a"), Name: aws.String("a")}},
			{{ApiId: aws.String("b"), Name: aws.String("b")}},
		},
	}

	got, err := apigwcollector.NewV2API(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, res := range got {
		names = append(names, res.ID)
	}
	if want := []string{"a", "b"}; !slices.Equal(names, want) {
		t.Errorf("수집 결과 = %v, want %v", names, want)
	}
	if api.calls != 2 {
		t.Errorf("GetApis 호출 = %d회, want 2", api.calls)
	}
}

// TestV2APICollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestV2APICollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	// 첫 페이지는 성공하고 NextToken을 주며, 다음 호출에서 오류가 난다.
	api := &fakeV2API{
		pages:   [][]apigwv2types.Api{{{ApiId: aws.String("a"), Name: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := apigwcollector.NewV2API(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
