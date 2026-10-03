package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

func TestWorkloadRoutesEnforceAuthenticationAndStrictBounds(t *testing.T) {
	base := "/api/v1/deployments/a/namespaces/n/workloads/graph/id"
	for _, path := range []string{"/api/v1/workloads/collection", base, base + "/children", base + "/dependencies", base + "/neighborhood?nodeId=node"} {
		w := httptest.NewRecorder()
		app(t, false).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s=%d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/workloads/array?limit=201", "/api/v1/workloads/graph?phase=running", base + "?limit=1&limit=2", base + "/children?limit=0", base + "/dependencies?limit=501", base + "/dependencies?direction=incoming", base + "/dependencies?nodeId=%zz", base + "/neighborhood", base + "/neighborhood?nodeId=node&maxNodes=201", base + "/neighborhood?nodeId=node&maxEdges=501"} {
		w := httptest.NewRecorder()
		app(t, true).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("invalid %s=%d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestFixtureGroupRoutesPreserveScopesPagingAndNeighborhoodBounds(t *testing.T) {
	handler := app(t, true)
	get := func(path string, into any) {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}
	var catalog api.WorkloadPage
	get("/api/v1/workloads/graph?limit=1", &catalog)
	if len(catalog.Items) != 1 || catalog.Total != "2" || catalog.NextCursor == "" {
		t.Fatalf("catalog %+v", catalog)
	}
	first := catalog.Items[0]
	get("/api/v1/workloads/graph?limit=1&cursor="+url.QueryEscape(catalog.NextCursor), &catalog)
	if len(catalog.Items) != 1 || catalog.Items[0].ID != first.ID || catalog.Items[0].DeploymentID == first.DeploymentID {
		t.Fatalf("source identities lost %+v", catalog)
	}
	base := "/api/v1/deployments/" + first.DeploymentID + "/namespaces/" + first.NamespaceID + "/workloads/graph/" + first.ID
	var detail api.WorkloadDetail
	get(base+"?limit=1", &detail)
	if len(detail.Children) != 1 || detail.Children[0].Index != "0" || detail.NextCursor == "" || detail.Total != "85" {
		t.Fatalf("detail %+v", detail)
	}
	var children api.WorkloadChildrenPage
	get(base+"/children?limit=1&cursor="+url.QueryEscape(detail.NextCursor), &children)
	if len(children.Items) != 1 || children.Items[0].Index != "1" {
		t.Fatalf("children %+v", children)
	}
	var neighborhood api.GraphNeighborhood
	node := children.Items[0].ID
	get(base+"/neighborhood?nodeId="+node+"&maxNodes=1&maxEdges=1", &neighborhood)
	if len(neighborhood.Nodes) != 1 || len(neighborhood.Edges) != 0 || neighborhood.OmittedNodes != "2" || neighborhood.OmittedEdges != "2" {
		t.Fatalf("bounds %+v", neighborhood)
	}
	var edges api.GraphEdgePage
	get(base+"/dependencies?limit=1&nodeId="+node+"&direction=incoming", &edges)
	if len(edges.Items) != 1 || edges.Items[0].ToJobID != node || edges.Total != "1" {
		t.Fatalf("edges %+v", edges)
	}
	get("/api/v1/workloads/array", &catalog)
	array := catalog.Items[0]
	get("/api/v1/deployments/"+array.DeploymentID+"/namespaces/"+array.NamespaceID+"/workloads/array/"+array.ID+"?limit=1", &detail)
	if detail.Children[0].TaskIndex != "0" {
		t.Fatal("zero array index lost")
	}
}
