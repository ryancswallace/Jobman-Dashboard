package logs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type ServiceAuthenticator interface {
	Authenticate(*http.Request, api.Scope) (monitoring.Actor, error)
}

type ChunkRequest struct {
	Scope       api.Scope `json:"scope"`
	JobID       string    `json:"jobId"`
	RunNumber   string    `json:"runNumber"`
	ExecutionID string    `json:"executionId"`
	Stream      string    `json:"stream"`
	Sequence    string    `json:"sequence"`
}

type Service struct {
	sources map[string]ManifestSource
	local   *LocalChunks
	auth    ServiceAuthenticator
	gate    chan struct{}
	now     func() time.Time
}

func NewService(sources map[string]ManifestSource, local *LocalChunks, auth ServiceAuthenticator) (*Service, error) {
	if len(sources) == 0 || len(sources) > 32 || local == nil || auth == nil {
		return nil, errors.New("incomplete storage broker configuration")
	}
	s := &Service{sources: make(map[string]ManifestSource), local: local, auth: auth, gate: make(chan struct{}, 32), now: time.Now}
	for id, source := range sources {
		if !uuid(id) || source == nil {
			return nil, errors.New("invalid broker source")
		}
		s.sources[id] = source
	}
	for key := range local.roots {
		if s.sources[key.deployment] == nil {
			return nil, errors.New("log root refers to an unconfigured source")
		}
	}
	return s, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chunks/read", s.read)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"alive"}`)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) read(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
		serviceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var request ChunkRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil {
		serviceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		serviceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !uuid(request.Scope.DeploymentID) || !uuid(request.Scope.NamespaceID) || !uuid(request.JobID) || !positive(request.RunNumber) || !uuid(request.ExecutionID) || !positive(request.Sequence) || (request.Stream != "stdout" && request.Stream != "stderr") {
		serviceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	actor, err := s.auth.Authenticate(r, request.Scope)
	if err != nil {
		serviceError(w, http.StatusUnauthorized, "authorization_unavailable")
		return
	}
	source := s.sources[request.Scope.DeploymentID]
	if source == nil {
		serviceError(w, http.StatusNotFound, "not_found_or_inaccessible")
		return
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	default:
		serviceError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	seq, _ := strconv.ParseInt(request.Sequence, 10, 64)
	after := seq - 1
	q := ManifestQuery{Scope: request.Scope, JobID: request.JobID, RunNumber: request.RunNumber, Stream: request.Stream, AfterSequence: &after, Limit: 1}
	m, err := source.Manifest(ctx, actor, q)
	if err != nil {
		serviceSourceError(w, err)
		return
	}
	if !validManifest(m, q, s.now()) || m.ExecutionID != request.ExecutionID {
		serviceError(w, http.StatusConflict, "stream_changed")
		return
	}
	if len(m.Chunks) != 1 || m.Chunks[0].Sequence != seq {
		serviceError(w, http.StatusNotFound, "not_found_or_inaccessible")
		return
	}
	chunk := m.Chunks[0]
	result, err := s.local.ReadChunk(ctx, actor, m, chunk)
	if err != nil {
		serviceSourceError(w, err)
		return
	}
	final, err := source.Manifest(ctx, actor, q)
	if err != nil {
		serviceSourceError(w, err)
		return
	}
	if !validManifest(final, q, s.now()) || !sameAuthority(m, final) || m.RunID != final.RunID || m.ExecutionID != final.ExecutionID || len(final.Chunks) != 1 || !sameChunk(chunk, final.Chunks[0]) || ctx.Err() != nil {
		serviceError(w, http.StatusForbidden, "authorization_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func sameChunk(a, b Chunk) bool {
	return a.Sequence == b.Sequence && a.ByteOffset == b.ByteOffset && a.ByteLength == b.ByteLength && a.StoreName == b.StoreName && a.StoreVersion == b.StoreVersion && a.ObjectKey == b.ObjectKey && a.Checksum == b.Checksum && a.CapturedAt.Equal(b.CapturedAt)
}
func serviceError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.Error{Code: code, Message: "The authorized log read could not be completed."})
}
func serviceSourceError(w http.ResponseWriter, err error) {
	var known *api.Error
	if errors.As(err, &known) {
		switch known.Code {
		case "forbidden", "authorization_unavailable", "unauthenticated":
			serviceError(w, http.StatusForbidden, "authorization_unavailable")
			return
		case "not_found_or_inaccessible":
			serviceError(w, http.StatusNotFound, known.Code)
			return
		}
	}
	serviceError(w, http.StatusServiceUnavailable, "source_unavailable")
}
