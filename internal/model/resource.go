// Package model은 수집된 AWS 리소스를 표현하는 도메인 타입을 정의한다.
//
// 이 패키지는 표준 라이브러리 외에 아무것도 의존하지 않고, 다른 cloudloupe
// 패키지를 import하지 않는다. 나머지 모든 internal 패키지가 이 패키지를 의존할 수
// 있다. 이 단방향 화살표가 report, graph, findings 계층에서 AWS SDK를
// 몰아내는 장치다.
package model

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// 리소스 타입 식별자. "<서비스>:<종류>" 형식이다.
//
// 수집기는 이 값을 그대로 보고하고, 리포트는 이 값으로 그룹을 나누며, TUI는 탭
// 이름으로 쓴다. 안정적으로 유지해야 한다. JSON 출력과 스냅샷 행에 그대로 실리므로
// 이름을 바꾸면 출력 계약이 바뀐다. 그래서 한국어로 번역하지 않는다.
const (
	TypeEC2Instance         = "ec2:instance"
	TypeEC2Volume           = "ec2:volume"
	TypeEC2NetworkInterface = "ec2:networkInterface"
	TypeEC2Address          = "ec2:address"
	TypeEC2VPC              = "ec2:vpc"
	TypeEC2Subnet           = "ec2:subnet"
	TypeEC2RouteTable       = "ec2:routeTable"
	TypeEC2InternetGateway  = "ec2:internetGateway"
	TypeEC2NATGateway       = "ec2:natGateway"
	TypeEC2VPCEndpoint      = "ec2:vpcEndpoint"
	TypeEC2SecurityGroup    = "ec2:securityGroup"
	TypeELBv2LoadBalancer   = "elbv2:loadBalancer"
	TypeELBv2Listener       = "elbv2:listener"
	TypeELBv2TargetGroup    = "elbv2:targetGroup"
	TypeRoute53RecordSet    = "route53:recordSet"
	TypeWAFv2WebACL         = "wafv2:webAcl"
	TypeIAMRole             = "iam:role"
	TypeKMSKey              = "kms:key"
	TypeS3Bucket            = "s3:bucket"
	TypeRDSDBCluster        = "rds:dbCluster"
	TypeRDSDBInstance       = "rds:dbInstance"
	TypeLambdaFunction      = "lambda:function"
	TypeAutoScalingGroup    = "autoscaling:autoScalingGroup"
	TypeECSCluster          = "ecs:cluster"
	TypeECSService          = "ecs:service"
	TypeECSTaskDefinition   = "ecs:taskDefinition"
	TypeECRRepository       = "ecr:repository"
	TypeEKSCluster          = "eks:cluster"
	TypeEKSNodegroup        = "eks:nodegroup"
	TypeEKSFargateProfile   = "eks:fargateProfile"
)

// RegionGlobal은 리전 개념이 없는 글로벌 리소스의 Region 값이다.
//
// IAM이나 Route 53처럼 계정 단위로만 존재하는 리소스가 쓴다. 출력 계약이므로 수집기마다
// 문자열을 따로 적지 않고 이 상수를 쓴다.
const RegionGlobal = "global"

// [Ref.Relation]에는 이 관계를 만든 SDK 응답 필드 경로를 넣는다(예: "DBClusterIdentifier",
// "VpcConfig.SubnetIds", "Routes.NatGatewayId").
//
// 관계 이름을 상수로 고정하지 않는 이유가 있다. 한때 열 몇 개의 관계 상수로 모든 연결을
// 표현하려다 보니 대부분이 "associated-with" 하나로 쏠렸다. 어느 응답 필드에서 나온
// 연결인지 화면에서 알 수 없었고, aws CLI 출력과 대조할 수도 없었다. 필드 경로를 그대로
// 쓰면 각 관계가 스스로를 설명하고, 수집기를 쓰는 사람은 값을 꺼낸 경로를 적기만 하면 된다.
//
// 관계 이름으로 분기하는 코드는 없다. 표시와 그래프 색인에만 쓰이므로 자유 문자열이어도
// 안전하다. 반면 연결 대상은 [Ref.Type]에 model.Type* 상수를 쓰므로 컴파일 시점에 검사된다.
//
// 역방향은 [graph]가 만든다. 한쪽 끝에서만 관계를 기록해도 상세 화면의 "Referenced by"에
// 반대 방향이 나오므로, 수집기가 같은 관계를 양쪽에 중복 기록할 필요가 없다.

