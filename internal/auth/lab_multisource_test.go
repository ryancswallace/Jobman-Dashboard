//go:build integration

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const labSecondaryDeployment = "72000000-0000-4000-8000-000000000002"
const labSecondaryInstance = "a4f0e2ab-7323-4c90-9510-1f073c660f06"
const labSyntheticLog = "SYNTHETIC Dashboard Lab log: metadata and byte delivery acceptance only.\n"

type labMultiNamespace struct {
	ID, Name, TargetGenerationID   string
	JobIDs                         []string `json:"jobIds"`
	ArrayID, CollectionID, GraphID string
}

type labMultiFixture struct {
	Profile, InstanceID, Endpoint string
	Synthetic                     bool
	Namespaces                    []labMultiNamespace
}

type labMultiRead func(string, any, int)

func labReadMultiFixture(t *testing.T, path string, secondary bool) labMultiFixture {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatal("Absolute public multi-source fixture path required")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("Public multi-source fixture unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("Regular public fixture required")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	var result labMultiFixture
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &result) != nil || !result.Synthetic || len(result.Namespaces) != 2 {
		t.Fatal("Bounded synthetic multi-source fixture required")
	}
	wantInstance, wantEndpoint := "e633cf92-258d-48ff-965a-fda88d68ef3a", "https://10.77.0.21:18443"
	if secondary {
		wantInstance, wantEndpoint = labSecondaryInstance, "https://10.77.0.21:28443"
		if result.Profile != "secondary-v1" {
			t.Fatal("Secondary fixture profile differs")
		}
	}
	if result.InstanceID != wantInstance || result.Endpoint != wantEndpoint {
		t.Fatal("Fixture is not one of the two reviewed source instances")
	}
	seen := map[string]bool{}
	for _, scope := range result.Namespaces {
		if !uuid(scope.ID) || !uuid(scope.TargetGenerationID) || len(scope.JobIDs) != 5 || seen[scope.Name] || (scope.Name != "dashboard-research" && scope.Name != "dashboard-operations") {
			t.Fatal("Source-qualified namespace or target fixture differs")
		}
		seen[scope.Name] = true
		for _, id := range append(slices.Clone(scope.JobIDs), scope.ArrayID, scope.CollectionID, scope.GraphID) {
			if !uuid(id) {
				t.Fatal("Noncanonical fixture resource")
			}
		}
	}
	return result
}

func labMultiScopes(fixtures map[string]labMultiFixture, user string) map[api.Scope]labMultiNamespace {
	result := map[api.Scope]labMultiNamespace{}
	for deployment, fixture := range fixtures {
		for _, scope := range fixture.Namespaces {
			if scope.Name == "dashboard-research" || user == "alice" && deployment == labDeployment || user == "bob" && deployment == labSecondaryDeployment {
				result[api.Scope{DeploymentID: deployment, NamespaceID: scope.ID}] = scope
			}
		}
	}
	return result
}

