package apigateway_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsapigw "github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	apigwcollector "github.com/cnlgks1/cloudloupe/internal/collector/apigateway"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// fakeRestAPI는 REST API 수집기가 쓰는 GetRestApis를 대신한다.
//
// pages는 GetRestApis의 페이지들, pageErr는 마지막 페이지 뒤에 낼 오류다.
type fakeRestAPI struct {
	pages   [][]apigwtypes.RestApi
	pageErr error

	calls int
}

func (f *fakeRestAPI) GetRestApis(
	_ context.Context,
	_ *awsapigw.GetRestApisInput,
	_ ...func(*awsapigw.Options),
) (*awsapigw.GetRestApisOutput, error) {
	i := f.calls
	f.calls++

	if i >= len(f.pages) {
		if f.pageErr != nil {
			return nil, f.pageErr
		}

		return &awsapigw.GetRestApisOutput{}, nil
	}

	out := &awsapigw.GetRestApisOutput{Items: f.pages[i]}
	if i+1 < len(f.pages) || f.pageErr != nil {
		out.Position = aws.String("next")
	}

	return out, nil
}

func testScope() collect.Scope {
	return collect.Scope{Profile: "prod", Region: "ap-northeast-2", AccountID: "123456789012"}
}

func TestRestAPICollectorType(t *testing.T) {
	t.Parallel()

	if got := apigwcollector.NewRestAPI(&fakeRestAPI{}).Type(); got != model.TypeAPIGatewayRestAPI {
		t.Errorf("Type() = %q, want %q", got, model.TypeAPIGatewayRestAPI)
	}
}

// TestRestAPICollectConvertsFieldsAndVPCEndpoint는 SDK 값을 그대로 담고 프라이빗
// 엔드포인트면 VPC 엔드포인트 관계를 만드는지 확인한다.
func TestRestAPICollectConvertsFieldsAndVPCEndpoint(t *testing.T) {
	t.Parallel()

	created := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	api := &fakeRestAPI{
		pages: [][]apigwtypes.RestApi{{
			{
				Id:           aws.String("abc123"),
				Name:         aws.String("orders-api"),
				Description:  aws.String("주문 API"),
				Version:      aws.String("v1"),
				CreatedDate:  &created,
				ApiKeySource: apigwtypes.ApiKeySourceTypeHeader,
				EndpointConfiguration: &apigwtypes.EndpointConfiguration{
					Types:          []apigwtypes.EndpointType{apigwtypes.EndpointTypePrivate},
					VpcEndpointIds: []string{"vpce-1", "vpce-2"},
				},
			},
		}},
	}

	got, err := apigwcollector.NewRestAPI(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("REST API %d개 수집, want 1", len(got))
	}

	res := got[0]
	if res.ID != "abc123" || res.Name != "orders-api" {
		t.Errorf("ID/Name = %q/%q", res.ID, res.Name)
	}
	if res.CreatedAt == nil || !res.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", res.CreatedAt, created)
	}
	// 값은 AWS가 준 그대로. PRIVATE/HEADER를 번역하지 않는다.
	if got, want := res.FieldValue("EndpointConfiguration.Types"), "PRIVATE"; got != want {
		t.Errorf("EndpointTypes = %q, want %q", got, want)
	}
	if got, want := res.FieldValue("ApiKeySource"), "HEADER"; got != want {
		t.Errorf("ApiKeySource = %q, want %q", got, want)
	}

	type rel struct {
		relation string
		typ      string
		id       string
	}
	gotRels := make([]rel, 0, len(res.Related))
	for _, r := range res.Related {
		gotRels = append(gotRels, rel{r.Relation, r.Type, r.ID})
	}
	want := []rel{
		{"EndpointConfiguration.VpcEndpointIds", model.TypeEC2VPCEndpoint, "vpce-1"},
		{"EndpointConfiguration.VpcEndpointIds", model.TypeEC2VPCEndpoint, "vpce-2"},
	}
	if !slices.Equal(gotRels, want) {
		t.Errorf("관계 = %+v, want %+v", gotRels, want)
	}
}

// TestRestAPICollectRegionalHasNoVPCEndpoint는 리전 엔드포인트면 VPC 엔드포인트 관계가
// 없는지 확인한다.
func TestRestAPICollectRegionalHasNoVPCEndpoint(t *testing.T) {
	t.Parallel()

	api := &fakeRestAPI{
		pages: [][]apigwtypes.RestApi{{
			{
				Id:   aws.String("abc"),
				Name: aws.String("regional-api"),
				EndpointConfiguration: &apigwtypes.EndpointConfiguration{
					Types: []apigwtypes.EndpointType{apigwtypes.EndpointTypeRegional},
				},
			},
		}},
	}

	got, err := apigwcollector.NewRestAPI(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if err != nil {
		t.Fatalf("Collect() 실패: %v", err)
	}
	if len(got[0].Related) != 0 {
		t.Errorf("리전 엔드포인트면 관계 없음, got %+v", got[0].Related)
	}
	if got, want := got[0].FieldValue("EndpointConfiguration.Types"), "REGIONAL"; got != want {
		t.Errorf("EndpointTypes = %q, want %q", got, want)
	}
}

// TestRestAPICollectFollowsPages는 페이지네이션을 이어 받는지 확인한다.
func TestRestAPICollectFollowsPages(t *testing.T) {
	t.Parallel()

	api := &fakeRestAPI{
		pages: [][]apigwtypes.RestApi{
			{{Id: aws.String("a"), Name: aws.String("a")}},
			{{Id: aws.String("b"), Name: aws.String("b")}},
		},
	}

	got, err := apigwcollector.NewRestAPI(api).Collect(context.Background(), collect.Request{Scope: testScope()})
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
		t.Errorf("GetRestApis 호출 = %d회, want 2", api.calls)
	}
}

// TestRestAPICollectKeepsPartialOnPageError는 페이지 오류 전까지 받은 리소스를 살리는지
// 확인한다.
func TestRestAPICollectKeepsPartialOnPageError(t *testing.T) {
	t.Parallel()

	denied := errors.New("access denied")
	api := &fakeRestAPI{
		pages:   [][]apigwtypes.RestApi{{{Id: aws.String("a"), Name: aws.String("a")}}},
		pageErr: denied,
	}

	got, err := apigwcollector.NewRestAPI(api).Collect(context.Background(), collect.Request{Scope: testScope()})
	if !errors.Is(err, denied) {
		t.Fatalf("err = %v, want %v로 감싼 오류", err, denied)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("수집 결과 = %+v, want a 하나", got)
	}
}