// Field는 화면 표시에 쓰이는 순서 있는 키/값 쌍이다.
//
// 표시 순서가 의미 있는 데이터를 map이 아니라 슬라이스로 담는 건 의도적이다. Go는
// map 순회 순서를 무작위화하므로, map을 쓰면 상세 뷰의 행 순서가 렌더링마다 바뀌고
// 스냅샷 diff가 실제로 일어나지 않은 변경을 보고하게 된다.
type Field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// IdentifierKind는 리소스를 찾는 식별자의 안정적인 종류다.
type IdentifierKind string

const (
	// IdentifierID는 Resource.ID와 같은 공급자 리소스 ID다.
	IdentifierID IdentifierKind = "id"
	// IdentifierARN은 AWS ARN이다.
	IdentifierARN IdentifierKind = "arn"
	// IdentifierDNS는 대소문자와 마지막 점을 무시하는 DNS 이름이다.
	IdentifierDNS IdentifierKind = "dns"
)

// Identifier는 정식 ID 외에 리소스를 찾을 수 있는 보조 식별자다.
//
// Resource.ID와 ARN은 graph가 자동으로 색인하므로 Identifiers에는 DNS처럼 추가로 필요한
// 값만 넣는다. Kind의 zero value는 기존 Ref와의 호환을 위해 IdentifierID로 해석한다.
type Identifier struct {
	Kind  IdentifierKind `json:"kind"`
	Value string         `json:"value"`
}

// Ref는 다른 리소스를 가리키면서 그 관계의 이름을 함께 담는다.
//
// IdentifierKind가 비어 있으면 ID가 Resource.ID를 가리킨다. Namespace를 지정하면
// 부모 범위 안에서만 ID가 유일한 대상 하나를 고를 수 있다. ARN이나 DNS로 참조할 때만
// 식별자 종류를 명시한다. Via에는 어떤 규칙이나 상태가 간선을 만들었는지 같은 한정어가
// 들어간다.
type Ref struct {
	Type           string         `json:"type"`
	ID             string         `json:"id"`
	Namespace      string         `json:"namespace,omitempty"`
	IdentifierKind IdentifierKind `json:"identifierKind,omitempty"`
	Relation       string         `json:"relation"`
	Via            string         `json:"via,omitempty"`
}

// Resource는 수집된 AWS 리소스 하나다.
//
// Fields는 상세 뷰에 보여줄 사람이 읽는 형태의 투영이다. 키는 API 필드명이 아니라
// 표시용 라벨이다. Tags는 AWS 태그를 키 순으로 정렬해 담는다. 둘 다 순서 있는
// 슬라이스인 이유는 [Field]에 적어두었다.
type Resource struct {
	Type        string       `json:"type"`
	ID          string       `json:"id"`
	Namespace   string       `json:"namespace,omitempty"`
	Name        string       `json:"name"`
	ARN         string       `json:"arn,omitempty"`
	Region      string       `json:"region"`
	Profile     string       `json:"profile"`
	AccountID   string       `json:"accountId"`
	Status      string       `json:"status"`
	CreatedAt   *time.Time   `json:"createdAt,omitempty"`
	Fields      []Field      `json:"fields"`
	Tags        []Field      `json:"tags"`
	Identifiers []Identifier `json:"identifiers,omitempty"`
	Related     []Ref        `json:"related"`
}

// Key는 프로필과 리전, 타입, 선택적 namespace를 포함한 안정적 식별자를 반환한다.
//
// Namespace는 Route 53 호스팅 영역처럼 ID가 부모 범위 안에서만 유일한 리소스를
// 구분한다. 비어 있으면 기존 키 형식을 유지한다. 반환 문자열은 ID 자체에 구분자가 들어갈
// 수 있으므로 호출자가 분해하지 않고 불투명 키로 다뤄야 한다.
func (r Resource) Key() string {
	parts := []string{r.Profile, r.Region, r.Type}
	if r.Namespace != "" {
		parts = append(parts, r.Namespace)
	}
	parts = append(parts, r.ID)

	return strings.Join(parts, "|")
}

// DisplayName은 Name을 반환하고, 이름이 없는 리소스면 ID로 대체한다.
func (r Resource) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}

	return r.ID
}

