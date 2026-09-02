// Package rds는 RDS DB 클러스터와 DB 인스턴스를 조회해 도메인 모델로 바꾼다.
package rds

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// rdsTags는 SDK 태그의 포인터를 값으로 바꾸고 키 순으로 정렬한다.
func rdsTags(tags []rdstypes.Tag) []model.Field {
	values := make(map[string]string, len(tags))
	for _, tag := range tags {
		values[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	return model.TagFields(values)
}

// orDash는 빈 문자열을 상세 화면에서 사용하는 없음 표기로 바꾼다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// stringListValue는 문자열 목록을 API 순서대로 표시 값으로 바꾼다.
func stringListValue(values []string) string {
	return orDash(strings.Join(values, ", "))
}

// int32Value는 SDK 정수 포인터를 값으로 바꾸되 nil과 실제 0을 구분한다.
func int32Value(value *int32) string {
	if value == nil {
		return "-"
	}

	return strconv.FormatInt(int64(aws.ToInt32(value)), 10)
}

// boolValue는 SDK 불리언 포인터를 값으로 바꾸되 nil과 false를 구분한다.
func boolValue(value *bool) string {
	if value == nil {
		return "-"
	}

	return strconv.FormatBool(aws.ToBool(value))
}

// endpointValue는 SDK 엔드포인트를 주소와 포트가 포함된 표시 값으로 바꾼다.
func endpointValue(endpoint *rdstypes.Endpoint) string {
	if endpoint == nil {
		return "-"
	}

	address := aws.ToString(endpoint.Address)
	if address == "" {
		return "-"
	}
	if endpoint.Port == nil {
		return address
	}

	return net.JoinHostPort(address, strconv.FormatInt(int64(aws.ToInt32(endpoint.Port)), 10))
}

// utcTime은 SDK 시각 포인터를 UTC 도메인 값으로 복사한다.
func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	converted := value.UTC()
	return &converted
}

// appendIDRelation은 빈 ID를 제외하고 ID 기반 관계를 추가한다.
func appendIDRelation(refs []model.Ref, resourceType, id string) []model.Ref {
	if id == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:     resourceType,
		ID:       id,
		Relation: model.RelationAssociatedWith,
	})
}

// appendKMSRelation은 RDS가 반환한 KMS 식별자 형식에 맞춰 관계를 추가한다.
//
// RDS는 key ARN뿐 아니라 key ID, alias ARN, alias name도 반환할 수 있다. 현재 KMS 모델은
// key ID와 key ARN만 색인하므로 그 둘만 관계로 만들고, alias는 RDS의 KmsKeyId 필드에만
// 보존한다. alias를 ARN으로 잘못 분류하면 존재해도 해석할 수 없는 관계가 생긴다.
func appendKMSRelation(refs []model.Ref, identifier string) []model.Ref {
	if identifier == "" || strings.HasPrefix(identifier, "alias/") || strings.Contains(identifier, ":alias/") {
		return refs
	}

	ref := model.Ref{
		Type:     model.TypeKMSKey,
		ID:       identifier,
		Relation: model.RelationAssociatedWith,
	}
	if strings.HasPrefix(identifier, "arn:") {
		ref.IdentifierKind = model.IdentifierARN
	}

	return append(refs, ref)
}

// dnsIdentifiers는 빈 주소를 제외하고 DNS 보조 식별자를 만든다.
func dnsIdentifiers(values ...string) []model.Identifier {
	identifiers := make([]model.Identifier, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}

		identifiers = append(identifiers, model.Identifier{Kind: model.IdentifierDNS, Value: value})
	}

	return identifiers
}
