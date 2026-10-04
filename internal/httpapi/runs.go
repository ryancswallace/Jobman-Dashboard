package httpapi

import (
	"net/http"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "limit", "cursor")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 50, 100)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.Engine.Runs(r.Context(), actor(r), monitoring.RunQuery{Scope: groupScope(r), JobID: r.PathValue("job"), Limit: limit}, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	if _, err := groupQuery(r); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.Engine.Run(r.Context(), actor(r), groupScope(r), r.PathValue("job"), r.PathValue("run"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}
