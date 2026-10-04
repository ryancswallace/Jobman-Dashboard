package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
)

type reportServiceFixture struct {
	result  reports.Result
	err     error
	request reports.Request
	calls   int
}

func (service *reportServiceFixture) Request(_ context.Context, _ monitoring.Actor, request reports.Request) (reports.Result, error) {
	service.calls++
	service.request = request
	return service.result, service.err
}
func (service *reportServiceFixture) Get(context.Context, monitoring.Actor, string) (reports.Result, error) {
	service.calls++
	return service.result, service.err
}
func (service *reportServiceFixture) List(context.Context, monitoring.Actor, api.Scope, string, string, int) (reports.Page, error) {
	service.calls++
	return reports.Page{Items: []reports.Result{service.result}}, service.err
}

func reportApp() (*reportServiceFixture, http.Handler, string) {
	subject := reports.Subject{Scope: api.Scope{DeploymentID: "90000000-0000-4000-8000-000000000001", NamespaceID: "90000000-0000-4000-8000-000000000002"}, ControlInstanceID: "90000000-0000-4000-8000-000000000003", JobID: "90000000-0000-4000-8000-000000000004", RecoveryEpoch: "1", Revision: "9007199254740993", Profile: "metadata", SnapshotFingerprint: strings.Repeat("a", 64), EngineVersion: "2", CollectorVersion: "1", CompanionVersion: "test", JobmanVersion: "test"}
	service := &reportServiceFixture{result: reports.Result{Task: reports.Task{ID: "90000000-0000-4000-8000-000000000099", Subject: subject, State: "queued", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}}}
	s := &Server{Reports: service, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "test-account"}}, nil
	})}
	base := "/api/v1/deployments/" + subject.DeploymentID + "/namespaces/" + subject.NamespaceID + "/jobs/" + subject.JobID + "/reports"
	return service, s.Handler(), base
}

func TestReportHTTPStrictRequestsAndAuthenticatedTaskIdentity(t *testing.T) {
	service, handler, base := reportApp()
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", base, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "0123456789abcdef")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := post(`{"profile":"metadata"}`)
	if w.Code != 202 || w.Header().Get("Cache-Control") != "no-store" || service.request.Profile != "metadata" || service.request.RunID != "" || service.request.IdempotencyKey != "0123456789abcdef" {
		t.Fatalf("request=%d %s", w.Code, w.Body.String())
	}
	var result api.Report
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.TaskID != service.result.Task.ID || result.SourceRevision != "9007199254740993" || result.ReportID != "" {
		t.Fatal("task/original report identity confused")
	}
	prior := service.calls
	for _, body := range []string{`{}`, `{"includeLogTail":true}`, `{"profile":"metadata","profile":"include_log_tail"}`, `{"profile":null}`, `{"profile":"metadata","runId":null}`, `{"profile":"metadata","runId":""}`, `{"profile":"metadata","sourceRevision":"12"}`, `{"profile":"unknown"}`, `{"profile":"metadata"} {}`, strings.Repeat("x", 5000)} {
		if post(body).Code != 400 {
			t.Fatal("invalid report request accepted")
		}
	}
	if service.calls != prior {
		t.Fatal("invalid body reached report service")
	}
	for _, path := range []string{base + "?limit=21", base + "?limit=0", base + "?cursor=", base + "?limit=1&limit=2", base + "?scope=x", base + "/not-a-task", base + "/" + service.result.Task.ID + "?currentLog=true"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatalf("invalid route=%d %s", w.Code, path)
		}
	}
	missingKey := httptest.NewRequest("POST", base, strings.NewReader(`{"profile":"metadata"}`))
	missingKey.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, missingKey)
	if w.Code != 400 {
		t.Fatal("missing idempotency key accepted")
	}
	unauthorized := (&Server{Reports: service}).Handler()
	w = httptest.NewRecorder()
	unauthorized.ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 401 {
		t.Fatal("report list accessible unauthenticated")
	}
}

func TestReportHTTPChecksSourceAndJobBeforeDetailOrCitationProjection(t *testing.T) {
	service, handler, base := reportApp()
	taskPath := base + "/" + service.result.Task.ID
	for _, path := range []string{taskPath, taskPath + "/citations/synthetic"} {
		wrong := strings.Replace(path, "/namespaces/"+service.result.Task.Subject.NamespaceID, "/namespaces/"+service.result.Task.Subject.JobID, 1)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", wrong, nil))
		if w.Code != 404 {
			t.Fatalf("cross-source report=%d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", taskPath, nil))
	if w.Code != 200 {
		t.Fatal("pending detail should return authorized task")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", taskPath+"/citations/synthetic", nil))
	if w.Code != 404 {
		t.Fatal("pending task fabricated citation")
	}
	for _, sample := range []struct {
		err    error
		status int
		code   string
	}{{reports.ErrTaskNotFound, 404, "not_found_or_inaccessible"}, {reports.ErrRedactionUnavailable, 503, "redaction_unavailable"}, {reports.ErrLimit, 429, "rate_limited"}, {reports.ErrConflict, 409, "revision_conflict"}, {monitoring.ErrForbidden, 403, "forbidden"}, {&api.Error{Code: "snapshot_changed", Message: "Snapshot changed"}, 409, "snapshot_changed"}} {
		service.err = sample.err
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", taskPath, nil))
		if w.Code != sample.status || !strings.Contains(w.Body.String(), sample.code) {
			t.Fatalf("mapped report failure=%d %s", w.Code, w.Body.String())
		}
	}
}
