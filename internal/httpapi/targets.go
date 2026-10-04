package httpapi

import (
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"net/http"
)

func (s *Server) registerTargetRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/targets", s.targets)
	m.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/targets/{target}", s.target)
	m.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/targets/{target}/partitions", s.targetPartitions)
}
func (s *Server) targets(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "scope", "limit", "cursor")
	if err != nil {
		writeError(w, r, err)
		return
	}
	parsed, err := parseQuery(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := s.Engine.Targets(r.Context(), actor(r), monitoring.TargetQuery{Scopes: parsed.Scopes, Limit: parsed.Limit}, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}
func (s *Server) target(w http.ResponseWriter, r *http.Request) {
	if _, err := groupQuery(r); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.Engine.Target(r.Context(), actor(r), groupScope(r), r.PathValue("target"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) targetPartitions(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "generationId", "limit", "cursor")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 50, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := s.Engine.TargetPartitions(r.Context(), actor(r), monitoring.TargetPartitionQuery{Scope: groupScope(r), TargetID: r.PathValue("target"), GenerationID: q.Get("generationId"), Limit: limit}, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}
