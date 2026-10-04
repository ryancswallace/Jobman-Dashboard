package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
)

type ReportService interface {
	Request(context.Context, monitoring.Actor, reports.Request) (reports.Result, error)
	Get(context.Context, monitoring.Actor, string) (reports.Result, error)
	List(context.Context, monitoring.Actor, api.Scope, string, string, int) (reports.Page, error)
}

var reportUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Server) registerReportRoutes(mux *http.ServeMux) {
	base := "/api/v1/deployments/{deployment}/namespaces/{namespace}/jobs/{job}/reports"
	mux.HandleFunc("GET "+base, s.listReports)
	mux.HandleFunc("POST "+base, s.requestReport)
	mux.HandleFunc("GET "+base+"/{task}", s.report)
	mux.HandleFunc("GET "+base+"/{task}/citations/{citation}", s.reportCitation)
}

func reportSelection(r *http.Request) (api.Scope, string, error) {
	scope, job := groupScope(r), r.PathValue("job")
	for _, id := range []string{scope.DeploymentID, scope.NamespaceID, job} {
		if !reportUUID.MatchString(id) {
			return scope, job, reports.ErrInvalid
		}
	}
	return scope, job, nil
}

func writeReportError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reports.ErrInvalid):
		err = &api.Error{Code: "invalid_request", Message: "Supply valid report identifiers, profile and request fields."}
	case errors.Is(err, reports.ErrConflict):
		err = &api.Error{Code: "revision_conflict", Message: "This request key is already bound to another request. Start a new request."}
	case errors.Is(err, reports.ErrLimit):
		err = &api.Error{Code: "rate_limited", Message: "Report capacity has been reached. Retry later or contact the operator."}
	case errors.Is(err, reports.ErrTaskNotFound):
		err = monitoring.ErrNotFound
	case errors.Is(err, reports.ErrRedactionUnavailable):
		err = &api.Error{Code: "redaction_unavailable", Message: "Log-tail reports require an operator-configured value-aware redaction policy. Metadata reports remain available."}
	}
	writeError(w, r, err)
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "cursor", "limit")
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	limit, err := groupLimit(q, "limit", 20, 20)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	scope, job, err := reportSelection(r)
	if err != nil || q.Has("cursor") && !reportUUID.MatchString(q.Get("cursor")) {
		writeReportError(w, r, reports.ErrInvalid)
		return
	}
	if s.Reports == nil {
		writeReportError(w, r, monitoring.ErrSource)
		return
	}
	page, err := s.Reports.List(r.Context(), actor(r), scope, job, q.Get("cursor"), limit)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	if len(page.Items) > limit || page.NextCursor != "" && !reportUUID.MatchString(page.NextCursor) {
		writeReportError(w, r, reports.ErrObject)
		return
	}
	result := api.ReportPage{Items: []api.Report{}, NextCursor: page.NextCursor, FetchedAt: time.Now().UTC()}
	for _, item := range page.Items {
		if item.Task.Subject.Scope != scope || item.Task.Subject.JobID != job || item.Pair != nil {
			writeReportError(w, r, reports.ErrObject)
			return
		}
		projected, err := reports.Project(item)
		if err != nil {
			writeReportError(w, r, err)
			return
		}
		result.Items = append(result.Items, projected)
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeReportRequest(w http.ResponseWriter, r *http.Request) (api.ReportRequest, error) {
	var result api.ReportRequest
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Idempotency-Key")) != 1 {
		return result, reports.ErrInvalid
	}
	if _, err := reports.IdempotencyHash(r.Header.Get("Idempotency-Key")); err != nil {
		return result, err
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return result, reports.ErrInvalid
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return result, reports.ErrInvalid
		}
		name, ok := key.(string)
		if !ok || seen[name] || name != "profile" && name != "runId" {
			return result, reports.ErrInvalid
		}
		seen[name] = true
		var value *string
		if decoder.Decode(&value) != nil || value == nil {
			return result, reports.ErrInvalid
		}
		if name == "profile" {
			result.Profile = *value
		} else {
			result.RunID = *value
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return result, reports.ErrInvalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || result.Profile != "metadata" && result.Profile != "include_log_tail" || seen["runId"] && !reportUUID.MatchString(result.RunID) {
		return result, reports.ErrInvalid
	}
	return result, nil
}

func (s *Server) requestReport(w http.ResponseWriter, r *http.Request) {
	if _, err := groupQuery(r); err != nil {
		writeReportError(w, r, err)
		return
	}
	scope, job, err := reportSelection(r)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	body, err := decodeReportRequest(w, r)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	if s.Reports == nil {
		writeReportError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Reports.Request(r.Context(), actor(r), reports.Request{Scope: scope, JobID: job, RunID: body.RunID, Profile: body.Profile, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	if result.Task.Subject.Scope != scope || result.Task.Subject.JobID != job || result.Task.Subject.RunID != body.RunID || result.Task.Subject.Profile != body.Profile {
		writeReportError(w, r, reports.ErrObject)
		return
	}
	// POST is always a task summary, including idempotently reused ready tasks.
	result.Pair = nil
	projected, err := reports.Project(result)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, projected)
}

func (s *Server) authorizedReport(r *http.Request) (reports.Result, error) {
	if _, err := groupQuery(r); err != nil {
		return reports.Result{}, err
	}
	scope, job, err := reportSelection(r)
	if err != nil || !reportUUID.MatchString(r.PathValue("task")) {
		return reports.Result{}, reports.ErrInvalid
	}
	if s.Reports == nil {
		return reports.Result{}, monitoring.ErrSource
	}
	result, err := s.Reports.Get(r.Context(), actor(r), r.PathValue("task"))
	if err != nil {
		return reports.Result{}, err
	}
	if result.Task.ID != r.PathValue("task") || result.Task.Subject.Scope != scope || result.Task.Subject.JobID != job {
		return reports.Result{}, monitoring.ErrNotFound
	}
	return result, nil
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	result, err := s.authorizedReport(r)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	projected, err := reports.Project(result)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, projected)
}
func (s *Server) reportCitation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("citation")
	if len(id) == 0 || len(id) > 256 {
		writeReportError(w, r, reports.ErrInvalid)
		return
	}
	result, err := s.authorizedReport(r)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	if result.Pair == nil {
		writeReportError(w, r, monitoring.ErrNotFound)
		return
	}
	citation, err := reports.ResolveCitation(result, id)
	if err != nil {
		writeReportError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, citation)
}
