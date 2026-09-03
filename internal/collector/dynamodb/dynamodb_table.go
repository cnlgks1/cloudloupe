// Package dynamodb는 DynamoDB 리소스를 조회해 도메인 모델로 바꾼다.
//
// DynamoDB는 ECS·EKS와 같은 "목록 조회 + 항목별 상세 조회"(N+1) 형태다. ListTables는
// 테이블 이름만 주고 상태·용량·암호화는 DescribeTable로 다시 물어야 한다. 그래서
// [collect.FanOut]으로 상한 있는 팬아웃을 쓴다.
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// tableAPI는 테이블 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListTables는 테이블 이름 목록을, DescribeTable은 이름 하나의 상세를 준다. 클라이언트
// 전체가 아니라 이 둘만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type tableAPI interface {
	ListTables(context.Context, *awsddb.ListTablesInput, ...func(*awsddb.Options)) (*awsddb.ListTablesOutput, error)
	DescribeTable(context.Context, *awsddb.DescribeTableInput, ...func(*awsddb.Options)) (*awsddb.DescribeTableOutput, error)
}

// tableCollector는 DynamoDB 테이블을 조회한다.
type tableCollector struct {
	api tableAPI
	// limit은 DescribeTable 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewTable은 DynamoDB 테이블 수집기를 만든다.
func NewTable(api tableAPI) collect.Collector {
	return tableCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c tableCollector) Type() string { return model.TypeDynamoDBTable }

// Collect는 리전의 DynamoDB 테이블을 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListTables로 테이블 이름 목록을 받는다(페이지네이션).
//  2. 이름마다 DescribeTable을 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 이름으로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c tableCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	names, listErr := c.tableNames(ctx)
	if len(names) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, names,
		func(ctx context.Context, name string) (*ddbtypes.TableDescription, error) {
			out, err := c.api.DescribeTable(ctx, &awsddb.DescribeTableInput{
				TableName: aws.String(name),
			})
			if err != nil {
				return nil, fmt.Errorf("describe table (%s): %w", name, err)
			}

			return out.Table, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, table := range described {
		if table == nil {
			continue
		}

		out = append(out, tableToResource(req.Scope, *table))
	}

	return out, errors.Join(listErr, describeErr)
}

// tableNames는 리전의 테이블 이름 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c tableCollector) tableNames(ctx context.Context) ([]string, error) {
	paginator := awsddb.NewListTablesPaginator(c.api, &awsddb.ListTablesInput{})

	var names []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return names, fmt.Errorf("list tables: %w", err)
		}

		names = append(names, page.TableNames...)
	}

	return names, nil
}

// tableToResource는 SDK 테이블 상세를 도메인 리소스로 변환한다.
//
// ID·이름은 TableName, ARN은 TableArn을 그대로 쓴다. SSE가 KMS면 KMSMasterKeyArn에서 키
// 관계를 만든다. 관계 이름에는 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func tableToResource(scope collect.Scope, table ddbtypes.TableDescription) model.Resource {
	var refs []model.Ref

	if table.SSEDescription != nil {
		refs = appendARNRef(refs, model.TypeKMSKey, "SSEDescription.KMSMasterKeyArn", aws.ToString(table.SSEDescription.KMSMasterKeyArn))
	}

	return model.Resource{
		Type:      model.TypeDynamoDBTable,
		ID:        aws.ToString(table.TableName),
		Name:      aws.ToString(table.TableName),
		ARN:       aws.ToString(table.TableArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(table.TableStatus),
		CreatedAt: table.CreationDateTime,
		Fields: []model.Field{
			{Key: "TableStatus", Value: orDash(string(table.TableStatus))},
			{Key: "KeySchema", Value: orDash(keySchema(table.KeySchema))},
			{Key: "BillingMode", Value: orDash(billingMode(table.BillingModeSummary))},
			{Key: "ItemCount", Value: int64PtrValue(table.ItemCount)},
			{Key: "TableSizeBytes", Value: int64PtrValue(table.TableSizeBytes)},
			{Key: "ReadCapacityUnits", Value: readCapacity(table.ProvisionedThroughput)},
			{Key: "WriteCapacityUnits", Value: writeCapacity(table.ProvisionedThroughput)},
			{Key: "GlobalSecondaryIndexes", Value: globalSecondaryIndexes(table.GlobalSecondaryIndexes)},
			{Key: "SSEType", Value: orDash(sseType(table.SSEDescription))},
		},
		Related: refs,
	}
}

// keySchema는 기본 키 스키마를 "속성명(키타입)" 형태로 API 값 그대로 잇는다.
//
// 파티션 키(HASH)와 정렬 키(RANGE)가 테이블을 이해하는 핵심이라 목록에서 바로 보여준다.
func keySchema(elements []ddbtypes.KeySchemaElement) string {
	parts := make([]string, 0, len(elements))
	for _, e := range elements {
		parts = append(parts, fmt.Sprintf("%s(%s)", aws.ToString(e.AttributeName), string(e.KeyType)))
	}

	return strings.Join(parts, ", ")
}

// billingMode는 청구 모드를 API 값 그대로 반환한다. 설정이 없으면 빈 문자열이다.
func billingMode(summary *ddbtypes.BillingModeSummary) string {
	if summary == nil {
		return ""
	}

	return string(summary.BillingMode)
}

// readCapacity는 프로비저닝 읽기 용량을 API 값 그대로 표시한다. 온디맨드면 설정이 없어 "-"다.
func readCapacity(pt *ddbtypes.ProvisionedThroughputDescription) string {
	if pt == nil {
		return "-"
	}

	return int64PtrValue(pt.ReadCapacityUnits)
}

// writeCapacity는 프로비저닝 쓰기 용량을 API 값 그대로 표시한다. 온디맨드면 설정이 없어 "-"다.
func writeCapacity(pt *ddbtypes.ProvisionedThroughputDescription) string {
	if pt == nil {
		return "-"
	}

	return int64PtrValue(pt.WriteCapacityUnits)
}

// globalSecondaryIndexes는 GSI 이름을 API 값 그대로 콤마로 잇는다.
func globalSecondaryIndexes(indexes []ddbtypes.GlobalSecondaryIndexDescription) string {
	names := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		names = append(names, aws.ToString(idx.IndexName))
	}

	return orDash(strings.Join(names, ", "))
}

// sseType은 서버 측 암호화 종류를 API 값 그대로 반환한다. 설정이 없으면 빈 문자열이다.
func sseType(sse *ddbtypes.SSEDescription) string {
	if sse == nil {
		return ""
	}

	return string(sse.SSEType)
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// int64PtrValue는 선택적인 정수 값을 API가 준 단위 그대로 표시한다. nil이면 "-"다.
func int64PtrValue(value *int64) string {
	if value == nil {
		return "-"
	}

	return strconv.FormatInt(*value, 10)
}

// appendARNRef는 비어 있지 않은 ARN 관계를 추가한다.
//
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다.
func appendARNRef(refs []model.Ref, typeID, relation, arn string) []model.Ref {
	if arn == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:           typeID,
		ID:             arn,
		IdentifierKind: model.IdentifierARN,
		Relation:       relation,
	})
}
