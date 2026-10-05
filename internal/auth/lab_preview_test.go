//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// Exercises the current two-source Lab without assuming old catalog counts.
// Only test-owned, disabled personal rules are mutated. No job, existing rule,
// inbox read state, device registration, or source configuration is changed.
func TestLabPreviewWorkflows(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_PREVIEW") != "1" {
		t.Skip("set JOBMAN_DASHBOARD_LAB_PREVIEW=1 for the authorized synthetic Lab")
	}
	session := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	client := newLabNotificationHTTP(t, session)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	requests := 0
	call := func(method, path, revision string, input, output any, want int) {
		t.Helper()
		requests++
		if requests > 80 {
			t.Fatal("preview smoke request budget exceeded")
		}
		status, err := client.call(ctx, method, path, session.accessToken, revision, input, output)
		if err != nil || status != want {
			t.Fatalf("%s %s returned HTTP%d, expected HTTP%d (transport failure=%t)", method, path, status, want, err != nil)
		}
	}
	var bootstrap api.Bootstrap
	call("GET", "/api/v1/bootstrap", "", nil, &bootstrap, 200)
	if bootstrap.FixtureMode || bootstrap.Completeness != "complete" || len(bootstrap.Deployments) != 2 {
		t.Fatal("two healthy configured Lab sources required")
	}
	var scopes []api.Scope
	for _, source := range bootstrap.Deployments {
		if source.Status != "available" {
			t.Fatal("source unavailable")
		}
		for _, ns := range source.Namespaces {
			if ns.Name == "dashboard-research" {
				scopes = append(scopes, api.Scope{DeploymentID: source.ID, NamespaceID: ns.ID})
			}
		}
	}
	if len(scopes) != 2 {
		t.Fatal("expected research access in both sources")
	}
	raw, _ := json.Marshal(scopes)
	query := url.Values{"scope": {string(raw)}, "limit": {"10"}}.Encode()
	var jobs api.Page[api.Job]
	call("GET", "/api/v1/jobs?"+query, "", nil, &jobs, 200)
	if jobs.Completeness != "complete" || len(jobs.Items) == 0 {
		t.Fatal("job catalog incomplete")
	}
	var overview api.Overview
	call("GET", "/api/v1/overview?"+url.Values{"scope": {string(raw)}}.Encode(), "", nil, &overview, 200)
	if overview.Completeness != "complete" {
		t.Fatal("aggregate overview incomplete")
	}
	var targets api.TargetPage
	call("GET", "/api/v1/targets?"+query, "", nil, &targets, 200)
	if targets.Completeness != "complete" || len(targets.Items) == 0 {
		t.Fatal("target catalog incomplete")
	}
	for _, target := range targets.Items {
		path := labMultiPrefix(target.Scope) + "/targets/" + target.TargetID
		var detail api.TargetDetail
		call("GET", path, "", nil, &detail, 200)
		if detail.Target.TargetID != target.TargetID || detail.Completeness != "complete" {
			t.Fatal("target detail differs")
		}
		var partitions api.TargetPartitionPage
		call("GET", path+"/partitions?"+url.Values{"generationId": {target.Generation.ID}, "limit": {"10"}}.Encode(), "", nil, &partitions, 200)
		if partitions.Completeness != "complete" || partitions.GenerationID != target.Generation.ID {
			t.Fatal("target partitions incomplete")
		}
	}
	for _, kind := range []string{"collection", "array", "graph"} {
		var page api.WorkloadPage
		call("GET", "/api/v1/workloads/"+kind+"?"+query, "", nil, &page, 200)
		if page.Completeness != "complete" || len(page.Items) == 0 {
			t.Fatal("workload catalog incomplete: " + kind)
		}
		item := page.Items[0]
		path := labMultiPrefix(item.Scope) + "/workloads/" + kind + "/" + item.ID
		var detail api.WorkloadDetail
		call("GET", path, "", nil, &detail, 200)
		if detail.Completeness != "complete" || len(detail.Children) == 0 {
			t.Fatal("workload detail incomplete: " + kind)
		}
		if kind == "graph" {
			var graph api.GraphNeighborhood
			call("GET", path+"/neighborhood?"+url.Values{"nodeId": {detail.Children[0].ID}}.Encode(), "", nil, &graph, 200)
			if graph.Completeness != "complete" || len(graph.Nodes) == 0 {
				t.Fatal("graph neighborhood incomplete")
			}
		}
	}
	var inbox notifications.InboxPage
	call("GET", "/api/v1/inbox?limit=10", "", nil, &inbox, 200)
	if inbox.Validate() != nil || inbox.Completeness != "complete" {
		t.Fatal("inbox unavailable or incomplete")
	}
	if len(inbox.Items) > 0 {
		var item notifications.InboxItem
		call("GET", "/api/v1/inbox/"+inbox.Items[0].ID, "", nil, &item, 200)
		if item.ID != inbox.Items[0].ID {
			t.Fatal("inbox item identity differs")
		}
	}
	var devices json.RawMessage
	call("GET", "/api/v1/devices", "", nil, &devices, 200)
	var rules notifications.RulePage
	call("GET", "/api/v1/rules?limit=20", "", nil, &rules, 200)
	for _, kind := range []string{notifications.ScopeNamespaceJobs, notifications.ScopeMyJobs, notifications.ScopeWatchedJobs} {
		job := jobs.Items[0]
		input := notifications.RuleInput{Name: "Preview smoke " + kind, Enabled: false, Scope: kind,
			Namespaces: []notifications.NamespaceRef{{DeploymentID: job.DeploymentID, NamespaceID: job.NamespaceID}},
			Jobs:       []notifications.JobRef{}, OutcomeMode: notifications.OutcomeAllTerminal, Outcomes: []string{}}
		if kind == notifications.ScopeWatchedJobs {
			input.Jobs = []notifications.JobRef{{DeploymentID: job.DeploymentID, NamespaceID: job.NamespaceID, JobID: job.ID}}
		}
		var created notifications.RuleView
		call("POST", "/api/v1/rules", "", input, &created, 201)
		id := created.ID
		if !uuid(id) {
			t.Fatal("created rule identity invalid; inspect preview smoke rules")
		}
		// Retain the exact server ID for cleanup even if a later assertion fails.
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 25*time.Second)
			defer done()
			var current notifications.RuleView
			path := "/api/v1/rules/" + id
			status, err := client.call(cleanup, "GET", path, session.accessToken, "", nil, &current)
			if status == 404 && err == nil {
				return
			}
			if status != 200 || err != nil {
				t.Error("test-owned rule cleanup could not read current revision")
				return
			}
			status, err = client.call(cleanup, "DELETE", path, session.accessToken, current.Revision, nil, nil)
			if status != 204 || err != nil {
				t.Error("test-owned rule cleanup failed")
			}
		})
		if created.Enabled || created.Scope != kind {
			t.Fatal("disabled rule intent changed")
		}
		input.OutcomeMode = notifications.OutcomeSelected
		input.Outcomes = []string{"failure", "success", "cancelled", "aborted", "timed_out", "lost"}
		var updated notifications.RuleView
		call("PUT", "/api/v1/rules/"+id, created.Revision, input, &updated, 200)
		if updated.Enabled || updated.OutcomeMode != notifications.OutcomeSelected || len(updated.Outcomes) != 6 {
			t.Fatal("selected outcomes did not persist")
		}
		call("DELETE", "/api/v1/rules/"+id, updated.Revision, nil, nil, 204)
	}
	t.Logf("PASS: %d bounded requests covered two-source monitoring, targets, collections, arrays, graph, inbox, device catalog and all three personal rule scopes; no APNs/device acceptance implied", requests)
}
