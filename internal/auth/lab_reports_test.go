//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const labDiagnosticRaw = "SYNTHETIC OBSERVATION ONLY: open synthetic-output.txt: permission denied; metadata and byte delivery\n"
const labDiagnosticCanary = "metadata and byte delivery"

// This baseline needs no supplemental target and can validate report storage
// immediately after the reviewed report-capable deployment.
func TestLabDeployedMetadataReport(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_REPORTS") != "1" {
		t.Skip("set runtime and reports Lab opt-ins after exact reviewed deployment")
	}
	session := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	var source struct {
		Synthetic  bool   `json:"synthetic"`
		InstanceID string `json:"instanceId"`
		Namespaces []struct {
			ID     string   `json:"id"`
			Name   string   `json:"name"`
			JobIDs []string `json:"jobIds"`
		} `json:"namespaces"`
	}
	data, err := os.ReadFile(filepath.Join(session.root, ".lab/dashboard/fixture-info.json"))
	if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &source) != nil || !source.Synthetic || len(source.Namespaces) != 2 || source.Namespaces[0].Name != "dashboard-research" || len(source.Namespaces[0].JobIDs) < 5 || !uuid(source.InstanceID) {
		t.Fatal("Original synthetic metadata fixture is unavailable")
	}
	namespace, jobID := source.Namespaces[0].ID, source.Namespaces[0].JobIDs[4]
	if !uuid(namespace) || !uuid(jobID) {
		t.Fatal("Original metadata fixture identity is invalid")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	request := labReportClient(t, ctx, session)
	jobPath := "/api/v1/deployments/" + labDeployment + "/namespaces/" + namespace + "/jobs/" + jobID
	var job api.JobDetail
	request("GET", jobPath, session.accessToken, "", nil, &job, 200)
	if job.Job.Outcome != "failure" || !job.Job.Imported {
		t.Fatal("Imported failure metadata baseline differs")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("Request identity unavailable")
	}
	key := "lab-metadata-" + hex.EncodeToString(nonce[:])
	var task api.Report
	request("POST", jobPath+"/reports", session.accessToken, key, api.ReportRequest{Profile: "metadata"}, &task, 202)
	if !uuid(task.TaskID) || task.Detail != nil {
		t.Fatal("Metadata admission did not return a task summary")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var report api.Report
		request("GET", jobPath+"/reports/"+task.TaskID, session.accessToken, "", nil, &report, 200)
		if report.State == "ready" {
			if report.Detail == nil || report.ReportID == "" || report.EvidenceID == "" || report.AnalysisEvidenceID == "" || report.SourceRevision != job.Job.Revision || report.Detail.ControlInstanceID != source.InstanceID || report.Detail.Disclosure.ProviderInvoked || report.Detail.Disclosure.GeneratedContentUsed || report.Profile != "metadata" || report.Outdated {
				t.Fatal("Actual metadata report lacks sealed pair/source provenance")
			}
			for _, ref := range report.Detail.Citations {
				var citation api.Citation
				request("GET", jobPath+"/reports/"+task.TaskID+"/citations/"+url.PathEscape(ref.ID), session.accessToken, "", nil, &citation, 200)
				if citation.BytesBase64 != nil || citation.ReportID != report.ReportID || citation.EvidenceID != report.EvidenceID || citation.AnalysisEvidenceID != report.AnalysisEvidenceID || !json.Valid([]byte(citation.ValueJSON)) {
					t.Fatal("Metadata citation is not from the exact sealed pair")
				}
			}
			break
		}
		if report.State == "failed" {
			t.Fatalf("Metadata worker failed with code %q", report.FailureCode)
		}
		select {
		case <-ctx.Done():
			t.Fatal("Metadata worker exceeded acceptance deadline")
		case <-ticker.C:
		}
	}
	t.Log("PASS: namespace member generated metadata diagnosis through actual Control snapshot, collector, deterministic engine, private paired storage and report/citation APIs; source history is synthetic")
}

