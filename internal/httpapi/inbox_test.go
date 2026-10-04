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
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type inboxFixture struct {
	page  notifications.InboxPage
	item  notifications.InboxItem
	q     notifications.InboxQuery
	actor monitoring.Actor
	id    string
	read  bool
	calls int
	err   error
}

func (f *inboxFixture) List(_ context.Context, a monitoring.Actor, q notifications.InboxQuery) (notifications.InboxPage, error) {
	f.calls++
	f.actor = a
	f.q = q
	return f.page, f.err
}
func (f *inboxFixture) Get(_ context.Context, a monitoring.Actor, id string) (notifications.InboxItem, error) {
	f.calls++
	f.actor = a
	f.id = id
	return f.item, f.err
}
func (f *inboxFixture) SetRead(_ context.Context, a monitoring.Actor, id string, read bool) (notifications.InboxItem, error) {
	f.calls++
	f.actor = a
	f.id = id
	f.read = read
	return f.item, f.err
}
func inboxHTTPFixture() (*inboxFixture, http.Handler) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	id := testRuleID
	item := notifications.InboxItem{ID: id, Job: notifications.JobRef{DeploymentID: "20000000-0000-4000-8000-000000000001", NamespaceID: "30000000-0000-4000-8000-000000000001", JobID: "60000000-0000-4000-8000-000000000001"}, ControlInstanceID: "70000000-0000-4000-8000-000000000001", EventID: "80000000-0000-4000-8000-000000000001", Outcome: "failure", EventAt: now, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), MatchedRules: []notifications.InboxMatch{{RuleID: id, Revision: "9007199254740993", Name: "Original rule", Scope: notifications.ScopeNamespaceJobs, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"failure"}}}, JobAvailability: "missing", Delivery: notifications.InboxDelivery{Total: "0", ByState: map[string]string{}}}
	f := &inboxFixture{item: item, page: notifications.InboxPage{Items: []notifications.InboxItem{item}, UnreadCount: "9007199254740993", Completeness: "complete", FetchedAt: now}}
	return f, (&Server{Inbox: f, Auth: AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: "40000000-0000-4000-8000-000000000001"}, Subject: "verified"}, nil
	})}).Handler()
}
func TestInboxHTTPRoutesAndExplicitScopeSemantics(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/v1/inbox", ""}, {"GET", "/api/v1/inbox?scope=%5B%5D&unread=true&limit=1", ""}, {"GET", "/api/v1/inbox/" + testRuleID, ""}, {"PATCH", "/api/v1/inbox/" + testRuleID, `{"read":true}`}} {
		f, h := inboxHTTPFixture()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, ruleRequest(tc.method, tc.path, tc.body))
		if w.Code != 200 || f.calls != 1 || f.actor.Subject != "verified" {
			t.Fatal(w.Code, w.Body.String(), f.calls)
		}
		if strings.Contains(tc.path, "scope=") {
			if f.q.Scopes == nil || len(f.q.Scopes) != 0 || !f.q.Unread || f.q.Limit != 1 {
				t.Fatal(f.q)
			}
		} else if tc.path == "/api/v1/inbox" && f.q.Scopes != nil {
			t.Fatal("absent scope changed")
		}
		if tc.method == "PATCH" && (!f.read || f.id != testRuleID) {
			t.Fatal("mutation selection", f)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("history cached")
		}
	}
	refs := []notifications.NamespaceRef{}
	for n := 1; n <= 320; n++ {
		refs = append(refs, notifications.NamespaceRef{DeploymentID: "20000000-0000-4000-8000-000000000001", NamespaceID: fmt.Sprintf("30000000-0000-4000-8000-%012d", n)})
	}
	encoded, _ := json.Marshal(refs)
	f, h := inboxHTTPFixture()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, ruleRequest("GET", "/api/v1/inbox?scope="+url.QueryEscape(string(encoded)), ""))
	if w.Code != 200 || len(f.q.Scopes) != 320 {
		t.Fatal("320-scope request rejected", w.Code)
	}
}
func TestInboxHTTPRejectsMalformedBoundsAndBodies(t *testing.T) {
	cases := []struct{ method, path, body string }{{"GET", "/api/v1/inbox?limit=51", ""}, {"GET", "/api/v1/inbox?limit=01", ""}, {"GET", "/api/v1/inbox?unread=1", ""}, {"GET", "/api/v1/inbox?unread=true&unread=false", ""}, {"GET", "/api/v1/inbox?scope=null", ""}, {"GET", "/api/v1/inbox?scope=%5B%5D%5B%5D", ""}, {"GET", "/api/v1/inbox?owner=other", ""}, {"GET", "/api/v1/inbox", `{}`}, {"PATCH", "/api/v1/inbox/" + testRuleID, `{"read":null}`}, {"PATCH", "/api/v1/inbox/" + testRuleID, `{"read":true,"read":false}`}, {"PATCH", "/api/v1/inbox/" + testRuleID, `{"read":true,"accountId":"other"}`}, {"PATCH", "/api/v1/inbox/" + testRuleID, `{"read":true} {}`}, {"PATCH", "/api/v1/inbox/not-an-id", `{"read":true}`}}
	for _, tc := range cases {
		f, h := inboxHTTPFixture()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, ruleRequest(tc.method, tc.path, tc.body))
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("%s %s %s => %d calls%d", tc.method, tc.path, tc.body, w.Code, f.calls)
		}
	}
}
func TestInboxHTTPDoesNotExposeMalformedProjection(t *testing.T) {
	f, h := inboxHTTPFixture()
	f.item.ID = "50000000-0000-4000-8000-000000000099"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, ruleRequest("GET", "/api/v1/inbox/"+testRuleID, ""))
	if w.Code != 503 || strings.Contains(w.Body.String(), f.item.ID) {
		t.Fatal("wrong item published", w.Code, w.Body.String())
	}
	f, h = inboxHTTPFixture()
	f.page.Items[0].Delivery.ByState["accepted"] = "1"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, ruleRequest("GET", "/api/v1/inbox", ""))
	if w.Code != 503 {
		t.Fatal("inconsistent handoff count published", w.Code)
	}
}
