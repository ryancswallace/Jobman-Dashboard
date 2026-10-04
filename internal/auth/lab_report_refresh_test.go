//go:build integration && (darwin || linux)

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const labOriginalSlurmReceiptSHA = "186719a51c266e8d48157a508d5e485382811e7a60b1fbc32b1a2f1ab2107801"
const labRefreshedControlVersion = "dashboard-lab-04bd83db28bc"

// This preparation admits only two diagnosis tasks for the already accepted
// Slurm execution. It never submits, cancels or re-executes a source workload.
// The original reports, citations and receipt remain intact; the derived receipt
// keeps the original execution verification time and adds separate refresh facts.
func TestLabRefreshAcceptedSlurmReports(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_REFRESH_SLURM_REPORTS") != "1" {
		t.Skip("reviewed current-version report preparation requires explicit opt-in")
	}
	input := os.Getenv("JOBMAN_DASHBOARD_LAB_REFRESH_ORIGINAL")
	output := os.Getenv("JOBMAN_DASHBOARD_LAB_REFRESH_RESULT")
	if !filepath.IsAbs(input) || !filepath.IsAbs(output) || filepath.Clean(output) != output {
		t.Fatal("absolute original receipt and fresh result paths required")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil || parent != filepath.Dir(output) {
		t.Fatal("result parent must be canonical")
	}
	raw, err := labScaleReadFile(input, 32768)
	sum := sha256.Sum256(raw)
	var prior labMixedSlurm
	var receipt map[string]json.RawMessage
	if err != nil || hex.EncodeToString(sum[:]) != labOriginalSlurmReceiptSHA || json.Unmarshal(raw, &prior) != nil || !prior.valid() || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("exact independently accepted original Slurm receipt required")
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("fresh result required; retain any previous attempt")
	}
	defer file.Close()
	if file.Chmod(0600) != nil {
		t.Fatal("result private mode")
	}
	journal, err := os.OpenFile(output+".progress.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("fresh journal required")
	}
	defer journal.Close()
	if journal.Chmod(0600) != nil {
		t.Fatal("journal private mode")
	}
	progress := func(value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil || len(data) > 8192 {
			t.Fatal("bounded progress required")
		}
		if _, err := journal.Write(append(data, '\n')); err != nil || journal.Sync() != nil {
			t.Fatal("progress durability")
		}
	}
	progress(map[string]any{"phase": "started", "originalReceiptSHA256": labOriginalSlurmReceiptSHA, "maximumNewReports": 2, "newJobs": 0})
	directory, err := os.Open(parent)
	if err != nil {
		t.Fatal("result parent unavailable")
	}
	syncErr, closeErr := directory.Sync(), directory.Close()
	if syncErr != nil || closeErr != nil {
		t.Fatal("result directory durability")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	request := labReportClient(t, ctx, alice)
	control := labExecutionControl(t, ctx, alice)
	path := "/api/v1/deployments/" + labDeployment + "/namespaces/" + labMixedOperationsNS + "/jobs/" + prior.Jobs["task-2"]
	var current api.JobDetail
	request("GET", path, alice.accessToken, "", nil, &current, 200)
	run, err := prior.pinnedRun(current)
	if err != nil {
		t.Fatal(err)
	}
	var source labActualJob
	control("GET", "/v1/namespaces/dashboard-operations/jobs/"+current.Job.ID, "", nil, &source)
	if source.Metadata.ID != current.Job.ID || source.Status.CurrentRun == nil || *source.Status.CurrentRun != run || source.Status.Phase != "terminal" || source.Status.Outcome != "failure" || source.Status.Imported {
		t.Fatal("original source run differs")
	}
	verifyIdentity := func() {
		t.Helper()
		var caps struct {
			Capabilities struct{ InstanceID, RecoveryEpoch string }
		}
		control("GET", "/v1/capabilities", "", nil, &caps)
		if caps.Capabilities.InstanceID != prior.Fixture.ControlInstanceID || caps.Capabilities.RecoveryEpoch != prior.Fixture.RecoveryEpoch {
			t.Fatal("source identity changed")
		}
		var latest api.JobDetail
		request("GET", path, alice.accessToken, "", nil, &latest, 200)
		if labRestoreDigest(latest.Job) != labRestoreDigest(current.Job) {
			t.Fatal("accepted job changed during report refresh")
		}
	}
	verifyIdentity()
	oldHashes := map[string]string{}
	oldVersions := map[string]string{}
	checkOld := func(record bool) {
		t.Helper()
		for _, profile := range []string{"metadata", "include_log_tail"} {
			var value api.Report
			oldPath := path + "/reports/" + prior.Reports[profile]
			request("GET", oldPath, alice.accessToken, "", nil, &value, 200)
			if value.TaskID != prior.Reports[profile] || value.JobID != current.Job.ID || value.Scope != current.Job.Scope || value.Profile != profile || value.State != "ready" || value.Detail == nil || !value.Outdated || value.SourceRevision != current.Job.Revision || value.RunID != run.ID || len(value.Detail.Runs) != 1 || value.Detail.Runs[0] != run || value.Detail.ControlInstanceID != prior.Fixture.ControlInstanceID || value.Detail.ControlVersion == labRefreshedControlVersion || len(value.Detail.Citations) > 256 {
				t.Fatal("historical report source or expected version transition differs")
			}
			if record {
				oldHashes[oldPath] = labRestoreDigest(value)
				oldVersions[profile] = value.Detail.ControlVersion
			} else if oldHashes[oldPath] != labRestoreDigest(value) {
				t.Fatal("historical sealed report changed")
			}
			for _, ref := range value.Detail.Citations {
				var citation api.Citation
				citationPath := oldPath + "/citations/" + url.PathEscape(ref.ID)
				request("GET", citationPath, alice.accessToken, "", nil, &citation, 200)
				if citation.TaskID != value.TaskID || citation.ReportID != value.ReportID || citation.EvidenceID != value.EvidenceID || citation.ID != ref.ID {
					t.Fatal("historical citation identity differs")
				}
				if record {
					oldHashes[citationPath] = labRestoreDigest(citation)
				} else if oldHashes[citationPath] != labRestoreDigest(citation) {
					t.Fatal("historical sealed citation changed")
				}
			}
		}
	}
	checkOld(true)
	checkLog := func() {
		labActualCheckLog(t, control, request, alice.accessToken, bob.accessToken, prior.Fixture, source, path, "stderr", labSlurmFailureLog)
	}
	checkLog()
	// Reuse all actual Slurm diagnosis/citation/redaction/denial assertions. Only
	// the fixed preparation idempotency key differs from the original acceptance.
	admitted := map[string]string{}
	wrapped := func(method, endpoint, token, key string, body, target any, want int) []byte {
		t.Helper()
		if method == "POST" {
			input, ok := body.(api.ReportRequest)
			if !ok || endpoint != path+"/reports" || input.RunID != run.ID || (input.Profile != "metadata" && input.Profile != "include_log_tail") || token != alice.accessToken {
				t.Fatal("report preparation escaped fixed scope")
			}
			key = "slurm-report-refresh-control04bd83d-v1-" + input.Profile
			progress(map[string]string{"phase": "report-request-intent", "profile": input.Profile, "key": key})
		}
		data := request(method, endpoint, token, key, body, target, want)
		if method == "POST" {
			value, ok := target.(*api.Report)
			if !ok || !uuid(value.TaskID) {
				t.Fatal("report admission identity")
			}
			profile := body.(api.ReportRequest).Profile
			if old := admitted[profile]; old != "" && old != value.TaskID {
				t.Fatal("report replay identity")
			}
			admitted[profile] = value.TaskID
			progress(map[string]string{"phase": "report-admitted", "profile": profile, "taskId": value.TaskID})
		}
		return data
	}
	refreshed := labSlurmReports(t, ctx, wrapped, alice.accessToken, bob.accessToken, prior.Fixture, source, path)
	newHashes := map[string]string{}
	for profile, id := range refreshed {
		var report api.Report
		request("GET", path+"/reports/"+id, alice.accessToken, "", nil, &report, 200)
		if id == prior.Reports[profile] || report.Detail == nil || report.Detail.ControlVersion != labRefreshedControlVersion || report.Outdated || report.SourceRevision != current.Job.Revision {
			t.Fatal("new report does not represent reviewed current source")
		}
		newHashes[profile] = labRestoreDigest(report)
	}
	checkOld(false)
	checkLog()
	verifyIdentity()
	latest, err := labScaleReadFile(input, 32768)
	if err != nil || sha256.Sum256(latest) != sum {
		t.Fatal("original receipt changed")
	}
	receipt["reports"], _ = json.Marshal(refreshed)
	receipt["reportRefresh"], _ = json.Marshal(map[string]any{"verifiedAt": time.Now().UTC(), "originalReceiptSHA256": labOriginalSlurmReceiptSHA, "originalReports": prior.Reports, "historicalControlVersions": oldVersions, "controlVersion": labRefreshedControlVersion, "jobRevision": current.Job.Revision, "run": run, "historicalSemanticSHA256": oldHashes, "currentReportSHA256": newHashes, "newJobs": 0})
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil || len(encoded) > 32768 {
		t.Fatal("derived receipt bound")
	}
	if _, err = file.Write(append(encoded, '\n')); err != nil || file.Sync() != nil {
		t.Fatal("derived receipt durability")
	}
	progress(map[string]any{"phase": "completed", "reports": refreshed, "historicalPreservation": true})
	t.Log("PASS: current-version diagnosis pairs for the same accepted Slurm execution; original reports/citations/logs/job/receipt preserved, no new source workloads")
}
