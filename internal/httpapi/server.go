package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type Authenticator interface {
	Authenticate(*http.Request) (monitoring.Actor, error)
}
type AuthFunc func(*http.Request) (monitoring.Actor, error)

func (f AuthFunc) Authenticate(r *http.Request) (monitoring.Actor, error) { return f(r) }

type Server struct {
	Engine      *monitoring.Engine
	Auth        Authenticator
	Static      fs.FS
	FixtureMode bool
	AuthRoutes  interface{ RegisterRoutes(*http.ServeMux) }
	Preferences PreferenceStore
	Logs        LogService
	Reports     ReportService
}
type actorKey struct{}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerGroupRoutes(mux)
	s.registerTargetRoutes(mux)
	s.registerReportRoutes(mux)
	if s.AuthRoutes != nil {
		s.AuthRoutes.RegisterRoutes(mux)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /api/v1/bootstrap", s.bootstrap)
	mux.HandleFunc("GET /api/v1/jobs", s.jobs)
	mux.HandleFunc("GET /api/v1/overview", s.overview)
	mux.HandleFunc("PUT /api/v1/preferences", s.updatePreferences)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/jobs/{job}/logs", s.logs)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/jobs/{job}/artifacts", s.artifacts)
	mux.HandleFunc("GET /api/v1/deployments/{deployment}/namespaces/{namespace}/jobs/{job}", s.job)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, &api.Error{Code: "not_found_or_inaccessible", Message: "This API operation is not available."})
	})
	if s.Static != nil {
		assets := http.FileServerFS(s.Static)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/auth/") {
				http.NotFound(w, r)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if !fs.ValidPath(path) || strings.HasPrefix(path, ".") {
				http.NotFound(w, r)
				return
			}
			if _, err := fs.Stat(s.Static, path); err != nil {
				r2 := r.Clone(r.Context())
				u := *r.URL
				u.Path = "/"
				r2.URL = &u
				assets.ServeHTTP(w, r2)
				return
			}
			assets.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [16]byte
		_, _ = rand.Read(id[:])
		w.Header().Set("X-Request-ID", hex.EncodeToString(id[:]))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; worker-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if s.FixtureMode {
			w.Header().Set("X-Jobman-Fixture-Mode", "true")
		}
		if len(r.URL.RawQuery) > 96<<10 {
			writeError(w, r, &api.Error{Code: "invalid_request", Message: "The query is too large."})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if s.Auth == nil {
				writeError(w, r, &api.Error{Code: "unauthenticated", Message: "Sign in to continue."})
				return
			}
			a, err := s.Auth.Authenticate(r)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if a.Account.ID == "" {
				writeError(w, r, &api.Error{Code: "unauthenticated", Message: "Sign in to continue."})
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), actorKey{}, a))
		}
		mux.ServeHTTP(w, r)
	})
}