func labMultiQuery(scopes []api.Scope, limit int, cursor string) string {
	raw, _ := json.Marshal(scopes)
	q := url.Values{"scope": {string(raw)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q.Encode()
}

func labMultiPrefix(scope api.Scope) string {
	return "/api/v1/deployments/" + scope.DeploymentID + "/namespaces/" + scope.NamespaceID
}

func labMultiSources(t *testing.T, sources []api.SourceStatus, allowed map[api.Scope]labMultiNamespace) {
	t.Helper()
	seen := map[api.Scope]bool{}
	for _, source := range sources {
		if _, ok := allowed[source.Scope]; !ok || seen[source.Scope] || source.Status != "available" || source.FetchedAt.IsZero() {
			t.Fatal("Aggregate provenance omitted, duplicated or exposed another scope")
		}
		seen[source.Scope] = true
	}
	if len(seen) != len(allowed) {
		t.Fatal("Aggregate source contribution missing")
	}
}

func labMultiJobs(t *testing.T, read labMultiRead, scopes []api.Scope, allowed map[api.Scope]labMultiNamespace) (map[string]api.Job, string) {
	t.Helper()
	seen := map[string]api.Job{}
	cursors := map[string]bool{}
	cursor, first := "", ""
	for n := 0; ; n++ {
		if n >= 100 || cursors[cursor] {
			t.Fatal("Multi-source job pagination exceeded its fixture bound")
		}
		cursors[cursor] = true
		var page api.Page[api.Job]
		read("/api/v1/jobs?"+labMultiQuery(scopes, 2, cursor), &page, 200)
		if page.Completeness != "complete" || len(page.Items) > 2 {
			t.Fatal("Complete bounded job page required")
		}
		labMultiSources(t, page.Sources, allowed)
		for _, item := range page.Items {
			if _, ok := allowed[item.Scope]; !ok || !uuid(item.ID) {
				t.Fatal("Job escaped its current source-qualified scope")
			}
			key := item.DeploymentID + "/" + item.NamespaceID + "/" + item.ID
			if _, exists := seen[key]; exists {
				t.Fatal("Aggregate cursor repeated a job")
			}
			seen[key] = item
		}
		if n == 0 {
			first = page.NextCursor
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	for scope, fixture := range allowed {
		for _, id := range fixture.JobIDs {
			if _, ok := seen[scope.DeploymentID+"/"+scope.NamespaceID+"/"+id]; !ok {
				t.Fatal("Pagination lost an immutable fixture job")
			}
		}
	}
	if first == "" {
		t.Fatal("Pagination acceptance requires multiple actual pages")
	}
	return seen, first
}

func labMultiWorkloads(t *testing.T, read labMultiRead, scopes []api.Scope, allowed map[api.Scope]labMultiNamespace, kind string) map[string]bool {
	t.Helper()
	seen, cursors := map[string]bool{}, map[string]bool{}
	cursor, total := "", ""
	for n := 0; ; n++ {
		if n >= 32 || cursors[cursor] {
			t.Fatal("Workload pagination exceeded bounded fixture")
		}
		cursors[cursor] = true
		var page api.WorkloadPage
		read("/api/v1/workloads/"+kind+"?"+labMultiQuery(scopes, 1, cursor), &page, 200)
		if page.Completeness != "complete" || len(page.Items) != 1 || n > 0 && page.Total != total {
			t.Fatal("Workload totals or completeness changed")
		}
		total = page.Total
		labMultiSources(t, page.Sources, allowed)
		item := page.Items[0]
		if _, ok := allowed[item.Scope]; !ok {
			t.Fatal("Workload escaped selected scope")
		}
		key := item.DeploymentID + "/" + item.NamespaceID + "/" + item.ID
		if seen[key] {
			t.Fatal("Workload cursor repeated a source-qualified identity")
		}
		seen[key] = true
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if total != strconv.Itoa(len(seen)) {
		t.Fatal("Workload exact total does not equal all returned pages")
	}
	return seen
}

func labMultiTargets(t *testing.T, read labMultiRead, scopes []api.Scope, allowed map[api.Scope]labMultiNamespace) map[string]bool {
	t.Helper()
	seen, cursors := map[string]bool{}, map[string]bool{}
	cursor, total := "", ""
	for n := 0; ; n++ {
		if n >= 32 || cursors[cursor] {
			t.Fatal("Target pagination exceeded bounded fixture")
		}
		cursors[cursor] = true
		var page api.TargetPage
		read("/api/v1/targets?"+labMultiQuery(scopes, 1, cursor), &page, 200)
		if page.Completeness != "complete" || len(page.Items) != 1 || n > 0 && page.Total != total {
			t.Fatal("Target totals or completeness changed")
		}
		total = page.Total
		labMultiSources(t, page.Sources, allowed)
		item := page.Items[0]
		fixture, ok := allowed[item.Scope]
		if !ok {
			t.Fatal("Target escaped selected scope")
		}
		if item.Name == "synthetic-host" && item.Generation.ID != fixture.TargetGenerationID {
			t.Fatal("Same-name target resolved to another source generation")
		}
		key := item.DeploymentID + "/" + item.NamespaceID + "/" + item.TargetID
		if seen[key] {
			t.Fatal("Target cursor repeated a source-qualified identity")
		}
		seen[key] = true
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if total != strconv.Itoa(len(seen)) {
		t.Fatal("Target exact total does not equal all pages")
	}
	return seen
}

func labMultiOverview(t *testing.T, read labMultiRead, research []api.Scope, allowed map[api.Scope]labMultiNamespace) {
	t.Helper()
	to := time.Now().UTC().Truncate(time.Second)
	from := to.Add(-24 * time.Hour)
	counts := func(scopes []api.Scope, sources map[api.Scope]labMultiNamespace) map[string]int64 {
		query := labMultiQuery(scopes, 0, "") + "&from=" + url.QueryEscape(from.Format(time.RFC3339)) + "&to=" + url.QueryEscape(to.Format(time.RFC3339))
		var overview api.Overview
		read("/api/v1/overview?"+query, &overview, 200)
		if overview.Completeness != "complete" || !overview.Window.From.Equal(from) || !overview.Window.To.Equal(to) {
			t.Fatal("Overview completeness or fixed time window differs")
		}
		labMultiSources(t, overview.Sources, sources)
		values := map[string]*int64{"active": overview.Active, "awaiting": overview.AwaitingExecution, "running": overview.Running, "attention": overview.EvidenceAttention, "missingCompletionTime": overview.MissingCompletionTime}
		for _, outcome := range []string{"success", "failure", "cancelled", "timed_out", "aborted", "lost", "unknown"} {
			values["terminal/"+outcome] = overview.Terminal[outcome]
		}
		result := map[string]int64{}
		for name, value := range values {
			if value == nil || *value < 0 {
				t.Fatal("Healthy overview returned an absent or invalid contribution")
			}
			result[name] = *value
		}
		return result
	}
	sum := map[string]int64{}
	for _, scope := range research {
		for name, value := range counts([]api.Scope{scope}, map[api.Scope]labMultiNamespace{scope: allowed[scope]}) {
			sum[name] += value
		}
	}
	if !reflect.DeepEqual(sum, counts(research, allowed)) {
		t.Fatal("Aggregate overview is not the sum of both authorized source contributions")
	}
}

// Read-only ordinary deployed API acceptance. Source observations for these
// fixture jobs are synthetic; this does not claim actual secondary execution.
func TestLabMultiSourceMonitoringAndIsolation(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_MULTISOURCE") != "1" {
		t.Skip("reviewed multi-source deployment and explicit opt-in required")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	fixtures := map[string]labMultiFixture{
		labDeployment:          labReadMultiFixture(t, filepath.Join(alice.root, ".lab/dashboard/fixture-info.json"), false),
		labSecondaryDeployment: labReadMultiFixture(t, os.Getenv("JOBMAN_DASHBOARD_LAB_SECONDARY_FIXTURE"), true),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	var aliceCursor string
	var research []api.Scope
	for _, deployment := range []string{labDeployment, labSecondaryDeployment} {
		for _, scope := range fixtures[deployment].Namespaces {
			if scope.Name == "dashboard-research" {
				research = append(research, api.Scope{DeploymentID: deployment, NamespaceID: scope.ID})
			}
		}
	}
	for _, subject := range []struct {
		name    string
		session labNativeSession
	}{{"alice", alice}, {"bob", bob}} {
		t.Run(subject.name, func(t *testing.T) {
			request := labReportClient(t, ctx, subject.session)
			read := func(path string, target any, status int) {
				t.Helper()
				request("GET", path, subject.session.accessToken, "", nil, target, status)
			}
			allowed := labMultiScopes(fixtures, subject.name)
			var bootstrap api.Bootstrap
			read("/api/v1/bootstrap", &bootstrap, 200)
			if bootstrap.FixtureMode || bootstrap.Completeness != "complete" || len(bootstrap.Deployments) != 2 {
				t.Fatal("Two live authorized deployment contributions required")
			}
			seen := map[api.Scope]bool{}
			for _, deployment := range bootstrap.Deployments {
				if deployment.Status != "available" {
					t.Fatal("Bootstrap source unavailable")
				}
				for _, grant := range deployment.Namespaces {
					scope := api.Scope{DeploymentID: deployment.ID, NamespaceID: grant.ID}
					fixture, ok := allowed[scope]
					if !ok || seen[scope] || grant.Name != fixture.Name || !grant.AuthorizationExpiresAt.After(time.Now()) || !slices.Contains(grant.Capabilities, "jobs.read") || !slices.Contains(grant.Capabilities, "logs.read") {
						t.Fatal("Current asymmetric namespace authorization differs")
					}
					want := []string{"viewer"}
					if fixture.Name == "dashboard-operations" {
						want = []string{"namespace_admin"}
					} else if subject.name == "alice" && deployment.ID == labDeployment || subject.name == "bob" && deployment.ID == labSecondaryDeployment {
						want = []string{"submitter", "viewer"}
					}
					actual := slices.Clone(grant.Roles)
					slices.Sort(actual)
					if !slices.Equal(actual, want) {
						t.Fatal("Asymmetric direct AD group role union differs")
					}
					seen[scope] = true
				}
			}
			if len(seen) != 3 {
				t.Fatal("Expected exactly three granted source namespaces")
			}
			researchAllowed := map[api.Scope]labMultiNamespace{research[0]: allowed[research[0]], research[1]: allowed[research[1]]}
			labMultiOverview(t, read, research, researchAllowed)
			all, cursor := labMultiJobs(t, read, research, researchAllowed)
			if subject.name == "alice" {
				aliceCursor = cursor
			} else {
				read("/api/v1/jobs?"+labMultiQuery(research, 2, aliceCursor), nil, 409)
			}
			read("/api/v1/jobs?"+labMultiQuery(research[:1], 2, cursor), nil, 409)
			union := map[string]api.Job{}
			for _, scope := range research {
				rows, _ := labMultiJobs(t, read, []api.Scope{scope}, map[api.Scope]labMultiNamespace{scope: allowed[scope]})
				for key, value := range rows {
					union[key] = value
				}
			}
			if len(all) != len(union) {
				t.Fatal("Cross-source job aggregation lost or duplicated rows")
			}
			for key := range all {
				if _, ok := union[key]; !ok {
					t.Fatal("Aggregate job absent from its individual source")
				}
			}
			for _, kind := range []string{"collection", "array", "graph"} {
				aggregate := labMultiWorkloads(t, read, research, researchAllowed, kind)
				singles := map[string]bool{}
				for _, scope := range research {
					for key := range labMultiWorkloads(t, read, []api.Scope{scope}, map[api.Scope]labMultiNamespace{scope: allowed[scope]}, kind) {
						singles[key] = true
					}
				}
				if !reflect.DeepEqual(aggregate, singles) {
					t.Fatal("Workload aggregation differs from source union")
				}
			}
			targets := labMultiTargets(t, read, research, researchAllowed)
			singles := map[string]bool{}
			for _, scope := range research {
				for key := range labMultiTargets(t, read, []api.Scope{scope}, map[api.Scope]labMultiNamespace{scope: allowed[scope]}) {
					singles[key] = true
				}
			}
			if !reflect.DeepEqual(targets, singles) {
				t.Fatal("Target aggregation differs from source union")
			}
			for deployment, fixture := range fixtures {
				for _, ns := range fixture.Namespaces {
					scope := api.Scope{DeploymentID: deployment, NamespaceID: ns.ID}
					prefix := labMultiPrefix(scope) + "/jobs/" + ns.JobIDs[0]
					if _, ok := allowed[scope]; !ok {
						for _, suffix := range []string{"", "/logs?stream=stdout", "/artifacts"} {
							read(prefix+suffix, nil, 403)
						}
						continue
					}
					for kind, id := range map[string]string{"collection": ns.CollectionID, "array": ns.ArrayID, "graph": ns.GraphID} {
						var detail api.WorkloadDetail
						path := labMultiPrefix(scope) + "/workloads/" + kind + "/" + id
						read(path, &detail, 200)
						if detail.Completeness != "complete" || detail.Workload.ID != id || detail.Workload.Scope != scope || len(detail.Children) == 0 {
							t.Fatal("Source-qualified workload detail or children missing")
						}
						for _, child := range detail.Children {
							if child.Job.Scope != scope || child.Job.ID != child.ID {
								t.Fatal("Workload child crossed its source scope")
							}
						}
						if kind == "graph" {
							var graph api.GraphNeighborhood
							read(path+"/neighborhood?nodeId="+url.QueryEscape(detail.Children[0].ID), &graph, 200)
							if graph.CenterID != detail.Children[0].ID || len(graph.Nodes) == 0 || graph.Completeness != "complete" {
								t.Fatal("Graph neighborhood lost its exact source identity")
							}
						}
					}
					var detail api.JobDetail
					read(prefix, &detail, 200)
					if detail.Job.Scope != scope || detail.Job.ID != ns.JobIDs[0] || detail.Job.Owner == nil || detail.Job.Owner.IsCurrentUser != (subject.name == "alice") {
						t.Fatal("Cross-owner job identity or authorization differs")
					}
					if detail.Job.CurrentRun == nil || !uuid(detail.Job.CurrentRun.ID) || !uuid(detail.Job.CurrentRun.ExecutionID) {
						t.Fatal("Fixture current run identity is missing")
					}
					var output api.LogRange
					read(prefix+"/logs?stream=stdout&limitBytes=8", &output, 200)
					originalCursor := output.NextCursor
					var bytes []byte
					next := ""
					for chunk := 0; ; chunk++ {
						if chunk >= 16 {
							t.Fatal("Synthetic log range did not terminate")
						}
						if chunk > 0 {
							output = api.LogRange{}
							read(prefix+"/logs?stream=stdout&limitBytes=8&cursor="+url.QueryEscape(next), &output, 200)
						}
						raw, err := base64.StdEncoding.DecodeString(output.BytesBase64)
						if err != nil || len(raw) > 8 || output.StartOffset != strconv.Itoa(len(bytes)) || output.EndOffset != strconv.Itoa(len(bytes)+len(raw)) || output.RunID != detail.Job.CurrentRun.ID || output.RunNumber != detail.Job.CurrentRun.Number || output.ExecutionID != detail.Job.CurrentRun.ExecutionID || output.Stream != "stdout" {
							t.Fatal("Source-qualified log byte offsets or run provenance differ")
						}
						bytes = append(bytes, raw...)
						next = output.NextCursor
						if len(bytes) == len(labSyntheticLog) {
							break
						}
						if next == "" || len(raw) == 0 {
							t.Fatal("Log bytes lost before declared completion")
						}
					}
					if string(bytes) != labSyntheticLog || output.State != "complete" || originalCursor == "" {
						t.Fatal("Actual NFS synthetic bytes or complete state differ")
					}
					var emptyDetail api.JobDetail
					read(labMultiPrefix(scope)+"/jobs/"+ns.JobIDs[1], &emptyDetail, 200)
					var empty api.LogRange
					read(labMultiPrefix(scope)+"/jobs/"+ns.JobIDs[1]+"/logs?stream=stdout", &empty, 200)
					if emptyDetail.Job.CurrentRun == nil || emptyDetail.Job.Scope != scope || emptyDetail.Job.ID != ns.JobIDs[1] || empty.State != "complete" || empty.BytesBase64 != "" || empty.StartOffset != "0" || empty.EndOffset != "0" || empty.RunID != emptyDetail.Job.CurrentRun.ID || empty.RunNumber != emptyDetail.Job.CurrentRun.Number || empty.ExecutionID != emptyDetail.Job.CurrentRun.ExecutionID || empty.Stream != "stdout" {
						t.Fatal("Empty stream was not independently represented")
					}
					other := research[0]
					if deployment == labDeployment {
						other = research[1]
					}
					read(labMultiPrefix(other)+"/jobs/"+ns.JobIDs[0], nil, 404)
					read(labMultiPrefix(other)+"/jobs/"+allowed[other].JobIDs[0]+"/logs?stream=stdout&limitBytes=8&cursor="+url.QueryEscape(originalCursor), nil, 409)
				}
			}
			t.Log("PASS: two real Control sources, asymmetric direct grants, source-qualified paginated catalogs and cross-owner NFS byte/empty streams; fixture observations remain synthetic")
		})
	}
}
