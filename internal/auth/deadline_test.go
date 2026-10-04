package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type deadlineIdentityStore struct {
	IdentityStore
	stage string
	wait  bool
}

func (s *deadlineIdentityStore) failure(ctx context.Context, stage string) error {
	if stage != s.stage {
		return nil
	}
	if s.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return errors.New("private-database-path-and-credential-must-not-leak")
}
func (s *deadlineIdentityStore) PutLogin(ctx context.Context, hash, data []byte, expires time.Time) error {
	if err := s.failure(ctx, "put"); err != nil {
		return err
	}
	return s.IdentityStore.PutLogin(ctx, hash, data, expires)
}
func (s *deadlineIdentityStore) ConsumeLogin(ctx context.Context, hash []byte) ([]byte, error) {
	if err := s.failure(ctx, "consume"); err != nil {
		return nil, err
	}
	return s.IdentityStore.ConsumeLogin(ctx, hash)
}
func (s *deadlineIdentityStore) ResolveIdentity(ctx context.Context, identity Identity) (monitoring.Actor, error) {
	if err := s.failure(ctx, "resolve"); err != nil {
		return monitoring.Actor{}, err
	}
	return s.IdentityStore.ResolveIdentity(ctx, identity)
}
func (s *deadlineIdentityStore) CreateSession(ctx context.Context, session Session) error {
	if err := s.failure(ctx, "create"); err != nil {
		return err
	}
	return s.IdentityStore.CreateSession(ctx, session)
}
func (s *deadlineIdentityStore) Session(ctx context.Context, hash []byte) (Session, error) {
	if err := s.failure(ctx, "session"); err != nil {
		return Session{}, err
	}
	return s.IdentityStore.Session(ctx, hash)
}

func assertAuthUnavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body api.Error
	if w.Code != 503 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "source_unavailable" || strings.Contains(w.Body.String(), "private-") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("dependency failure was not a sanitized503")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.MaxAge >= 0 {
			t.Fatal("unavailable authentication published a session")
		}
	}
}

func TestLoginDependencyFailureIsUnavailable(t *testing.T) {
	f := newOIDCFixture(t)
	for _, wait := range []bool{false, true} {
		f.o.store = &deadlineIdentityStore{IdentityStore: f.o.store, stage: "put", wait: wait}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		r := httptest.NewRequest("GET", "https://dashboard.example/auth/login", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		f.o.login(w, r)
		cancel()
		assertAuthUnavailable(t, w)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("failed pending login issued a state cookie")
		}
	}
}

func TestCallbackDependencyFailurePreservesConsumedStateAndNoSession(t *testing.T) {
	for _, stage := range []string{"consume", "resolve", "create"} {
		for _, wait := range []bool{false, true} {
			t.Run(stage+"/"+map[bool]string{false: "unavailable", true: "deadline"}[wait], func(t *testing.T) {
				f := newOIDCFixture(t)
				memory := f.o.store.(*memoryIdentityStore)
				cookie, state := f.begin(t)
				f.o.store = &deadlineIdentityStore{IdentityStore: memory, stage: stage, wait: wait}
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				r := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil).WithContext(ctx)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				f.o.callback(w, r)
				cancel()
				assertAuthUnavailable(t, w)
				if len(memory.sessions) != 0 {
					t.Fatal("failed creation retained a pending session")
				}
				// A failed Consume did not prove consumption. Once consumption
				// succeeded, downstream unavailability cannot permit code replay.
				if stage != "consume" {
					f.o.store = memory
					calls := f.tokenCalls
					replay := httptest.NewRecorder()
					f.o.callback(replay, r.WithContext(t.Context()))
					if replay.Code != 401 || f.tokenCalls != calls || len(memory.sessions) != 0 {
						t.Fatal("unavailable callback resurrected consumed state")
					}
				}
			})
		}
	}
}

func TestLogoutDatabaseFailureDoesNotInvalidateCookie(t *testing.T) {
	f := newOIDCFixture(t)
	f.o.store = &deadlineIdentityStore{IdentityStore: f.o.store, stage: "session"}
	r := httptest.NewRequest("POST", "https://dashboard.example/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: strings.Repeat("a", 43)})
	w := httptest.NewRecorder()
	f.o.logout(w, r)
	assertAuthUnavailable(t, w)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("temporary unavailable lookup cleared the browser session")
	}
}

type failingVerifier struct{ err error }

func (f failingVerifier) Verify(context.Context, string) (*oidc.IDToken, error) { return nil, f.err }

func TestVerifierFailureClassification(t *testing.T) {
	f := newOIDCFixture(t)
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"invalid", errors.New("signature invalid"), ErrUnauthenticated},
		{"network", &net.DNSError{Err: "private endpoint", Name: "private.example", IsTimeout: true}, monitoring.ErrSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.o.apiVerifier = failingVerifier{test.err}
			r := httptest.NewRequest("GET", "https://dashboard.example/api/v1/bootstrap", nil)
			r.Header.Set("Authorization", "Bearer synthetic")
			if _, err := f.o.Authenticate(r); !errors.Is(err, test.want) {
				t.Fatal("verifier failure classification differs")
			}
		})
	}
}

type deadlineRoundTripper func(*http.Request) (*http.Response, error)

func (f deadlineRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCallbackTokenRejectionDiffersFromProviderUnavailability(t *testing.T) {
	for _, status := range []int{400, 429, 503} {
		f := newOIDCFixture(t)
		cookie, state := f.begin(t)
		client := *f.o.options.HTTPClient
		transport := client.Transport
		client.Transport = deadlineRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/token" {
				return transport.RoundTrip(r)
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"private-provider-detail"}`)), Request: r}, nil
		})
		f.o.options.HTTPClient = &client
		r := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		f.o.callback(w, r)
		if status == 400 {
			if w.Code != 401 || strings.Contains(w.Body.String(), "private-") {
				t.Fatal("token rejection was misclassified or exposed provider detail")
			}
		} else {
			assertAuthUnavailable(t, w)
		}
	}
}
