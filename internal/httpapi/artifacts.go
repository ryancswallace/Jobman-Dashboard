package httpapi

import (
	"net/http"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "runNumber", "limit", "cursor")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 50, 100)
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := s.Engine.Artifacts(r.Context(), actor(r), monitoring.ArtifactQuery{Scope: groupScope(r), JobID: r.PathValue("job"), RunNumber: q.Get("runNumber"), Limit: limit}, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}
