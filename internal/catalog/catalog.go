// Package catalog는 cloudloupe가 지원하는 AWS 리소스 타입을 명시적으로 조립한다.
//
// 타입 ID, 사용자 표시명, 조회 범위, 목록 열과 수집기 생성자를 한 Definition에 둔다.
// 신규 리소스를 추가할 때 서로 다른 파일의 라벨·글로벌 여부·등록 목록이 어긋나지 않게
// 하는 단일 출처다. init 부수효과나 변경 가능한 전역 레지스트리는 사용하지 않는다.
package catalog

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/cnlgks1/cloudloupe/internal/collect"
)

// ScopeKind는 리소스가 리전별인지 계정 전체의 글로벌 리소스인지 나타낸다.
type ScopeKind uint8

const (
	scopeUnknown ScopeKind = iota
	// Regional은 선택한 각 리전에서 조회하는 리소스다.
	Regional
	// Global은 선택 리전 수와 관계없이 실행당 한 번만 조회하는 리소스다.
	Global
)

// Definition은 지원 리소스 타입 하나의 변경되지 않는 메타데이터다.
//
// Columns는 Resource.Fields에 들어가는 키 순서다. TUI가 첫 번째 결과의 우연한 필드
// 모양으로 열을 결정하지 않고, 결과가 없어도 안정적인 스키마를 사용하게 한다.
// SummaryColumns는 여러 타입을 한 서비스 목록에 함께 표시할 때 "주요 정보"로 압축할
// 필드다. 상세 타입을 각각 선택지로 노출하지 않으면서도 핵심 값은 목록에서 볼 수 있게 한다.
type Definition struct {
	Type           string
	Label          string
	Scope          ScopeKind
	Columns        []string
	SummaryColumns []string

	newCollector func() collect.Collector
}

// Group은 사용자가 한 번에 선택하는 큰 AWS 리소스 묶음이다.
//
// Types는 내부 수집과 관계 모델에 사용하는 세부 타입이다. TUI는 그룹만 보여주고 선택된
// 그룹의 Types를 수집기에 전달하므로, 화면을 단순하게 유지하면서 내부 타입 정밀도는 잃지 않는다.
type Group struct {
	ID    string
	Label string
	Types []Definition
}

// Definitions는 지원 타입 정의를 표시·실행 순서대로 반환한다.
//
// 호출자가 내부 상태를 바꿀 수 없도록 매번 새 슬라이스와 필드 슬라이스 복사본을 반환한다.
func Definitions() []Definition {
	definitions := allDefinitions(aws.Config{})
	out := make([]Definition, len(definitions))

	for i, definition := range definitions {
		out[i] = copyDefinition(definition)
	}

	return out
}

// Groups는 사용자가 선택할 서비스별 큰 리소스 묶음을 반환한다.
//
// 세부 타입은 그룹 안에 남아 수집기 선택과 관계 식별에 사용된다. 반환값은 호출자가
// 변경해도 카탈로그 내부 조립에 영향을 주지 않는 방어적 복사본이다.
func Groups() ([]Group, error) {
	groups := allGroups(aws.Config{})
	if err := validateGroups(groups); err != nil {
		return nil, fmt.Errorf("리소스 그룹 검증: %w", err)
	}

	out := make([]Group, len(groups))
	for i, group := range groups {
		out[i] = Group{ID: group.ID, Label: group.Label, Types: make([]Definition, len(group.Types))}
		for j, definition := range group.Types {
			out[i].Types[j] = copyDefinition(definition)
		}
	}

	return out, nil
}

func copyDefinition(definition Definition) Definition {
	definition.Columns = append([]string(nil), definition.Columns...)
	definition.SummaryColumns = append([]string(nil), definition.SummaryColumns...)

	return definition
}

