package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

const testRuleID = "10000000-0000-4000-8000-000000000001"

type ruleServiceFixture struct {
	value    notifications.RuleView
	page     notifications.RulePage
	err      error
	calls    int
	op       string
	actor    monitoring.Actor
	id       string
	revision int64
	enabled  bool
	explicit bool
	input    notifications.RuleInput
	cursor   string
	limit    int
}

func newRuleFixture() *ruleServiceFixture {
	now := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	ref := notifications.NamespaceRef{DeploymentID: "20000000-0000-4000-8000-000000000001", NamespaceID: "30000000-0000-4000-8000-000000000001"}
	v := notifications.RuleView{ID: testRuleID, Revision: "9007199254740993", Name: "Failures", Enabled: true, Scope: notifications.ScopeNamespaceJobs, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"failure"}, Scopes: []notifications.RuleScopeView{{NamespaceRef: ref, Status: notifications.ActivationPending}}, Jobs: []notifications.JobRef{}, CreatedAt: now, UpdatedAt: now}
	return &ruleServiceFixture{value: v, page: notifications.RulePage{Items: []notifications.RuleView{v}}}
}
func (f *ruleServiceFixture) record(op string, a monitoring.Actor, id string, revision int64) {
	f.calls++
	f.op = op
	f.actor = a
	f.id = id
	f.revision = revision
}
func (f *ruleServiceFixture) List(_ context.Context, a monitoring.Actor, cursor string, limit int) (notifications.RulePage, error) {
	f.record("list", a, "", 0)
	f.cursor = cursor
	f.limit = limit
	return f.page, f.err
}
func (f *ruleServiceFixture) Get(_ context.Context, a monitoring.Actor, id string) (notifications.RuleView, error) {
	f.record("get", a, id, 0)
	return f.value, f.err
}
func (f *ruleServiceFixture) Create(_ context.Context, a monitoring.Actor, input notifications.RuleInput) (notifications.RuleView, error) {
	f.record("create", a, "", 0)
	f.input = input
	return f.value, f.err
}
func (f *ruleServiceFixture) Update(_ context.Context, a monitoring.Actor, id string, revision int64, input notifications.RuleInput) (notifications.RuleView, error) {
	f.record("update", a, id, revision)
	f.input = input
	return f.value, f.err
}
func (f *ruleServiceFixture) SetEnabled(_ context.Context, a monitoring.Actor, id string, revision int64, enabled bool) (notifications.RuleView, error) {
	f.record("enabled", a, id, revision)
	f.enabled = enabled
	return f.value, f.err
}
func (f *ruleServiceFixture) Revalidate(_ context.Context, a monitoring.Actor, id string, revision int64, explicit bool) (notifications.RuleView, error) {
	f.record("revalidate", a, id, revision)
	f.explicit = explicit
	return f.value, f.err
}
func (f *ruleServiceFixture) Delete(_ context.Context, a monitoring.Actor, id string, revision int64) error {
	f.record("delete", a, id, revision)
	return f.err
}

func ruleHandler(f *ruleServiceFixture) http.Handler {
	return (&Server{Rules: f, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "40000000-0000-4000-8000-000000000001"}, DirectoryID: "50000000-0000-4000-8000-000000000001", Issuer: "https://synthetic.example", Subject: "verified"}, nil
	})}).Handler()
}
func ruleRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPut || method == http.MethodDelete || strings.HasSuffix(path, "/revalidate") {
		r.Header.Set("If-Match", `"9007199254740993"`)
	}
	return r
}
func ruleInputJSON() string {
	return `{"name":" Failures ","enabled":true,"scope":"namespace_jobs","namespaces":[{"deploymentId":"20000000-0000-4000-8000-000000000001","namespaceId":"30000000-0000-4000-8000-000000000001"}],"jobs":[],"outcomeMode":"selected","outcomes":["failure","failure"]}`
}

