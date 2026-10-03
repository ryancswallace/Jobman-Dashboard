package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type logRecorder struct {
	request logs.Request
	actor   monitoring.Actor
	calls   int
}

func (l *logRecorder) Read(_ context.Context, a monitoring.Actor, r logs.Request) (api.LogRange, error) {
	l.request = r
	l.actor = a
	l.calls++
	return api.LogRange{BytesBase64: "", ExecutionID: "execution", Stream: r.Stream, StartOffset: "7", EndOffset: "7", State: "open"}, nil
}

func TestLogRoutePassesOnlyAuthenticatedSubjectAndBoundedSelectors(t *testing.T) {
	reader := &logRecorder{}
	s := &Server{Logs: reader, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "verified-account"}, DirectoryID: "verified-directory"}, nil
	})}
	base := "/api/v1/deployments/source/namespaces/ns/jobs/job/logs"
	h := s.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base+"?stream=stderr&limitBytes=100&runNumber=3&cursor=opaque", nil))
	if w.Code != 200 || reader.actor.Account.ID != "verified-account" || reader.request.Scope.DeploymentID != "source" || reader.request.Scope.NamespaceID != "ns" || reader.request.JobID != "job" || reader.request.RunNumber != "3" || reader.request.LimitBytes != 100 || reader.request.Cursor != "opaque" {
		t.Fatalf("log request %+v status%d", reader.request, w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("logs cacheable")
	}
	for _, query := range []string{"root=/secret", "accountId=other", "stream=stdin", "stream=stdout&stream=stderr", "limitBytes=262145", "limitBytes=0", "limitBytes=01", "runNumber=-1", "runNumber=00", "cursor=%zz"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", base+"?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("query %q accepted %d", query, w.Code)
		}
	}
	if reader.calls != 1 {
		t.Fatal("invalid query reached log service")
	}
}

func TestMaximumNamespaceSelectionFitsBothCatalogQueryBudgets(t *testing.T) {
	scopes := make([]api.Scope, 320)
	for i := range scopes {
		scopes[i] = api.Scope{DeploymentID: "11111111-1111-4111-8111-111111111111", NamespaceID: fmt.Sprintf("22222222-2222-4222-8222-%012d", i+1)}
	}
	encoded, err := json.Marshal(scopes)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"scope": {string(encoded)}}.Encode()
	if len(query) < 16384 || len(query) > 96<<10 {
		t.Fatalf("unexpected scale query bytes %d", len(query))
	}
	for _, path := range []string{"/api/v1/jobs", "/api/v1/workloads/graph"} {
		r := httptest.NewRequest("GET", path+"?"+query, nil)
		q, err := parseQuery(r)
		if err != nil || len(q.Scopes) != 320 {
			t.Fatalf("scope parse %v", err)
		}
		if _, err = groupQuery(r, "scope"); err != nil {
			t.Fatalf("group scope budget %v", err)
		}
		w := httptest.NewRecorder()
		app(t, true).ServeHTTP(w, r)
		if w.Code == 400 {
			t.Fatalf("valid sized query rejected before authorization: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	app(t, true).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/jobs?scope="+strings.Repeat("x", 96<<10), nil))
	if w.Code != 400 {
		t.Fatal("unbounded query accepted")
	}
}
