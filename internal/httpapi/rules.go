package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type RuleService interface {
	List(context.Context, monitoring.Actor, string, int) (notifications.RulePage, error)
	Get(context.Context, monitoring.Actor, string) (notifications.RuleView, error)
	Create(context.Context, monitoring.Actor, notifications.RuleInput) (notifications.RuleView, error)
	Update(context.Context, monitoring.Actor, string, int64, notifications.RuleInput) (notifications.RuleView, error)
	SetEnabled(context.Context, monitoring.Actor, string, int64, bool) (notifications.RuleView, error)
	Revalidate(context.Context, monitoring.Actor, string, int64, bool) (notifications.RuleView, error)
	Delete(context.Context, monitoring.Actor, string, int64) error
}

func (s *Server) registerRuleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/rules", s.rules)
	mux.HandleFunc("POST /api/v1/rules", s.createRule)
	mux.HandleFunc("GET /api/v1/rules/{rule}", s.rule)
	mux.HandleFunc("PUT /api/v1/rules/{rule}", s.updateRule)
	mux.HandleFunc("DELETE /api/v1/rules/{rule}", s.deleteRule)
	mux.HandleFunc("PUT /api/v1/rules/{rule}/enabled", s.enableRule)
	mux.HandleFunc("POST /api/v1/rules/{rule}/revalidate", s.revalidateRule)
}

func writeRuleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, notifications.ErrUnsupportedOutcome):
		err = &api.Error{Code: "unsupported_outcome", Message: "This server does not support one of the selected job results. Reload the rule editor or choose all final results."}
	case errors.Is(err, notifications.ErrInvalid), errors.Is(err, notifications.ErrTransition):
		err = &api.Error{Code: "invalid_request", Message: "The alert rule could not be saved. Reload the rule, check its settings, and try again."}
	case errors.Is(err, notifications.ErrConflict):
		err = &api.Error{Code: "revision_conflict", Message: "This rule changed. Reload it before editing."}
	case errors.Is(err, notifications.ErrNotFound):
		err = monitoring.ErrNotFound
	case errors.Is(err, notifications.ErrRateLimited):
		err = &api.Error{Code: "rate_limited", Message: "Too many alert-rule edits. Retry later; disabling and deleting rules remain available."}
	case errors.Is(err, notifications.ErrCapacity):
		err = &api.Error{Code: "rule_capacity", Message: "The server has reached its alert rule or history limit. You can still disable or delete rules. Contact your administrator for help adding rules."}
	case errors.Is(err, notifications.ErrInactiveFeed):
		err = &api.Error{Code: "source_unavailable", Message: "Monitoring data changed. Reload the rule and try again when Jobman Control is available."}
	}
	writeError(w, r, err)
}

func ruleUUID(id string) bool {
	return reportUUID.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

func ruleRevision(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return 0, notifications.ErrInvalid
	}
	return n, nil
}

func ruleSelection(r *http.Request, mutation bool) (string, int64, error) {
	id := r.PathValue("rule")
	if !ruleUUID(id) {
		return "", 0, notifications.ErrInvalid
	}
	if _, err := groupQuery(r); err != nil {
		return "", 0, notifications.ErrInvalid
	}
	if !mutation {
		return id, 0, nil
	}
	if len(r.Header.Values("If-Match")) != 1 {
		return "", 0, notifications.ErrInvalid
	}
	value := r.Header.Get("If-Match")
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	revision, err := ruleRevision(value)
	return id, revision, err
}

// Decode objects by token before assigning fields: encoding/json's ordinary
// struct decoder otherwise accepts duplicate and case-insensitive field names.
func ruleObject(data []byte, fields ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, notifications.ErrInvalid
	}
	result := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || !slices.Contains(fields, name) || result[name] != nil {
			return nil, notifications.ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return nil, notifications.ErrInvalid
		}
		result[name] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(result) != len(fields) {
		return nil, notifications.ErrInvalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, notifications.ErrInvalid
	}
	return result, nil
}

func ruleBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, notifications.ErrInvalid
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || !utf8.Valid(data) {
		return nil, notifications.ErrInvalid
	}
	return data, nil
}

