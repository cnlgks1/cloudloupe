// Package graph는 수집된 리소스의 관계를 공급자 비의존 그래프로 해석한다.
//
// 이 패키지는 AWS SDK나 TUI를 모르며 model.Resource 스냅샷만 소비한다. 수집기가 남긴
// ID·ARN·DNS 참조를 실제 노드로 연결하고, 찾지 못하거나 하나로 확정할 수 없는 관계도
// 버리지 않고 보존한다.
package graph

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/cnlgks1/cloudloupe/internal/model"
)

// Resolution은 간선 대상의 해석 상태다.
type Resolution uint8

const (
	// ResolutionUnresolved는 일치하는 대상이 없는 상태다.
	ResolutionUnresolved Resolution = iota
	// ResolutionResolved는 대상이 하나로 확정된 상태다.
	ResolutionResolved
	// ResolutionAmbiguous는 같은 우선순위의 대상이 둘 이상인 상태다.
	ResolutionAmbiguous
)

// Edge는 한 리소스가 남긴 Ref와 해석된 대상 노드를 함께 보존한다.
//
// Ref의 식별자 종류와 값은 Build에서 정규화되므로 의미가 같은 ID·DNS 참조는 하나의
// 간선으로 합쳐진다. TargetKeys는 결정적으로 정렬된다. 미해결이면 비어 있고, 확정이면
// 하나, 모호하면 같은 우선순위의 모든 후보가 들어간다.
type Edge struct {
	SourceKey  string
	Ref        model.Ref
	Resolution Resolution
	TargetKeys []string
}

// Graph는 리소스 스냅샷과 해석된 관계의 불변 인덱스다.
//
// 필드는 외부에 노출하지 않고 조회 메서드도 방어적 복사본을 반환한다. zero value도 빈
// 그래프로 안전하게 조회할 수 있다.
type Graph struct {
	nodes    []model.Resource
	byKey    map[string]model.Resource
	edges    []Edge
	outgoing map[string][]Edge
	incoming map[string][]Edge
}

type identifierKey struct {
	typeID string
	kind   model.IdentifierKind
	value  string
}

// Build는 리소스 스냅샷으로 결정적인 관계 그래프를 만든다.
//
// 동일한 Resource.Key가 두 번 들어오면 어느 값을 대표 노드로 삼을 수 없으므로 오류를
// 반환한다. 관계 대상이 없거나 모호한 것은 입력 오류가 아니며 Edge 상태로 보존한다.
func Build(resources []model.Resource) (*Graph, error) {
	nodes := cloneResources(resources)
	// model.SortResources가 비교하지 않는 profile까지 먼저 정렬해 stable tie-breaker로 쓴다.
	slices.SortFunc(nodes, func(a, b model.Resource) int {
		return cmp.Compare(a.Key(), b.Key())
	})
	model.SortResources(nodes)

	g := &Graph{
		nodes:    nodes,
		byKey:    make(map[string]model.Resource, len(nodes)),
		outgoing: make(map[string][]Edge),
		incoming: make(map[string][]Edge),
	}

	for _, resource := range nodes {
		key := resource.Key()
		if _, exists := g.byKey[key]; exists {
			return nil, fmt.Errorf("중복 리소스 키: %s", key)
		}
		g.byKey[key] = resource
	}

	index := buildIdentifierIndex(nodes)
	for _, source := range nodes {
		for _, ref := range source.Related {
			g.edges = append(g.edges, resolveEdge(source, ref, index, g.byKey))
		}
	}

	slices.SortFunc(g.edges, compareEdges)
	g.edges = deduplicateEdges(g.edges)

	for _, edge := range g.edges {
		g.outgoing[edge.SourceKey] = append(g.outgoing[edge.SourceKey], edge)
		if edge.Resolution == ResolutionResolved {
			targetKey := edge.TargetKeys[0]
			g.incoming[targetKey] = append(g.incoming[targetKey], edge)
		}
	}

	return g, nil
}

func buildIdentifierIndex(resources []model.Resource) map[identifierKey][]string {
	index := make(map[identifierKey][]string)

	for _, resource := range resources {
		identifiers := []model.Identifier{{Kind: model.IdentifierID, Value: resource.ID}}
		if resource.ARN != "" {
			identifiers = append(identifiers, model.Identifier{Kind: model.IdentifierARN, Value: resource.ARN})
		}
		identifiers = append(identifiers, resource.Identifiers...)

		seen := make(map[identifierKey]struct{}, len(identifiers))
		for _, identifier := range identifiers {
			kind := identifier.Kind
			if kind == "" {
				kind = model.IdentifierID
			}
			lookup := identifierKey{
				typeID: resource.Type,
				kind:   kind,
				value:  normalizeIdentifier(kind, identifier.Value),
			}
			if lookup.value == "" {
				continue
			}
			if _, exists := seen[lookup]; exists {
				continue
			}
			seen[lookup] = struct{}{}
			index[lookup] = append(index[lookup], resource.Key())
		}
	}

	for lookup := range index {
		slices.Sort(index[lookup])
	}

	return index
}

