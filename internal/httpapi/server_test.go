package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/fixtures"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func app(t *testing.T, authenticated bool) http.Handler {
	t.Helper()
	e, err := monitoring.New(fixtures.Sources(time.Now()), monitoring.NewMemoryCursors())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Engine: e, Static: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>Dashboard</html>")}}}
	if authenticated {
		s.Auth = AuthFunc(func(*http.Request) (monitoring.Actor, error) {
			return monitoring.Actor{Account: api.Account{ID: fixtures.AccountID}}, nil
		})
	}
	return s.Handler()
}
func TestNoAuthenticationFailsClosed(t *testing.T) {
	h := app(t, false)
	for _, path := range []string{"/api/v1/bootstrap", "/api/v1/jobs", "/api/v1/overview"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 401 {
			t.Fatalf("%s: %d", path, r.Code)
		}
		if r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
	}
}
func TestFixtureJobsAreBoundedAndSourceQualified(t *testing.T) {
	h := app(t, true)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/jobs?limit=3", nil))
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var page api.Page[api.Job]
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.NextCursor == "" || len(page.Sources) != 2 {
		t.Fatalf("invalid bounded page: %#v", page)
	}
	if page.Items[0].ID != page.Items[1].ID || page.Items[0].DeploymentID == page.Items[1].DeploymentID {
		t.Fatal("duplicate IDs across sources not retained")
	}
}
func TestNoExecutionMutationSurface(t *testing.T) {
	h := app(t, true)
	for _, path := range []string{"/api/v1/jobs", "/api/v1/jobs/id/cancel", "/api/v1/proxy"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", path, nil))
		if r.Code != 404 && r.Code != 405 {
			t.Fatalf("mutation route accepted: %s %d", path, r.Code)
		}
	}
}
func TestInvalidPageSizeAndScopeRejected(t *testing.T) {
	h := app(t, true)
	for _, path := range []string{"/api/v1/jobs?limit=201", "/api/v1/jobs?limit=-1", "/api/v1/jobs?scope=garbage"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 400 {
			t.Fatalf("%s: %d", path, r.Code)
		}
	}
}