func ruleArray(data []byte, limit int) ([]json.RawMessage, error) {
	var result []json.RawMessage
	if json.Unmarshal(data, &result) != nil || result == nil || len(result) > limit {
		return nil, notifications.ErrInvalid
	}
	return result, nil
}

func decodeRuleInput(w http.ResponseWriter, r *http.Request) (notifications.RuleInput, error) {
	var result notifications.RuleInput
	data, err := ruleBody(w, r, notifications.MaximumInputBytes)
	if err != nil {
		return result, err
	}
	fields, err := ruleObject(data, "name", "enabled", "scope", "namespaces", "jobs", "outcomeMode", "outcomes")
	if err != nil {
		return result, err
	}
	for name, target := range map[string]any{"name": &result.Name, "enabled": &result.Enabled, "scope": &result.Scope, "outcomeMode": &result.OutcomeMode} {
		if json.Unmarshal(fields[name], target) != nil {
			return result, notifications.ErrInvalid
		}
	}
	outcomes, err := ruleArray(fields["outcomes"], 6)
	if err != nil {
		return result, err
	}
	result.Outcomes = make([]string, len(outcomes))
	for i, raw := range outcomes {
		var outcome *string
		if json.Unmarshal(raw, &outcome) != nil || outcome == nil {
			return result, notifications.ErrInvalid
		}
		result.Outcomes[i] = *outcome
	}
	namespaces, err := ruleArray(fields["namespaces"], notifications.MaximumNamespaces)
	if err != nil {
		return result, err
	}
	result.Namespaces = make([]notifications.NamespaceRef, len(namespaces))
	for i, raw := range namespaces {
		if _, err = ruleObject(raw, "deploymentId", "namespaceId"); err != nil || json.Unmarshal(raw, &result.Namespaces[i]) != nil {
			return result, notifications.ErrInvalid
		}
	}
	jobs, err := ruleArray(fields["jobs"], notifications.MaximumWatchedJobs)
	if err != nil {
		return result, err
	}
	result.Jobs = make([]notifications.JobRef, len(jobs))
	for i, raw := range jobs {
		if _, err = ruleObject(raw, "deploymentId", "namespaceId", "jobId"); err != nil || json.Unmarshal(raw, &result.Jobs[i]) != nil {
			return result, notifications.ErrInvalid
		}
	}
	return result.Canonical()
}

func noRuleBody(r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	var b [1]byte
	n, err := r.Body.Read(b[:])
	return n == 0 && err == io.EOF
}

func validRuleView(value notifications.RuleView) bool {
	_, err := ruleRevision(value.Revision)
	if err != nil || !ruleUUID(value.ID) || len(value.Name) == 0 || len(value.Name) > notifications.MaximumNameBytes || !utf8.ValidString(value.Name) || strings.ContainsFunc(value.Name, unicode.IsControl) || value.Scopes == nil || value.Jobs == nil || value.Outcomes == nil || len(value.Scopes) > notifications.MaximumNamespaces || len(value.Jobs) > notifications.MaximumWatchedJobs || value.InaccessibleScopes < 0 || value.UnavailableScopes < 0 || len(value.Scopes)+value.InaccessibleScopes+value.UnavailableScopes < 1 || len(value.Scopes)+value.InaccessibleScopes+value.UnavailableScopes > notifications.MaximumNamespaces || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return false
	}
	if value.Scope != notifications.ScopeNamespaceJobs && value.Scope != notifications.ScopeMyJobs && value.Scope != notifications.ScopeWatchedJobs {
		return false
	}
	if value.OutcomeMode != notifications.OutcomeAllTerminal && value.OutcomeMode != notifications.OutcomeSelected || len(value.Outcomes) > 6 || value.OutcomeMode == notifications.OutcomeSelected && len(value.Outcomes) == 0 || value.OutcomeMode == notifications.OutcomeAllTerminal && len(value.Outcomes) != 0 {
		return false
	}
	for _, outcome := range value.Outcomes {
		if !slices.Contains([]string{"success", "failure", "cancelled", "timed_out", "aborted", "lost"}, outcome) {
			return false
		}
	}
	if value.CreatedAt.Year() < 1 || value.CreatedAt.Year() > 9999 || value.UpdatedAt.Year() > 9999 {
		return false
	}
	visible := map[notifications.NamespaceRef]bool{}
	for _, scope := range value.Scopes {
		if !ruleUUID(scope.DeploymentID) || !ruleUUID(scope.NamespaceID) || visible[scope.NamespaceRef] || !slices.Contains([]string{"active", "pending", "disabled", "inaccessible", "unavailable"}, scope.Status) || (scope.Status == "active") != (scope.ActivatedAt != nil) || scope.ActivatedAt != nil && (scope.ActivatedAt.IsZero() || scope.ActivatedAt.Year() < 1 || scope.ActivatedAt.Year() > 9999) {
			return false
		}
		visible[scope.NamespaceRef] = true
	}
	for _, job := range value.Jobs {
		if !ruleUUID(job.JobID) || !visible[job.Namespace()] {
			return false
		}
	}
	return true
}

