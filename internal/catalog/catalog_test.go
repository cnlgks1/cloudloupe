package catalog

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

type fakeCatalogCollector struct {
	typ string
}

func (c fakeCatalogCollector) Type() string { return c.typ }

func (c fakeCatalogCollector) Collect(context.Context, collect.Request) ([]model.Resource, error) {
	return nil, nil
}

func TestDefinitionsAreValidAndOrdered(t *testing.T) {
	t.Parallel()

	definitions := allDefinitions(aws.Config{})
	if err := validateDefinitions(definitions); err != nil {
		t.Fatalf("기본 카탈로그 검증 실패: %v", err)
	}

	want := []string{
		model.TypeEC2Instance,
		model.TypeEC2Volume,
		model.TypeEC2NetworkInterface,
		model.TypeEC2Address,
		model.TypeEC2VPC,
		model.TypeEC2Subnet,
		model.TypeEC2SecurityGroup,
		model.TypeEC2RouteTable,
		model.TypeEC2InternetGateway,
		model.TypeEC2NATGateway,
		model.TypeEC2VPCEndpoint,
		model.TypeAutoScalingGroup,
		model.TypeELBv2LoadBalancer,
		model.TypeELBv2Listener,
		model.TypeELBv2TargetGroup,
		model.TypeLambdaFunction,
		model.TypeECSCluster,
		model.TypeECSService,
		model.TypeECSTaskDefinition,
		model.TypeECRRepository,
		model.TypeEKSCluster,
		model.TypeEKSNodegroup,
		model.TypeEKSFargateProfile,
		model.TypeRDSDBCluster,
		model.TypeRDSDBInstance,
		model.TypeDynamoDBTable,
		model.TypeSNSTopic,
		model.TypeSQSQueue,
		model.TypeAPIGatewayRestAPI,
		model.TypeAPIGatewayV2API,
		model.TypeRoute53RecordSet,
		model.TypeWAFv2WebACL,
		model.TypeIAMRole,
		model.TypeKMSKey,
		model.TypeS3Bucket,
	}

	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.Type)

		collector := definition.newCollector()
		if collector == nil {
			t.Errorf("%s 생성자가 nil 수집기를 반환함", definition.Type)
			continue
		}
		if collector.Type() != definition.Type {
			t.Errorf("%s 생성자 Type() = %q", definition.Type, collector.Type())
		}
	}

	if !slices.Equal(got, want) {
		t.Errorf("정의 순서 = %v, want %v", got, want)
	}
}

func TestGroupsAreOrderedAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	groups, err := Groups()
	if err != nil {
		t.Fatalf("Groups() 실패: %v", err)
	}
	wantIDs := []string{"ec2", "vpc", "network", "autoscaling", "elbv2", "lambda", "ecs", "ecr", "eks", "rds", "dynamodb", "sns", "sqs", "apigateway", "route53", "wafv2", "iam", "kms", "s3"}
	gotIDs := make([]string, 0, len(groups))
	for _, group := range groups {
		gotIDs = append(gotIDs, group.ID)
		if group.Label == "" || len(group.Types) == 0 {
			t.Errorf("그룹 %q 메타데이터가 비어 있음: %+v", group.ID, group)
		}
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("그룹 순서 = %v, want %v", gotIDs, wantIDs)
	}

	wantEC2Types := []string{
		model.TypeEC2Instance,
		model.TypeEC2Volume,
		model.TypeEC2NetworkInterface,
		model.TypeEC2Address,
	}
	gotEC2Types := make([]string, 0, len(groups[0].Types))
	for _, definition := range groups[0].Types {
		gotEC2Types = append(gotEC2Types, definition.Type)
	}
	if !slices.Equal(gotEC2Types, wantEC2Types) {
		t.Errorf("EC2 타입 = %v, want %v", gotEC2Types, wantEC2Types)
	}

	wantVPCTypes := []string{
		model.TypeEC2VPC,
		model.TypeEC2Subnet,
		model.TypeEC2SecurityGroup,
	}
	gotVPCTypes := make([]string, 0, len(groups[1].Types))
	for _, definition := range groups[1].Types {
		gotVPCTypes = append(gotVPCTypes, definition.Type)
	}
	if !slices.Equal(gotVPCTypes, wantVPCTypes) {
		t.Errorf("VPC 타입 = %v, want %v", gotVPCTypes, wantVPCTypes)
	}

	// 선택 화면에서 포함 항목이 잘리지 않도록 트래픽 경로 리소스는 별도 그룹에 둔다.
	wantNetworkTypes := []string{
		model.TypeEC2RouteTable,
		model.TypeEC2InternetGateway,
		model.TypeEC2NATGateway,
		model.TypeEC2VPCEndpoint,
	}
	gotNetworkTypes := make([]string, 0, len(groups[2].Types))
	for _, definition := range groups[2].Types {
		gotNetworkTypes = append(gotNetworkTypes, definition.Type)
	}
	if !slices.Equal(gotNetworkTypes, wantNetworkTypes) {
		t.Errorf("네트워크 타입 = %v, want %v", gotNetworkTypes, wantNetworkTypes)
	}

	if got, want := groups[14].Types[0].Columns,
		[]string{"Type", "SetIdentifier", "HostedZoneName", "TTL", "ResourceRecords", "AliasTarget"}; !slices.Equal(got, want) {
		t.Errorf("Route 53 열 = %v, want %v", got, want)
	}
	if got, want := groups[15].Types[0].Columns, []string{"Rules"}; !slices.Equal(got, want) {
		t.Errorf("WAF 열 = %v, want %v", got, want)
	}

	groups[0].Types[0].Columns[0] = "changed"
	groups[0].Types[0].SummaryColumns[0] = "changed"
	fresh, err := Groups()
	if err != nil {
		t.Fatalf("두 번째 Groups() 실패: %v", err)
	}
	if fresh[0].Types[0].Columns[0] != "InstanceType" {
		t.Errorf("Columns 방어적 복사 실패: %v", fresh[0].Types[0].Columns)
	}
	if fresh[0].Types[0].SummaryColumns[0] != "InstanceType" {
		t.Errorf("SummaryColumns 방어적 복사 실패: %v", fresh[0].Types[0].SummaryColumns)
	}
}