type labDiagnosticFixture struct {
	Synthetic          bool   `json:"synthetic"`
	FixtureVersion     int    `json:"fixtureVersion"`
	ObservationMode    string `json:"observationMode"`
	HelperCommit       string `json:"helperCommit"`
	DeploymentID       string `json:"deploymentId"`
	ControlInstanceID  string `json:"controlInstanceId"`
	RecoveryEpoch      string `json:"recoveryEpoch"`
	NamespaceID        string `json:"namespaceId"`
	Namespace          string `json:"namespace"`
	TargetID           string `json:"targetId"`
	TargetName         string `json:"targetName"`
	TargetGenerationID string `json:"targetGenerationId"`
	JobID              string `json:"jobId"`
	JobRevision        string `json:"jobRevision"`
	RunID              string `json:"runId"`
	ExecutionID        string `json:"executionId"`
	Streams            []struct {
		Stream     string `json:"stream"`
		ByteLength string `json:"byteLength"`
		Chunks     []struct {
			Checksum string `json:"checksum"`
		} `json:"chunks"`
	} `json:"streams"`
}

func readLabDiagnosticFixture(t *testing.T, root string, required bool) *labDiagnosticFixture {
	t.Helper()
	file, err := os.Open(filepath.Join(root, ".lab/dashboard/diagnostic-fixture.json"))
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		t.Fatal("Supplemental diagnostic fixture is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	var fixture labDiagnosticFixture
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &fixture) != nil || !fixture.Synthetic || fixture.FixtureVersion != 1 || fixture.ObservationMode != "synthetic-store-observations-no-execution" || fixture.DeploymentID != labDeployment || fixture.Namespace != "dashboard-operations" || fixture.TargetName != "synthetic-diagnostics" || len(fixture.HelperCommit) != 40 || fixture.RecoveryEpoch == "" || fixture.JobRevision == "" || len(fixture.Streams) != 2 {
		t.Fatal("Supplemental synthetic diagnostic fixture is invalid")
	}
	for _, id := range []string{fixture.ControlInstanceID, fixture.NamespaceID, fixture.TargetID, fixture.TargetGenerationID, fixture.JobID, fixture.RunID, fixture.ExecutionID} {
		if !uuid(id) {
			t.Fatal("Supplemental fixture identity is not canonical")
		}
	}
	return &fixture
}