func TestRuleHTTPRoutesBindAuthenticatedActorAndWideRevisions(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, op string
		status                 int
	}{
		{"GET", "/api/v1/rules?limit=1&cursor=opaque", "", "list", 200},
		{"GET", "/api/v1/rules/" + testRuleID, "", "get", 200},
		{"POST", "/api/v1/rules", ruleInputJSON(), "create", 201},
		{"PUT", "/api/v1/rules/" + testRuleID, ruleInputJSON(), "update", 200},
		{"PUT", "/api/v1/rules/" + testRuleID + "/enabled", `{"enabled":false}`, "enabled", 200},
		{"POST", "/api/v1/rules/" + testRuleID + "/revalidate", "", "revalidate", 200},
		{"DELETE", "/api/v1/rules/" + testRuleID, "", "delete", 204},
	} {
		t.Run(tc.op, func(t *testing.T) {
			f := newRuleFixture()
			w := httptest.NewRecorder()
			ruleHandler(f).ServeHTTP(w, ruleRequest(tc.method, tc.path, tc.body))
			if w.Code != tc.status || f.calls != 1 || f.op != tc.op || f.actor.Subject != "verified" || f.actor.Account.ID != "40000000-0000-4000-8000-000000000001" {
				t.Fatalf("%d %s call=%#v", w.Code, w.Body.String(), f)
			}
			if tc.op == "update" || tc.op == "enabled" || tc.op == "delete" || tc.op == "revalidate" {
				if f.id != testRuleID || f.revision != 9007199254740993 {
					t.Fatal("revision lost precision or wrong resource")
				}
			}
			if tc.op == "enabled" && f.enabled || tc.op == "revalidate" && !f.explicit {
				t.Fatal("wrong personal operation intent")
			}
			if tc.op == "create" || tc.op == "update" {
				if f.input.Name != "Failures" || len(f.input.Outcomes) != 1 {
					t.Fatal("input was not canonicalized")
				}
			}
			if tc.op == "list" {
				if f.limit != 1 || f.cursor != "opaque" {
					t.Fatal("pagination not passed through")
				}
			} else if tc.status != 204 && w.Header().Get("ETag") != `"9007199254740993"` {
				t.Fatal("missing string-safe revision ETag")
			}
			if tc.status == 201 && w.Header().Get("Location") != "/api/v1/rules/"+testRuleID {
				t.Fatal("missing new rule location")
			}
			if tc.status == 204 && w.Body.Len() != 0 {
				t.Fatal("delete returned a body")
			}
			for _, private := range []string{"accountId", "directoryId", "principalId", "headCursor", "activationId", "boundary"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatal("private data serialized", private)
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing private response headers")
			}
		})
	}
}

func TestRuleHTTPRejectsAmbiguousOrPrivateInputBeforeService(t *testing.T) {
	base := ruleInputJSON()
	invalid := []string{
		strings.Replace(base, `"name":`, `"Name":`, 1),
		strings.Replace(base, `"name":" Failures "`, `"name":"one","name":"two"`, 1),
		strings.Replace(base, `"name":" Failures "`, `"name":"one","na\u006de":"two"`, 1),
		strings.Replace(base, `"enabled":true`, `"enabled":null`, 1),
		strings.Replace(base, `"enabled":true`, `"enabled":"true"`, 1),
		strings.Replace(base, `"enabled":true,`, ``, 1),
		strings.Replace(base, `"jobs":[]`, `"jobs":null`, 1),
		strings.Replace(base, `"outcomes":["failure","failure"]`, `"outcomes":null`, 1),
		strings.Replace(strings.Replace(base, `"outcomeMode":"selected"`, `"outcomeMode":"all_terminal"`, 1), `"outcomes":["failure","failure"]`, `"outcomes":[null]`, 1),
		strings.Replace(base, `"deploymentId":`, `"DeploymentId":`, 1),
		strings.Replace(base, `"namespaceId":`, `"namespaceId":"30000000-0000-4000-8000-000000000001","namespaceId":`, 1),
		strings.Replace(base, `"jobs":[]`, `"jobs":[{"deploymentId":"20000000-0000-4000-8000-000000000001","namespaceId":"30000000-0000-4000-8000-000000000001","jobId":"60000000-0000-4000-8000-000000000001","path":"/secret"}]`, 1),
		base + ` {}`, `null`, `[]`, strings.Repeat(" ", notifications.MaximumInputBytes) + base,
	}
	for _, key := range []string{"accountId", "directoryId", "issuer", "subject", "activation", "boundary", "revision"} {
		invalid = append(invalid, strings.TrimSuffix(base, "}")+`,"`+key+`":"injected"}`)
	}
	for i, body := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newRuleFixture()
			w := httptest.NewRecorder()
			ruleHandler(f).ServeHTTP(w, ruleRequest("POST", "/api/v1/rules", body))
			if w.Code != 400 || f.calls != 0 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, body := range []string{`{"Enabled":false}`, `{"enabled":false,"enabled":true}`, `{"enabled":null}`, `{"enabled":false,"namespaces":[]}`, `{}`, `{"enabled":true} {}`} {
		f := newRuleFixture()
		w := httptest.NewRecorder()
		ruleHandler(f).ServeHTTP(w, ruleRequest("PUT", "/api/v1/rules/"+testRuleID+"/enabled", body))
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("ambiguous enabled body admitted", body, w.Code)
		}
	}
}

