package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type groupFixture struct {
	*testSource
	workloads    []api.Workload
	noCapability bool
	children     WorkloadChildSourcePage
	edges        GraphEdgeSourcePage
	neighborhood GraphNeighborhoodSource
}

func groupSource(id string, n int) *groupFixture {
	s := &groupFixture{testSource: source(id, 0, time.Minute)}
	for i := n - 1; i >= 0; i-- {
		s.workloads = append(s.workloads, api.Workload{Scope: api.Scope{DeploymentID: id, NamespaceID: "ns"}, ID: fmt.Sprintf("group-%03d", i), Kind: "collection", Revision: "1", CreatedAt: clock, AsOf: clock, TotalChildren: "100", Counts: map[string]string{"active": "20"}})
	}
	return s
}
func (s *groupFixture) Discover(c context.Context, a Actor) (Discovery, error) {
	d, err := s.testSource.Discover(c, a)
	if !s.noCapability {
		for i := range d.Deployment.Namespaces {
			d.Deployment.Namespaces[i].Capabilities = append(d.Deployment.Namespaces[i].Capabilities, "groups.read")
		}
	}
	return d, err
}
func (s *groupFixture) Workloads(_ context.Context, _ Actor, q GroupSourceQuery) (WorkloadSourcePage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return WorkloadSourcePage{}, ErrSource
	}
	start := 0
	if q.Cursor != "" {
		start, _ = strconv.Atoi(q.Cursor)
	}
	end := min(start+q.Limit, len(s.workloads))
	if s.pageSize > 0 {
		end = min(end, start+s.pageSize)
	}
	next := ""
	if end < len(s.workloads) {
		next = strconv.Itoa(end)
	}
	rows := slices.Clone(s.workloads[start:end])
	for i := range rows {
		rows[i].Kind = q.Kind
	}
	if s.hook != nil {
		s.hook()
	}
	return WorkloadSourcePage{Items: rows, Total: strconv.Itoa(len(s.workloads)), AsOf: clock, NextCursor: next}, nil
}
func (s *groupFixture) Workload(_ context.Context, _ Actor, scope api.Scope, kind, id string) (api.Workload, error) {
	for _, w := range s.workloads {
		if w.ID == id && w.Scope == scope {
			w.Kind = kind
			return w, nil
		}
	}
	return api.Workload{}, ErrNotFound
}
func (s *groupFixture) WorkloadChildren(context.Context, Actor, api.Scope, string, string, int, int) (WorkloadChildSourcePage, error) {
	if s.hook != nil {
		s.hook()
	}
	return s.children, nil
}
func (s *groupFixture) GraphEdges(context.Context, Actor, api.Scope, string, GraphEdgeQuery) (GraphEdgeSourcePage, error) {
	if s.hook != nil {
		s.hook()
	}
	return s.edges, nil
}
func (s *groupFixture) GraphNeighborhood(context.Context, Actor, api.Scope, string, string, int, int) (GraphNeighborhoodSource, error) {
	if s.hook != nil {
		s.hook()
	}
	return s.neighborhood, nil
}
func groupEngine(t *testing.T, sources ...Source) *Engine {
	t.Helper()
	e, err := New(sources, NewMemoryCursors())
	if err != nil {
		t.Fatal(err)
	}
	e.Now = func() time.Time { return clock }
	e.cursors.(*MemoryCursors).Now = e.Now
	return e
}

