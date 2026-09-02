// Package kms는 KMS 키를 조회해 도메인 모델로 바꾼다.
//
// 이 패키지는 이 프로젝트에서 처음으로 "목록 조회 + 항목별 상세 조회"(N+1) 형태를 다룬다.
// ListKeys는 키 ID만 주고 상태·용도·설명은 키마다 DescribeKey를 불러야 한다. 키가 수백 개인
// 계정에서 순차 조회는 느리고 무제한 고루틴은 API 스로틀링을 부르므로, 상한 있는 팬아웃인
// [collect.FanOut]을 쓴다. S3·RDS·Lambda도 같은 형태이므로 이 파일의 구조를 본떠 추가한다.
package kms

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// keyAPI는 키 수집기가 필요로 하는 SDK 메서드만 담은 인터페이스다.
//
// 세 메서드가 필요한 이유는 KMS가 정보를 셋으로 나눠 주기 때문이다. ListKeys는 키 ID 목록,
// DescribeKey는 키 하나의 속성, ListAliases는 별칭을 준다. 별칭이 없으면 화면에 32자
// UUID만 남아 어떤 키인지 알 수 없다.
type keyAPI interface {
	ListKeys(context.Context, *awskms.ListKeysInput, ...func(*awskms.Options)) (*awskms.ListKeysOutput, error)
	DescribeKey(context.Context, *awskms.DescribeKeyInput, ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error)
	ListAliases(context.Context, *awskms.ListAliasesInput, ...func(*awskms.Options)) (*awskms.ListAliasesOutput, error)
}

// keyCollector는 KMS 키를 조회한다.
type keyCollector struct {
	api keyAPI
	// limit은 DescribeKey 팬아웃의 동시 실행 상한이다. 0이면 collect.ItemLimit을 쓴다.
	// 테스트에서 순서와 상한을 고정하려고 필드로 둔다.
	limit int
}

// NewKey는 KMS 키 수집기를 만든다.
func NewKey(api keyAPI) collect.Collector {
	return keyCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c keyCollector) Type() string { return model.TypeKMSKey }

// Collect는 리전의 KMS 키를 모두 조회해 도메인 리소스로 변환한다.
//
// 순서는 이렇다.
//  1. ListKeys로 키 ID 목록을 받는다(페이지네이션).
//  2. ListAliases로 계정·리전의 별칭을 한 번에 받아 키 ID로 색인한다. 키마다 부르지 않는다.
//  3. 키마다 DescribeKey를 상한 있는 팬아웃으로 부른다.
//
// 목록 조회가 중간에 실패하면 그때까지 받은 키로 계속 진행한다. 별칭 조회가 실패하면 별칭
// 없이 진행한다. 이름을 못 붙이는 것이 키를 아예 못 보여주는 것보다 낫다. 모든 부분 실패는
// 수집한 리소스와 함께 반환되어 화면의 오류 목록에 남는다.
func (c keyCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	entries, listErr := c.keyEntries(ctx)
	if len(entries) == 0 {
		return nil, listErr
	}

	aliases, aliasErr := c.aliasesByKeyID(ctx)

	described, describeErr := collect.FanOut(ctx, c.limit, entries,
		func(ctx context.Context, entry kmstypes.KeyListEntry) (*kmstypes.KeyMetadata, error) {
			out, err := c.api.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: entry.KeyId})
			if err != nil {
				return nil, fmt.Errorf("describe key (%s): %w", aws.ToString(entry.KeyId), err)
			}

			return out.KeyMetadata, nil
		})

	out := make([]model.Resource, 0, len(described))
	for _, metadata := range described {
		if metadata == nil {
			// DescribeKey가 성공했는데 메타데이터가 비는 경우는 없지만, nil을 그대로
			// 넘기면 변환 함수가 터진다. 조용히 건너뛴다.
			continue
		}

		out = append(out, keyToResource(req.Scope, *metadata, aliases[aws.ToString(metadata.KeyId)]))
	}

	// errors.Join은 nil을 무시하므로 세 단계의 부분 실패를 그대로 묶어 올린다. Runner가
	// 이를 펼쳐 각각의 오류 행으로 보여준다.
	return out, errors.Join(listErr, aliasErr, describeErr)
}

// keyEntries는 리전의 키 목록을 모두 받는다.
//
// 페이지 하나가 실패해도 앞서 받은 목록은 살린다. 절반의 키라도 보여주는 편이 낫다.
func (c keyCollector) keyEntries(ctx context.Context) ([]kmstypes.KeyListEntry, error) {
	paginator := awskms.NewListKeysPaginator(c.api, &awskms.ListKeysInput{})

	var entries []kmstypes.KeyListEntry

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return entries, fmt.Errorf("list keys: %w", err)
		}

		entries = append(entries, page.Keys...)
	}

	return entries, nil
}