func TestRuleHTTPBoundsQueriesAndRevisionHeaders(t *testing.T) {
	for _, query := range []string{"?limit=21", "?limit=0", "?limit=01", "?limit=1&limit=2", "?limit=one", "?unknown=1", "?cursor=%ZZ", "?cursor=" + strings.Repeat("a", 129), "?cursor=a&cursor=b"} {
		f := newRuleFixture()
		w := httptest.NewRecorder()
		ruleHandler(f).ServeHTTP(w, ruleRequest("GET", "/api/v1/rules"+query, ""))
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("bad list query", query, w.Code)
		}
	}
	for _, revision := range []string{"", "0", "01", "+1", "-1", "*", "W/\"1\"", "1,2", "9223372036854775808", "\"1", "1\""} {
		f := newRuleFixture()
		r := ruleRequest("DELETE", "/api/v1/rules/"+testRuleID, "")
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		ruleHandler(f).ServeHTTP(w, r)
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("bad revision", revision, w.Code)
		}
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/rules", "{}"}, {"GET", "/api/v1/rules/" + testRuleID, "{}"},
		{"DELETE", "/api/v1/rules/" + testRuleID, "{}"}, {"POST", "/api/v1/rules/" + testRuleID + "/revalidate", "{}"},
		{"PUT", "/api/v1/rules/" + testRuleID + "?ignored=1", ruleInputJSON()},
		{"GET", "/api/v1/rules/00000000-0000-0000-0000-000000000000", ""}, {"GET", "/api/v1/rules/not-a-uuid", ""},
	} {
		f := newRuleFixture()
		w := httptest.NewRecorder()
		ruleHandler(f).ServeHTTP(w, ruleRequest(tc.method, tc.path, tc.body))
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("bad route input", tc, w.Code)
		}
	}
	f := newRuleFixture()
	r := ruleRequest("DELETE", "/api/v1/rules/"+testRuleID, "")
	r.Header.Add("If-Match", "1")
	w := httptest.NewRecorder()
	ruleHandler(f).ServeHTTP(w, r)
	if w.Code != 400 || f.calls != 0 {
		t.Fatal("duplicate revision admitted")
	}
}

func TestRuleHTTPErrorMappingNeverEchoesInternalFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{notifications.ErrUnsupportedOutcome, 422, "unsupported_outcome"}, {notifications.ErrConflict, 409, "revision_conflict"}, {notifications.ErrNotFound, 404, "not_found_or_inaccessible"},
		{notifications.ErrRateLimited, 429, "rate_limited"}, {notifications.ErrCapacity, 429, "rule_capacity"}, {notifications.ErrInactiveFeed, 503, "source_unavailable"},
		{monitoring.ErrAuthority, 503, "authorization_unavailable"}, {monitoring.ErrForbidden, 403, "forbidden"}, {monitoring.ErrCursor, 409, "cursor_expired"},
		{errors.New("secret DSN private token"), 503, "source_unavailable"},
	} {
		f := newRuleFixture()
		f.err = tc.err
		w := httptest.NewRecorder()
		ruleHandler(f).ServeHTTP(w, ruleRequest("GET", "/api/v1/rules", ""))
		var value api.Error
		if json.Unmarshal(w.Body.Bytes(), &value) != nil || w.Code != tc.status || value.Code != tc.code || strings.Contains(w.Body.String(), "secret") {
			t.Fatal("unsafe or wrong error", w.Code, w.Body.String())
		}
		if tc.code == "rate_limited" && w.Header().Get("Retry-After") == "" || tc.code == "rule_capacity" && w.Header().Get("Retry-After") != "" {
			t.Fatal("wrong retry advice")
		}
	}
	f := newRuleFixture()
	body := strings.Replace(ruleInputJSON(), `["failure","failure"]`, `["future_outcome"]`, 1)
	w := httptest.NewRecorder()
	ruleHandler(f).ServeHTTP(w, ruleRequest("POST", "/api/v1/rules", body))
	if w.Code != 422 || f.calls != 0 || !strings.Contains(w.Body.String(), "unsupported_outcome") {
		t.Fatal("individual unsupported outcome not visible")
	}
}