// Opt-in writes only ordinary synthetic report requests. It does not alter
// directory state, source jobs/logs, migrations or private object files. Login
// uses real PKCE/JWKS and tokens stay in memory without appearing in output.
func TestLabDeployedReportsAndSealedCitations(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_REPORTS") != "1" {
		t.Skip("set runtime and reports Lab opt-ins after exact reviewed deployment")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	fixture := readLabDiagnosticFixture(t, alice.root, true)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	request := labReportClient(t, ctx, alice)
	jobPath := "/api/v1/deployments/" + labDeployment + "/namespaces/" + fixture.NamespaceID + "/jobs/" + fixture.JobID
	var job api.JobDetail
	request("GET", jobPath, alice.accessToken, "", nil, &job, 200)
	if job.Job.Outcome != "failure" || job.Job.Revision != fixture.JobRevision || job.Job.TargetGenerationID != fixture.TargetGenerationID || job.Job.CurrentRun == nil || job.Job.CurrentRun.ID != fixture.RunID || job.Job.CurrentRun.ExecutionID != fixture.ExecutionID {
		t.Fatal("Recognized synthetic failed-job baseline differs")
	}
	var original api.LogRange
	request("GET", jobPath+"/logs?stream=stderr&limitBytes=262144", alice.accessToken, "", nil, &original, 200)
	decoded, err := base64.StdEncoding.DecodeString(original.BytesBase64)
	if err != nil || string(decoded) != labDiagnosticRaw || original.State != "complete" || original.RunID != fixture.RunID || original.ExecutionID != fixture.ExecutionID || original.StartOffset != "0" || original.EndOffset != strconv.Itoa(len(decoded)) {
		t.Fatal("Exact original synthetic NFS log is unavailable")
	}
	digest := sha256.Sum256(decoded)
	if fixture.Streams[1].Stream != "stderr" || fixture.Streams[1].ByteLength != strconv.Itoa(len(decoded)) || len(fixture.Streams[1].Chunks) != 1 || fixture.Streams[1].Chunks[0].Checksum != "sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatal("Supplemental manifest hash differs from actual NFS bytes")
	}
	reportsPath := jobPath + "/reports"
	request("GET", reportsPath, bob.accessToken, "", nil, nil, 403)
	var completed []api.Report
	for _, profile := range []string{"metadata", "include_log_tail"} {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			t.Fatal("Request identity unavailable")
		}
		key := "lab-report-" + hex.EncodeToString(nonce[:])
		body := api.ReportRequest{Profile: profile, RunID: fixture.RunID}
		var admitted api.Report
		request("POST", reportsPath, alice.accessToken, key, body, &admitted, 202)
		if !uuid(admitted.TaskID) || admitted.JobID != fixture.JobID || admitted.Profile != profile || admitted.RunID != fixture.RunID || admitted.Detail != nil {
			t.Fatal("Report admission did not preserve exact selection")
		}
		var replay api.Report
		request("POST", reportsPath, alice.accessToken, key, body, &replay, 202)
		if replay.TaskID != admitted.TaskID || replay.Detail != nil {
			t.Fatal("Idempotent report replay changed task identity")
		}
		other := body
		other.Profile = "metadata"
		if profile == "metadata" {
			other.Profile = "include_log_tail"
		}
		request("POST", reportsPath, alice.accessToken, key, other, nil, 409)
		taskPath := reportsPath + "/" + admitted.TaskID
		deadline := time.NewTimer(90 * time.Second)
		ticker := time.NewTicker(time.Second)
		var ready api.Report
		for {
			raw := request("GET", taskPath, alice.accessToken, "", nil, &ready, 200)
			if bytes.Contains(raw, []byte(labDiagnosticCanary)) {
				t.Fatal("Report contains configured redaction canary")
			}
			if ready.State == "ready" {
				break
			}
			if ready.Detail != nil || ready.State == "failed" {
				t.Fatalf("Report did not produce a verified pair (state %q, failure %q)", ready.State, ready.FailureCode)
			}
			if ready.State != "queued" && ready.State != "collecting" && ready.State != "analyzing" {
				t.Fatal("Unexpected report state")
			}
			select {
			case <-ctx.Done():
				t.Fatal("Report acceptance deadline exceeded")
			case <-deadline.C:
				t.Fatal("Report worker did not finish within bound")
			case <-ticker.C:
			}
		}
		deadline.Stop()
		ticker.Stop()
		detail := ready.Detail
		if detail == nil || ready.TaskID != admitted.TaskID || ready.Scope != (api.Scope{DeploymentID: labDeployment, NamespaceID: fixture.NamespaceID}) || ready.SourceRevision != fixture.JobRevision || ready.Outdated || ready.ReportID == "" || ready.EvidenceID == "" || ready.AnalysisEvidenceID == "" || detail.ControlInstanceID != fixture.ControlInstanceID || detail.Outcome != "failure" || len(detail.Runs) != 1 || detail.Runs[0].ID != fixture.RunID || detail.Runs[0].ExecutionID != fixture.ExecutionID || detail.Versions.Companion == "" || detail.Versions.Jobman == "" || detail.Disclosure.ProviderInvoked || detail.Disclosure.GeneratedContentUsed || len(detail.Generators) != 0 || len(detail.Findings) == 0 {
			t.Fatal("Ready report lost sealed IDs, source provenance or deterministic disclosure")
		}
		itemCount, logCount := 0, 0
		masked := strings.ReplaceAll(labDiagnosticRaw, labDiagnosticCanary, strings.Repeat("*", len(labDiagnosticCanary)))
		for _, ref := range detail.Citations {
			var citation api.Citation
			citationPath := taskPath + "/citations/" + url.PathEscape(ref.ID)
			raw := request("GET", citationPath, alice.accessToken, "", nil, &citation, 200)
			if bytes.Contains(raw, []byte(labDiagnosticCanary)) || citation.TaskID != ready.TaskID || citation.ReportID != ready.ReportID || citation.EvidenceID != ready.EvidenceID || citation.AnalysisEvidenceID != ready.AnalysisEvidenceID || citation.ID != ref.ID {
				t.Fatal("Citation identity or redaction boundary differs")
			}
			if citation.BytesBase64 == nil {
				if citation.Kind != "item" || !json.Valid([]byte(citation.ValueJSON)) {
					t.Fatal("Metadata citation is not exact valid item JSON")
				}
				itemCount++
			} else {
				data, decodeErr := base64.StdEncoding.DecodeString(*citation.BytesBase64)
				start, startErr := strconv.ParseUint(citation.StartOffset, 10, 64)
				end, endErr := strconv.ParseUint(citation.EndOffset, 10, 64)
				if profile != "include_log_tail" || decodeErr != nil || startErr != nil || endErr != nil || start > end || end > uint64(len(masked)) || string(data) != masked[start:end] || citation.Stream != "stderr" || citation.RunID != fixture.RunID || citation.ExecutionID != fixture.ExecutionID || !citation.OriginalOffsetsExact || citation.OriginalStartOffset != citation.StartOffset || citation.OriginalEndOffset != citation.EndOffset || citation.RangeBasis != "sanitized_artifact_bytes" {
					t.Fatal("Sealed citation bytes or original offset attribution differ")
				}
				logCount++
			}
			request("GET", citationPath, bob.accessToken, "", nil, nil, 404)
		}
		if profile == "metadata" && itemCount == 0 {
			t.Fatal("Metadata report has no actual item citation")
		}
		if profile == "include_log_tail" {
			recognized := false
			for _, finding := range detail.Findings {
				recognized = recognized || finding.Code == "target.permission_message"
			}
			if logCount == 0 || len(detail.RedactionNotices) == 0 || !recognized {
				t.Fatal("Actual redacted log evidence did not produce its deterministic diagnostic and citation")
			}
		}
		request("GET", taskPath+"/citations/unknown-synthetic-citation", alice.accessToken, "", nil, nil, 404)
		request("GET", taskPath, bob.accessToken, "", nil, nil, 404)
		request("POST", reportsPath, bob.accessToken, key, body, nil, 403)
		completed = append(completed, ready)
	}
	// Both exact profile tasks must be reachable through bounded history pages;
	// already-retained synthetic tasks may precede them on repeat acceptance.
	seen := map[string]bool{}
	cursor := ""
	for pageNumber := 0; pageNumber < 40; pageNumber++ {
		var page api.ReportPage
		path := reportsPath + "?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		request("GET", path, alice.accessToken, "", nil, &page, 200)
		if len(page.Items) > 1 || page.FetchedAt.IsZero() {
			t.Fatal("Report catalog bounds or freshness differ")
		}
		for _, item := range page.Items {
			if item.Detail != nil || item.Scope != completed[0].Scope || item.JobID != fixture.JobID || seen[item.TaskID] {
				t.Fatal("Report catalog exposed content, wrong scope or duplicate")
			}
			seen[item.TaskID] = true
		}
		if seen[completed[0].TaskID] && seen[completed[1].TaskID] {
			break
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if !seen[completed[0].TaskID] || !seen[completed[1].TaskID] {
		t.Fatal("Report catalog lost admitted tasks")
	}
	var unchanged api.LogRange
	request("GET", jobPath+"/logs?stream=stderr&limitBytes=262144", alice.accessToken, "", nil, &unchanged, 200)
	if unchanged.BytesBase64 != original.BytesBase64 || unchanged.RunID != original.RunID || unchanged.ExecutionID != original.ExecutionID {
		t.Fatal("Analysis altered original source log bytes")
	}
	t.Log("PASS: real PKCE, Control/broker/NFS collection, deterministic metadata and redacted-log reports, paired IDs, exact sealed citation bytes/offsets, idempotency, catalog and cross-account denial; observations remain synthetic")
}

func labReportClient(t *testing.T, ctx context.Context, session labNativeSession) func(string, string, string, string, any, any, int) []byte {
	t.Helper()
	transport := session.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "dashboard.lab.test:8443" {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:8443")
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(method, path, token, key string, body any, target any, want int) []byte {
		t.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal("Cannot encode synthetic request")
			}
		}
		r, err := http.NewRequestWithContext(ctx, method, "https://dashboard.lab.test:8443"+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal("Invalid synthetic report endpoint")
		}
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("Deployed report request failed")
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		if err != nil || len(raw) > 4<<20 {
			t.Fatal("Deployed report response exceeded bound")
		}
		if response.StatusCode != want {
			var failure api.Error
			_ = json.Unmarshal(raw, &failure)
			t.Fatalf("Report request returned HTTP%d, expected%d (code %q)", response.StatusCode, want, failure.Code)
		}
		if target != nil && json.Unmarshal(raw, target) != nil {
			t.Fatal("Report response differs from API contract")
		}
		return raw
	}
}