func actor(r *http.Request) monitoring.Actor { return r.Context().Value(actorKey{}).(monitoring.Actor) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, _ *http.Request, err error) {
	ae := &api.Error{Code: "source_unavailable", Message: "The request could not be completed. Retry or contact the operator with the request ID."}
	var known *api.Error
	if errors.As(err, &known) {
		copy := *known
		ae = &copy
	}
	ae.RequestID = w.Header().Get("X-Request-ID")
	status := 503
	switch ae.Code {
	case "unauthenticated":
		status = 401
	case "forbidden":
		status = 403
	case "not_found_or_inaccessible":
		status = 404
	case "cursor_expired", "revision_conflict", "target_changed", "snapshot_changed":
		status = 409
	case "invalid_request":
		status = 400
	case "invalid_settings":
		status = 422
	case "rate_limited":
		status = 429
		w.Header().Set("Retry-After", "5")
	}
	writeJSON(w, status, ae)
}
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	b, err := s.Engine.Bootstrap(r.Context(), actor(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	b.FixtureMode = s.FixtureMode
	b.CSRFToken = actor(r).CSRFToken
	if s.Preferences != nil {
		b.Preferences, err = s.Preferences.Preferences(r.Context(), actor(r).Account.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	writeJSON(w, 200, b)
}
func parseQuery(r *http.Request) (monitoring.Query, error) {
	q := monitoring.Query{}
	invalid := func(message string) (monitoring.Query, error) {
		return q, &api.Error{Code: "invalid_request", Message: message}
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return invalid("The query string is malformed.")
	}
	known := map[string]bool{"scope": true, "limit": true, "cursor": true, "phase": true, "outcome": true, "owner": true, "jobId": true, "attention": true, "from": true, "to": true, "completedFrom": true, "completedTo": true, "windowHours": true}
	for key, entries := range values {
		if !known[key] {
			return invalid("The query contains an unsupported parameter.")
		}
		if len(entries) != 1 {
			return invalid("Supply each query parameter only once.")
		}
	}
	q.Phase, q.Outcome, q.Owner, q.JobID = values.Get("phase"), values.Get("outcome"), values.Get("owner"), values.Get("jobId")
	for _, text := range []string{q.Phase, q.Outcome, q.Owner, q.JobID} {
		if len(text) > 128 || strings.ContainsAny(text, "\r\n\x00") {
			return invalid("A filter exceeds its permitted length or contains control characters.")
		}
	}
	if values.Has("scope") {
		text := values.Get("scope")
		d := json.NewDecoder(strings.NewReader(text))
		d.DisallowUnknownFields()
		if d.Decode(&q.Scopes) != nil || q.Scopes == nil || len(q.Scopes) > 320 {
			return invalid("Scope must be a bounded array of deployment and namespace IDs.")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return invalid("Scope must contain exactly one JSON array.")
		}
		for _, scope := range q.Scopes {
			if scope.DeploymentID == "" || scope.NamespaceID == "" || len(scope.DeploymentID) > 128 || len(scope.NamespaceID) > 128 || strings.TrimSpace(scope.DeploymentID) != scope.DeploymentID || strings.TrimSpace(scope.NamespaceID) != scope.NamespaceID || strings.ContainsAny(scope.DeploymentID+scope.NamespaceID, "\r\n\x00") {
				return invalid("Each scope requires valid deployment and namespace IDs.")
			}
		}
	}
	if values.Has("limit") {
		q.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || q.Limit < 1 || q.Limit > 200 {
			return invalid("Page size must be between 1 and 200.")
		}
	}
	if len(values.Get("cursor")) > 512 {
		return invalid("The browse cursor is too long.")
	}
	if values.Has("attention") {
		if values.Get("attention") != "true" && values.Get("attention") != "false" {
			return invalid("Attention must be true or false.")
		}
		q.Attention = values.Get("attention") == "true"
	}
	for _, field := range []struct {
		canonical, alias string
		dest             **time.Time
	}{{"completedFrom", "from", &q.CompletedFrom}, {"completedTo", "to", &q.CompletedTo}} {
		if values.Has(field.canonical) && values.Has(field.alias) {
			return invalid("Use one name for each completion-time bound.")
		}
		key := field.canonical
		if values.Has(field.alias) {
			key = field.alias
		}
		if values.Has(key) {
			v, err := time.Parse(time.RFC3339Nano, values.Get(key))
			if err != nil {
				return invalid("Completion timestamps must use RFC 3339 with a timezone.")
			}
			v = v.UTC()
			*field.dest = &v
		}
	}
	if q.CompletedFrom != nil && q.CompletedTo != nil && !q.CompletedFrom.Before(*q.CompletedTo) {
		return invalid("Completion windows use an increasing start and end.")
	}
	if values.Has("windowHours") {
		q.WindowHours, err = strconv.Atoi(values.Get("windowHours"))
		if err != nil || (q.WindowHours != 24 && q.WindowHours != 168) {
			return invalid("Choose a 24-hour or 168-hour completion window.")
		}
		if r.URL.Path != "/api/v1/overview" {
			return invalid("Window hours is supported only by the overview; job queries require explicit time bounds.")
		}
		if q.CompletedFrom != nil || q.CompletedTo != nil {
			return invalid("Choose a window duration or explicit completion bounds, not both.")
		}
	}
	return q, nil
}
func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.Engine.Jobs(r.Context(), actor(r), q, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	d, err := s.Engine.Job(r.Context(), actor(r), api.Scope{DeploymentID: r.PathValue("deployment"), NamespaceID: r.PathValue("namespace")}, r.PathValue("job"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	now := s.Engine.Now()
	hours := q.WindowHours
	if hours == 0 {
		hours = 24
	}
	window := api.Window{From: now.Add(-time.Duration(hours) * time.Hour), To: now}
	if q.CompletedFrom != nil {
		window.From = *q.CompletedFrom
	}
	if q.CompletedTo != nil {
		window.To = *q.CompletedTo
	}
	o, err := s.Engine.Overview(r.Context(), actor(r), q, window)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, o)
}
