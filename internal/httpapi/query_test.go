package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/fixtures"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestParseQueryPreservesAbsentAndExplicitEmptyScope(t *testing.T) {
	absent, err := parseQuery(httptest.NewRequest("GET", "/api/v1/jobs", nil))
	if err != nil || absent.Scopes != nil {
		t.Fatalf("absent scope: %#v %v", absent, err)
	}
	empty, err := parseQuery(httptest.NewRequest("GET", "/api/v1/jobs?scope=%5B%5D", nil))
	if err != nil || empty.Scopes == nil || len(empty.Scopes) != 0 {
		t.Fatalf("explicit empty scope: %#v %v", empty, err)
	}
}

func TestParseQueryRejectsMalformedAmbiguousAndUnboundedInputs(t *testing.T) {
	inputs := []string{
		"scope=null", "scope=", "scope=" + url.QueryEscape(`[] {}`), "scope=" + url.QueryEscape(`[{"deploymentId":"a","namespaceId":""}]`), "scope=" + url.QueryEscape(`[{"deploymentId":"a","namespaceId":"n","unknown":true}]`),
		"limit=0", "limit=-1", "limit=201", "limit=bad", "limit=10&limit=20", "phase=running&phase=terminal", "unused=value", "attention=maybe", "attention=1", "from=not-a-date", "from=2026-10-03T00:00:00Z&completedFrom=2026-10-03T00:00:00Z",
		"from=2026-10-04T00:00:00Z&to=2026-10-03T00:00:00Z", "scope=%zz", "phase=running;outcome=failure", "jobId=" + strings.Repeat("a", 129), "cursor=" + strings.Repeat("a", 513), "phase=%0A",
		"windowHours=24", // only overview accepts relative window shorthand
	}
	for _, query := range inputs {
		t.Run(query, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v1/jobs", nil)
			request.URL.RawQuery = query
			if _, err := parseQuery(request); err == nil {
				t.Fatal("accepted invalid query")
			}
		})
	}
}

func TestParseQueryAlignsClientFiltersAndNormalizesTime(t *testing.T) {
	values := url.Values{"scope": {`[{"deploymentId":"east","namespaceId":"research"}]`}, "phase": {"active"}, "outcome": {"future_outcome"}, "owner": {"me"}, "jobId": {"exact-job"}, "attention": {"true"}, "from": {"2026-10-03T01:00:00-04:00"}, "to": {"2026-10-03T09:00:00Z"}, "limit": {"50"}}
	q, err := parseQuery(httptest.NewRequest("GET", "/api/v1/jobs?"+values.Encode(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if q.JobID != "exact-job" || !q.Attention || q.Phase != "active" || q.Outcome != "future_outcome" || q.Owner != "me" {
		t.Fatalf("filters not preserved: %#v", q)
	}
	if q.CompletedFrom == nil || q.CompletedFrom.Location() != time.UTC || q.CompletedFrom.Hour() != 5 {
		t.Fatalf("start not UTC: %v", q.CompletedFrom)
	}
	for _, phase := range []string{"active", "awaiting", "accepted", "running", "future_phase"} {
		if _, err := parseQuery(httptest.NewRequest("GET", "/api/v1/jobs?phase="+phase, nil)); err != nil {
			t.Fatalf("phase %s: %v", phase, err)
		}
	}
}

func TestOverviewUsesRequestedDurationAndRejectsConflicts(t *testing.T) {
	handler := app(t, true)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/overview?windowHours=168", nil))
	if recorder.Code != 200 {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var overview api.Overview
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.Window.To.Sub(overview.Window.From) != 168*time.Hour {
		t.Fatalf("duration %v", overview.Window.To.Sub(overview.Window.From))
	}
	for _, query := range []string{"windowHours=25", "windowHours=24&from=2026-10-03T00:00:00Z", "windowHours=168&to=2026-10-03T00:00:00Z"} {
		if _, err := parseQuery(httptest.NewRequest("GET", "/api/v1/overview?"+query, nil)); err == nil {
			t.Fatalf("accepted conflicting window %s", query)
		}
	}
}

func TestOverviewHTTPPublishesCanonicalWindowAndRejectsCollapsedBounds(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 123456789, time.UTC)
	e, err := monitoring.New(fixtures.Sources(now), monitoring.NewMemoryCursors())
	if err != nil {
		t.Fatal(err)
	}
	e.Now = func() time.Time { return now }
	s := &Server{Engine: e, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: fixtures.AccountID}}, nil
	})}
	h := s.Handler()
	for _, tc := range []struct {
		name, query, from, to string
		status                int
	}{
		{name: "default", from: "2026-10-03T12:00:00.123456Z", to: "2026-10-04T12:00:00.123456Z", status: 200},
		{name: "week", query: "windowHours=168", from: "2026-09-27T12:00:00.123456Z", to: "2026-10-04T12:00:00.123456Z", status: 200},
		{name: "explicit offset", query: "from=2026-10-04T07:00:00.987654321-04:00&to=2026-10-04T12:00:00.999999999Z", from: "2026-10-04T11:00:00.987654Z", to: "2026-10-04T12:00:00.999999Z", status: 200},
		{name: "collapsed", query: "from=2026-10-04T12:00:00.000000123Z&to=2026-10-04T12:00:00.000000999Z", status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/overview?"+tc.query, nil))
			if r.Code != tc.status {
				t.Fatalf("status=%d, want=%d", r.Code, tc.status)
			}
			if tc.status == 400 {
				var failure api.Error
				if json.Unmarshal(r.Body.Bytes(), &failure) != nil || failure.Code != "invalid_request" {
					t.Fatal("collapsed window must fail explicitly")
				}
				return
			}
			var o api.Overview
			if json.Unmarshal(r.Body.Bytes(), &o) != nil || o.Window.From.Format(time.RFC3339Nano) != tc.from || o.Window.To.Format(time.RFC3339Nano) != tc.to {
				t.Fatal("HTTP response did not publish the exact UTC microsecond bounds")
			}
		})
	}
}