// Registry는 AWS 설정과 선택 타입으로 실행 가능한 수집기 레지스트리를 만든다.
//
// includeGlobal이 false면 글로벌 타입은 제외한다. selected가 비면 지원 타입을 모두
// 포함하며, 알 수 없는 타입은 unknown으로 반환해 호출자가 사용자에게 알려줄 수 있다.
func Registry(cfg aws.Config, includeGlobal bool, selected []string) (*collect.Registry, []string, error) {
	groups := allGroups(cfg)
	if err := validateGroups(groups); err != nil {
		return nil, nil, fmt.Errorf("리소스 그룹 검증: %w", err)
	}

	return buildRegistry(flattenGroups(groups), includeGlobal, selected)
}

func buildRegistry(definitions []Definition, includeGlobal bool, selected []string) (*collect.Registry, []string, error) {
	if err := validateDefinitions(definitions); err != nil {
		return nil, nil, fmt.Errorf("카탈로그 검증: %w", err)
	}

	known := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		known[definition.Type] = struct{}{}
	}

	var unknown []string
	for _, typ := range selected {
		if _, ok := known[typ]; !ok {
			unknown = append(unknown, typ)
		}
	}

	wanted := make(map[string]struct{}, len(selected))
	for _, typ := range selected {
		wanted[typ] = struct{}{}
	}

	registry := collect.NewRegistry()

	for _, definition := range definitions {
		if definition.Scope == Global && !includeGlobal {
			continue
		}

		if len(wanted) > 0 {
			if _, ok := wanted[definition.Type]; !ok {
				continue
			}
		}

		collector := definition.newCollector()
		if collector == nil {
			return nil, unknown, fmt.Errorf("%s 수집기 생성: nil 반환", definition.Type)
		}
		if collector.Type() != definition.Type {
			return nil, unknown, fmt.Errorf("%s 수집기 생성: 타입 불일치 (%s)", definition.Type, collector.Type())
		}

		if err := registry.Add(collector); err != nil {
			return nil, unknown, fmt.Errorf("%s 수집기 등록: %w", definition.Type, err)
		}
	}

	return registry, unknown, nil
}

func validateDefinitions(definitions []Definition) error {
	seenTypes := make(map[string]struct{}, len(definitions))

	for i, definition := range definitions {
		if strings.TrimSpace(definition.Type) == "" {
			return fmt.Errorf("정의 %d: 타입이 비어 있음", i)
		}
		if _, exists := seenTypes[definition.Type]; exists {
			return fmt.Errorf("리소스 타입 중복: %s", definition.Type)
		}
		seenTypes[definition.Type] = struct{}{}

		if strings.TrimSpace(definition.Label) == "" {
			return fmt.Errorf("%s: 표시명이 비어 있음", definition.Type)
		}
		if definition.Scope != Regional && definition.Scope != Global {
			return fmt.Errorf("%s: 조회 범위가 올바르지 않음", definition.Type)
		}
		if len(definition.Columns) == 0 {
			return fmt.Errorf("%s: 목록 열이 비어 있음", definition.Type)
		}
		if definition.newCollector == nil {
			return fmt.Errorf("%s: 수집기 생성자가 없음", definition.Type)
		}

		seenColumns := make(map[string]struct{}, len(definition.Columns))
		for _, column := range definition.Columns {
			if strings.TrimSpace(column) == "" {
				return fmt.Errorf("%s: 빈 목록 열이 있음", definition.Type)
			}
			if _, exists := seenColumns[column]; exists {
				return fmt.Errorf("%s: 목록 열 중복 (%s)", definition.Type, column)
			}
			seenColumns[column] = struct{}{}
		}

		seenSummary := make(map[string]struct{}, len(definition.SummaryColumns))
		for _, column := range definition.SummaryColumns {
			if _, exists := seenColumns[column]; !exists {
				return fmt.Errorf("%s: 주요 정보 열이 목록 열에 없음 (%s)", definition.Type, column)
			}
			if _, exists := seenSummary[column]; exists {
				return fmt.Errorf("%s: 주요 정보 열 중복 (%s)", definition.Type, column)
			}
			seenSummary[column] = struct{}{}
		}
	}

	return nil
}

