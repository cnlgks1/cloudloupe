package model_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

var (
	testNow  = time.Date(2026, time.August, 28, 4, 12, 33, 0, time.UTC)
	testTool = model.ToolInfo{Name: "cloudloupe", Version: "0.1.0"}
)

func TestNewSnapshotDerivesSummary(t *testing.T) {
	t.Parallel()

	resources := []model.Resource{
		{Type: model.TypeEC2Instance, ID: "i-a"},
		{Type: model.TypeEC2Instance, ID: "i-b"},
		{Type: model.TypeELBv2LoadBalancer, ID: "alb"},
	}
	errs := []model.CollectError{
		{Type: model.TypeWAFv2WebACL, Profile: "prod", Region: "eu-west-1", Code: "AccessDeniedException"},
	}

	snap := model.NewSnapshot(testNow, testTool, model.Scope{Profile: "prod"}, resources, errs)

	if snap.SchemaVersion != model.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", snap.SchemaVersion, model.SchemaVersion)
	}

	if snap.Summary.ResourceCount != 3 {
		t.Errorf("ResourceCount = %d, want 3", snap.Summary.ResourceCount)
	}

	if snap.Summary.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", snap.Summary.ErrorCount)
	}

	if got := snap.Summary.ByType[model.TypeEC2Instance]; got != 2 {
		t.Errorf("ByType[ec2:instance] = %d, want 2", got)
	}

	if got := snap.Summary.ByType[model.TypeELBv2LoadBalancer]; got != 1 {
		t.Errorf("ByType[elbv2:loadBalancer] = %d, want 1", got)
	}
}

func TestNewSnapshotDoesNotMutateCallerSlice(t *testing.T) {
	t.Parallel()

	// TUI는 수집된 리소스의 사본을 따로 들고 있다. 호출자의 배열을 밑에서 정렬해버리면
	// 렌더링 중에 목록 순서가 뒤바뀐다.
	resources := []model.Resource{
		{Type: model.TypeEC2Address, ID: "eipalloc-1"},
		{Type: model.TypeELBv2LoadBalancer, ID: "alb"},
	}

	model.NewSnapshot(testNow, testTool, model.Scope{}, resources, nil)

	if resources[0].ID != "eipalloc-1" || resources[1].ID != "alb" {
		t.Errorf("호출자의 슬라이스가 재정렬되었다: %q, %q", resources[0].ID, resources[1].ID)
	}
}

func TestNewSnapshotNormalizesTimestampToUTC(t *testing.T) {
	t.Parallel()

	seoul := time.FixedZone("KST", 9*60*60)
	local := time.Date(2026, time.August, 28, 13, 12, 33, 0, seoul)

	snap := model.NewSnapshot(local, testTool, model.Scope{}, nil, nil)

	if snap.GeneratedAt.Location() != time.UTC {
		t.Errorf("GeneratedAt 위치 = %v, want UTC", snap.GeneratedAt.Location())
	}

	if !snap.GeneratedAt.Equal(testNow) {
		t.Errorf("GeneratedAt = %v, want %v", snap.GeneratedAt, testNow)
	}
}

func TestNewSnapshotEmitsEmptyArraysNotNull(t *testing.T) {
	t.Parallel()

	// nil 슬라이스는 null로, 빈 슬라이스는 []로 직렬화된다. 문서화된 스키마에는 이
	// 배열들이 항상 존재하므로, 소비하는 쪽이 null을 특수 처리할 일이 없어야 한다.
	snap := model.NewSnapshot(testNow, testTool, model.Scope{Profile: "prod"},
		[]model.Resource{{Type: model.TypeEC2Instance, ID: "i-a"}}, nil)

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	body := string(out)
	for _, unwanted := range []string{
		`"errors":null`,
		`"fields":null`,
		`"tags":null`,
		`"related":null`,
		`"regions":null`,
		`"types":null`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("출력에 %s가 있다. 빈 배열이어야 한다\n%s", unwanted, body)
		}
	}
}

