package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const maximumSnapshotBytes = 2 << 20

var ErrUnavailable = errors.New("process observation unavailable")

type Options struct {
	SocketPath string
	Registry   *Registry
	Ready      func(context.Context) error
	Sample     func(context.Context) ([]byte, error)
}
type Server struct {
	registry      *Registry
	ready         func(context.Context) error
	sample        func(context.Context) ([]byte, error)
	started       atomic.Bool
	draining      atomic.Bool
	gate          chan struct{}
	mu            sync.Mutex
	probing       chan struct{}
	probeTime     time.Time
	probeOK       bool
	sampleData    []byte
	sampleTime    time.Time
	sampleOK      bool
	sampleAttempt time.Time
}

func NewServer(o Options) (*Server, error) {
	if o.Registry == nil || o.Ready == nil {
		return nil, ErrUnavailable
	}
	return &Server{registry: o.Registry, ready: o.Ready, sample: o.Sample, gate: make(chan struct{}, 4)}, nil
}
func (s *Server) Started()  { s.started.Store(true) }
func (s *Server) Draining() { s.draining.Store(true) }
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			http.Error(w, "invalid request", 400)
			return
		}
		select {
		case s.gate <- struct{}{}:
			defer func() { <-s.gate }()
		default:
			http.Error(w, "busy", 503)
			return
		}
		switch r.URL.Path {
		case "/livez":
			s.json(w, 200, map[string]any{"state": "alive", "role": s.registry.role})
		case "/readyz":
			if !s.started.Load() || s.draining.Load() {
				s.json(w, 503, map[string]any{"state": "starting_or_draining", "role": s.registry.role})
				return
			}
			ok, at := s.checkReady(r.Context())
			status, state := 200, "ready"
			if !ok {
				status, state = 503, "unavailable"
			}
			s.json(w, status, map[string]any{"state": state, "role": s.registry.role, "checkedAt": at, "scope": "local_required_dependencies"})
		case "/metrics":
			data := s.registry.Render()
			data = append(data, s.sampleMetrics()...)
			if len(data) > 4<<20 {
				http.Error(w, "metrics unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	})
}
func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) checkReady(parent context.Context) (bool, time.Time) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	for {
		s.mu.Lock()
		if !s.probeTime.IsZero() && time.Since(s.probeTime) < time.Second {
			ok, at := s.probeOK, s.probeTime
			s.mu.Unlock()
			return ok, at
		}
		if s.probing != nil {
			done := s.probing
			s.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return false, time.Now().UTC()
			}
		}
		done := make(chan struct{})
		s.probing = done
		s.mu.Unlock()
		err := s.ready(ctx)
		at := time.Now().UTC()
		s.mu.Lock()
		s.probeOK = err == nil && ctx.Err() == nil
		s.probeTime = at
		s.probing = nil
		close(done)
		ok := s.probeOK
		s.mu.Unlock()
		return ok, at
	}
}

// RunSampler has exactly one sequential, bounded database observation in flight.
// A request to /metrics never triggers a database or dependency request.
func (s *Server) RunSampler(ctx context.Context) {
	if s.sample == nil {
		return
	}
	for {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		data, err := s.sample(bounded)
		timedOut := bounded.Err() != nil
		cancel()
		now := time.Now().UTC()
		s.mu.Lock()
		s.sampleAttempt = now
		s.sampleOK = err == nil && !timedOut && len(data) <= maximumSnapshotBytes
		if s.sampleOK {
			s.sampleData = append([]byte(nil), data...)
			s.sampleTime = now
		}
		s.mu.Unlock()
		timer := time.NewTimer(15 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (s *Server) sampleMetrics() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	configured, available := "0", "0"
	if s.sample != nil {
		configured = "1"
	}
	if s.sampleOK && time.Since(s.sampleTime) <= 30*time.Second {
		available = "1"
	}
	data := []byte("# TYPE jobman_dashboard_operator_snapshot_configured gauge\njobman_dashboard_operator_snapshot_configured " + configured + "\n# TYPE jobman_dashboard_operator_snapshot_available gauge\njobman_dashboard_operator_snapshot_available " + available + "\n")
	if !s.sampleTime.IsZero() {
		data = append(data, []byte(formatSampleAge(s.sampleTime))...)
	}
	// Retain short stale evidence with an explicit unavailable flag and timestamp;
	// retire it after60s instead of exporting arbitrarily old queue/provider state.
	if !s.sampleTime.IsZero() && time.Since(s.sampleTime) <= 60*time.Second {
		data = append(data, s.sampleData...)
	}
	return data
}
