package demo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/catalog"
	"github.com/cnlgks1/cloudloupe/internal/demo"
	"github.com/cnlgks1/cloudloupe/internal/graph"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// TestFieldsCoverCatalogColumns는 데모 리소스의 Fields 키가 카탈로그 열을 모두 채우는지
// 확인한다. 열은 있는데 셀이 비는 어긋남을 잡는다. 데모는 체험용이라 화면이 -로 비면 값이
// 떨어진다. 카탈로그 열이 바뀌면 여기서 드러난다.
func TestFieldsCoverCatalogColumns(t *testing.T) {
	t.Parallel()

	columns := make(map[string][]string)
	for _, def := range catalog.Definitions() {
		columns[def.Type] = def.Columns
	}

	for _, resource := range demo.Resources() {
		want, ok := columns[resource.Type]
		if !ok {
			t.Errorf("%s: 카탈로그에 없는 타입", resource.Type)

			continue
		}

		have := make(map[string]bool, len(resource.Fields))
		for _, field := range resource.Fields {
			have[field.Key] = true
		}

		for _, column := range want {
			if !have[column] {
				t.Errorf("%s(%s): 열 %q에 대응하는 필드가 없다", resource.Type, resource.ID, column)
			}
		}
	}
}

// TestResourcesUseExampleAccountOnly는 데모 데이터가 오직 예제 계정만 쓰는지 확인한다.
// 실수로 실제 계정 ID가 섞여 스크린샷에 노출되는 것을 막는 안전장치다.
func TestResourcesUseExampleAccountOnly(t *testing.T) {
	t.Parallel()

	for _, resource := range demo.Resources() {
		if resource.AccountID != demo.AccountID {
			t.Errorf("%s: AccountID = %q, want %q", resource.ID, resource.AccountID, demo.AccountID)
		}
		// ARN 안에도 다른 계정이 섞이지 않았는지 본다.
		if resource.ARN != "" && strings.Contains(resource.ARN, ":") &&
			!strings.Contains(resource.ARN, demo.AccountID) &&
			strings.Contains(resource.ARN, "arn:aws:") &&
			// S3·글로벌 ARN 등 계정이 없는 형식은 예외.
			accountSegmentPresent(resource.ARN) {
			t.Errorf("%s: ARN에 예제 계정이 아닌 계정이 있다: %s", resource.ID, resource.ARN)
		}
	}
}

// accountSegmentPresent는 ARN의 다섯 번째 세그먼트(계정)가 비어 있지 않은지 본다.
// arn:aws:service:region:account:resource 형식에서 account 자리가 채워진 경우만 검사 대상이다.
func accountSegmentPresent(arn string) bool {
	parts := strings.SplitN(arn, ":", 6)

	return len(parts) >= 5 && parts[4] != ""
}

// TestGraphBuildsWithoutDuplicateKeys는 데모 리소스로 그래프가 빌드되고 관계가 실제로
// 해석되는지 확인한다. 관계가 하나도 안 이어지면 상세·그래프 화면이 비어 데모 의미가 없다.
func TestGraphBuildsWithoutDuplicateKeys(t *testing.T) {
	t.Parallel()

	g, err := graph.Build(demo.Resources())
	if err != nil {
		t.Fatalf("graph.Build 실패: %v", err)
	}

	resolved := 0
	for _, edge := range g.Edges() {
		if edge.Resolution == graph.ResolutionResolved {
			resolved++
		}
	}
	if resolved == 0 {
		t.Error("해석된 관계가 0개다. ID/ARN 매칭이 어긋났다")
	}
}

// TestCollectFiltersByType은 선택한 타입만 반환하는지 확인한다. 실제 Collect와 같은 동작이다.
func TestCollectFiltersByType(t *testing.T) {
	t.Parallel()

	deps := demo.NewDeps()

	all := deps.Collect(context.Background(), demo.Profile, []string{demo.Region}, nil, awsclient.Locations{})
	if len(all.Resources) == 0 {
		t.Fatal("타입 미지정 조회가 비었다")
	}

	only := deps.Collect(context.Background(), demo.Profile, []string{demo.Region},
		[]string{model.TypeEC2Instance}, awsclient.Locations{})
	for _, r := range only.Resources {
		if r.Type != model.TypeEC2Instance {
			t.Errorf("타입 필터가 새어 %s가 나왔다", r.Type)
		}
	}
	if len(only.Resources) == 0 {
		t.Error("ec2:instance만 골랐는데 결과가 비었다")
	}
}
