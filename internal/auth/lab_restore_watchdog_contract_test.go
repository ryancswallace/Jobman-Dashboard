//go:build integration && (darwin || linux)

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/httpapi"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/reports"
)

type watchdogDeniedResources struct{ logCalls, reportCalls int }

func (s *watchdogDeniedResources) Read(context.Context, monitoring.Actor, logs.Request) (api.LogRange, error) {
	s.logCalls++
	return api.LogRange{}, monitoring.ErrForbidden
}
func (s *watchdogDeniedResources) Get(context.Context, monitoring.Actor, string) (reports.Result, error) {
	s.reportCalls++
	return reports.Result{}, reports.ErrTaskNotFound
}
func (*watchdogDeniedResources) Request(context.Context, monitoring.Actor, reports.Request) (reports.Result, error) {
	panic("no report mutation authorized by this test")
}
func (*watchdogDeniedResources) List(context.Context, monitoring.Actor, api.Scope, string, string, int) (reports.Page, error) {
	panic("no report catalog read expected")
}

func TestLabWatchdogUsesActualRouteSpecificDenials(t *testing.T) {
	source := &watchdogDeniedResources{}
	handler := (&httpapi.Server{Logs: source, Reports: source, Auth: httpapi.AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "71000000-0000-4000-8000-000000000002"}}, nil
	})}).Handler()
	prefix := "/api/v1/deployments/72000000-0000-4000-8000-000000000001/namespaces/11111111-1111-4111-8111-111111111111/jobs/22222222-2222-4222-8222-222222222222"
	logPath := prefix + "/logs?stream=stderr"
	reportPath := prefix + "/reports/33333333-3333-4333-8333-333333333333"
	for _, tc := range []struct {
		path, other string
		status      int
	}{{logPath, reportPath, 403}, {reportPath, logPath, 404}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Values("Set-Cookie")) != 0 || !auth.LabWatchdogDenialContract(tc.path, w.Code, w.Body.Bytes()) {
			t.Fatal("actual product denial must satisfy the watchdog's route-specific contract")
		}
		if auth.LabWatchdogDenialContract(tc.other, w.Code, w.Body.Bytes()) {
			t.Fatal("report and log denials must never be accepted interchangeably")
		}
		// This was the failed live preflight's assertion: logs expected404.
		if tc.status == 403 && auth.LabWatchdogDenialContract(tc.path, 404, w.Body.Bytes()) {
			t.Fatal("the old log404 expectation must fail")
		}
		unsafe := []byte(strings.Replace(w.Body.String(), `"message":`, `"message":"private canary","message":`, 1))
		if auth.LabWatchdogDenialContract(tc.path, w.Code, unsafe) {
			t.Fatal("duplicate private error fields must fail closed")
		}
	}
	if source.logCalls != 1 || source.reportCalls != 1 {
		t.Fatal("expected exactly one read per actual route")
	}
}