func validateGroups(groups []Group) error {
	seenIDs := make(map[string]struct{}, len(groups))
	typeGroups := make(map[string]string)

	for i, group := range groups {
		if strings.TrimSpace(group.ID) == "" {
			return fmt.Errorf("그룹 %d: ID가 비어 있음", i)
		}
		if _, exists := seenIDs[group.ID]; exists {
			return fmt.Errorf("그룹 ID 중복: %s", group.ID)
		}
		seenIDs[group.ID] = struct{}{}

		if strings.TrimSpace(group.Label) == "" {
			return fmt.Errorf("%s: 표시명이 비어 있음", group.ID)
		}
		if len(group.Types) == 0 {
			return fmt.Errorf("%s: 포함 타입이 없음", group.ID)
		}

		for _, definition := range group.Types {
			if owner, exists := typeGroups[definition.Type]; exists {
				return fmt.Errorf("%s: 타입 %s가 그룹 %s에도 포함됨", group.ID, definition.Type, owner)
			}
			typeGroups[definition.Type] = group.ID

			if len(group.Types) > 1 && len(definition.SummaryColumns) == 0 {
				return fmt.Errorf("%s: 타입 %s의 주요 정보 열이 없음", group.ID, definition.Type)
			}
		}
	}

	return validateDefinitions(flattenGroups(groups))
}

func flattenGroups(groups []Group) []Definition {
	var definitions []Definition
	for _, group := range groups {
		definitions = append(definitions, group.Types...)
	}

	return definitions
}

// allDefinitions는 서비스별 정의를 최종 표시·실행 순서로 펼친다.
func allDefinitions(cfg aws.Config) []Definition {
	return flattenGroups(allGroups(cfg))
}

// allGroups는 사용자가 보는 큰 AWS 리소스 단위와 내부 세부 타입을 함께 조립한다.
//
// 새 리소스는 해당 서비스의 definitions 함수에 추가한다. 새 서비스는 definitions 함수를
// 만들고 이곳에 그룹 한 개를 추가한다. 순서를 숨기는 init 자동 등록은 사용하지 않는다.
func allGroups(cfg aws.Config) []Group {
	groups := ec2Groups(cfg)

	return append(groups,
		Group{ID: "autoscaling", Label: "Auto Scaling", Types: autoscalingDefinitions(cfg)},
		Group{ID: "elbv2", Label: "ELB", Types: elbv2Definitions(cfg)},
		Group{ID: "lambda", Label: "Lambda", Types: lambdaDefinitions(cfg)},
		Group{ID: "ecs", Label: "ECS", Types: ecsDefinitions(cfg)},
		Group{ID: "ecr", Label: "ECR", Types: ecrDefinitions(cfg)},
		Group{ID: "eks", Label: "EKS", Types: eksDefinitions(cfg)},
		Group{ID: "rds", Label: "RDS", Types: rdsDefinitions(cfg)},
		Group{ID: "dynamodb", Label: "DynamoDB", Types: dynamodbDefinitions(cfg)},
		Group{ID: "sns", Label: "SNS", Types: snsDefinitions(cfg)},
		Group{ID: "sqs", Label: "SQS", Types: sqsDefinitions(cfg)},
		Group{ID: "apigateway", Label: "API Gateway", Types: apigatewayDefinitions(cfg)},
		Group{ID: "secretsmanager", Label: "Secrets Manager", Types: secretsmanagerDefinitions(cfg)},
		Group{ID: "ssm", Label: "SSM Parameter Store", Types: ssmDefinitions(cfg)},
		Group{ID: "route53", Label: "Route 53", Types: route53Definitions(cfg)},
		Group{ID: "wafv2", Label: "WAF", Types: wafv2Definitions(cfg)},
		Group{ID: "iam", Label: "IAM", Types: iamDefinitions(cfg)},
		Group{ID: "kms", Label: "KMS", Types: kmsDefinitions(cfg)},
		Group{ID: "s3", Label: "S3", Types: s3Definitions(cfg)},
	)
}
