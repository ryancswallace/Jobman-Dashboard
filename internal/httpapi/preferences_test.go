package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/fixtures"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type preferenceFixture struct {
	account string
	value   api.Preferences
}

func (p *preferenceFixture) Preferences(_ context.Context, account string) (api.Preferences, error) {
	p.account = account
	return p.value, nil
}
func (p *preferenceFixture) UpdatePreferences(_ context.Context, account, revision string, value api.Preferences) (api.Preferences, error) {
	p.account = account
	if revision != p.value.Revision {
		return api.Preferences{}, &api.Error{Code: "revision_conflict", Message: "Reload"}
	}
	value.Revision = "2"
	p.value = value
	return value, nil
}
func TestPreferencesBoundToAuthenticatedAccountAndRevision(t *testing.T) {
	engine, err := monitoring.New(fixtures.Sources(time.Now()), monitoring.NewMemoryCursors())
	if err != nil {
		t.Fatal(err)
	}
	store := &preferenceFixture{value: api.DefaultPreferences()}
	server := (&Server{Engine: engine, Preferences: store, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: fixtures.AccountID}, CSRFToken: "synthetic-csrf"}, nil
	})}).Handler()
	for _, tc := range []struct {
		body, revision string
		status         int
	}{
		{`{"revision":"1","timezone":"UTC","appearance":"dark","refreshSeconds":10}`, "1", 200},
		{`{"revision":"1","timezone":"UTC","appearance":"light","refreshSeconds":5}`, "1", 409},
		{`{"revision":"2","timezone":"UTC","appearance":"dark","refreshSeconds":10,"accountId":"intruder"}`, "2", 400},
		{`{"revision":"2","timezone":"UTC","appearance":"dark","refreshSeconds":10}`, "*", 400},
		{`{"revision":"1","timezone":"UTC","appearance":"dark","refreshSeconds":10}`, "2", 400},
	} {
		r := httptest.NewRequest("PUT", "/api/v1/preferences", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", tc.revision)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.revision, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/bootstrap", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"appearance":"dark"`) || !strings.Contains(w.Body.String(), `"csrfToken":"synthetic-csrf"`) || store.account != fixtures.AccountID {
		t.Fatalf("personal bootstrap: %d %s", w.Code, w.Body.String())
	}
}
