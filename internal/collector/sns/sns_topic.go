// Package sns는 SNS 리소스를 조회해 도메인 모델로 바꾼다.
//
// SNS는 다른 수집기와 같은 "목록 조회 + 항목별 상세 조회"(N+1) 형태다. ListTopics는 토픽
// ARN만 주고 표시 이름·구독 수·암호화 키는 GetTopicAttributes로 다시 물어야 한다. 그래서
// [collect.FanOut]으로 상한 있는 팬아웃을 쓴다.
package sns

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// topicAPI는 토픽 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// ListTopics는 토픽 ARN 목록을, GetTopicAttributes는 ARN 하나의 속성 맵을 준다. 클라이언트
// 전체가 아니라 이 둘만 받으므로 자격증명 없이 fake로 테스트할 수 있다.
type topicAPI interface {
	ListTopics(context.Context, *awssns.ListTopicsInput, ...func(*awssns.Options)) (*awssns.ListTopicsOutput, error)
	GetTopicAttributes(context.Context, *awssns.GetTopicAttributesInput, ...func(*awssns.Options)) (*awssns.GetTopicAttributesOutput, error)
}

// topicCollector는 SNS 토픽을 조회한다.
type topicCollector struct {
	api topicAPI
	// limit은 GetTopicAttributes 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	limit int
}

// NewTopic은 SNS 토픽 수집기를 만든다.
func NewTopic(api topicAPI) collect.Collector {
	return topicCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c topicCollector) Type() string { return model.TypeSNSTopic }

// describedTopic은 팬아웃 결과를 토픽 ARN과 속성 맵으로 함께 나른다.
//
// GetTopicAttributes 응답에는 ARN이 없어 목록 단계의 ARN을 붙여 내려보낸다.
type describedTopic struct {
	arn        string
	attributes map[string]string
}

// Collect는 리전의 SNS 토픽을 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListTopics로 토픽 ARN 목록을 받는다(페이지네이션).
//  2. ARN마다 GetTopicAttributes를 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 ARN으로 계속 진행한다. 상세 조회 하나가
// 실패해도 나머지는 살린다. 부분 실패는 모두 수집한 리소스와 함께 반환된다.
func (c topicCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	arns, listErr := c.topicARNs(ctx)
	if len(arns) == 0 {
		return nil, listErr
	}

	described, describeErr := collect.FanOut(ctx, c.limit, arns,
		func(ctx context.Context, arn string) (describedTopic, error) {
			out, err := c.api.GetTopicAttributes(ctx, &awssns.GetTopicAttributesInput{
				TopicArn: aws.String(arn),
			})
			if err != nil {
				return describedTopic{}, fmt.Errorf("get topic attributes (%s): %w", arn, err)
			}

			return describedTopic{arn: arn, attributes: out.Attributes}, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, topic := range described {
		out = append(out, topicToResource(req.Scope, topic))
	}

	return out, errors.Join(listErr, describeErr)
}

// topicARNs는 리전의 토픽 ARN 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다.
func (c topicCollector) topicARNs(ctx context.Context) ([]string, error) {
	paginator := awssns.NewListTopicsPaginator(c.api, &awssns.ListTopicsInput{})

	var arns []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return arns, fmt.Errorf("list topics: %w", err)
		}

		for _, topic := range page.Topics {
			arns = append(arns, aws.ToString(topic.TopicArn))
		}
	}

	return arns, nil
}

// topicToResource는 토픽 ARN과 속성 맵을 도메인 리소스로 변환한다.
//
// 이름은 ARN의 마지막 부분(토픽 이름)을 쓴다. 속성 맵의 값은 AWS가 준 그대로 담는다. 관계
// 이름에는 값을 꺼낸 속성 키를 넣는다.
func topicToResource(scope collect.Scope, topic describedTopic) model.Resource {
	attr := topic.attributes

	var refs []model.Ref
	refs = appendARNRef(refs, model.TypeKMSKey, "KmsMasterKeyId", attr["KmsMasterKeyId"])

	return model.Resource{
		Type:      model.TypeSNSTopic,
		ID:        topic.arn,
		Name:      arnName(topic.arn),
		ARN:       topic.arn,
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Fields: []model.Field{
			{Key: "DisplayName", Value: orDash(attr["DisplayName"])},
			{Key: "Owner", Value: orDash(attr["Owner"])},
			{Key: "SubscriptionsConfirmed", Value: orDash(attr["SubscriptionsConfirmed"])},
			{Key: "SubscriptionsPending", Value: orDash(attr["SubscriptionsPending"])},
			{Key: "SubscriptionsDeleted", Value: orDash(attr["SubscriptionsDeleted"])},
			{Key: "FifoTopic", Value: orDash(attr["FifoTopic"])},
			{Key: "KmsMasterKeyId", Value: orDash(attr["KmsMasterKeyId"])},
		},
		Related: refs,
	}
}

// arnName은 ARN의 마지막 콜론/슬래시 뒤 부분을 이름으로 뽑는다.
//
// SNS 토픽 ARN은 arn:aws:sns:region:account:topic-name 형태라 마지막 콜론 뒤가 이름이다.
// 이름을 못 뽑으면 ARN 전체를 반환한다.
func arnName(arn string) string {
	if idx := strings.LastIndexAny(arn, ":/"); idx >= 0 {
		return arn[idx+1:]
	}

	return arn
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
// 관계 이름(relation)에는 값을 꺼낸 SDK 응답 필드 경로를 그대로 넣는다. SNS의 KmsMasterKeyId는
// 키 ID나 alias, ARN 중 하나가 올 수 있으나, cloudloupe는 값을 그대로 담아 ARN 참조로 색인한다.
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
