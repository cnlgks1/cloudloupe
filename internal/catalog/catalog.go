// Package catalog는 cloudloupe가 지원하는 AWS 리소스 타입을 명시적으로 조립한다.
//
// 타입 ID, 사용자 표시명, 조회 범위, 목록 열과 수집기 생성자를 한 Definition에 둔다.
// 신규 리소스를 추가할 때 서로 다른 파일의 라벨·글로벌 여부·등록 목록이 어긋나지 않게
// 하는 단일 출처다. init 부수효과나 변경 가능한 전역 레지스트리는 사용하지 않는다.
package catalog

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awselbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	awsroute53 "github.com/aws/aws-sdk-go-v2/service/route53"
	awswafv2 "github.com/aws/aws-sdk-go-v2/service/wafv2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	ec2collector "github.com/cnlgks1/cloudloupe/internal/collector/ec2"
	elbv2collector "github.com/cnlgks1/cloudloupe/internal/collector/elbv2"
	route53collector "github.com/cnlgks1/cloudloupe/internal/collector/route53"
	wafv2collector "github.com/cnlgks1/cloudloupe/internal/collector/wafv2"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// ScopeKind는 리소스가 리전별인지 계정 전체의 글로벌 리소스인지 나타낸다.
type ScopeKind uint8

const (
	// Regional은 선택한 각 리전에서 조회하는 리소스다.
	Regional ScopeKind = iota
	// Global은 선택 리전 수와 관계없이 실행당 한 번만 조회하는 리소스다.
	Global
)

// Definition은 지원 리소스 타입 하나의 변경되지 않는 메타데이터다.
//
// Columns는 Resource.Fields에 들어가는 키 순서다. TUI가 첫 번째 결과의 우연한 필드
// 모양으로 열을 결정하지 않고, 결과가 없어도 안정적인 스키마를 사용하게 한다.
type Definition struct {
	Type    string
	Label   string
	Scope   ScopeKind
	Columns []string

	newCollector func(clients) collect.Collector
}

type clients struct {
	ec2     *awsec2.Client
	elbv2   *awselbv2.Client
	route53 *awsroute53.Client
	wafv2   *awswafv2.Client
}

// Definitions는 지원 타입 정의를 표시·실행 순서대로 반환한다.
//
// 호출자가 내부 상태를 바꿀 수 없도록 매번 새 슬라이스와 Columns 복사본을 반환한다.
func Definitions() []Definition {
	definitions := allDefinitions()
	out := make([]Definition, len(definitions))

	for i, definition := range definitions {
		out[i] = definition
		out[i].Columns = append([]string(nil), definition.Columns...)
	}

	return out
}

// Registry는 AWS 설정과 선택 타입으로 실행 가능한 수집기 레지스트리를 만든다.
//
// includeGlobal이 false면 글로벌 타입은 제외한다. selected가 비면 지원 타입을 모두
// 포함하며, 알 수 없는 타입은 unknown으로 반환해 호출자가 사용자에게 알려줄 수 있다.
func Registry(cfg aws.Config, includeGlobal bool, selected []string) (*collect.Registry, []string, error) {
	definitions := allDefinitions()
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

	serviceClients := clients{
		ec2:     awsec2.NewFromConfig(cfg),
		elbv2:   awselbv2.NewFromConfig(cfg),
		route53: awsroute53.NewFromConfig(cfg),
		wafv2:   awswafv2.NewFromConfig(cfg),
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

		if err := registry.Add(definition.newCollector(serviceClients)); err != nil {
			return nil, unknown, fmt.Errorf("%s 수집기 등록: %w", definition.Type, err)
		}
	}

	return registry, unknown, nil
}

// allDefinitions는 지원 리소스의 단일 등록 지점이다.
//
// 기존 AWS 서비스에 리소스를 추가하면 서비스 패키지 구현과 이 항목 하나만 추가한다.
// 새로운 AWS 서비스라면 clients에 SDK 클라이언트를 한 번 추가한다.
func allDefinitions() []Definition {
	return []Definition{
		{
			Type:    model.TypeEC2Instance,
			Label:   "EC2 인스턴스",
			Scope:   Regional,
			Columns: []string{"인스턴스 타입", "가용 영역", "사설 IP", "공인 IP"},
			newCollector: func(c clients) collect.Collector {
				return ec2collector.NewInstance(c.ec2)
			},
		},
		{
			Type:    model.TypeEC2Volume,
			Label:   "EBS 볼륨",
			Scope:   Regional,
			Columns: []string{"타입", "크기(GiB)", "IOPS", "가용 영역", "암호화"},
			newCollector: func(c clients) collect.Collector {
				return ec2collector.NewVolume(c.ec2)
			},
		},
		{
			Type:    model.TypeEC2NetworkInterface,
			Label:   "네트워크 인터페이스 (ENI)",
			Scope:   Regional,
			Columns: []string{"종류", "사설 IP", "VPC", "서브넷"},
			newCollector: func(c clients) collect.Collector {
				return ec2collector.NewNetworkInterface(c.ec2)
			},
		},
		{
			Type:    model.TypeEC2Address,
			Label:   "Elastic IP",
			Scope:   Regional,
			Columns: []string{"공인 IP", "사설 IP", "도메인"},
			newCollector: func(c clients) collect.Collector {
				return ec2collector.NewAddress(c.ec2)
			},
		},
		{
			Type:    model.TypeELBv2LoadBalancer,
			Label:   "로드밸런서 (ALB/NLB)",
			Scope:   Regional,
			Columns: []string{"종류", "스킴", "DNS 이름", "VPC"},
			newCollector: func(c clients) collect.Collector {
				return elbv2collector.NewLoadBalancer(c.elbv2)
			},
		},
		{
			Type:    model.TypeELBv2TargetGroup,
			Label:   "타깃 그룹",
			Scope:   Regional,
			Columns: []string{"프로토콜", "포트", "타깃 종류", "타깃 수"},
			newCollector: func(c clients) collect.Collector {
				return elbv2collector.NewTargetGroup(c.elbv2)
			},
		},
		{
			Type:    model.TypeRoute53RecordSet,
			Label:   "Route 53 레코드",
			Scope:   Global,
			Columns: []string{"타입", "호스팅 영역", "TTL", "값", "별칭 대상"},
			newCollector: func(c clients) collect.Collector {
				return route53collector.NewRecordSet(c.route53)
			},
		},
		{
			Type:    model.TypeWAFv2WebACL,
			Label:   "WAF Web ACL",
			Scope:   Regional,
			Columns: []string{"설명", "규칙 수"},
			newCollector: func(c clients) collect.Collector {
				return wafv2collector.NewWebACL(c.wafv2)
			},
		},
	}
}