func TestDefinitionsReturnsCopies(t *testing.T) {
	t.Parallel()

	first := Definitions()
	first[0].Type = "changed"
	first[0].Columns[0] = "changed"
	first[0].SummaryColumns[0] = "changed"

	second := Definitions()
	if second[0].Type != model.TypeEC2Instance {
		t.Errorf("Type = %q, want %q", second[0].Type, model.TypeEC2Instance)
	}
	if second[0].Columns[0] != "InstanceType" {
		t.Errorf("Columns[0] = %q, want %q", second[0].Columns[0], "InstanceType")
	}
	if second[0].SummaryColumns[0] != "InstanceType" {
		t.Errorf("SummaryColumns[0] = %q, want %q", second[0].SummaryColumns[0], "InstanceType")
	}
}

func TestBuildRegistrySelection(t *testing.T) {
	t.Parallel()

	definitions := allDefinitions(aws.Config{})

	t.Run("선택 타입만 카탈로그 순서로 등록", func(t *testing.T) {
		t.Parallel()

		registry, unknown, err := buildRegistry(definitions, true, []string{
			model.TypeEC2Volume,
			model.TypeEC2Instance,
			model.TypeEC2Volume,
		})
		if err != nil {
			t.Fatalf("레지스트리 생성 실패: %v", err)
		}
		if len(unknown) != 0 {
			t.Errorf("unknown = %v, want empty", unknown)
		}

		want := []string{model.TypeEC2Instance, model.TypeEC2Volume}
		if got := registry.Types(); !slices.Equal(got, want) {
			t.Errorf("Types() = %v, want %v", got, want)
		}
	})

	t.Run("글로벌 타입 제외", func(t *testing.T) {
		t.Parallel()

		registry, _, err := buildRegistry(definitions, false, nil)
		if err != nil {
			t.Fatalf("레지스트리 생성 실패: %v", err)
		}
		if slices.Contains(registry.Types(), model.TypeRoute53RecordSet) {
			t.Errorf("글로벌 제외 결과에 %s가 포함됨", model.TypeRoute53RecordSet)
		}
	})

	t.Run("알 수 없는 타입은 입력 순서로 보고", func(t *testing.T) {
		t.Parallel()

		registry, unknown, err := buildRegistry(definitions, true, []string{
			"ec2:unknown",
			model.TypeEC2Volume,
			"route53:unknown",
		})
		if err != nil {
			t.Fatalf("레지스트리 생성 실패: %v", err)
		}
		if got, want := registry.Types(), []string{model.TypeEC2Volume}; !slices.Equal(got, want) {
			t.Errorf("Types() = %v, want %v", got, want)
		}
		if want := []string{"ec2:unknown", "route53:unknown"}; !slices.Equal(unknown, want) {
			t.Errorf("unknown = %v, want %v", unknown, want)
		}
	})
}

func TestValidateGroupsRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	validDefinition := func(typ string) Definition {
		return Definition{
			Type:           typ,
			Label:          "테스트",
			Scope:          Regional,
			Columns:        []string{"상태"},
			SummaryColumns: []string{"상태"},
			newCollector: func() collect.Collector {
				return fakeCatalogCollector{typ: typ}
			},
		}
	}

	withoutSummary := validDefinition("test:other")
	withoutSummary.SummaryColumns = nil

	tests := []struct {
		name   string
		groups []Group
		want   string
	}{
		{name: "빈 ID", groups: []Group{{Label: "테스트", Types: []Definition{validDefinition("test:item")}}}, want: "ID가 비어"},
		{name: "중복 ID", groups: []Group{{ID: "test", Label: "테스트", Types: []Definition{validDefinition("test:a")}}, {ID: "test", Label: "다른 그룹", Types: []Definition{validDefinition("test:b")}}}, want: "그룹 ID 중복"},
		{name: "빈 표시명", groups: []Group{{ID: "test", Types: []Definition{validDefinition("test:item")}}}, want: "표시명이 비어"},
		{name: "빈 타입 목록", groups: []Group{{ID: "test", Label: "테스트"}}, want: "포함 타입이 없음"},
		{name: "타입 중복 소속", groups: []Group{{ID: "one", Label: "하나", Types: []Definition{validDefinition("test:item")}}, {ID: "two", Label: "둘", Types: []Definition{validDefinition("test:item")}}}, want: "그룹 one에도 포함"},
		{name: "혼합 그룹 요약 없음", groups: []Group{{ID: "test", Label: "테스트", Types: []Definition{validDefinition("test:item"), withoutSummary}}}, want: "주요 정보 열이 없음"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateGroups(tt.groups)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestValidateDefinitionsRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	valid := func(typ string) Definition {
		return Definition{
			Type:    typ,
			Label:   "테스트",
			Scope:   Regional,
			Columns: []string{"상태"},
			newCollector: func() collect.Collector {
				return fakeCatalogCollector{typ: typ}
			},
		}
	}

	tests := []struct {
		name        string
		definitions []Definition
		want        string
	}{
		{name: "빈 타입", definitions: []Definition{{Label: "테스트", Scope: Regional, Columns: []string{"상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{} }}}, want: "타입이 비어"},
		{name: "중복 타입", definitions: []Definition{valid("test:item"), valid("test:item")}, want: "타입 중복"},
		{name: "빈 표시명", definitions: []Definition{{Type: "test:item", Scope: Regional, Columns: []string{"상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "표시명이 비어"},
		{name: "잘못된 범위", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: scopeUnknown, Columns: []string{"상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "조회 범위"},
		{name: "빈 열 목록", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "목록 열이 비어"},
		{name: "빈 열", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, Columns: []string{" "}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "빈 목록 열"},
		{name: "중복 열", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, Columns: []string{"상태", "상태"}, newCollector: func() collect.Collector { return fakeCatalogCollector{typ: "test:item"} }}}, want: "목록 열 중복"},
		{name: "생성자 없음", definitions: []Definition{{Type: "test:item", Label: "테스트", Scope: Regional, Columns: []string{"상태"}}}, want: "생성자가 없음"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateDefinitions(tt.definitions)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestBuildRegistryRejectsBrokenFactory(t *testing.T) {
	t.Parallel()

	base := Definition{
		Type:    "test:item",
		Label:   "테스트",
		Scope:   Regional,
		Columns: []string{"상태"},
	}

	t.Run("nil 반환", func(t *testing.T) {
		t.Parallel()

		definition := base
		definition.newCollector = func() collect.Collector { return nil }

		_, _, err := buildRegistry([]Definition{definition}, true, nil)
		if err == nil || !strings.Contains(err.Error(), "nil 반환") {
			t.Errorf("err = %v, want nil 반환", err)
		}
	})

	t.Run("타입 불일치", func(t *testing.T) {
		t.Parallel()

		definition := base
		definition.newCollector = func() collect.Collector {
			return fakeCatalogCollector{typ: "test:other"}
		}

		_, _, err := buildRegistry([]Definition{definition}, true, nil)
		if err == nil || !strings.Contains(err.Error(), "타입 불일치") {
			t.Errorf("err = %v, want 타입 불일치", err)
		}
	})
}

func TestEC2GroupsShareLazyClient(t *testing.T) {
	t.Parallel()

	calls := 0
	groups := ec2GroupsWithClientFactory(func() *awsec2.Client {
		calls++
		return awsec2.New(awsec2.Options{})
	})

	if calls != 0 {
		t.Fatalf("그룹 조립 중 EC2 클라이언트를 %d회 생성함, want 0", calls)
	}
	if len(groups) != 3 || groups[0].ID != "ec2" || groups[1].ID != "vpc" || groups[2].ID != "network" {
		t.Fatalf("EC2 계열 그룹 = %+v", groups)
	}

	groups[0].Types[0].newCollector()
	if calls != 1 {
		t.Fatalf("첫 EC2 수집기 생성 뒤 클라이언트 생성 횟수 = %d, want 1", calls)
	}

	for _, group := range groups {
		for _, definition := range group.Types {
			definition.newCollector()
		}
	}
	if calls != 1 {
		t.Errorf("EC2와 VPC 그룹이 클라이언트를 공유하지 않음: 생성 %d회, want 1", calls)
	}
}
