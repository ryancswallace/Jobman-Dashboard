package control

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type groupDocument struct {
	Metadata struct {
		ID        string    `json:"id"`
		Namespace string    `json:"namespace"`
		Name      string    `json:"name"`
		Revision  int64     `json:"revision"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	} `json:"metadata"`
	Spec struct {
		MaxActive         *int64 `json:"maxActive"`
		FailurePolicy     string `json:"failurePolicy"`
		ArrayPolicy       string `json:"arrayPolicy"`
		UnsatisfiedPolicy string `json:"unsatisfiedPolicy"`
	} `json:"spec"`
	Status struct {
		Phase     string `json:"phase"`
		Outcome   string `json:"outcome"`
		ArrayMode string `json:"arrayMode"`
		Total     *int64 `json:"total"`
		Active    *int64 `json:"active"`
		Terminal  *int64 `json:"terminal"`
		Succeeded *int64 `json:"succeeded"`
		Failed    *int64 `json:"failed"`
		Cancelled *int64 `json:"cancelled"`
		Waiting   *int64 `json:"waiting"`
		Skipped   *int64 `json:"skipped"`
		Blocked   *int64 `json:"blocked"`
	} `json:"status"`
}

func (g groupDocument) normalizeGroup(deployment string, ns api.Namespace, kind string, asOf time.Time) (api.Workload, error) {
	m, s, p := g.Metadata, g.Status, g.Spec
	if !uuid(m.ID) || m.Namespace != ns.Name || m.Revision < 1 || m.CreatedAt.IsZero() || asOf.IsZero() || s.Total == nil || *s.Total < 0 || p.MaxActive == nil || *p.MaxActive < 0 || s.Phase == "" {
		return api.Workload{}, monitoring.ErrSource
	}
	if kind == "array" && s.ArrayMode != "slurm-array" {
		return api.Workload{}, monitoring.ErrNotFound
	}
	counts := map[string]*int64{"active": s.Active, "terminal": s.Terminal, "success": s.Succeeded, "failure": s.Failed, "cancelled": s.Cancelled}
	if kind == "graph" {
		counts["waiting"], counts["skipped"], counts["blocked"] = s.Waiting, s.Skipped, s.Blocked
	}
	result := api.Workload{Scope: api.Scope{DeploymentID: deployment, NamespaceID: ns.ID}, ID: m.ID, Name: m.Name, Kind: kind, Revision: strconv.FormatInt(m.Revision, 10), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, AsOf: asOf, TotalChildren: strconv.FormatInt(*s.Total, 10), Counts: map[string]string{}, Concurrency: strconv.FormatInt(*p.MaxActive, 10), FailurePolicy: p.FailurePolicy, UnsatisfiedPolicy: p.UnsatisfiedPolicy, Phase: s.Phase, Outcome: s.Outcome, ArrayMode: s.ArrayMode, ArrayPolicy: p.ArrayPolicy}
	for key, n := range counts {
		if n == nil || *n < 0 || *n > *s.Total {
			return api.Workload{}, monitoring.ErrSource
		}
		result.Counts[key] = strconv.FormatInt(*n, 10)
	}
	return result, nil
}
func groupRoute(kind string) (string, string, error) {
	if !monitoring.ValidGroupKind(kind) {
		return "", "", monitoring.ErrNotFound
	}
	if kind == "graph" {
		return "graphs", "Graph", nil
	}
	return "collections", "Collection", nil
}
func (c *Client) Workloads(ctx context.Context, actor monitoring.Actor, q monitoring.GroupSourceQuery) (monitoring.WorkloadSourcePage, error) {
	route, label, err := groupRoute(q.Kind)
	if err != nil {
		return monitoring.WorkloadSourcePage{}, err
	}
	if q.Limit < 1 || q.Limit > 200 || q.CreatedBefore.IsZero() || len(q.Cursor) > 8192 {
		return monitoring.WorkloadSourcePage{}, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, actor, api.Scope{DeploymentID: c.ID(), NamespaceID: q.NamespaceID}, "groups.read")
	if err != nil {
		return monitoring.WorkloadSourcePage{}, err
	}
	query := url.Values{"limit": {strconv.Itoa(q.Limit)}, "createdBefore": {q.CreatedBefore.UTC().Format(time.RFC3339Nano)}}
	if q.Cursor != "" {
		query.Set("pageToken", q.Cursor)
	}
	if q.Kind == "array" {
		query.Set("arrayMode", "slurm-array")
	}
	var response struct {
		APIVersion    string          `json:"apiVersion"`
		Kind          string          `json:"kind"`
		Items         []groupDocument `json:"items"`
		Total         string          `json:"total"`
		AsOf          time.Time       `json:"asOf"`
		CreatedBefore time.Time       `json:"createdBefore"`
		Next          string          `json:"nextPageToken"`
	}
	if err = c.get(ctx, actor, "groups.read", ns.ID, "/v1/namespaces/"+ns.Name+"/"+route, query, &response); err != nil {
		return monitoring.WorkloadSourcePage{}, err
	}
	if response.APIVersion != contract || response.Kind != label+"List" || response.Items == nil || len(response.Items) > q.Limit || !decimal(response.Total) || response.AsOf.IsZero() || !response.CreatedBefore.Equal(q.CreatedBefore) || len(response.Next) > 8192 || (response.Next != "" && (len(response.Items) == 0 || response.Next == q.Cursor)) {
		return monitoring.WorkloadSourcePage{}, monitoring.ErrSource
	}
	result := monitoring.WorkloadSourcePage{Items: []api.Workload{}, Total: response.Total, AsOf: response.AsOf, NextCursor: response.Next}
	for _, g := range response.Items {
		w, err := g.normalizeGroup(c.ID(), ns, q.Kind, response.AsOf)
		if err != nil {
			return monitoring.WorkloadSourcePage{}, err
		}
		result.Items = append(result.Items, w)
	}
	if err = c.recheck(ctx, actor, d, ns, "groups.read"); err != nil {
		return monitoring.WorkloadSourcePage{}, err
	}
	return result, nil
}
func (c *Client) Workload(ctx context.Context, actor monitoring.Actor, scope api.Scope, kind, id string) (api.Workload, error) {
	route, label, err := groupRoute(kind)
	if err != nil || !uuid(id) {
		return api.Workload{}, monitoring.ErrNotFound
	}
	d, ns, err := c.authorize(ctx, actor, scope, "groups.read")
	if err != nil {
		return api.Workload{}, err
	}
	var response struct {
		APIVersion string        `json:"apiVersion"`
		Kind       string        `json:"kind"`
		Summary    groupDocument `json:"summary"`
		AsOf       time.Time     `json:"asOf"`
	}
	if err = c.get(ctx, actor, "groups.read", ns.ID, "/v1/namespaces/"+ns.Name+"/"+route+"/"+id+"/summary", nil, &response); err != nil {
		return api.Workload{}, err
	}
	if response.APIVersion != contract || response.Kind != label+"Summary" || response.Summary.Metadata.ID != id {
		return api.Workload{}, monitoring.ErrSource
	}
	result, err := response.Summary.normalizeGroup(c.ID(), ns, kind, response.AsOf)
	if err != nil {
		return api.Workload{}, err
	}
	if err = c.recheck(ctx, actor, d, ns, "groups.read"); err != nil {
		return api.Workload{}, err
	}
	return result, nil
}

type groupChild struct {
	Index            int              `json:"index"`
	Name             string           `json:"name"`
	Disposition      string           `json:"disposition"`
	ArrayTaskIndex   *int             `json:"arrayTaskIndex"`
	DependencyCounts map[string]int64 `json:"dependencyCounts"`
	Job              jobResponse      `json:"job"`
}

func normalizeChildren(items []groupChild, c *Client, ns api.Namespace, principal, kind, id string) ([]api.WorkloadChild, error) {
	result := make([]api.WorkloadChild, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		job, err := item.Job.normalize(c.ID(), ns, principal)
		if err != nil {
			return nil, err
		}
		if item.Index < 0 || seen[job.ID] || job.Group == nil {
			return nil, monitoring.ErrSource
		}
		seen[job.ID] = true
		if kind == "graph" {
			if job.Group.GraphID != id || job.Group.GraphIndex == nil || *job.Group.GraphIndex != item.Index {
				return nil, monitoring.ErrSource
			}
		} else if job.Group.CollectionID != id || job.Group.CollectionIndex == nil || *job.Group.CollectionIndex != item.Index {
			return nil, monitoring.ErrSource
		}
		child := api.WorkloadChild{ID: job.ID, Name: item.Name, Index: strconv.Itoa(item.Index), Job: job, Disposition: item.Disposition}
		if item.ArrayTaskIndex != nil {
			if *item.ArrayTaskIndex < 0 || kind == "graph" {
				return nil, monitoring.ErrSource
			}
			child.TaskIndex = strconv.Itoa(*item.ArrayTaskIndex)
		}
		if kind == "array" && item.ArrayTaskIndex == nil {
			return nil, monitoring.ErrSource
		}
		if kind == "graph" {
			if len(item.DependencyCounts) != 4 {
				return nil, monitoring.ErrSource
			}
			child.DependencyCounts = map[string]string{}
			for _, key := range []string{"total", "satisfied", "waiting", "unsatisfied"} {
				v, ok := item.DependencyCounts[key]
				if !ok || v < 0 || v > 1000000 {
					return nil, monitoring.ErrSource
				}
				child.DependencyCounts[key] = strconv.FormatInt(v, 10)
			}
			if item.DependencyCounts["total"] != item.DependencyCounts["satisfied"]+item.DependencyCounts["waiting"]+item.DependencyCounts["unsatisfied"] {
				return nil, monitoring.ErrSource
			}
		}
		result = append(result, child)
	}
	return result, nil
}
func (c *Client) WorkloadChildren(ctx context.Context, actor monitoring.Actor, scope api.Scope, kind, id string, after, limit int) (monitoring.WorkloadChildSourcePage, error) {
	route, label, err := groupRoute(kind)
	if err != nil || !uuid(id) {
		return monitoring.WorkloadChildSourcePage{}, monitoring.ErrNotFound
	}
	if limit < 1 || limit > 200 || after < -1 || after > 9999 {
		return monitoring.WorkloadChildSourcePage{}, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, actor, scope, "groups.read")
	if err != nil {
		return monitoring.WorkloadChildSourcePage{}, err
	}
	childRoute := "items"
	label += "ItemList"
	if kind == "graph" {
		childRoute = "nodes"
		label = "GraphNodeList"
	}
	var response struct {
		APIVersion string       `json:"apiVersion"`
		Kind       string       `json:"kind"`
		Items      []groupChild `json:"items"`
		Total      string       `json:"total"`
		AsOf       time.Time    `json:"asOf"`
		Next       *int         `json:"nextAfterIndex"`
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "afterIndex": {strconv.Itoa(after)}}
	if err = c.get(ctx, actor, "groups.read", ns.ID, "/v1/namespaces/"+ns.Name+"/"+route+"/"+id+"/"+childRoute, q, &response); err != nil {
		return monitoring.WorkloadChildSourcePage{}, err
	}
	if response.APIVersion != contract || response.Kind != label || response.Items == nil || len(response.Items) > limit || response.AsOf.IsZero() || !decimal(response.Total) {
		return monitoring.WorkloadChildSourcePage{}, monitoring.ErrSource
	}
	last := after
	for _, item := range response.Items {
		if item.Index <= last || item.Index > 9999 {
			return monitoring.WorkloadChildSourcePage{}, monitoring.ErrSource
		}
		last = item.Index
	}
	if response.Next != nil && (*response.Next != last || len(response.Items) == 0) {
		return monitoring.WorkloadChildSourcePage{}, monitoring.ErrSource
	}
	items, err := normalizeChildren(response.Items, c, ns, d.principalID, kind, id)
	if err != nil {
		return monitoring.WorkloadChildSourcePage{}, err
	}
	if err = c.recheck(ctx, actor, d, ns, "groups.read"); err != nil {
		return monitoring.WorkloadChildSourcePage{}, err
	}
	return monitoring.WorkloadChildSourcePage{Items: items, Total: response.Total, AsOf: response.AsOf, NextIndex: response.Next}, nil
}
func validEdge(edge api.GraphEdge) bool {
	return uuid(edge.FromJobID) && uuid(edge.ToJobID) && edge.From != "" && edge.To != "" && edge.Predicate != "" && edge.State != "" && edge.UpstreamPhase != "" && len(edge.Outcomes) <= 64
}
func (c *Client) GraphEdges(ctx context.Context, actor monitoring.Actor, scope api.Scope, id string, q monitoring.GraphEdgeQuery) (monitoring.GraphEdgeSourcePage, error) {
	if !uuid(id) || q.Limit < 1 || q.Limit > 500 || len(q.Cursor) > 8192 || (q.NodeID != "" && !uuid(q.NodeID)) || (q.Direction != "" && q.Direction != "incoming" && q.Direction != "outgoing") || (q.Direction != "" && q.NodeID == "") {
		return monitoring.GraphEdgeSourcePage{}, monitoring.ErrNotFound
	}
	d, ns, err := c.authorize(ctx, actor, scope, "groups.read")
	if err != nil {
		return monitoring.GraphEdgeSourcePage{}, err
	}
	query := url.Values{"limit": {strconv.Itoa(q.Limit)}}
	for key, v := range map[string]string{"pageToken": q.Cursor, "nodeId": q.NodeID, "direction": q.Direction} {
		if v != "" {
			query.Set(key, v)
		}
	}
	var response struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Items      []api.GraphEdge `json:"items"`
		Total      string          `json:"total"`
		AsOf       time.Time       `json:"asOf"`
		Next       string          `json:"nextPageToken"`
	}
	if err = c.get(ctx, actor, "groups.read", ns.ID, "/v1/namespaces/"+ns.Name+"/graphs/"+id+"/dependencies", query, &response); err != nil {
		return monitoring.GraphEdgeSourcePage{}, err
	}
	if response.APIVersion != contract || response.Kind != "GraphDependencyList" || response.Items == nil || len(response.Items) > q.Limit || response.AsOf.IsZero() || !decimal(response.Total) || len(response.Next) > 8192 || (response.Next != "" && (response.Next == q.Cursor || len(response.Items) == 0)) {
		return monitoring.GraphEdgeSourcePage{}, monitoring.ErrSource
	}
	prior := ""
	for i, edge := range response.Items {
		key := edge.FromJobID + "/" + edge.ToJobID
		if !validEdge(edge) || (i > 0 && key <= prior) || (q.NodeID != "" && edge.FromJobID != q.NodeID && edge.ToJobID != q.NodeID) || (q.Direction == "incoming" && edge.ToJobID != q.NodeID) || (q.Direction == "outgoing" && edge.FromJobID != q.NodeID) {
			return monitoring.GraphEdgeSourcePage{}, monitoring.ErrSource
		}
		prior = key
		if edge.Outcomes == nil {
			response.Items[i].Outcomes = []string{}
		}
	}
	if err = c.recheck(ctx, actor, d, ns, "groups.read"); err != nil {
		return monitoring.GraphEdgeSourcePage{}, err
	}
	return monitoring.GraphEdgeSourcePage{Items: response.Items, Total: response.Total, AsOf: response.AsOf, NextCursor: response.Next}, nil
}
func (c *Client) GraphNeighborhood(ctx context.Context, actor monitoring.Actor, scope api.Scope, id, node string, maxNodes, maxEdges int) (monitoring.GraphNeighborhoodSource, error) {
	if !uuid(id) || !uuid(node) || maxNodes < 1 || maxNodes > 200 || maxEdges < 1 || maxEdges > 500 {
		return monitoring.GraphNeighborhoodSource{}, monitoring.ErrNotFound
	}
	d, ns, err := c.authorize(ctx, actor, scope, "groups.read")
	if err != nil {
		return monitoring.GraphNeighborhoodSource{}, err
	}
	var response struct {
		APIVersion   string          `json:"apiVersion"`
		Kind         string          `json:"kind"`
		CenterID     string          `json:"centerId"`
		Nodes        []groupChild    `json:"nodes"`
		Edges        []api.GraphEdge `json:"edges"`
		AsOf         time.Time       `json:"asOf"`
		TotalNodes   int64           `json:"totalNodes"`
		TotalEdges   int64           `json:"totalEdges"`
		OmittedNodes int64           `json:"omittedNodes"`
		OmittedEdges int64           `json:"omittedEdges"`
	}
	q := url.Values{"nodeId": {node}, "maxNodes": {strconv.Itoa(maxNodes)}, "maxEdges": {strconv.Itoa(maxEdges)}}
	if err = c.get(ctx, actor, "groups.read", ns.ID, "/v1/namespaces/"+ns.Name+"/graphs/"+id+"/neighborhood", q, &response); err != nil {
		return monitoring.GraphNeighborhoodSource{}, err
	}
	if response.APIVersion != contract || response.Kind != "GraphNeighborhood" || response.CenterID != node || response.AsOf.IsZero() || response.Nodes == nil || response.Edges == nil || len(response.Nodes) > maxNodes || len(response.Edges) > maxEdges || response.OmittedNodes < 0 || response.OmittedEdges < 0 || response.TotalNodes != int64(len(response.Nodes))+response.OmittedNodes || response.TotalEdges != int64(len(response.Edges))+response.OmittedEdges {
		return monitoring.GraphNeighborhoodSource{}, monitoring.ErrSource
	}
	nodes, err := normalizeChildren(response.Nodes, c, ns, d.principalID, "graph", id)
	if err != nil {
		return monitoring.GraphNeighborhoodSource{}, err
	}
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n.ID] = true
	}
	if !ids[node] {
		return monitoring.GraphNeighborhoodSource{}, monitoring.ErrSource
	}
	seen := map[string]bool{}
	for i, edge := range response.Edges {
		key := edge.FromJobID + "/" + edge.ToJobID
		if !validEdge(edge) || !ids[edge.FromJobID] || !ids[edge.ToJobID] || seen[key] {
			return monitoring.GraphNeighborhoodSource{}, monitoring.ErrSource
		}
		seen[key] = true
		if edge.Outcomes == nil {
			response.Edges[i].Outcomes = []string{}
		}
	}
	if err = c.recheck(ctx, actor, d, ns, "groups.read"); err != nil {
		return monitoring.GraphNeighborhoodSource{}, err
	}
	return monitoring.GraphNeighborhoodSource{GraphNeighborhood: api.GraphNeighborhood{CenterID: node, Nodes: nodes, Edges: response.Edges, TotalNodes: strconv.FormatInt(response.TotalNodes, 10), TotalEdges: strconv.FormatInt(response.TotalEdges, 10), OmittedNodes: strconv.FormatInt(response.OmittedNodes, 10), OmittedEdges: strconv.FormatInt(response.OmittedEdges, 10)}, AsOf: response.AsOf}, nil
}
