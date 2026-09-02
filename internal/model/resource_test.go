package model_test

import (
	"slices"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

func TestResourceKeyDistinguishesAccountsAndRegions(t *testing.T) {
	t.Parallel()

	// 같은 리소스 ID가 두 리전에 정당하게 존재할 수 있고, 두 계정이 이름이 같은
	// 로드밸런서를 가질 수 있다. 리소스를 색인하는 코드는 ID만으로 키를 만들면 안 된다.
	base := model.Resource{
		Type:    model.TypeEC2Instance,
		ID:      "i-0a1b2c3d4e5f60718",
		Profile: "prod",
		Region:  "ap-northeast-2",
	}

	other := base
	other.Region = "us-east-1"

	third := base
	third.Profile = "staging"

	keys := map[string]bool{base.Key(): true, other.Key(): true, third.Key(): true}
	if len(keys) != 3 {
		t.Fatalf("서로 다른 키 3개를 기대했으나 %d개: %v", len(keys), keys)
	}
}

func TestResourceDisplayNameFallsBackToID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		res  model.Resource
		want string
	}{
		{
			name: "이름이 있으면 이름을 쓴다",
			res:  model.Resource{ID: "eni-09876543210fedcba", Name: "bastion-eni"},
			want: "bastion-eni",
		},
		{
			name: "이름이 없으면 ID로 대체한다",
			res:  model.Resource{ID: "eni-09876543210fedcba"},
			want: "eni-09876543210fedcba",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.res.DisplayName(); got != tc.want {
				t.Errorf("DisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResourceLookups(t *testing.T) {
	t.Parallel()

	res := model.Resource{
		Fields: []model.Field{
			{Key: "인스턴스 타입", Value: "t3.medium"},
			{Key: "사설 IP", Value: "10.0.1.24"},
		},
		Tags: []model.Field{
			{Key: "Environment", Value: "production"},
		},
		Related: []model.Ref{
			{Type: model.TypeEC2Volume, ID: "vol-1", Relation: model.RelationAttachedVolume, Via: "/dev/xvda"},
			{Type: model.TypeEC2Volume, ID: "vol-2", Relation: model.RelationAttachedVolume, Via: "/dev/xvdb"},
			{Type: model.TypeELBv2TargetGroup, ID: "web-tg", Relation: model.RelationTargetOf},
		},
	}

	if got := res.FieldValue("인스턴스 타입"); got != "t3.medium" {
		t.Errorf("FieldValue(인스턴스 타입) = %q, want t3.medium", got)
	}

	if got := res.FieldValue("없는 필드"); got != "" {
		t.Errorf("FieldValue(없는 필드) = %q, want 빈 문자열", got)
	}

	if got := res.Tag("Environment"); got != "production" {
		t.Errorf("Tag(Environment) = %q, want production", got)
	}

	if got := res.Tag("Name"); got != "" {
		t.Errorf("Tag(Name) = %q, want 빈 문자열", got)
	}

	volumes := res.RelatedBy(model.RelationAttachedVolume)
	if len(volumes) != 2 {
		t.Fatalf("RelatedBy(attached-volume)가 ref %d개를 반환했다, want 2", len(volumes))
	}

	if volumes[0].ID != "vol-1" || volumes[1].ID != "vol-2" {
		t.Errorf("RelatedBy가 순서를 유지하지 않았다: %+v", volumes)
	}

	if got := res.RelatedBy(model.RelationForwardsTo); got != nil {
		t.Errorf("RelatedBy(forwards-to) = %+v, want nil", got)
	}
}

func TestSortResourcesGroupsRelatedTypesTogether(t *testing.T) {
	t.Parallel()

	// 의도적으로 순서를 뒤섞었고, 의도적으로 타입 식별자의 알파벳 순서가 아니다. 순위를
	// 따로 둔 목적은 리포트를 위에서 아래로 읽었을 때 로드밸런서에서 시작해 인스턴스에
	// 매달린 것들로 자연스럽게 내려가게 하는 것이다.
	resources := []model.Resource{
		{Type: model.TypeEC2SecurityGroup, ID: "sg-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2Address, ID: "eipalloc-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2VPCEndpoint, ID: "vpce-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2Subnet, ID: "subnet-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2NATGateway, ID: "nat-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2Instance, ID: "i-0f1e", Region: "ap-northeast-2"},
		{Type: model.TypeELBv2TargetGroup, ID: "web-tg", Region: "ap-northeast-2"},
		{Type: model.TypeELBv2Listener, ID: "https:443", Region: "ap-northeast-2"},
		{Type: model.TypeEC2InternetGateway, ID: "igw-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2VPC, ID: "vpc-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2Volume, ID: "vol-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2RouteTable, ID: "rtb-1", Region: "ap-northeast-2"},
		{Type: model.TypeEC2Instance, ID: "i-0a1b", Region: "ap-northeast-2"},
		{Type: model.TypeELBv2LoadBalancer, ID: "web-alb", Region: "ap-northeast-2"},
		{Type: model.TypeEC2NetworkInterface, ID: "eni-1", Region: "ap-northeast-2"},
	}

	model.SortResources(resources)

	want := []string{"web-alb", "https:443", "web-tg", "i-0a1b", "i-0f1e", "vol-1", "eni-1", "eipalloc-1", "vpc-1", "subnet-1", "rtb-1", "igw-1", "nat-1", "vpce-1", "sg-1"}

	got := make([]string, 0, len(resources))
	for _, r := range resources {
		got = append(got, r.ID)
	}

	if !slices.Equal(got, want) {
		t.Errorf("SortResources 순서 =\n  %v\nwant\n  %v", got, want)
	}
}

func TestSortResourcesPlacesUnknownTypesLast(t *testing.T) {
	t.Parallel()

	// 새 수집기가 기존 출력 순서를 실수로 뒤바꿀 수 없어야 한다.
	resources := []model.Resource{
		{Type: "rds:dbInstance", ID: "db-1"},
		{Type: model.TypeEC2Instance, ID: "i-1"},
		{Type: "aaa:first-alphabetically", ID: "x-1"},
	}

	model.SortResources(resources)

	if resources[0].Type != model.TypeEC2Instance {
		t.Errorf("알려진 타입이 먼저 와야 한다, got %q", resources[0].Type)
	}

	if resources[1].Type != "aaa:first-alphabetically" || resources[2].Type != "rds:dbInstance" {
		t.Errorf("모르는 타입은 알파벳 순으로 맨 뒤여야 한다: %q 다음 %q",
			resources[1].Type, resources[2].Type)
	}
}

func TestSortResourcesIsDeterministicAcrossRuns(t *testing.T) {
	t.Parallel()

	// 실행마다 순서가 흔들리면 스냅샷 diff가 쓸모없어진다.
	build := func() []model.Resource {
		return []model.Resource{
			{Type: model.TypeEC2Instance, ID: "i-c", Region: "us-east-1"},
			{Type: model.TypeEC2Instance, ID: "i-a", Region: "ap-northeast-2"},
			{Type: model.TypeEC2Instance, ID: "i-b", Region: "ap-northeast-2"},
			{Type: model.TypeEC2Volume, ID: "vol-a", Region: "us-east-1"},
		}
	}

	first, second := build(), build()
	model.SortResources(first)
	model.SortResources(second)

	for i := range first {
		if first[i].Key() != second[i].Key() {
			t.Fatalf("%d번째에서 순서가 다르다: %q vs %q", i, first[i].Key(), second[i].Key())
		}
	}

	// 같은 타입 안에서는 리전이 ID보다 상위 기준이다.
	wantIDs := []string{"i-a", "i-b", "i-c", "vol-a"}
	for i, want := range wantIDs {
		if first[i].ID != want {
			t.Errorf("%d번째 = %q, want %q", i, first[i].ID, want)
		}
	}
}

func TestTagFieldsSortsByKey(t *testing.T) {
	t.Parallel()

	// map 순회 순서는 무작위다. TagFields가 정렬하지 않으면 이 테스트가 간헐적으로
	// 깨지도록 여러 번 반복해서, 그 실패 양상을 시끄럽게 만든다.
	tags := map[string]string{
		"Service":     "web",
		"Name":        "web-prod-01",
		"Environment": "production",
	}

	want := []model.Field{
		{Key: "Environment", Value: "production"},
		{Key: "Name", Value: "web-prod-01"},
		{Key: "Service", Value: "web"},
	}

	for range 50 {
		if got := model.TagFields(tags); !slices.Equal(got, want) {
			t.Fatalf("TagFields() = %+v, want %+v", got, want)
		}
	}
}

func TestTagFieldsHandlesEmptyInput(t *testing.T) {
	t.Parallel()

	got := model.TagFields(nil)
	if got == nil {
		t.Fatal("TagFields(nil)이 nil을 반환했다. JSON이 null 대신 []가 되도록 빈 슬라이스여야 한다")
	}

	if len(got) != 0 {
		t.Errorf("TagFields(nil) = %+v, want 빈 슬라이스", got)
	}
}

func TestResourceKeyUsesOptionalNamespace(t *testing.T) {
	t.Parallel()

	base := model.Resource{
		Type: model.TypeRoute53RecordSet, ID: "www.example.com.|A",
		Profile: "prod", Region: "global",
	}
	first := base
	first.Namespace = "Z1"
	second := base
	second.Namespace = "Z2"

	if first.Key() == second.Key() {
		t.Fatalf("서로 다른 namespace의 키가 충돌함: %q", first.Key())
	}
	if got, want := base.Key(), "prod|global|route53:recordSet|www.example.com.|A"; got != want {
		t.Errorf("namespace 없는 기존 Key = %q, want %q", got, want)
	}
}

func TestSortResourcesUsesNamespaceAsTieBreaker(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{Type: model.TypeRoute53RecordSet, ID: "www.example.com.|A", Namespace: "Z2", Region: "global"},
		{Type: model.TypeRoute53RecordSet, ID: "www.example.com.|A", Namespace: "Z1", Region: "global"},
	}
	model.SortResources(resources)

	if resources[0].Namespace != "Z1" || resources[1].Namespace != "Z2" {
		t.Errorf("namespace 정렬 = %q, %q", resources[0].Namespace, resources[1].Namespace)
	}
}