// Tag는 지정한 태그의 값을 반환한다. 태그가 없으면 빈 문자열이다.
func (r Resource) Tag(key string) string {
	return lookup(r.Tags, key)
}

// FieldValue는 지정한 표시 필드의 값을 반환한다. 없으면 빈 문자열이다.
func (r Resource) FieldValue(key string) string {
	return lookup(r.Fields, key)
}

// RelatedBy는 주어진 관계에 해당하는 ref들을 순서를 유지한 채 반환한다.
func (r Resource) RelatedBy(relation string) []Ref {
	var out []Ref

	for _, ref := range r.Related {
		if ref.Relation == relation {
			out = append(out, ref)
		}
	}

	return out
}

func lookup(fields []Field, key string) string {
	for _, f := range fields {
		if f.Key == key {
			return f.Value
		}
	}

	return ""
}

// SortResources는 리소스를 제자리에서 결정적으로 정렬한다.
//
// 1차 정렬 기준은 알파벳 순이 아니라 의도적으로 배치한 타입 순위다. 그래야 리포트에서
// 관련된 리소스가 함께 모인다. 로드밸런서 다음에 리스너와 타깃 그룹, 그다음 인스턴스,
// 이어서 인스턴스에 붙은 스토리지와 네트워크 자원이 온다.
//
// 모르는 타입은 알파벳 순으로 맨 뒤에 놓는다. 새 수집기가 기존 출력 순서를 조용히
// 뒤바꿀 수 없게 하기 위한 것이다.
func SortResources(resources []Resource) {
	slices.SortStableFunc(resources, func(a, b Resource) int {
		if c := cmp.Compare(typeRank(a.Type), typeRank(b.Type)); c != 0 {
			return c
		}

		if c := cmp.Compare(a.Type, b.Type); c != 0 {
			return c
		}

		if c := cmp.Compare(a.Region, b.Region); c != 0 {
			return c
		}

		if c := cmp.Compare(a.ID, b.ID); c != 0 {
			return c
		}

		return cmp.Compare(a.Key(), b.Key())
	})
}

// typeRank를 패키지 수준 map이 아니라 switch로 둔 이유는 두 가지다. 실행 중에 변경될
// 수 없고, 순서 전체가 한자리에서 눈에 보인다.
func typeRank(resourceType string) int {
	switch resourceType {
	case TypeRoute53RecordSet:
		return 0
	case TypeELBv2LoadBalancer:
		return 1
	case TypeELBv2Listener:
		return 2
	case TypeELBv2TargetGroup:
		return 3
	case TypeAutoScalingGroup:
		return 4
	case TypeEC2Instance:
		return 5
	case TypeEC2Volume:
		return 6
	case TypeEC2NetworkInterface:
		return 7
	case TypeEC2Address:
		return 8
	case TypeLambdaFunction:
		return 9
	case TypeECSCluster:
		return 10
	case TypeECSService:
		return 11
	case TypeECSTaskDefinition:
		return 12
	case TypeECRRepository:
		return 13
	case TypeEKSCluster:
		return 14
	case TypeEKSNodegroup:
		return 15
	case TypeEKSFargateProfile:
		return 16
	case TypeRDSDBCluster:
		return 17
	case TypeRDSDBInstance:
		return 18
	case TypeEC2VPC:
		return 19
	case TypeEC2Subnet:
		return 20
	case TypeEC2RouteTable:
		return 21
	case TypeEC2InternetGateway:
		return 22
	case TypeEC2NATGateway:
		return 23
	case TypeEC2VPCEndpoint:
		return 24
	case TypeEC2SecurityGroup:
		return 25
	case TypeWAFv2WebACL:
		return 26
	case TypeIAMRole:
		return 27
	case TypeKMSKey:
		return 28
	case TypeS3Bucket:
		return 29
	default:
		return 1000
	}
}

// TagFields는 AWS 태그 map을 키 순으로 정렬된 표시 필드로 변환한다.
//
// 수집기는 SDK로부터 순서 없는 map으로 태그를 받는다. 여기서 정렬해두는 것이 리포트
// 출력과 스냅샷 diff를 재현 가능하게 만드는 지점이다.
func TagFields(tags map[string]string) []Field {
	out := make([]Field, 0, len(tags))

	for k, v := range tags {
		out = append(out, Field{Key: k, Value: v})
	}

	slices.SortFunc(out, func(a, b Field) int {
		return cmp.Compare(a.Key, b.Key)
	})

	return out
}
