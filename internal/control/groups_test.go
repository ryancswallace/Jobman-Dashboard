package control

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func groupGrants(now time.Time, version string) map[string]any {
	v := grants(now, version)
	v["namespaces"].([]any)[0].(map[string]any)["capabilities"] = []string{"namespace.read", "jobs.read", "groups.read"}
	return v
}
func groupDoc(now time.Time) map[string]any {
	return map[string]any{"metadata": map[string]any{"id": jobID, "namespace": "lab", "name": "Synthetic array", "revision": 1, "createdAt": now.Add(-time.Hour), "updatedAt": now}, "spec": map[string]any{"maxActive": 20, "failurePolicy": "continue"}, "status": map[string]any{"total": 100, "active": 20, "terminal": 60, "succeeded": 50, "failed": 10, "cancelled": 0, "arrayMode": "slurm-array", "phase": "running"}}
}
func groupChildDoc(now time.Time) map[string]any {
	j := job(now)
	j["status"].(map[string]any)["group"] = map[string]any{"collectionId": jobID, "collectionIndex": 0}
	return map[string]any{"index": 0, "name": "First task", "arrayTaskIndex": 0, "job": j}
}
func TestGroupAdapterPreservesSourceCountsAndZeroArrayIndex(t *testing.T) {
	now := time.Now().UTC()
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			respond(w, capabilities(now))
		case "/v1/me":
			respond(w, groupGrants(now, "1"))
		case "/v1/namespaces/lab/collections":
			if r.URL.Query().Get("arrayMode") != "slurm-array" {
				t.Error("array filter missing")
			}
			respond(w, map[string]any{"apiVersion": contract, "kind": "CollectionList", "items": []any{groupDoc(now)}, "asOf": now, "createdBefore": now, "total": "1"})
		case "/v1/namespaces/lab/collections/" + jobID + "/summary":
			respond(w, map[string]any{"apiVersion": contract, "kind": "CollectionSummary", "summary": groupDoc(now), "asOf": now})
		case "/v1/namespaces/lab/collections/" + jobID + "/items":
			respond(w, map[string]any{"apiVersion": contract, "kind": "CollectionItemList", "items": []any{groupChildDoc(now)}, "total": "100", "asOf": now, "nextAfterIndex": 0})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	scope := api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}
	p, err := c.Workloads(context.Background(), testActor, monitoring.GroupSourceQuery{GroupQuery: monitoring.GroupQuery{Kind: "array", Limit: 50}, NamespaceID: namespaceID, CreatedBefore: now})
	if err != nil || p.Total != "1" || p.Items[0].TotalChildren != "100" || p.Items[0].Counts["active"] != "20" || p.Items[0].ArrayID != "" {
		t.Fatalf("catalog=%+v,%v", p, err)
	}
	summary, err := c.Workload(t.Context(), testActor, scope, "array", jobID)
	if err != nil || summary.AsOf.IsZero() {
		t.Fatalf("summary=%+v,%v", summary, err)
	}
	children, err := c.WorkloadChildren(t.Context(), testActor, scope, "array", jobID, -1, 1)
	if err != nil || children.Items[0].TaskIndex != "0" || children.Items[0].Index != "0" || children.Items[0].Job.Owner.ID != principalID || children.NextIndex == nil || *children.NextIndex != 0 {
		t.Fatalf("children=%+v,%v", children, err)
	}
}
func TestGroupAdapterRejectsMalformedFactsAndAuthorityChanges(t *testing.T) {
	for _, mode := range []string{"negative-count", "foreign-namespace", "access-change", "missing-cutoff", "wrong-array-mode", "missing-count"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			version := "1"
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					respond(w, capabilities(now))
					return
				}
				if r.URL.Path == "/v1/me" {
					respond(w, groupGrants(now, version))
					return
				}
				g := groupDoc(now)
				response := map[string]any{"apiVersion": contract, "kind": "CollectionList", "items": []any{g}, "total": "1", "asOf": now, "createdBefore": now}
				switch mode {
				case "missing-count":
					delete(g["status"].(map[string]any), "active")
				case "negative-count":
					g["status"].(map[string]any)["active"] = -1
				case "foreign-namespace":
					g["metadata"].(map[string]any)["namespace"] = "elsewhere"
				case "access-change":
					version = "2"
				case "missing-cutoff":
					delete(response, "createdBefore")
				case "wrong-array-mode":
					g["status"].(map[string]any)["arrayMode"] = "individual"
				}
				respond(w, response)
			}))
			if _, err := c.Workloads(t.Context(), testActor, monitoring.GroupSourceQuery{GroupQuery: monitoring.GroupQuery{Kind: "array", Limit: 50}, NamespaceID: namespaceID, CreatedBefore: now}); err == nil {
				t.Fatal("invalid group response accepted")
			}
		})
	}
}
func TestGraphAdapterRejectsDanglingNeighborhoodAndKeepsPredicateState(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "dangling"}[bad], func(t *testing.T) {
			now := time.Now().UTC()
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					respond(w, capabilities(now))
				case "/v1/me":
					respond(w, groupGrants(now, "1"))
				default:
					j := job(now)
					j["status"].(map[string]any)["group"] = map[string]any{"graphId": jobID, "graphIndex": 0}
					node := map[string]any{"index": 0, "name": "first", "job": j, "dependencyCounts": map[string]int{"total": 1, "satisfied": 0, "waiting": 1, "unsatisfied": 0}}
					edge := api.GraphEdge{From: "first", To: "first", FromJobID: jobID, ToJobID: jobID, Predicate: "afterok", Outcomes: []string{}, UpstreamPhase: "running", State: "waiting"}
					if bad {
						edge.ToJobID = instanceID
					}
					if strings.HasSuffix(r.URL.Path, "/dependencies") {
						respond(w, map[string]any{"apiVersion": contract, "kind": "GraphDependencyList", "items": []api.GraphEdge{edge}, "total": "1", "asOf": now})
					} else {
						respond(w, map[string]any{"apiVersion": contract, "kind": "GraphNeighborhood", "centerId": jobID, "nodes": []any{node}, "edges": []api.GraphEdge{edge}, "totalNodes": 1, "totalEdges": 1, "omittedNodes": 0, "omittedEdges": 0, "asOf": now})
					}
				}
			}))
			scope := api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}
			n, err := c.GraphNeighborhood(t.Context(), testActor, scope, jobID, jobID, 50, 100)
			if bad {
				if err == nil {
					t.Fatal("dangling edge accepted")
				}
				return
			}
			if err != nil || n.Nodes[0].DependencyCounts["waiting"] != "1" || n.Edges[0].State != "waiting" {
				t.Fatalf("neighborhood=%+v,%v", n, err)
			}
			p, err := c.GraphEdges(t.Context(), testActor, scope, jobID, monitoring.GraphEdgeQuery{Limit: 100, NodeID: jobID, Direction: "incoming"})
			if err != nil || p.Total != "1" || p.Items[0].Predicate != "afterok" {
				t.Fatalf("edges=%+v,%v", p, err)
			}
		})
	}
}
