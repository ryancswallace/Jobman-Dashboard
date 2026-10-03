package fixtures

import (
	"context"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

// These groups exist only in the explicitly selected synthetic fixture server.
const collectionID = "90000000-0000-4000-8000-000000000001"
const arrayID = "90000000-0000-4000-8000-000000000002"
const graphID = "90000000-0000-4000-8000-000000000003"

func (s *Source) fixtureWorkload(kind, id string) (api.Workload, bool) {
	count, name, mode := 65, "Synthetic collection", "individual"
	switch id {
	case collectionID:
		if kind != "collection" {
			return api.Workload{}, false
		}
	case arrayID:
		if kind != "array" && kind != "collection" {
			return api.Workload{}, false
		}
		count, name, mode = 55, "Synthetic Slurm array", "slurm-array"
	case graphID:
		if kind != "graph" {
			return api.Workload{}, false
		}
		count, name = 85, "Synthetic analysis graph"
	default:
		return api.Workload{}, false
	}
	w := api.Workload{Scope: api.Scope{DeploymentID: s.ID(), NamespaceID: NamespaceID}, ID: id, Name: name, Kind: kind, Revision: "1", CreatedAt: s.rows[0].CreatedAt.Add(-time.Hour), UpdatedAt: s.rows[0].UpdatedAt, AsOf: s.Now(), TotalChildren: strconv.Itoa(count), Counts: map[string]string{}, Concurrency: "8", FailurePolicy: "continue", Phase: "running"}
	if kind == "graph" {
		w.UnsatisfiedPolicy = "skip"
		w.FailurePolicy = ""
	} else {
		w.ArrayMode = mode
		w.ArrayPolicy = "auto"
	}
	totals := map[string]int{"active": 0, "terminal": 0, "success": 0, "failure": 0, "cancelled": 0}
	for _, j := range s.rows[:count] {
		if j.Phase == "terminal" {
			totals["terminal"]++
			if _, ok := totals[j.Outcome]; ok {
				totals[j.Outcome]++
			}
		} else {
			totals["active"]++
		}
	}
	if kind == "graph" {
		totals["waiting"], totals["skipped"], totals["blocked"] = 0, 0, 0
	}
	for name, n := range totals {
		w.Counts[name] = strconv.Itoa(n)
	}
	return w, true
}
func (s *Source) Workloads(_ context.Context, a monitoring.Actor, q monitoring.GroupSourceQuery) (monitoring.WorkloadSourcePage, error) {
	if err := s.allow(a, q.NamespaceID); err != nil {
		return monitoring.WorkloadSourcePage{}, err
	}
	ids := []string{arrayID, collectionID}
	if q.Kind == "array" {
		ids = []string{arrayID}
	} else if q.Kind == "graph" {
		ids = []string{graphID}
	}
	rows := []api.Workload{}
	for _, id := range ids {
		w, _ := s.fixtureWorkload(q.Kind, id)
		if !w.CreatedAt.After(q.CreatedBefore) {
			rows = append(rows, w)
		}
	}
	offset := 0
	if q.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(q.Cursor)
		if err != nil || offset < 0 || offset > len(rows) {
			return monitoring.WorkloadSourcePage{}, monitoring.ErrCursor
		}
	}
	end := min(offset+q.Limit, len(rows))
	next := ""
	if end < len(rows) {
		next = strconv.Itoa(end)
	}
	return monitoring.WorkloadSourcePage{Items: rows[offset:end], Total: strconv.Itoa(len(rows)), NextCursor: next, AsOf: s.Now()}, nil
}
func (s *Source) Workload(_ context.Context, a monitoring.Actor, scope api.Scope, kind, id string) (api.Workload, error) {
	if err := s.allow(a, scope.NamespaceID); err != nil {
		return api.Workload{}, err
	}
	if scope.DeploymentID != s.ID() {
		return api.Workload{}, monitoring.ErrNotFound
	}
	w, ok := s.fixtureWorkload(kind, id)
	if !ok {
		return api.Workload{}, monitoring.ErrNotFound
	}
	return w, nil
}
func (s *Source) fixtureChild(kind, id string, index int) api.WorkloadChild {
	j := s.rows[index]
	groupIndex := index
	if kind == "graph" {
		j.Group = &api.GroupReference{GraphID: id, GraphIndex: &groupIndex}
	} else {
		j.Group = &api.GroupReference{CollectionID: id, CollectionIndex: &groupIndex}
	}
	child := api.WorkloadChild{ID: j.ID, Name: "Node " + strconv.Itoa(index), Index: strconv.Itoa(index), Job: j}
	if id == arrayID {
		child.TaskIndex = strconv.Itoa(index)
	}
	if kind == "graph" {
		child.DependencyCounts = map[string]string{"total": "1", "satisfied": "0", "waiting": "1", "unsatisfied": "0"}
		if index == 0 {
			child.DependencyCounts["total"] = "0"
			child.DependencyCounts["waiting"] = "0"
		} else if state := fixturePredicateState(s.rows[index-1]); state != "waiting" {
			child.DependencyCounts["waiting"] = "0"
			child.DependencyCounts[state] = "1"
		}
		child.Disposition = "run"
	}
	return child
}
func (s *Source) WorkloadChildren(ctx context.Context, a monitoring.Actor, scope api.Scope, kind, id string, after, limit int) (monitoring.WorkloadChildSourcePage, error) {
	w, err := s.Workload(ctx, a, scope, kind, id)
	if err != nil {
		return monitoring.WorkloadChildSourcePage{}, err
	}
	total, _ := strconv.Atoi(w.TotalChildren)
	out := monitoring.WorkloadChildSourcePage{Items: []api.WorkloadChild{}, Total: w.TotalChildren, AsOf: s.Now()}
	end := min(after+1+limit, total)
	for i := after + 1; i < end; i++ {
		out.Items = append(out.Items, s.fixtureChild(kind, id, i))
	}
	if end < total {
		last := end - 1
		out.NextIndex = &last
	}
	return out, nil
}
func (s *Source) fixtureEdges() []api.GraphEdge {
	edges := []api.GraphEdge{}
	for i := 0; i < 84; i++ {
		up := s.rows[i]
		edges = append(edges, api.GraphEdge{From: "Node " + strconv.Itoa(i), To: "Node " + strconv.Itoa(i+1), FromJobID: up.ID, ToJobID: s.rows[i+1].ID, Predicate: "afterok", Outcomes: []string{}, UpstreamPhase: up.Phase, UpstreamOutcome: up.Outcome, State: fixturePredicateState(up)})
	}
	return edges
}
func (s *Source) GraphEdges(ctx context.Context, a monitoring.Actor, scope api.Scope, id string, q monitoring.GraphEdgeQuery) (monitoring.GraphEdgeSourcePage, error) {
	if _, err := s.Workload(ctx, a, scope, "graph", id); err != nil {
		return monitoring.GraphEdgeSourcePage{}, err
	}
	edges := []api.GraphEdge{}
	for _, edge := range s.fixtureEdges() {
		if q.NodeID != "" && ((q.Direction == "incoming" && edge.ToJobID != q.NodeID) || (q.Direction == "outgoing" && edge.FromJobID != q.NodeID) || (edge.FromJobID != q.NodeID && edge.ToJobID != q.NodeID)) {
			continue
		}
		edges = append(edges, edge)
	}
	offset := 0
	if q.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(q.Cursor)
		if err != nil || offset < 0 || offset > len(edges) {
			return monitoring.GraphEdgeSourcePage{}, monitoring.ErrCursor
		}
	}
	end := min(offset+q.Limit, len(edges))
	next := ""
	if end < len(edges) {
		next = strconv.Itoa(end)
	}
	return monitoring.GraphEdgeSourcePage{Items: edges[offset:end], Total: strconv.Itoa(len(edges)), AsOf: s.Now(), NextCursor: next}, nil
}
func (s *Source) GraphNeighborhood(ctx context.Context, a monitoring.Actor, scope api.Scope, id, node string, maxNodes, maxEdges int) (monitoring.GraphNeighborhoodSource, error) {
	if _, err := s.Workload(ctx, a, scope, "graph", id); err != nil {
		return monitoring.GraphNeighborhoodSource{}, err
	}
	center := -1
	for i := 0; i < 85; i++ {
		if s.rows[i].ID == node {
			center = i
			break
		}
	}
	if center < 0 {
		return monitoring.GraphNeighborhoodSource{}, monitoring.ErrNotFound
	}
	indices := []int{center}
	if center > 0 {
		indices = append(indices, center-1)
	}
	if center < 84 {
		indices = append(indices, center+1)
	}
	out := api.GraphNeighborhood{CenterID: node, Nodes: []api.WorkloadChild{}, Edges: []api.GraphEdge{}, TotalNodes: strconv.Itoa(len(indices)), TotalEdges: strconv.Itoa(len(indices) - 1)}
	present := map[string]bool{}
	for _, i := range indices[:min(maxNodes, len(indices))] {
		child := s.fixtureChild("graph", id, i)
		out.Nodes = append(out.Nodes, child)
		present[child.ID] = true
	}
	for _, edge := range s.fixtureEdges() {
		if len(out.Edges) < maxEdges && present[edge.FromJobID] && present[edge.ToJobID] {
			out.Edges = append(out.Edges, edge)
		}
	}
	out.OmittedNodes = strconv.Itoa(len(indices) - len(out.Nodes))
	out.OmittedEdges = strconv.Itoa(len(indices) - 1 - len(out.Edges))
	return monitoring.GraphNeighborhoodSource{GraphNeighborhood: out, AsOf: s.Now()}, nil
}

func fixturePredicateState(job api.Job) string {
	if job.Phase != "terminal" {
		return "waiting"
	}
	if job.Outcome == "success" {
		return "satisfied"
	}
	return "unsatisfied"
}
