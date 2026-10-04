package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testDeployment = "11111111-1111-4111-8111-111111111111"

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, e := NewRegistry("worker", 7, []string{testDeployment}, Build{Version: "development", GoVersion: "go1.26.6", OS: "linux", Architecture: "arm64"})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestMetricLabelsCannotContainRequestOrErrorContents(t *testing.T) {
	r := testRegistry(t)
	r.Observe("source", "jobs", testDeployment, "worker", "ok", 50*time.Millisecond)
	r.Observe("source", "jobs", testDeployment, "worker", "unavailable", 2*time.Second)
	for _, values := range [][5]string{{"private-account", "jobs", testDeployment, "worker", "ok"}, {"source", "/jobs/private-job", testDeployment, "worker", "ok"}, {"source", "jobs", "private-source", "worker", "ok"}, {"source", "jobs", testDeployment, "private-token", "ok"}, {"source", "jobs", testDeployment, "worker", "raw-private-error"}} {
		r.Observe(values[0], values[1], values[2], values[3], values[4], time.Second)
	}
	r.ObserveAuthority(testDeployment, time.Now().Add(-time.Second), time.Now().Add(time.Minute))
	r.Mismatch("raw-private-error")
	r.Mismatch("source_identity")
	out := string(r.Render())
	if strings.Contains(out, "private-") || !strings.Contains(out, `outcome="unavailable"} 1`) || !strings.Contains(out, `le="+Inf"} 2`) || !strings.Contains(out, `kind="source_identity"} 1`) {
		t.Fatal("unsafe or incorrect metrics", out)
	}
	if _, e := NewRegistry("api", 1, []string{testDeployment, testDeployment}, Build{}); e == nil {
		t.Fatal("duplicate source accepted")
	}
	if _, e := NewRegistry("api", 1, nil, Build{Revision: "/private/path"}); e == nil {
		t.Fatal("unbounded build label accepted")
	}
}
func TestReadinessCoalescesAndDoesNotGateLivenessOnSourceState(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	s, e := NewServer(Options{Registry: testRegistry(t), Ready: func(ctx context.Context) error {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return errors.New("PRIVATE-DB-ERROR")
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != 503 || calls.Load() != 0 {
		t.Fatal("startup queried DB")
	}
	s.Started()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			ok, _ := s.checkReady(t.Context())
			if ok {
				t.Error("database error reported ready")
			}
		})
	}
	<-entered
	close(release)
	group.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent readiness probes not coalesced", calls.Load())
	}
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "PRIVATE") {
		t.Fatal("liveness depended on readiness")
	}
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "PRIVATE") {
		t.Fatal("readiness leaked detail")
	}
	s.Draining()
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != 503 {
		t.Fatal("draining reported ready")
	}
}
func TestMetricsOnlyUsesCachedSnapshotAndMarksStaleness(t *testing.T) {
	var calls atomic.Int32
	s, _ := NewServer(Options{Registry: testRegistry(t), Ready: func(context.Context) error { return nil }, Sample: func(context.Context) ([]byte, error) { calls.Add(1); return []byte("private response"), nil }})
	for range 3 {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), "operator_snapshot_available 0") {
			t.Fatal("unobserved snapshot looked healthy")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("metric scrape triggered database")
	}
	s.sampleData = []byte("stored_count 7\n")
	s.sampleTime = time.Now().Add(-10 * time.Second)
	s.sampleOK = false
	result := string(s.sampleMetrics())
	if !strings.Contains(result, "available 0") || !strings.Contains(result, "stored_count 7") || !strings.Contains(result, "snapshot_age_seconds") {
		t.Fatal("short stale evidence hidden or healthy")
	}
	s.sampleTime = time.Now().Add(-61 * time.Second)
	if strings.Contains(string(s.sampleMetrics()), "stored_count") {
		t.Fatal("old snapshot retained indefinitely")
	}
	for _, target := range []string{"/metrics?secret=value", "/unknown"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code == 200 || strings.Contains(rr.Body.String(), "secret") {
			t.Fatal("unexpected operator route accepted/leaked")
		}
	}
}

func TestOperatorHandlerRejectsUnknownLengthAndChunkedBody(t *testing.T) {
	s, _ := NewServer(Options{Registry: testRegistry(t), Ready: func(context.Context) error { return nil }})
	for _, chunked := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		request.ContentLength = -1
		if chunked {
			request.TransferEncoding = []string{"chunked"}
		}
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatal("body-bearing GET accepted", chunked, response.Code)
		}
	}
}

func TestSamplerRejectsCancelledSuccessAndOversizedSnapshots(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		s, _ := NewServer(Options{Registry: testRegistry(t), Ready: func(context.Context) error { return nil }, Sample: func(context.Context) ([]byte, error) {
			calls++
			cancel()
			if oversized {
				return make([]byte, maximumSnapshotBytes+1), nil
			}
			return []byte("should_not_publish 1\n"), nil
		}})
		s.RunSampler(ctx)
		if calls != 1 || s.sampleOK || len(s.sampleData) != 0 || strings.Contains(string(s.sampleMetrics()), "should_not_publish") {
			t.Fatal("cancelled/oversized callback published healthy sample")
		}
	}
}
