package graph_test

import (
	"slices"
	"testing"

	"github.com/cnlgks1/cloudloupe/internal/graph"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

func TestBuildResolvesIDARNAndDNS(t *testing.T) {
	t.Parallel()

	const (
		accountID = "123456789012"
		lbARN     = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:loadbalancer/app/web/abc"
		tgARN     = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:targetgroup/web/def"
		listener  = "arn:aws:elasticloadbalancing:ap-northeast-2:123456789012:listener/app/web/abc/ghi"
	)

	lb := model.Resource{
		Type:      model.TypeELBv2LoadBalancer,
		ID:        "web",
		ARN:       lbARN,
		Profile:   "prod",
		Region:    "ap-northeast-2",
		AccountID: accountID,
		Identifiers: []model.Identifier{{
			Kind:  model.IdentifierDNS,
			Value: "DUALSTACK.WEB-123.ap-northeast-2.elb.amazonaws.com.",
		}},
	}
	tg := model.Resource{
		Type: model.TypeELBv2TargetGroup, ID: "web-tg", ARN: tgARN,
		Profile: "prod", Region: "ap-northeast-2", AccountID: accountID,
	}
	listenerResource := model.Resource{
		Type: model.TypeELBv2Listener, ID: listener, ARN: listener,
		Profile: "prod", Region: "ap-northeast-2", AccountID: accountID,
		Related: []model.Ref{
			{Type: model.TypeELBv2LoadBalancer, ID: lbARN, IdentifierKind: model.IdentifierARN, Relation: "LoadBalancerArn"},
			{Type: model.TypeELBv2TargetGroup, ID: tgARN, IdentifierKind: model.IdentifierARN, Relation: "Actions.TargetGroupArn"},
		},
	}
	record := model.Resource{
		Type: model.TypeRoute53RecordSet, ID: "www.example.com.|A",
		Profile: "prod", Region: "global", AccountID: accountID,
		Related: []model.Ref{{
			Type: model.TypeELBv2LoadBalancer, ID: "dualstack.web-123.ap-northeast-2.elb.amazonaws.com",
			IdentifierKind: model.IdentifierDNS, Relation: "AliasTarget.DNSName",
		}},
	}

	g, err := graph.Build([]model.Resource{tg, record, listenerResource, lb})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := g.Edges(); len(got) != 3 {
		t.Fatalf("Edges = %+v, want 3", got)
	}
	for _, edge := range g.Edges() {
		if edge.Resolution != graph.ResolutionResolved || len(edge.TargetKeys) != 1 {
			t.Errorf("해석되지 않은 간선: %+v", edge)
		}
	}

	incoming := g.Incoming(lb.Key())
	if len(incoming) != 2 {
		t.Fatalf("LB Incoming = %+v, want Listener와 Route 53 2개", incoming)
	}
	if got := g.Outgoing(record.Key()); len(got) != 1 || got[0].TargetKeys[0] != lb.Key() {
		t.Errorf("Route 53 → LB = %+v", got)
	}
}

func TestBuildPreservesUnresolvedAndAmbiguousRelations(t *testing.T) {
	t.Parallel()

	const targetType = "test:target"
	targetSeoul := model.Resource{Type: targetType, ID: "shared", Profile: "prod", AccountID: "123", Region: "ap-northeast-2"}
	targetVirginia := model.Resource{Type: targetType, ID: "shared", Profile: "prod", AccountID: "123", Region: "us-east-1"}
	regional := model.Resource{
		Type: "test:regional", ID: "source", Profile: "prod", AccountID: "123", Region: "ap-northeast-2",
		Related: []model.Ref{{Type: targetType, ID: "shared", Relation: "uses"}},
	}
	global := model.Resource{
		Type: "test:global", ID: "source", Profile: "prod", AccountID: "123", Region: "global",
		Related: []model.Ref{{Type: targetType, ID: "shared", Relation: "uses"}},
	}
	missing := model.Resource{
		Type: "test:missing", ID: "source", Profile: "prod", AccountID: "123", Region: "global",
		Related: []model.Ref{{Type: targetType, ID: "absent", Relation: "uses"}},
	}

	g, err := graph.Build([]model.Resource{targetVirginia, missing, regional, targetSeoul, global})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	regionalEdges := g.Outgoing(regional.Key())
	if len(regionalEdges) != 1 || regionalEdges[0].Resolution != graph.ResolutionResolved ||
		!slices.Equal(regionalEdges[0].TargetKeys, []string{targetSeoul.Key()}) {
		t.Errorf("같은 리전 우선 결과 = %+v", regionalEdges)
	}

	ambiguous := g.Ambiguous()
	wantTargets := []string{targetSeoul.Key(), targetVirginia.Key()}
	slices.Sort(wantTargets)
	if len(ambiguous) != 1 || !slices.Equal(ambiguous[0].TargetKeys, wantTargets) {
		t.Errorf("글로벌 모호 관계 = %+v, want %v", ambiguous, wantTargets)
	}
	if incoming := g.Incoming(targetSeoul.Key()); len(incoming) != 1 || incoming[0].SourceKey != regional.Key() {
		t.Errorf("확정 관계만 Incoming에 있어야 함: %+v", incoming)
	}

	unresolved := g.Unresolved()
	if len(unresolved) != 1 || unresolved[0].SourceKey != missing.Key() || len(unresolved[0].TargetKeys) != 0 {
		t.Errorf("미해결 관계 = %+v", unresolved)
	}
}

func TestBuildDeduplicatesAndDefensivelyCopies(t *testing.T) {
	t.Parallel()

	target := model.Resource{Type: "test:target", ID: "target", Profile: "prod", Region: "r"}
	ref := model.Ref{Type: target.Type, ID: target.ID, Relation: "uses"}
	explicitIDRef := ref
	explicitIDRef.IdentifierKind = model.IdentifierID
	source := model.Resource{
		Type: "test:source", ID: "source", Profile: "prod", Region: "r",
		Fields:  []model.Field{{Key: "이름", Value: "원본"}},
		Related: []model.Ref{ref, explicitIDRef},
	}
	input := []model.Resource{source, target}

	g, err := graph.Build(input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	input[0].Fields[0].Value = "외부 변경"
	input[0].Related[0].ID = "외부 변경"

	if got := g.Outgoing(source.Key()); len(got) != 1 {
		t.Fatalf("중복 제거 결과 = %+v", got)
	} else {
		got[0].TargetKeys[0] = "외부 변경"
	}
	resource, ok := g.Resource(source.Key())
	if !ok || resource.Fields[0].Value != "원본" || resource.Related[0].ID != target.ID {
		t.Fatalf("입력 변경이 그래프에 전파됨: %+v", resource)
	}
	if got := g.Outgoing(source.Key()); got[0].TargetKeys[0] != target.Key() {
		t.Fatalf("반환값 변경이 그래프에 전파됨: %+v", got)
	}
}

func TestBuildRejectsDuplicateResourceKey(t *testing.T) {
	t.Parallel()

	resource := model.Resource{Type: "test:item", ID: "same", Profile: "prod", Region: "r"}
	if _, err := graph.Build([]model.Resource{resource, resource}); err == nil {
		t.Fatal("중복 Resource.Key가 거부되어야 함")
	}
}

func TestZeroGraphIsUseful(t *testing.T) {
	t.Parallel()

	var g graph.Graph
	if len(g.Nodes()) != 0 || len(g.Edges()) != 0 || len(g.Outgoing("missing")) != 0 ||
		len(g.Incoming("missing")) != 0 || len(g.Unresolved()) != 0 || len(g.Ambiguous()) != 0 {
		t.Fatal("zero Graph 조회 결과가 비어 있지 않음")
	}
	if _, ok := g.Resource("missing"); ok {
		t.Fatal("zero Graph에서 리소스를 찾음")
	}
}

func TestBuildResolvesNamespacedTarget(t *testing.T) {
	t.Parallel()

	first := model.Resource{Type: "test:item", ID: "shared", Namespace: "one", Profile: "prod", Region: "global"}
	second := model.Resource{Type: "test:item", ID: "shared", Namespace: "two", Profile: "prod", Region: "global"}
	source := model.Resource{
		Type: "test:source", ID: "source", Profile: "prod", Region: "global",
		Related: []model.Ref{{Type: "test:item", ID: "shared", Namespace: "two", Relation: "uses"}},
	}

	g, err := graph.Build([]model.Resource{first, source, second})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	edges := g.Outgoing(source.Key())
	if len(edges) != 1 || edges[0].Resolution != graph.ResolutionResolved ||
		!slices.Equal(edges[0].TargetKeys, []string{second.Key()}) {
		t.Errorf("namespace 대상 해석 = %+v", edges)
	}
}

func TestBuildDeduplicatesNormalizedDNSRefs(t *testing.T) {
	t.Parallel()

	target := model.Resource{
		Type: "test:endpoint", ID: "endpoint", Profile: "prod", Region: "global",
		Identifiers: []model.Identifier{{Kind: model.IdentifierDNS, Value: "API.EXAMPLE.COM."}},
	}
	source := model.Resource{
		Type: "test:source", ID: "source", Profile: "prod", Region: "global",
		Related: []model.Ref{
			{Type: target.Type, ID: "api.example.com", IdentifierKind: model.IdentifierDNS, Relation: "uses"},
			{Type: target.Type, ID: "API.EXAMPLE.COM.", IdentifierKind: model.IdentifierDNS, Relation: "uses"},
		},
	}

	g, err := graph.Build([]model.Resource{source, target})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	edges := g.Outgoing(source.Key())
	if len(edges) != 1 || edges[0].Ref.ID != "api.example.com" {
		t.Errorf("정규화된 DNS 중복 제거 = %+v", edges)
	}
}