// aliasesByKeyID는 계정·리전의 별칭을 키 ID로 색인한다.
//
// 한 키에 별칭이 여럿 붙을 수 있어 값이 슬라이스다. 표시 순서를 안정적으로 만들려고 AWS가
// 준 순서를 그대로 유지한다. map은 색인 용도로만 쓰고 표시에는 슬라이스를 쓴다.
func (c keyCollector) aliasesByKeyID(ctx context.Context) (map[string][]string, error) {
	paginator := awskms.NewListAliasesPaginator(c.api, &awskms.ListAliasesInput{})
	aliases := make(map[string][]string)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return aliases, fmt.Errorf("list aliases: %w", err)
		}

		for _, alias := range page.Aliases {
			keyID := aws.ToString(alias.TargetKeyId)
			name := aws.ToString(alias.AliasName)
			if keyID == "" || name == "" {
				continue
			}

			aliases[keyID] = append(aliases[keyID], name)
		}
	}

	return aliases, nil
}

// keyToResource는 SDK 키 메타데이터를 도메인 리소스로 변환한다.
//
// 표시 이름은 첫 별칭을 쓴다. 별칭이 없으면 DisplayName이 키 ID로 대체한다. AWS가 관리하는
// 키(aws/s3 같은 것)는 계정마다 수십 개가 자동 생성되므로, 사용자가 만든 키와 구분할 수 있게
// 관리 주체를 필드로 남긴다.
func keyToResource(scope collect.Scope, metadata kmstypes.KeyMetadata, aliases []string) model.Resource {
	keyID := aws.ToString(metadata.KeyId)

	name := ""
	if len(aliases) > 0 {
		name = aliases[0]
	}

	r := model.Resource{
		Type:      model.TypeKMSKey,
		ID:        keyID,
		Name:      name,
		ARN:       aws.ToString(metadata.Arn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    string(metadata.KeyState),
		Fields: []model.Field{
			{Key: "Aliases", Value: orDash(strings.Join(aliases, ", "))},
			{Key: "KeyManager", Value: orDash(string(metadata.KeyManager))},
			{Key: "KeyUsage", Value: orDash(string(metadata.KeyUsage))},
			{Key: "KeySpec", Value: orDash(string(metadata.KeySpec))},
			{Key: "Origin", Value: orDash(string(metadata.Origin))},
			{Key: "MultiRegion", Value: boolValue(aws.ToBool(metadata.MultiRegion))},
			{Key: "Enabled", Value: boolValue(metadata.Enabled)},
			{Key: "DeletionDate", Value: deletionSchedule(metadata)},
			{Key: "Description", Value: orDash(aws.ToString(metadata.Description))},
		},
	}

	if metadata.CreationDate != nil {
		createdAt := metadata.CreationDate.UTC()
		r.CreatedAt = &createdAt
	}

	// 태그는 키마다 ListResourceTags를 부르는 또 한 번의 N+1이 필요해 지금은 수집하지 않는다.
	// 넣을 때는 DescribeKey와 같은 팬아웃 안에서 함께 불러 호출 수를 늘리지 않는 편이 좋다.

	// 키를 쓰는 리소스(EBS 볼륨, S3 버킷 등)에서 이 키로 향하는 관계를 남기는 쪽이 자연스럽다.
	// 키에서 사용처를 찾으려면 별도 조회가 필요하고, 그 목록은 KMS가 제공하지 않는다.

	return r
}

// deletionSchedule은 삭제 예정 시각을 표시 값으로 만든다.
//
// 삭제 대기 중인 키만 이 값을 가진다. 대기 일수만 있는 경우(복제 키가 남은 다중 리전 키)도
// 있어 둘을 함께 다룬다. 그때는 API가 날짜를 주지 않으므로 일수를 그대로 적는다.
func deletionSchedule(metadata kmstypes.KeyMetadata) string {
	if metadata.DeletionDate != nil {
		return metadata.DeletionDate.UTC().Format(time.RFC3339)
	}

	if days := aws.ToInt32(metadata.PendingDeletionWindowInDays); days > 0 {
		return "PendingDeletionWindowInDays=" + strconv.Itoa(int(days))
	}

	return "-"
}

// orDash는 빈 문자열을 "-"로 바꾼다. 상세 뷰에서 빈칸 대신 없음을 명확히 보이게 한다.
func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// boolValue는 불리언을 AWS가 주는 표기로 바꾼다. 화면 값이 aws CLI 출력과 같아야 한다.
func boolValue(b bool) string {
	return strconv.FormatBool(b)
}
