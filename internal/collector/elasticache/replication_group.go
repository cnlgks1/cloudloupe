// Package elasticache는 ElastiCache 리소스를 조회해 도메인 모델로 바꾼다.
//
// ElastiCache는 Describe 한 번으로 상세까지 주므로 항목별 상세 팬아웃이 필요 없다. Redis는
// 복제 그룹(replication group)으로, Memcached와 단일 노드 Redis는 캐시 클러스터(cache
// cluster)로 나타난다. 두 타입을 각각 둔다.
package elasticache

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// replicationGroupAPI는 복제 그룹 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
type replicationGroupAPI interface {
	DescribeReplicationGroups(context.Context, *awsec.DescribeReplicationGroupsInput, ...func(*awsec.Options)) (*awsec.DescribeReplicationGroupsOutput, error)
}

// replicationGroupCollector는 ElastiCache 복제 그룹을 조회한다.
type replicationGroupCollector struct {
	api replicationGroupAPI
}

// NewReplicationGroup은 ElastiCache 복제 그룹 수집기를 만든다.
func NewReplicationGroup(api replicationGroupAPI) collect.Collector {
	return replicationGroupCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c replicationGroupCollector) Type() string { return model.TypeElastiCacheReplicationGroup }

// Collect는 리전의 복제 그룹을 모두 조회해 도메인 리소스로 변환한다.
//
// DescribeReplicationGroups 페이지네이션만 돈다. 페이지 하나가 실패하면 그때까지 변환한
// 리소스를 오류와 함께 반환해 부분 결과를 살린다.
func (c replicationGroupCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsec.NewDescribeReplicationGroupsPaginator(c.api, &awsec.DescribeReplicationGroupsInput{})

	var out []model.Resource

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("describe replication groups: %w", err)
		}

		for i := range page.ReplicationGroups {
			out = append(out, replicationGroupToResource(req.Scope, page.ReplicationGroups[i]))
		}
	}

	return out, nil
}

// replicationGroupToResource는 SDK 복제 그룹을 도메인 리소스로 변환한다.
//
// ID·이름은 ReplicationGroupId, ARN은 ARN을 그대로 쓴다. 고객 관리 KMS 키로 암호화하면
// KmsKeyId에서 키 관계를, MemberClusters에서 소속 캐시 클러스터 관계를 만든다. 관계 이름에는
// 값을 꺼낸 SDK 응답 필드 경로를 넣는다.
func replicationGroupToResource(scope collect.Scope, group ectypes.ReplicationGroup) model.Resource {
	var refs []model.Ref

	refs = appendARNRef(refs, model.TypeKMSKey, "KmsKeyId", aws.ToString(group.KmsKeyId))

	for _, member := range group.MemberClusters {
		refs = appendIDRef(refs, model.TypeElastiCacheCacheCluster, "MemberClusters", member)
	}

	return model.Resource{
		Type:      model.TypeElastiCacheReplicationGroup,
		ID:        aws.ToString(group.ReplicationGroupId),
		Name:      aws.ToString(group.ReplicationGroupId),
		ARN:       aws.ToString(group.ARN),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(group.Status),
		Fields: []model.Field{
			{Key: "Status", Value: orDash(aws.ToString(group.Status))},
			{Key: "Description", Value: orDash(aws.ToString(group.Description))},
			{Key: "Engine", Value: orDash(aws.ToString(group.Engine))},
			{Key: "CacheNodeType", Value: orDash(aws.ToString(group.CacheNodeType))},
			{Key: "MemberClusters", Value: orDash(strings.Join(group.MemberClusters, ", "))},
			{Key: "MultiAZ", Value: orDash(string(group.MultiAZ))},
			{Key: "AutomaticFailover", Value: orDash(string(group.AutomaticFailover))},
			{Key: "AtRestEncryptionEnabled", Value: boolPtrValue(group.AtRestEncryptionEnabled)},
			{Key: "TransitEncryptionEnabled", Value: boolPtrValue(group.TransitEncryptionEnabled)},
			{Key: "KmsKeyId", Value: orDash(aws.ToString(group.KmsKeyId))},
		},
		Related: refs,
	}
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}

// boolPtrValue는 선택적인 불리언 값을 API 값 그대로 표시한다. nil이면 "-"다.
func boolPtrValue(value *bool) string {
	if value == nil {
		return "-"
	}

	if *value {
		return "true"
	}

	return "false"
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

// appendIDRef는 비어 있지 않은 리소스 ID 관계를 추가한다.
func appendIDRef(refs []model.Ref, typeID, relation, id string) []model.Ref {
	if id == "" {
		return refs
	}

	return append(refs, model.Ref{
		Type:     typeID,
		ID:       id,
		Relation: relation,
	})
}
