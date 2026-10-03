package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type LogService interface {
	Read(context.Context, monitoring.Actor, logs.Request) (api.LogRange, error)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	invalid := func() {
		writeError(w, r, &api.Error{Code: "invalid_request", Message: "Choose a stream, optional run/cursor, and byte limit from 1 to 262144."})
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		invalid()
		return
	}
	for key, entries := range q {
		if len(entries) != 1 || (key != "stream" && key != "runNumber" && key != "cursor" && key != "limitBytes") {
			invalid()
			return
		}
	}
	stream := q.Get("stream")
	if stream == "" {
		stream = "stdout"
	}
	if stream != "stdout" && stream != "stderr" {
		invalid()
		return
	}
	limit := logs.MaxChunkBytes
	if q.Has("limitBytes") {
		limit, err = strconv.Atoi(q.Get("limitBytes"))
		if err != nil || limit < 1 || limit > logs.MaxChunkBytes || strconv.Itoa(limit) != q.Get("limitBytes") {
			invalid()
			return
		}
	}
	if q.Has("runNumber") {
		number, e := strconv.ParseInt(q.Get("runNumber"), 10, 64)
		if e != nil || number < 1 || strconv.FormatInt(number, 10) != q.Get("runNumber") {
			invalid()
			return
		}
	}
	if len(q.Get("cursor")) > 4096 {
		invalid()
		return
	}
	if s.Logs == nil {
		writeError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Logs.Read(r.Context(), actor(r), logs.Request{Scope: api.Scope{DeploymentID: r.PathValue("deployment"), NamespaceID: r.PathValue("namespace")}, JobID: r.PathValue("job"), RunNumber: q.Get("runNumber"), Stream: stream, LimitBytes: limit, Cursor: q.Get("cursor")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
