package httpapi

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func (s *Server) registerGroupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workloads/{kind}", s.workloads)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/workloads/{kind}/{workload}", s.workload)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/workloads/{kind}/{workload}/children", s.workloadChildren)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/workloads/graph/{workload}/dependencies", s.graphEdges)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/workloads/graph/{workload}/neighborhood", s.graphNeighborhood)
}
func groupQuery(r *http.Request, names ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, invalidGroupQuery()
	}
	for k, values := range q {
		if len(values) != 1 || values[0] == "" || !slices.Contains(names, k) || len(values[0]) > 96<<10 {
			return nil, invalidGroupQuery()
		}
	}
	if len(q.Get("cursor")) > 512 {
		return nil, invalidGroupQuery()
	}
	return q, nil
}
func invalidGroupQuery() error {
	return &api.Error{Code: "invalid_request", Message: "The workload query or page bounds are invalid."}
}
func groupLimit(q url.Values, name string, fallback, max int) (int, error) {
	if !q.Has(name) {
		return fallback, nil
	}
	n, err := strconv.Atoi(q.Get(name))
	if err != nil || n < 1 || n > max {
		return 0, invalidGroupQuery()
	}
	return n, nil
}
func groupScope(r *http.Request) api.Scope {
	return api.Scope{DeploymentID: r.PathValue("deployment"), NamespaceID: r.PathValue("namespace")}
}
func (s *Server) workloads(w http.ResponseWriter, r *http.Request) {
	if _, err := groupQuery(r, "scope", "cursor", "limit"); err != nil {
		writeError(w, r, err)
		return
	}
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Engine.Workloads(r.Context(), actor(r), monitoring.GroupQuery{Scopes: q.Scopes, Kind: r.PathValue("kind"), Limit: q.Limit}, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) workload(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "limit", "cursor")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 50, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Engine.Workload(r.Context(), actor(r), groupScope(r), r.PathValue("kind"), r.PathValue("workload"), limit, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) workloadChildren(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "limit", "cursor")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 50, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Engine.WorkloadChildren(r.Context(), actor(r), groupScope(r), r.PathValue("kind"), r.PathValue("workload"), limit, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) graphEdges(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "limit", "cursor", "nodeId", "direction")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 100, 500)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Engine.GraphEdges(r.Context(), actor(r), groupScope(r), r.PathValue("workload"), monitoring.GraphEdgeQuery{Limit: limit, NodeID: q.Get("nodeId"), Direction: q.Get("direction")}, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) graphNeighborhood(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "nodeId", "maxNodes", "maxEdges")
	if err != nil || q.Get("nodeId") == "" {
		writeError(w, r, invalidGroupQuery())
		return
	}
	nodes, err := groupLimit(q, "maxNodes", 50, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	edges, err := groupLimit(q, "maxEdges", 100, 500)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Engine.GraphNeighborhood(r.Context(), actor(r), groupScope(r), r.PathValue("workload"), q.Get("nodeId"), nodes, edges)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
