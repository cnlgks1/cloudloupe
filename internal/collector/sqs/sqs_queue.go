// Package sqs는 SQS 리소스를 조회해 도메인 모델로 바꾼다.
//
// SQS는 다른 수집기와 같은 "목록 조회 + 항목별 상세 조회"(N+1) 형태다. ListQueues는 큐 URL만
// 주고 메시지 수·가시성 타임아웃·암호화 키는 GetQueueAttributes로 다시 물어야 한다. 그래서
// [collect.FanOut]으로 상한 있는 팬아웃을 쓴다.
package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// queueAPI는 큐 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListQueues는 큐 URL 목록을, GetQueueAttributes는 URL 하나의 속성 맵을 준다. 클라이언트
// 전체가 아니라 이 둘만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type queueAPI interface {
	ListQueues(context.Context, *awssqs.ListQueuesInput, ...func(*awssqs.Options)) (*awssqs.ListQueuesOutput, error)
	GetQueueAttributes(context.Context, *awssqs.GetQueueAttributesInput, ...func(*awssqs.Options)) (*awssqs.GetQueueAttributesOutput, error)
}

// queueCollector는 SQS 큐를 조회한다.
type queueCollector struct {
	api queueAPI
	// limit은 GetQueueAttributes 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewQueue는 SQS 큐 수집기를 만든다.
func NewQueue(api queueAPI) collect.Collector {
	return queueCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c queueCollector) Type() string { return model.TypeSQSQueue }

// describedQueue는 팬아웃 결과를 큐 URL과 속성 맵으로 함께 나른다.
type describedQueue struct {
	url        string
	attributes map[string]string
}

// Collect는 리전의 SQS 큐를 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListQueues로 큐 URL 목록을 받는다(페이지네이션).
//  2. URL마다 GetQueueAttributes(All)를 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 URL로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c queueCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	urls, listErr := c.queueURLs(ctx)
	if len(urls) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, urls,
		func(ctx context.Context, url string) (describedQueue, error) {
			out, err := c.api.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
				QueueUrl:       aws.String(url),
				AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
			})
			if err != nil {
				return describedQueue{}, fmt.Errorf("get queue attributes (%s): %w", url, err)
			}

			return describedQueue{url: url, attributes: out.Attributes}, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, queue := range described {
		out = append(out, queueToResource(req.Scope, queue))
	}

	return out, errors.Join(listErr, describeErr)
}

// queueURLs는 리전의 큐 URL 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c queueCollector) queueURLs(ctx context.Context) ([]string, error) {
	paginator := awssqs.NewListQueuesPaginator(c.api, &awssqs.ListQueuesInput{})

	var urls []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return urls, fmt.Errorf("list queues: %w", err)
		}

		urls = append(urls, page.QueueUrls...)
	}

	return urls, nil
}

// queueToResource는 큐 URL과 속성 맵을 도메인 리소스로 변환한다.
//
// 이름은 URL의 마지막 경로 부분(큐 이름)을 쓰고, ARN은 QueueArn 속성을 쓴다. 속성 맵의 값은
// AWS가 준 그대로 담는다. 관계 이름에는 값을 꺼낸 속성 키/JSON 경로를 넣는다.
func queueToResource(scope collect.Scope, queue describedQueue) model.Resource {
	attr := queue.attributes

	var refs []model.Ref
	refs = appendARNRef(refs, model.TypeKMSKey, "KmsMasterKeyId", attr["KmsMasterKeyId"])
	refs = appendARNRef(refs, model.TypeSQSQueue, "RedrivePolicy.deadLetterTargetArn", deadLetterTargetARN(attr["RedrivePolicy"]))

	return model.Resource{
		Type:      model.TypeSQSQueue,
		ID:        queueName(queue.url),
		Name:      queueName(queue.url),
		ARN:       attr["QueueArn"],
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "QueueArn", Value: orDash(attr["QueueArn"])},
			{Key: "FifoQueue", Value: orDash(attr["FifoQueue"])},
			{Key: "ApproximateNumberOfMessages", Value: orDash(attr["ApproximateNumberOfMessages"])},
			{Key: "ApproximateNumberOfMessagesNotVisible", Value: orDash(attr["ApproximateNumberOfMessagesNotVisible"])},
			{Key: "VisibilityTimeout", Value: orDash(attr["VisibilityTimeout"])},
			{Key: "MessageRetentionPeriod", Value: orDash(attr["MessageRetentionPeriod"])},
			{Key: "DelaySeconds", Value: orDash(attr["DelaySeconds"])},
			{Key: "KmsMasterKeyId", Value: orDash(attr["KmsMasterKeyId"])},
			{Key: "RedrivePolicy", Value: orDash(attr["RedrivePolicy"])},
		},
		Related: refs,
	}
}

// queueName은 큐 URL의 마지막 경로 부분을 이름으로 뽑는다.
//
// SQS 큐 URL은 https://sqs.region.amazonaws.com/account/queue-name 형태라 마지막 슬래시 뒤가
// 큐 이름이다. 뽑지 못하면 URL 전체를 반환한다.
func queueName(url string) string {
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		return url[idx+1:]
	}

	return url
}

// deadLetterTargetARN은 RedrivePolicy JSON에서 데드레터 큐 ARN을 꺼낸다.
//
// RedrivePolicy는 {"deadLetterTargetArn":"...","maxReceiveCount":N} 형태의 JSON 문자열이다.
// 파싱에 실패하거나 필드가 없으면 빈 문자열을 반환해 관계를 만들지 않는다.
func deadLetterTargetARN(policy string) string {
	if policy == "" {
		return ""
	}

	var parsed struct {
		DeadLetterTargetArn string `json:"deadLetterTargetArn"`
	}
	if err := json.Unmarshal([]byte(policy), &parsed); err != nil {
		return ""
	}

	return parsed.DeadLetterTargetArn
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
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