func writeRule(w http.ResponseWriter, r *http.Request, status int, expectedID string, value notifications.RuleView) {
	if !validRuleView(value) || expectedID != "" && value.ID != expectedID {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	w.Header().Set("ETag", "\""+value.Revision+"\"")
	if status == http.StatusCreated {
		w.Header().Set("Location", "/api/v1/rules/"+value.ID)
	}
	writeJSON(w, status, value)
}

func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	q, err := groupQuery(r, "cursor", "limit")
	if err != nil || len(q.Get("cursor")) > 128 || strings.ContainsFunc(q.Get("cursor"), unicode.IsControl) || !noRuleBody(r) {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	limit, err := groupLimit(q, "limit", 20, 20)
	if err != nil || q.Has("limit") && strconv.Itoa(limit) != q.Get("limit") {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	page, err := s.Rules.List(r.Context(), actor(r), q.Get("cursor"), limit)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	valid := page.Items != nil && len(page.Items) <= limit && len(page.NextCursor) <= 128 && !strings.ContainsFunc(page.NextCursor, unicode.IsControl)
	prior := ""
	for _, item := range page.Items {
		valid = valid && validRuleView(item) && item.ID > prior
		prior = item.ID
	}
	data, err := json.Marshal(page)
	if !valid || err != nil || len(data) > 2<<20 || page.NextCursor != "" && (len(page.Items) != limit || page.NextCursor == q.Get("cursor")) {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) rule(w http.ResponseWriter, r *http.Request) {
	id, _, err := ruleSelection(r, false)
	if err != nil || !noRuleBody(r) {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Rules.Get(r.Context(), actor(r), id)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	writeRule(w, r, http.StatusOK, id, result)
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	if _, err := groupQuery(r); err != nil {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	input, err := decodeRuleInput(w, r)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Rules.Create(r.Context(), actor(r), input)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	writeRule(w, r, http.StatusCreated, "", result)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, revision, err := ruleSelection(r, true)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	input, err := decodeRuleInput(w, r)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Rules.Update(r.Context(), actor(r), id, revision, input)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	writeRule(w, r, http.StatusOK, id, result)
}

func (s *Server) enableRule(w http.ResponseWriter, r *http.Request) {
	id, revision, err := ruleSelection(r, true)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	data, err := ruleBody(w, r, 1024)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	fields, err := ruleObject(data, "enabled")
	var enabled bool
	if err != nil || json.Unmarshal(fields["enabled"], &enabled) != nil {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Rules.SetEnabled(r.Context(), actor(r), id, revision, enabled)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	writeRule(w, r, http.StatusOK, id, result)
}

func (s *Server) revalidateRule(w http.ResponseWriter, r *http.Request) {
	id, revision, err := ruleSelection(r, true)
	if err != nil || !noRuleBody(r) {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	result, err := s.Rules.Revalidate(r.Context(), actor(r), id, revision, true)
	if err != nil {
		writeRuleError(w, r, err)
		return
	}
	writeRule(w, r, http.StatusOK, id, result)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, revision, err := ruleSelection(r, true)
	if err != nil || !noRuleBody(r) {
		writeRuleError(w, r, notifications.ErrInvalid)
		return
	}
	if s.Rules == nil {
		writeRuleError(w, r, monitoring.ErrSource)
		return
	}
	if err = s.Rules.Delete(r.Context(), actor(r), id, revision); err != nil {
		writeRuleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
