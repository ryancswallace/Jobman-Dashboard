package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestDynamicDeadlineCancelsAuthenticationAndPreservesLiveness(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan error, 1)
	var calls atomic.Int32
	s := &Server{Auth: AuthFunc(func(r *http.Request) (monitoring.Actor, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			stopped <- r.Context().Err()
			// A verifier may classify its canceled dependency as invalid auth.
			return monitoring.Actor{}, auth.ErrUnauthenticated
		}
		return monitoring.Actor{}, auth.ErrUnauthenticated
	})}
	server := httptest.NewServer(s.handler(80 * time.Millisecond))
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	result := make(chan *http.Response, 1)
	go func() {
		response, err := client.Get(server.URL + "/api/v1/bootstrap")
		if err != nil {
			result <- nil
			return
		}
		result <- response
	}()
	<-started
	live, err := client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = live.Body.Close()
	if live.StatusCode != 200 {
		t.Fatal("blocked authentication affected liveness")
	}
	response := <-result
	if response == nil {
		t.Fatal("dependency cancellation must produce an HTTP response")
	}
	defer response.Body.Close()
	var failure api.Error
	if response.StatusCode != 503 || json.NewDecoder(response.Body).Decode(&failure) != nil || failure.Code != "source_unavailable" || failure.RequestID == "" || failure.RequestID != response.Header.Get("X-Request-ID") || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("deadline was not a sanitized unavailable response")
	}
	if !errors.Is(<-stopped, context.DeadlineExceeded) {
		t.Fatal("authentication did not receive deadline cancellation")
	}
	followup, err := client.Get(server.URL + "/api/v1/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	_ = followup.Body.Close()
	if followup.StatusCode != 401 {
		t.Fatal("a later invalid credential must remain401")
	}
}

type deadlineRoutes struct{ inspect func(*http.Request) }

func (d deadlineRoutes) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/probe", func(w http.ResponseWriter, r *http.Request) {
		d.inspect(r)
		w.WriteHeader(204)
	})
}

func TestDynamicDeadlineStartsBeforeAuthAndPreservesEarlierDeadline(t *testing.T) {
	for _, path := range []string{"/api/v1/bootstrap", "/auth/probe"} {
		for _, earlier := range []bool{false, true} {
			t.Run(path+"/"+map[bool]string{false: "default", true: "earlier"}[earlier], func(t *testing.T) {
				start := time.Now()
				limit := start.Add(dynamicRequestTimeout)
				r := httptest.NewRequest("GET", path, nil)
				if earlier {
					limit = start.Add(time.Second)
					ctx, done := context.WithDeadline(r.Context(), limit)
					defer done()
					r = r.WithContext(ctx)
				}
				inspect := func(r *http.Request) {
					deadline, ok := r.Context().Deadline()
					if !ok || deadline.After(limit.Add(50*time.Millisecond)) || deadline.Before(limit.Add(-50*time.Millisecond)) {
						t.Error("request budget absent or caller deadline extended")
					}
				}
				s := &Server{AuthRoutes: deadlineRoutes{inspect}, Auth: AuthFunc(func(r *http.Request) (monitoring.Actor, error) {
					inspect(r)
					return monitoring.Actor{}, auth.ErrUnauthenticated
				})}
				s.Handler().ServeHTTP(httptest.NewRecorder(), r)
			})
		}
	}
}

func TestDynamicDeadlineDoesNotTurnCallerCancellationIntoSuccessfulAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Server{Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "synthetic"}}, nil
	})}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/bootstrap", nil).WithContext(ctx))
	body, _ := io.ReadAll(w.Result().Body)
	if w.Code != 503 || string(body) == "" {
		t.Fatal("canceled authentication published a successful actor")
	}
}
