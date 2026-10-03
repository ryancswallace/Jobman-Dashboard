package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
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