func resolveEdge(
	source model.Resource,
	ref model.Ref,
	index map[identifierKey][]string,
	resources map[string]model.Resource,
) Edge {
	kind := ref.IdentifierKind
	if kind == "" {
		kind = model.IdentifierID
	}
	lookup := identifierKey{
		typeID: ref.Type,
		kind:   kind,
		value:  normalizeIdentifier(kind, ref.ID),
	}

	namespace := strings.TrimSpace(ref.Namespace)
	bestScore := -1
	var best []string
	for _, candidateKey := range index[lookup] {
		candidate := resources[candidateKey]
		if namespace != "" && candidate.Namespace != namespace {
			continue
		}
		score, compatible := scopeScore(source, candidate)
		if !compatible {
			continue
		}
		switch {
		case score > bestScore:
			bestScore = score
			best = []string{candidateKey}
		case score == bestScore:
			best = append(best, candidateKey)
		}
	}

	canonicalRef := ref
	canonicalRef.Namespace = namespace
	canonicalRef.IdentifierKind = kind
	canonicalRef.ID = lookup.value
	edge := Edge{
		SourceKey:  source.Key(),
		Ref:        canonicalRef,
		Resolution: ResolutionUnresolved,
		TargetKeys: []string{},
	}
	if len(best) == 0 {
		return edge
	}

	slices.Sort(best)
	edge.TargetKeys = best
	if len(best) == 1 {
		edge.Resolution = ResolutionResolved
	} else {
		edge.Resolution = ResolutionAmbiguous
	}

	return edge
}

func scopeScore(source, target model.Resource) (int, bool) {
	score := 0

	switch {
	case source.AccountID != "" && target.AccountID != "":
		if source.AccountID != target.AccountID {
			return 0, false
		}
		score += 8
	case source.Profile != target.Profile:
		return 0, false
	}

	if source.Profile != "" && source.Profile == target.Profile {
		score += 2
	}
	if source.Region != "" && source.Region != "global" && source.Region == target.Region {
		score += 4
	}

	return score, true
}

func normalizeIdentifier(kind model.IdentifierKind, value string) string {
	value = strings.TrimSpace(value)
	if kind == model.IdentifierDNS {
		return strings.ToLower(strings.TrimRight(value, "."))
	}

	return value
}

func compareEdges(a, b Edge) int {
	if c := cmp.Compare(a.SourceKey, b.SourceKey); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Ref.Relation, b.Ref.Relation); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Ref.Type, b.Ref.Type); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Ref.Namespace, b.Ref.Namespace); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Ref.IdentifierKind, b.Ref.IdentifierKind); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Ref.ID, b.Ref.ID); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Ref.Via, b.Ref.Via); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Resolution, b.Resolution); c != 0 {
		return c
	}

	return slices.Compare(a.TargetKeys, b.TargetKeys)
}

func deduplicateEdges(edges []Edge) []Edge {
	if len(edges) < 2 {
		return edges
	}

	out := edges[:1]
	for _, edge := range edges[1:] {
		if compareEdges(out[len(out)-1], edge) == 0 {
			continue
		}
		out = append(out, edge)
	}

	return out
}

// Nodes는 모든 리소스를 결정적 순서의 방어적 복사본으로 반환한다.
func (g *Graph) Nodes() []model.Resource {
	if g == nil {
		return []model.Resource{}
	}

	return cloneResources(g.nodes)
}

// Edges는 확정·미해결·모호한 모든 간선을 결정적 순서로 반환한다.
func (g *Graph) Edges() []Edge {
	if g == nil {
		return []Edge{}
	}

	return cloneEdges(g.edges)
}

// Resource는 키에 해당하는 리소스의 방어적 복사본을 반환한다.
func (g *Graph) Resource(key string) (model.Resource, bool) {
	if g == nil {
		return model.Resource{}, false
	}

	resource, exists := g.byKey[key]
	if !exists {
		return model.Resource{}, false
	}

	return cloneResource(resource), true
}

// Outgoing은 source가 남긴 모든 관계를 반환한다. 미해결·모호한 관계도 포함한다.
func (g *Graph) Outgoing(key string) []Edge {
	if g == nil {
		return []Edge{}
	}

	return cloneEdges(g.outgoing[key])
}

// Incoming은 대상으로 확정된 관계만 반환한다.
//
// 모호한 후보를 확정 관계처럼 보여주지 않기 위해 ResolutionResolved 간선만 색인한다.
func (g *Graph) Incoming(key string) []Edge {
	if g == nil {
		return []Edge{}
	}

	return cloneEdges(g.incoming[key])
}

// Unresolved는 대상이 하나도 없는 관계만 반환한다.
func (g *Graph) Unresolved() []Edge {
	return g.edgesWithResolution(ResolutionUnresolved)
}

// Ambiguous는 대상을 하나로 확정할 수 없는 관계만 반환한다.
func (g *Graph) Ambiguous() []Edge {
	return g.edgesWithResolution(ResolutionAmbiguous)
}

func (g *Graph) edgesWithResolution(resolution Resolution) []Edge {
	if g == nil {
		return []Edge{}
	}

	out := make([]Edge, 0)
	for _, edge := range g.edges {
		if edge.Resolution == resolution {
			out = append(out, copyEdge(edge))
		}
	}

	return out
}

func cloneResources(resources []model.Resource) []model.Resource {
	out := make([]model.Resource, len(resources))
	for i, resource := range resources {
		out[i] = cloneResource(resource)
	}

	return out
}

func cloneResource(resource model.Resource) model.Resource {
	resource.Fields = append([]model.Field(nil), resource.Fields...)
	resource.Tags = append([]model.Field(nil), resource.Tags...)
	resource.Identifiers = append([]model.Identifier(nil), resource.Identifiers...)
	resource.Related = append([]model.Ref(nil), resource.Related...)
	if resource.CreatedAt != nil {
		createdAt := *resource.CreatedAt
		resource.CreatedAt = &createdAt
	}

	return resource
}

func cloneEdges(edges []Edge) []Edge {
	out := make([]Edge, len(edges))
	for i, edge := range edges {
		out[i] = copyEdge(edge)
	}

	return out
}

func copyEdge(edge Edge) Edge {
	edge.TargetKeys = append([]string(nil), edge.TargetKeys...)

	return edge
}
