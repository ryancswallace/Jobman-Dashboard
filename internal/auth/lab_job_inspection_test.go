//go:build integration && (darwin || linux)

package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// The existing Control devel/labfixture/seed.go fixtureWorkload submits this
// exact invocation for all five retained jobs in each namespace. Workload
// normalization supplies workspace:/ and an empty argument array. These are
// synthetic observations, not evidence of a real process executing this command.
func labInspectionExecution(raw json.RawMessage) bool {
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil {
		return false
	}
	_ = json.Unmarshal([]byte(`{"command":{"executable":"true","args":[]},"workingDirectory":"workspace:/"}`), &expected)
	// Exact shape also refuses accidental environment or extra workload fields.
	return reflect.DeepEqual(actual, expected)
}

func labInspectionSummary(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for _, name := range []string{"execution", "executionUnavailableReason", "command", "environment", "workingDirectory"} {
		if _, exists := fields[name]; exists {
			return false
		}
	}
	return true
}

// After the existing synthetic OIDC sign-ins, every request is GET-only. The
// shared reader bounds bodies to 4 MiB and requests to 100, enforces no-store,
// checks sanitized denials, and never prints responses or bearer credentials.
// There are no subprocesses, retries, persisted outputs, or fixture mutations.
func TestLabJobInspection(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_JOB_INSPECTION") != "1" {
		t.Skip("set runtime and job inspection Lab opt-ins after reviewed deployment")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	fixtures := map[string]labMultiFixture{
		labDeployment:          labReadMultiFixture(t, filepath.Join(alice.root, ".lab/dashboard/fixture-info.json"), false),
		labSecondaryDeployment: labReadMultiFixture(t, os.Getenv("JOBMAN_DASHBOARD_LAB_SECONDARY_FIXTURE"), true),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	requests, details, denials := 0, 0, 0
	for _, subject := range []struct {
		name    string
		session labNativeSession
	}{{"alice", alice}, {"bob", bob}} {
		reader := labRunsReader{http: labMixedClient(t, subject.session, nil, false), count: &requests}
		read := func(path string, output any, status int) {
			t.Helper()
			if err := reader.get(ctx, path, output, status); err != nil {
				t.Fatal(err) // The reader returns only sanitized local diagnostics.
			}
		}
		var bootstrap api.Bootstrap
		read("/api/v1/bootstrap", &bootstrap, 200)
		if bootstrap.FixtureMode || bootstrap.Completeness != "complete" || len(bootstrap.Deployments) != 2 {
			t.Fatal("Job inspection requires two complete actual Control sources")
		}
		allowed := labMultiScopes(fixtures, subject.name)
		for _, deployment := range []string{labDeployment, labSecondaryDeployment} {
			fixture := fixtures[deployment]
			for _, ns := range fixture.Namespaces {
				scope := api.Scope{DeploymentID: deployment, NamespaceID: ns.ID}
				if _, ok := allowed[scope]; !ok {
					read(labRunsPath(scope, ns.JobIDs[0]), nil, 403)
					denials++
					continue
				}
				var page api.Page[json.RawMessage]
				read("/api/v1/jobs?"+labMultiQuery([]api.Scope{scope}, 10, ""), &page, 200)
				if page.Completeness != "complete" || len(page.Items) == 0 || len(page.Items) > 10 {
					t.Fatal("Job inspection requires a bounded complete list sample")
				}
				labMultiSources(t, page.Sources, map[api.Scope]labMultiNamespace{scope: ns})
				for _, raw := range page.Items {
					var job api.Job
					if !labInspectionSummary(raw) || json.Unmarshal(raw, &job) != nil || job.Scope != scope {
						t.Fatal("Job list exposed execution details or crossed its namespace")
					}
				}
				for _, id := range ns.JobIDs {
					var raw json.RawMessage
					read(labRunsPath(scope, id), &raw, 200)
					var detail api.JobDetail
					var fields map[string]json.RawMessage
					if json.Unmarshal(raw, &detail) != nil || json.Unmarshal(raw, &fields) != nil || !labInspectionExecution(fields["execution"]) || !labInspectionSummary(fields["job"]) || detail.ExecutionUnavailableReason != "" {
						t.Fatal("Retained job did not expose the exact submitted invocation and allowlisted projection")
					}
					job := detail.Job
					digest, err := hex.DecodeString(strings.TrimPrefix(job.WorkloadDigest, "sha256:"))
					if job.Scope != scope || job.ID != id || job.TargetName != "synthetic-host" || !uuid(job.TargetID) || job.TargetGenerationID != ns.TargetGenerationID || job.Backend != "subprocess" || job.Partition != "" || err != nil || len(digest) != 32 || !strings.HasPrefix(job.WorkloadDigest, "sha256:") || detail.FetchedAt.IsZero() {
						t.Fatal("Retained job placement, workload identity, or source-qualified detail differs")
					}
					if job.CurrentRun != nil && job.CurrentRun.ExecutionID != "" && (job.ConfidenceUpdatedAt == nil || job.ConfidenceUpdatedAt.IsZero()) {
						t.Fatal("Observed retained execution omitted confidence observation time")
					}
					details++
				}
				// A known job ID must not resolve through another namespace, even
				// when the caller can view that namespace's other jobs.
				for _, other := range fixture.Namespaces {
					if other.ID != ns.ID {
						read(labRunsPath(scope, other.JobIDs[0]), nil, 404)
						denials++
					}
				}
			}
		}
	}
	if details != 30 || denials != 8 {
		t.Fatal("Asymmetric two-source inspection coverage differs from retained fixtures")
	}
	t.Logf("PASS: %d exact retained invocation reads across both Control deployments, %d namespace denials, %d bounded GETs; list samples omit execution; no jobs created", details, denials, requests)
}

func TestLabJobInspectionProjectionGuards(t *testing.T) {
	for _, input := range []string{
		`{"command":{"executable":"true","args":null},"workingDirectory":"workspace:/"}`,
		`{"command":{"executable":"true","args":[""]},"workingDirectory":"workspace:/"}`,
		`{"command":{"executable":"true","args":[]},"workingDirectory":"workspace:/","environment":{"SYNTHETIC":"value"}}`,
		`{"command":{"executable":"false","args":[]},"workingDirectory":"workspace:/"}`,
	} {
		if labInspectionExecution(json.RawMessage(input)) {
			t.Fatal("Inspection accepted a changed or expanded immutable fixture projection")
		}
	}
	if !labInspectionExecution(json.RawMessage(`{"workingDirectory":"workspace:/","command":{"args":[],"executable":"true"}}`)) {
		t.Fatal("Inspection must compare the invocation independently of JSON field order")
	}
	if !labInspectionSummary(json.RawMessage(`{"id":"synthetic","currentRun":{"executionId":"synthetic"}}`)) {
		t.Fatal("Summary guard must allow ordinary execution identity metadata")
	}
	for _, input := range []string{`null`, `{"execution":null}`, `{"executionUnavailableReason":"missing"}`, `{"environment":{}}`, `{"command":{}}`} {
		if labInspectionSummary(json.RawMessage(input)) {
			t.Fatal("Summary guard accepted absent summary or leaked execution fields")
		}
	}
}