func TestWorkloadMergeRetainsTiesBuffersAndSourceTotals(t *testing.T) {
	a, b := groupSource("a", 7), groupSource("b", 7)
	a.pageSize = 2
	b.pageSize = 2
	e := groupEngine(t, a, b)
	cursor := ""
	var got []string
	for range 10 {
		page, err := e.Workloads(t.Context(), actor("one"), GroupQuery{Kind: "collection", Limit: 3}, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != "14" || len(page.Totals) != 2 || page.Completeness != "complete" {
			t.Fatalf("lost source totals: %+v", page)
		}
		for _, w := range page.Items {
			got = append(got, w.DeploymentID+"/"+w.ID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	var want []string
	for _, id := range []string{"a", "b"} {
		for i := 6; i >= 0; i-- {
			want = append(want, fmt.Sprintf("%s/group-%03d", id, i))
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("catalog skipped/reordered: %v", got)
	}
}
func TestWorkloadCursorBindsActorKindQueryAuthorityAndEpoch(t *testing.T) {
	for _, mode := range []string{"actor", "kind", "limit", "scope", "version", "epoch", "removed-capability", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			s := groupSource("a", 8)
			e := groupEngine(t, s)
			q := GroupQuery{Kind: "collection", Limit: 2}
			a := actor("one")
			first, err := e.Workloads(t.Context(), a, q, "")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "actor":
				a = actor("other")
			case "kind":
				q.Kind = "graph"
			case "limit":
				q.Limit = 3
			case "scope":
				q.Scopes = []api.Scope{}
			case "version":
				s.version = "2"
			case "epoch":
				s.epoch = "2"
			case "removed-capability":
				s.noCapability = true
			case "revoked":
				s.revoked = true
			}
			if _, err = e.Workloads(t.Context(), a, q, first.NextCursor); err == nil {
				t.Fatal("cursor escaped binding")
			}
		})
	}
}
func TestWorkloadPartialRefillNeverCachesExcludedRows(t *testing.T) {
	a, b := groupSource("a", 5), groupSource("b", 8)
	a.pageSize = 2
	e := groupEngine(t, a, b)
	q := GroupQuery{Kind: "collection", Limit: 3}
	first, err := e.Workloads(t.Context(), actor("one"), q, "")
	if err != nil {
		t.Fatal(err)
	}
	a.fail = true
	second, err := e.Workloads(t.Context(), actor("one"), q, first.NextCursor)
	if err != nil || second.Completeness != "partial" || second.Total != "8" {
		t.Fatalf("partial=%+v,%v", second, err)
	}
	for _, w := range second.Items {
		if w.DeploymentID == "a" {
			t.Fatal("failed contribution was published")
		}
	}
	key := strings.TrimPrefix(first.NextCursor, "w.")
	data, version, _ := e.cursors.Load(t.Context(), key)
	var state workloadBrowseState
	_ = json.Unmarshal(data, &state)
	state.Response.Items = append(state.Response.Items, a.workloads[2])
	data, _ = json.Marshal(state)
	if err = e.cursors.Advance(t.Context(), key, version, data); err != nil {
		t.Fatal(err)
	}
	a.revoked = true
	if _, err = e.Workloads(t.Context(), actor("one"), q, first.NextCursor); err == nil {
		t.Fatal("legacy cache bypassed revoked grant")
	}
}
func TestWorkloadDiscoveryFailureFrozenAndExplicitEmptyDoesNotBroaden(t *testing.T) {
	a, b := groupSource("a", 4), groupSource("b", 4)
	a.failDiscovery = true
	e := groupEngine(t, a, b)
	q := GroupQuery{Kind: "collection", Limit: 1}
	first, err := e.Workloads(t.Context(), actor("one"), q, "")
	if err != nil || first.Completeness != "partial" {
		t.Fatalf("partial=%+v,%v", first, err)
	}
	a.failDiscovery = false
	next, err := e.Workloads(t.Context(), actor("one"), q, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range next.Items {
		if w.DeploymentID == "a" {
			t.Fatal("recovered source entered frozen browse session")
		}
	}
	empty, err := e.Workloads(t.Context(), actor("one"), GroupQuery{Kind: "collection", Scopes: []api.Scope{}, Limit: 1}, "")
	if err != nil || len(empty.Items) != 0 || empty.Total != "0" {
		t.Fatalf("explicit empty broadened: %+v,%v", empty, err)
	}
}

type noGroupSource struct{ Source }

func TestUnsupportedWorkloadSourceIsPartialNotEmptySuccess(t *testing.T) {
	a, b := groupSource("a", 2), groupSource("b", 3)
	e := groupEngine(t, noGroupSource{a}, b)
	p, err := e.Workloads(t.Context(), actor("one"), GroupQuery{Kind: "graph", Limit: 10}, "")
	if err != nil || p.Completeness != "partial" || p.Total != "3" || p.Sources[0].Status != "unsupported" {
		t.Fatalf("unsupported source hidden: %+v,%v", p, err)
	}
}
func TestWorkloadReauthorizationDiscardsReadAndPollingRemainsBounded(t *testing.T) {
	s := groupSource("a", 8)
	e := groupEngine(t, s)
	q := GroupQuery{Kind: "collection", Limit: 2}
	s.hook = func() { s.version = "2" }
	if _, err := e.Workloads(t.Context(), actor("one"), q, ""); err == nil {
		t.Fatal("authority change published rows")
	}
	s.hook = nil
	for range 100 {
		if _, err := e.Workloads(t.Context(), actor("one"), q, ""); err != nil {
			t.Fatal(err)
		}
	}
	store := e.cursors.(*MemoryCursors)
	if len(store.records) > 3 {
		t.Fatalf("unbounded poll state: %d", len(store.records))
	}
}
func TestChildCursorAndNeighborhoodSafety(t *testing.T) {
	s := groupSource("a", 1)
	scope := api.Scope{DeploymentID: "a", NamespaceID: "ns"}
	j := api.Job{Scope: scope, ID: "child"}
	next := 0
	s.children = WorkloadChildSourcePage{Items: []api.WorkloadChild{{ID: j.ID, Index: "0", TaskIndex: "0", Job: j}}, Total: "2", NextIndex: &next, AsOf: clock}
	e := groupEngine(t, s)
	p, err := e.WorkloadChildren(t.Context(), actor("one"), scope, "array", s.workloads[0].ID, 1, "")
	if err != nil || p.Items[0].TaskIndex != "0" || p.NextCursor == "" {
		t.Fatalf("child=%+v,%v", p, err)
	}
	if _, err = e.WorkloadChildren(t.Context(), actor("one"), scope, "array", "other", 1, p.NextCursor); err == nil {
		t.Fatal("child cursor crossed resource")
	}
	s.epoch = "2"
	if _, err = e.WorkloadChildren(t.Context(), actor("one"), scope, "array", s.workloads[0].ID, 1, p.NextCursor); err == nil {
		t.Fatal("child cursor survived recovery")
	}
	s.neighborhood = GraphNeighborhoodSource{GraphNeighborhood: api.GraphNeighborhood{CenterID: "child", Nodes: s.children.Items, Edges: []api.GraphEdge{{FromJobID: "child", ToJobID: "outside"}}, TotalNodes: "1", TotalEdges: "1", OmittedNodes: "0", OmittedEdges: "0"}, AsOf: clock}
	if _, err = e.GraphNeighborhood(t.Context(), actor("one"), scope, "graph", "child", 50, 100); err == nil {
		t.Fatal("dangling edge accepted")
	}
}