func TestRuleHTTPAuthenticationAndMalformedServiceResponses(t *testing.T) {
	f := newRuleFixture()
	w := httptest.NewRecorder()
	(&Server{Rules: f}).Handler().ServeHTTP(w, ruleRequest("POST", "/api/v1/rules", ruleInputJSON()))
	if w.Code != 401 || f.calls != 0 {
		t.Fatal("unauthenticated mutation reached service")
	}
	for _, mutate := range []func(*ruleServiceFixture){
		func(f *ruleServiceFixture) { f.page.Items = nil },
		func(f *ruleServiceFixture) { f.page.Items = append(f.page.Items, f.page.Items[0]) },
		func(f *ruleServiceFixture) { f.page.Items[0].Revision = "01" },
		func(f *ruleServiceFixture) { f.page.Items[0].Scopes = []notifications.RuleScopeView{} },
		func(f *ruleServiceFixture) {
			f.page.Items[0].Jobs = []notifications.JobRef{{DeploymentID: "unauthorized", NamespaceID: "hidden", JobID: testRuleID}}
		},
		func(f *ruleServiceFixture) { f.page.NextCursor = "next" },
	} {
		f := newRuleFixture()
		mutate(f)
		w := httptest.NewRecorder()
		ruleHandler(f).ServeHTTP(w, ruleRequest("GET", "/api/v1/rules", ""))
		if w.Code != 503 {
			t.Fatal("invalid projection serialized", w.Code, w.Body.String())
		}
	}
	f = newRuleFixture()
	f.value.ID = "10000000-0000-4000-8000-000000000002"
	w = httptest.NewRecorder()
	ruleHandler(f).ServeHTTP(w, ruleRequest("GET", "/api/v1/rules/"+testRuleID, ""))
	if w.Code != 503 {
		t.Fatal("mismatched resource emitted")
	}
}

func TestRuleHTTPMaximumPageFitsBothClientResponseBounds(t *testing.T) {
	f := newRuleFixture()
	value := f.value
	value.Name = strings.Repeat("<", notifications.MaximumNameBytes)
	value.Scope = notifications.ScopeWatchedJobs
	value.OutcomeMode = notifications.OutcomeAllTerminal
	value.Outcomes = []string{}
	value.Scopes = make([]notifications.RuleScopeView, notifications.MaximumNamespaces)
	for i := range value.Scopes {
		value.Scopes[i] = notifications.RuleScopeView{NamespaceRef: notifications.NamespaceRef{DeploymentID: "20000000-0000-4000-8000-000000000001", NamespaceID: fmt.Sprintf("30000000-0000-4000-8000-%012d", i+1)}, Status: notifications.ActivationActive, ActivatedAt: &value.CreatedAt}
	}
	value.Jobs = make([]notifications.JobRef, notifications.MaximumWatchedJobs)
	for i := range value.Jobs {
		value.Jobs[i] = notifications.JobRef{DeploymentID: value.Scopes[i].DeploymentID, NamespaceID: value.Scopes[i].NamespaceID, JobID: fmt.Sprintf("60000000-0000-4000-8000-%012d", i+1)}
	}
	f.page.Items = make([]notifications.RuleView, 20)
	for i := range f.page.Items {
		f.page.Items[i] = value
		f.page.Items[i].ID = fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1)
	}
	f.page.NextCursor = "opaque-next-page"
	w := httptest.NewRecorder()
	ruleHandler(f).ServeHTTP(w, ruleRequest("GET", "/api/v1/rules", ""))
	if w.Code != 200 || w.Body.Len() > 2<<20 {
		t.Fatal("valid maximum page exceeds response allowance", w.Code, w.Body.Len())
	}
	var decoded notifications.RulePage
	if json.Unmarshal(w.Body.Bytes(), &decoded) != nil || len(decoded.Items) != 20 || decoded.NextCursor != f.page.NextCursor || len(decoded.Items[19].Scopes) != 320 || len(decoded.Items[19].Jobs) != 100 {
		t.Fatal("maximum page was silently truncated")
	}
}