func TestSnapshotJSONMatchesDocumentedSchema(t *testing.T) {
	t.Parallel()

	// examples/reports/resources.json이 공개된 계약이다. 여기 적힌 키 이름들이 그 파일에
	// 실제로 등장하므로, 구조체 태그를 바꾸면 반드시 테스트가 깨져야 한다.
	created := time.Date(2025, time.March, 11, 2, 51, 19, 0, time.UTC)
	snap := model.NewSnapshot(testNow, testTool,
		model.Scope{
			Profile:   "prod",
			AccountID: "123456789012",
			ARN:       "arn:aws:sts::123456789012:assumed-role/ReadOnlyAccess/alice",
			Regions:   []string{"ap-northeast-2"},
			Types:     []string{model.TypeEC2Instance},
		},
		[]model.Resource{{
			Type:        model.TypeEC2Instance,
			ID:          "i-0a1b2c3d4e5f60718",
			Name:        "web-prod-01",
			ARN:         "arn:aws:ec2:ap-northeast-2:123456789012:instance/i-0a1b2c3d4e5f60718",
			Region:      "ap-northeast-2",
			Profile:     "prod",
			AccountID:   "123456789012",
			Status:      "running",
			CreatedAt:   &created,
			Fields:      []model.Field{{Key: "인스턴스 타입", Value: "t3.medium"}},
			Tags:        []model.Field{{Key: "Environment", Value: "production"}},
			Identifiers: []model.Identifier{{Kind: model.IdentifierDNS, Value: "web.internal.example.com"}},
			Related: []model.Ref{{
				Type:           model.TypeELBv2TargetGroup,
				ID:             "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web-prod-tg/abc",
				IdentifierKind: model.IdentifierARN,
				Relation:       "reverse-only",
			}},
		}}, nil)

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	body := string(out)
	for _, key := range []string{
		`"schemaVersion":2`,
		`"generatedAt":"2026-08-28T04:12:33Z"`,
		`"tool":{"name":"cloudloupe","version":"0.1.0"}`,
		`"accountId":"123456789012"`,
		`"resourceCount":1`,
		`"byType":{"ec2:instance":1}`,
		`"errorCount":0`,
		`"createdAt":"2025-03-11T02:51:19Z"`,
		`"identifiers":[{"kind":"dns","value":"web.internal.example.com"}]`,
		`"related":[{"type":"elbv2:targetGroup","id":"arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web-prod-tg/abc","identifierKind":"arn","relation":"reverse-only"}]`,
	} {
		if !strings.Contains(body, key) {
			t.Errorf("출력에 %s가 없다\n%s", key, body)
		}
	}
}

func TestSnapshotKeysAndTypeIDsStayEnglish(t *testing.T) {
	t.Parallel()

	// 주석과 문서는 한국어로 쓰지만 JSON 키, 리소스 타입 ID, 관계 이름은 출력 계약이므로
	// 영어로 유지한다. 번역하면 이 출력을 읽는 쪽과 저장된 스냅샷이 모두 깨진다.
	snap := model.NewSnapshot(testNow, testTool, model.Scope{},
		[]model.Resource{{
			Type:    model.TypeELBv2TargetGroup,
			ID:      "web-tg",
			Related: []model.Ref{{Type: model.TypeEC2Instance, ID: "i-a", Relation: "TargetHealthDescriptions.Target.Id"}},
		}}, nil)

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	body := string(out)
	for _, want := range []string{
		`"schemaVersion"`, `"generatedAt"`, `"resources"`, `"errors"`,
		`"elbv2:targetGroup"`, `"ec2:instance"`, `"TargetHealthDescriptions.Target.Id"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("출력에 %s가 없다\n%s", want, body)
		}
	}
}

func TestSnapshotOmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	// Elastic IP에는 ARN도 생성 시각도 없다. 빈 문자열이나 제로 시각을 내보내면 그것이
	// 데이터인 것처럼 잘못 표현하게 된다.
	snap := model.NewSnapshot(testNow, testTool, model.Scope{},
		[]model.Resource{{Type: model.TypeEC2Address, ID: "eipalloc-1"}}, nil)

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	body := string(out)
	for _, unwanted := range []string{`"arn":`, `"createdAt":`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("값이 없을 때 %s는 생략해야 한다\n%s", unwanted, body)
		}
	}
}

func TestCollectErrorMessage(t *testing.T) {
	t.Parallel()

	err := model.CollectError{
		Type:    model.TypeWAFv2WebACL,
		Profile: "prod",
		Region:  "eu-west-1",
		Code:    "AccessDeniedException",
		Message: "wafv2:ListWebACLs 권한이 없습니다",
	}

	got := err.Error()
	for _, want := range []string{"wafv2:webAcl", "prod", "eu-west-1", "권한이 없습니다"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, %q가 없다", got, want)
		}
	}
}
