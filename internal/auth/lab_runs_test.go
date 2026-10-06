//go:build integration && (darwin || linux)

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const labRunsHostSHA = "defe8baf9deec9cb17e3f62b9ddcea6051ac82d50787ab269ef8a7b689910241"
const labRunsMaximumRequests = 100
const labRunsUnknown = "ffffffff-ffff-4fff-8fff-ffffffffffff"

// This acceptance is deliberately GET-only after the existing synthetic OIDC
// sign-ins. It cannot create a run, report, rule, command or executor workload.
// Its two retained execution receipts currently contain one run per job. A
// successful result must state that limit rather than claim historical retry.
type labRunsHost struct {
	Synthetic             bool
	ObservationMode       string
	Fixture               labActualReceipt
	Jobs, Reports         map[string]string
	CollectionID, GraphID string
	VerifiedAt            time.Time
}
type labRunsSource struct {
	APIVersion, Kind, Namespace, NamespaceID, JobID, RecoveryEpoch string
	AuthorizationVersion                                           string
	AsOf, AuthorizationCheckedAt, AuthorizationExpiresAt           time.Time
	Total                                                          string
	NextPageToken                                                  string
	Items                                                          []api.JobRun
	Run                                                            api.JobRun
}
type labRunsJobProof struct {
	Scope                api.Scope        `json:"scope"`
	JobID                string           `json:"jobId"`
	RunCount             int              `json:"runCount"`
	Selected             api.RunReference `json:"selected"`
	ArtifactCount        int              `json:"artifactCount"`
	LogSHA256            string           `json:"logSHA256"`
	MultiAttemptObserved bool             `json:"multiAttemptObserved"`
}
type labRunsResult struct {
	Synthetic                             bool   `json:"synthetic"`
	Mode                                  string `json:"mode"`
	StartedAt, FinishedAt                 time.Time
	Passed                                bool   `json:"passed"`
	Phase                                 string `json:"phase"`
	Requests                              int    `json:"requests"`
	HostReceiptSHA256, SlurmReceiptSHA256 string
	Jobs                                  []labRunsJobProof `json:"jobs"`
	SkippedNoExecution                    int               `json:"skippedNoExecution"`
	ImportedOnly                          string            `json:"importedOnly"`
}

// One shared request budget covers all three authenticated clients, including
// failed denials and final rechecks. There are no automatic request retries.
type labRunsReader struct {
	http  labMixedHTTP
	count *int
}

func (r labRunsReader) get(ctx context.Context, path string, output any, status int) error {
	if *r.count >= labRunsMaximumRequests || len(path) > 8192 {
		return errors.New("run acceptance request bound reached")
	}
	u, err := url.Parse(path)
	prefix := "/api/v1/"
	if r.http.origin == "https://10.77.0.21:18443" {
		prefix = "/v1/"
	}
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, prefix) || strings.ContainsAny(path, "\r\n") {
		return errors.New("run acceptance endpoint invalid")
	}
	*r.count++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.http.origin+path, nil)
	if err != nil {
		return errors.New("run acceptance request invalid")
	}
	req.Header.Set("Authorization", "Bearer "+r.http.token)
	response, err := r.http.client.Do(req)
	if err != nil {
		return errors.New("run acceptance HTTPS request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return errors.New("run acceptance response exceeds bound or is incomplete")
	}
	if response.StatusCode != status {
		return fmt.Errorf("run acceptance returned HTTP%d, expected HTTP%d", response.StatusCode, status)
	}
	if prefix == "/api/v1/" && (response.Header.Get("Cache-Control") != "no-store" || len(response.Header.Values("Set-Cookie")) != 0) {
		return errors.New("run acceptance cache or cookie contract differs")
	}
	if status != 200 {
		e, ok := labFaultDecodeError(raw)
		id, decodeErr := hex.DecodeString(e.RequestID)
		expected := map[int]struct{ code, message string }{
			403: {"forbidden", "You do not currently have permission to view this information."},
			404: {"not_found_or_inaccessible", "This item could not be found, or you do not have access to it."},
		}[status]
		if !ok || decodeErr != nil || len(id) != 16 || hex.EncodeToString(id) != e.RequestID || e.Code != expected.code || e.Message != expected.message {
			return errors.New("run acceptance denial contract differs")
		}
		return nil
	}
	if output == nil || json.Unmarshal(raw, output) != nil {
		return errors.New("run acceptance response contract invalid")
	}
	return nil
}
func labRunsPath(scope api.Scope, job string) string {
	return "/api/v1/deployments/" + scope.DeploymentID + "/namespaces/" + scope.NamespaceID + "/jobs/" + job
}
func labRunsSourcePath(namespace, job string) string {
	return "/v1/namespaces/" + namespace + "/jobs/" + job
}
func labRunsDecimal(value string, positive bool) (int64, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && strconv.FormatInt(n, 10) == value && n >= 0 && (!positive || n > 0)
}
func labRunsValidRun(v api.JobRun) bool {
	if !uuid(v.ID) || v.ID == "00000000-0000-0000-0000-000000000000" || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.Phase == "" || v.DesiredState == "" {
		return false
	}
	if _, ok := labRunsDecimal(v.Number, true); !ok {
		return false
	}
	for _, value := range []string{v.Phase, v.DesiredState, v.Outcome, v.ExecutionPhase, v.Backend, v.Confidence} {
		if len(value) > 64 {
			return false
		}
	}
	if v.ExecutionID == "" {
		return v.ExecutionPhase == "" && v.TargetID == "" && v.TargetGenerationID == "" && v.Backend == "" && v.Confidence == ""
	}
	for _, id := range []string{v.ExecutionID, v.TargetID, v.TargetGenerationID} {
		if !uuid(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return v.ExecutionPhase != "" && v.Backend != ""
}
func labRunsFresh(scope api.Scope, sources []api.SourceStatus, completeness string, fetched time.Time) bool {
	now := time.Now()
	return completeness == "complete" && len(sources) == 1 && sources[0].Scope == scope && sources[0].Status == "available" && sources[0].AsOf != nil && !sources[0].AsOf.IsZero() && !sources[0].AsOf.After(now.Add(5*time.Second)) && !sources[0].FetchedAt.IsZero() && !sources[0].FetchedAt.After(now.Add(5*time.Second)) && !fetched.IsZero() && !fetched.After(now.Add(5*time.Second))
}
func (s labRunsSource) valid(scope api.Scope, namespace, job, kind string) bool {
	now := time.Now()
	return s.APIVersion == "jobman.control/v1alpha1" && s.Kind == kind && s.Namespace == namespace && s.NamespaceID == scope.NamespaceID && s.JobID == job && s.RecoveryEpoch == "1" && s.AuthorizationVersion != "" && !s.AsOf.IsZero() && !s.AsOf.After(now.Add(5*time.Second)) && !s.AuthorizationCheckedAt.IsZero() && !s.AuthorizationCheckedAt.After(now.Add(5*time.Second)) && now.Before(s.AuthorizationExpiresAt)
}
func labRunsAppend(items, page []api.JobRun, total, next string) ([]api.JobRun, error) {
	count, ok := labRunsDecimal(total, false)
	if !ok || count > 20 || len(page) > 10 || len(next) > 1024 || len(items)+len(page) > int(count) || next != "" && len(page) == 0 {
		return nil, errors.New("run catalog page bound differs")
	}
	out := slices.Clone(items)
	seen := map[string]bool{}
	last := int64(0)
	for _, item := range items {
		seen[item.ID] = true
		last, _ = strconv.ParseInt(item.Number, 10, 64)
	}
	for _, item := range page {
		n, _ := strconv.ParseInt(item.Number, 10, 64)
		if !labRunsValidRun(item) || seen[item.ID] || last != 0 && n >= last {
			return nil, errors.New("run catalog identity or order differs")
		}
		seen[item.ID] = true
		last = n
		out = append(out, item)
	}
	if (next == "") != (len(out) == int(count)) {
		return nil, errors.New("run catalog continuation or exact total differs")
	}
	return out, nil
}
func labRunsCatalog(ctx context.Context, reader labRunsReader, scope api.Scope, namespace, job string, source bool) ([]api.JobRun, error) {
	var items []api.JobRun
	cursor, total := "", ""
	for page := 0; page < 2; page++ {
		params := url.Values{"limit": {"10"}}
		var batch []api.JobRun
		var next, count string
		if source {
			if cursor != "" {
				params.Set("pageToken", cursor)
			}
			var p labRunsSource
			if err := reader.get(ctx, labRunsSourcePath(namespace, job)+"/runs?"+params.Encode(), &p, 200); err != nil {
				return nil, err
			}
			if !p.valid(scope, namespace, job, "RunList") {
				return nil, errors.New("source run catalog authority differs")
			}
			batch, next, count = p.Items, p.NextPageToken, p.Total
		} else {
			if cursor != "" {
				params.Set("cursor", cursor)
			}
			var p api.RunPage
			if err := reader.get(ctx, labRunsPath(scope, job)+"/runs?"+params.Encode(), &p, 200); err != nil {
				return nil, err
			}
			if !labRunsFresh(scope, p.Sources, p.Completeness, p.FetchedAt) {
				return nil, errors.New("Dashboard run catalog authority differs")
			}
			batch, next, count = p.Items, p.NextCursor, p.Total
		}
		if total != "" && total != count || next != "" && next == cursor {
			return nil, errors.New("run catalog changed while paging")
		}
		var err error
		items, err = labRunsAppend(items, batch, count, next)
		if err != nil {
			return nil, err
		}
		if next == "" {
			return items, nil
		}
		cursor, total = next, count
	}
	return nil, errors.New("run catalog exceeds two-page acceptance bound")
}
func labRunsLog(v api.LogRange, selected api.RunReference, stream, expected string, start int) bool {
	bytes, err := base64.StdEncoding.DecodeString(v.BytesBase64)
	return err == nil && base64.StdEncoding.EncodeToString(bytes) == v.BytesBase64 && string(bytes) == expected && v.RunID == selected.ID && v.RunNumber == selected.Number && v.ExecutionID == selected.ExecutionID && v.Stream == stream && v.StartOffset == strconv.Itoa(start) && v.EndOffset == strconv.Itoa(start+len(expected)) && v.State == "complete" && !v.Truncated && v.NextCursor != "" && len(v.NextCursor) <= 8192 && (len(bytes) == 0 || v.CapturedAt != nil) && (v.CapturedAt == nil || !v.CapturedAt.IsZero() && !v.CapturedAt.After(time.Now().Add(5*time.Second)))
}
func labRunsArtifact(v api.Artifact, run api.JobRun) bool {
	_, sizeOK := labRunsDecimal(v.SizeBytes, false)
	digest, digestErr := hex.DecodeString(strings.TrimPrefix(v.Checksum, "sha256:"))
	validName := len(v.Name) > 0 && len(v.Name) <= 128 && strings.IndexFunc(v.Name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	}) == -1
	return run.ExecutionID != "" && validName && v.ID == run.ExecutionID+"/"+v.Name && strings.HasPrefix(v.Checksum, "sha256:") && digestErr == nil && len(digest) == 32 && hex.EncodeToString(digest) == strings.TrimPrefix(v.Checksum, "sha256:") && v.RunID == run.ID && v.RunNumber == run.Number && v.ExecutionID == run.ExecutionID && v.TargetGenerationID == run.TargetGenerationID && v.Availability == "metadata_only" && !v.PublishedAt.IsZero() && sizeOK
}
func labRunsExclusive(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(raw) > 64<<10 {
		return errors.New("run receipt exceeds bound")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("run receipt path already exists or is unavailable")
	}
	defer f.Close()
	if f.Chmod(0600) != nil {
		return errors.New("run receipt mode failed")
	}
	if _, err = f.Write(append(raw, '\n')); err != nil {
		return errors.New("run receipt write failed")
	}
	if f.Sync() != nil {
		return errors.New("run receipt sync failed")
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errors.New("run receipt parent unavailable")
	}
	defer d.Close()
	if d.Sync() != nil {
		return errors.New("run receipt parent sync failed")
	}
	return nil
}
func labRunsResultPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasSuffix(path, ".json") {
		return false
	}
	parent := filepath.Dir(path)
	canonical, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	if err != nil || statErr != nil || canonical != parent || !info.IsDir() || info.Mode().Perm() != 0700 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func TestLabActualRunCatalog(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_RUNS") != "1" {
		t.Skip("set explicit runtime and run-catalog opt-ins after reviewed Control/Dashboard deployment")
	}
	if !filepath.IsAbs(os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT")) {
		t.Fatal("absolute authorized Lab root required before creating an intent")
	}
	resultPath := os.Getenv("JOBMAN_DASHBOARD_LAB_RUNS_RESULT")
	if !labRunsResultPath(resultPath) {
		t.Fatal("a new absolute result path in a canonical owner0700 directory is required")
	}
	if _, err := os.Lstat(resultPath); !os.IsNotExist(err) {
		t.Fatal("run result must not already exist")
	}
	result := labRunsResult{Synthetic: true, Mode: "read-only-accepted-run-catalog", StartedAt: time.Now().UTC(), Phase: "inputs", HostReceiptSHA256: labRunsHostSHA, SlurmReceiptSHA256: labAcceptedCurrentSlurmReceiptSHA, Jobs: []labRunsJobProof{}, ImportedOnly: "not_observed"}
	if err := labRunsExclusive(resultPath+".intent.json", map[string]any{"synthetic": true, "mode": result.Mode, "startedAt": result.StartedAt, "hostReceiptSHA256": labRunsHostSHA, "slurmReceiptSHA256": labAcceptedCurrentSlurmReceiptSHA}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		result.FinishedAt = time.Now().UTC()
		result.Passed = !t.Failed() && result.Phase == "complete"
		if err := labRunsExclusive(resultPath, result); err != nil {
			t.Error(err)
		}
	})
	load := func(path, expected string, out any) {
		t.Helper()
		if !filepath.IsAbs(path) {
			t.Fatal("absolute accepted receipt path required")
		}
		raw := labExecutionFile(t, path, 1<<20)
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != expected || json.Unmarshal(raw, out) != nil {
			t.Fatal("accepted public execution receipt differs")
		}
	}
	var host labRunsHost
	var slurm labMixedSlurm
	load(os.Getenv("JOBMAN_DASHBOARD_LAB_RUNS_HOST_RECEIPT"), labRunsHostSHA, &host)
	load(os.Getenv("JOBMAN_DASHBOARD_LAB_RUNS_SLURM_RECEIPT"), labAcceptedCurrentSlurmReceiptSHA, &slurm)
	if !host.Synthetic || host.ObservationMode != "actual-subprocess-agent-execution" || host.Fixture.DeploymentID != labDeployment || host.Fixture.ControlInstanceID != labRestoreInstance || host.Fixture.NamespaceID != labMixedOperationsNS || host.Fixture.Namespace != "dashboard-operations" || host.Fixture.RecoveryEpoch != "1" || host.VerifiedAt.IsZero() || !uuid(host.Jobs["failure"]) || !uuid(host.Reports["metadata"]) || !slurm.valid() {
		t.Fatal("accepted source identities differ")
	}
	result.Phase = "sign_in"
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	// Sign-in has two separate45s upper bounds; all following reads share3m.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	gate := &labMixedLimiter{slots: make(chan struct{}, 1)}
	a := labRunsReader{labMixedClient(t, alice, gate, false), &result.Requests}
	b := labRunsReader{labMixedClient(t, bob, gate, false), &result.Requests}
	source := labRunsReader{labMixedClient(t, alice, gate, true), &result.Requests}
	read := func(r labRunsReader, path string, out any, status int) {
		t.Helper()
		if err := r.get(ctx, path, out, status); err != nil {
			t.Fatal(err)
		}
	}
	capability := func() {
		t.Helper()
		var v struct {
			APIVersion, Kind string
			Capabilities     struct {
				InstanceID, RecoveryEpoch string
				Features                  []string
			}
		}
		read(source, "/v1/capabilities", &v, 200)
		if v.APIVersion != "jobman.control/v1alpha1" || v.Kind != "ControlCapabilities" || v.Capabilities.InstanceID != labRestoreInstance || v.Capabilities.RecoveryEpoch != "1" || !slices.Contains(v.Capabilities.Features, "bounded-run-catalog") {
			t.Fatal("reviewed source instance/epoch/run feature unavailable")
		}
	}
	result.Phase = "source_preflight"
	capability()
	fixture := labReadMultiFixture(t, filepath.Join(alice.root, ".lab/dashboard/fixture-info.json"), false)
	var research labMultiNamespace
	for _, ns := range fixture.Namespaces {
		if ns.Name == "dashboard-research" {
			research = ns
		}
	}
	if !uuid(research.ID) {
		t.Fatal("authorized research scope fixture missing")
	}
	for _, scenario := range []struct {
		name, job, backend, log, report string
		fixture                         labActualReceipt
		execution                       string
	}{
		{"host", host.Jobs["failure"], "subprocess", labActualFailureLog, host.Reports["metadata"], host.Fixture, ""},
		{"slurm", slurm.Jobs["task-2"], "slurm", labSlurmFailureLog, slurm.Reports["metadata"], slurm.Fixture, slurm.failureExecution()},
	} {
		result.Phase = scenario.name + "_catalog"
		scope := api.Scope{DeploymentID: scenario.fixture.DeploymentID, NamespaceID: scenario.fixture.NamespaceID}
		path := labRunsPath(scope, scenario.job)
		var before api.JobDetail
		var sourceBefore labActualJob
		read(a, path, &before, 200)
		read(source, labRunsSourcePath(scenario.fixture.Namespace, scenario.job), &sourceBefore, 200)
		j := before.Job
		if j.Scope != scope || j.ID != scenario.job || j.Imported || j.Phase != "terminal" || j.Outcome != "failure" || j.Owner == nil || !j.Owner.IsCurrentUser || !uuid(j.Owner.ID) || j.CurrentRun == nil || j.CompletedAt == nil || j.TargetID != scenario.fixture.TargetID || j.TargetGenerationID != scenario.fixture.TargetGenerationID || j.Backend != scenario.backend || sourceBefore.Metadata.ID != j.ID || sourceBefore.Metadata.NamespaceID != scope.NamespaceID || sourceBefore.Status.CurrentRun == nil || *sourceBefore.Status.CurrentRun != *j.CurrentRun || sourceBefore.Status.Imported || sourceBefore.Status.Phase != j.Phase || sourceBefore.Status.Outcome != j.Outcome || sourceBefore.Spec.Placement.TargetID != j.TargetID || sourceBefore.Spec.Placement.TargetGenerationID != j.TargetGenerationID || sourceBefore.Spec.Placement.ExecutionBackend != j.Backend {
			t.Fatal("accepted current job/run/owner/source provenance differs")
		}
		if scenario.execution != "" && j.CurrentRun.ExecutionID != scenario.execution {
			t.Fatal("accepted Slurm execution changed")
		}
		var report api.Report
		read(a, path+"/reports/"+scenario.report, &report, 200)
		if report.State != "ready" || report.Scope != scope || report.TaskID != scenario.report || report.JobID != j.ID || report.Profile != "metadata" || report.ReportID == "" || report.EvidenceID == "" || report.AnalysisEvidenceID == "" || report.Detail == nil || report.Detail.ControlInstanceID != labRestoreInstance || len(report.Detail.Runs) != 1 || report.Detail.Runs[0] != *j.CurrentRun {
			t.Fatal("retained accepted report no longer proves original run identity")
		}
		runs, err := labRunsCatalog(ctx, a, scope, scenario.fixture.Namespace, j.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		original, err := labRunsCatalog(ctx, source, scope, scenario.fixture.Namespace, j.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(runs, original) || len(runs) == 0 {
			t.Fatal("Dashboard run catalog differs from actual Control rows")
		}
		selected := runs[0]
		if (api.RunReference{ID: selected.ID, Number: selected.Number, ExecutionID: selected.ExecutionID}) != *j.CurrentRun || selected.TargetID != j.TargetID || selected.TargetGenerationID != j.TargetGenerationID || selected.Backend != scenario.backend || selected.Outcome != "failure" {
			t.Fatal("catalog selection differs from accepted current execution")
		}
		var detail api.RunDetail
		var originalDetail labRunsSource
		read(a, path+"/runs/"+selected.ID, &detail, 200)
		read(source, labRunsSourcePath(scenario.fixture.Namespace, j.ID)+"/runs/"+selected.ID, &originalDetail, 200)
		if !labRunsFresh(scope, detail.Sources, detail.Completeness, detail.FetchedAt) || detail.Run != selected || !originalDetail.valid(scope, scenario.fixture.Namespace, j.ID, "RunDetail") || originalDetail.Run != selected {
			t.Fatal("selected run detail differs from the exact catalog/source")
		}
		result.Phase = scenario.name + "_selected_evidence"
		for _, stream := range []string{"stdout", "stderr"} {
			expected := ""
			if stream == "stderr" {
				expected = scenario.log
			}
			params := url.Values{"stream": {stream}, "runNumber": {selected.Number}, "limitBytes": {"262144"}}
			var log api.LogRange
			read(a, path+"/logs?"+params.Encode(), &log, 200)
			if !labRunsLog(log, *j.CurrentRun, stream, expected, 0) {
				t.Fatal("selected log bytes or original run/execution differ")
			}
			params.Set("cursor", log.NextCursor)
			var follow api.LogRange
			read(a, path+"/logs?"+params.Encode(), &follow, 200)
			if !labRunsLog(follow, *j.CurrentRun, stream, "", len(expected)) {
				t.Fatal("selected log follow is not exact contiguous original execution")
			}
		}
		artifacts, total, cursor := 0, "", ""
		artifactDone := false
		artifactSeen := map[string]bool{}
		for page := 0; page < 2; page++ {
			params := url.Values{"runNumber": {selected.Number}, "limit": {"100"}}
			if cursor != "" {
				params.Set("cursor", cursor)
			}
			var p api.ArtifactPage
			read(a, path+"/artifacts?"+params.Encode(), &p, 200)
			n, ok := labRunsDecimal(p.Total, false)
			if !ok || n > 200 || len(p.Items) > 100 || !labRunsFresh(scope, p.Sources, p.Completeness, p.FetchedAt) || total != "" && total != p.Total || p.NextCursor != "" && (p.NextCursor == cursor || len(p.Items) == 0 || len(p.NextCursor) > 1024) {
				t.Fatal("artifact catalog bounds or source differ")
			}
			for _, artifact := range p.Items {
				if !labRunsArtifact(artifact, selected) || artifactSeen[artifact.ID] {
					t.Fatal("artifact metadata has incorrect selected execution attribution")
				}
				artifactSeen[artifact.ID] = true
			}
			artifacts += len(p.Items)
			if (p.NextCursor == "") != (artifacts == int(n)) {
				t.Fatal("artifact exact total differs")
			}
			if p.NextCursor == "" {
				artifactDone = true
				break
			}
			cursor, total = p.NextCursor, p.Total
		}
		if !artifactDone {
			t.Fatal("artifact acceptance pagination incomplete")
		}
		result.Phase = scenario.name + "_denials"
		for _, suffix := range []string{"/runs", "/runs/" + selected.ID, "/logs?stream=stderr&runNumber=" + selected.Number, "/artifacts?runNumber=" + selected.Number} {
			read(b, path+suffix, nil, 403)
		}
		read(a, path+"/runs/"+labRunsUnknown, nil, 404)
		wrong := labRunsPath(api.Scope{DeploymentID: labDeployment, NamespaceID: research.ID}, j.ID)
		read(a, wrong+"/runs", nil, 404)
		read(a, wrong+"/runs/"+selected.ID, nil, 404)
		var after api.JobDetail
		var sourceAfter labActualJob
		read(a, path, &after, 200)
		read(source, labRunsSourcePath(scenario.fixture.Namespace, j.ID), &sourceAfter, 200)
		if !reflect.DeepEqual(before.Job, after.Job) || !reflect.DeepEqual(sourceBefore, sourceAfter) {
			t.Fatal("read-only run acceptance changed or raced accepted job facts")
		}
		sum := sha256.Sum256([]byte(scenario.log))
		result.Jobs = append(result.Jobs, labRunsJobProof{Scope: scope, JobID: j.ID, RunCount: len(runs), Selected: *j.CurrentRun, ArtifactCount: artifacts, LogSHA256: hex.EncodeToString(sum[:]), MultiAttemptObserved: len(runs) > 1})
	}
	result.Phase = "cross_job_denials"
	if len(result.Jobs) != 2 || result.Jobs[0].Selected.ID == result.Jobs[1].Selected.ID {
		t.Fatal("accepted jobs did not retain distinct run identities")
	}
	for index, proof := range result.Jobs {
		other := result.Jobs[1-index]
		read(a, labRunsPath(proof.Scope, proof.JobID)+"/runs/"+other.Selected.ID, nil, 404)
	}
	result.Phase = "no_execution"
	noExecution := func(scope api.Scope, namespace, job string, imported bool) {
		t.Helper()
		var detail api.JobDetail
		read(a, labRunsPath(scope, job), &detail, 200)
		if detail.Job.ID != job || detail.Job.Scope != scope || detail.Job.Imported != imported || detail.Job.CurrentRun != nil || !imported && detail.Job.Disposition != "skipped" {
			t.Fatal("no-execution fixture facts differ")
		}
		runs, e := labRunsCatalog(ctx, a, scope, namespace, job, false)
		if e != nil || len(runs) != 0 {
			t.Fatal("Dashboard invented an execution for metadata-only/skipped job")
		}
		original, e := labRunsCatalog(ctx, source, scope, namespace, job, true)
		if e != nil || len(original) != 0 {
			t.Fatal("Control no-execution catalog differs")
		}
	}
	noExecution(api.Scope{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS}, "dashboard-operations", slurm.Jobs["graph-success-only"], false)
	result.SkippedNoExecution++
	// Only the five already-authorized original research fixtures are inspected.
	// Absence of imported-only history is recorded, not repaired by seeding data.
	for _, id := range research.JobIDs {
		scope := api.Scope{DeploymentID: labDeployment, NamespaceID: research.ID}
		var job api.JobDetail
		read(a, labRunsPath(scope, id), &job, 200)
		if job.Job.Scope != scope || job.Job.ID != id {
			t.Fatal("research fixture source identity differs")
		}
		if job.Job.Imported && job.Job.CurrentRun == nil {
			noExecution(scope, research.Name, id, true)
			result.ImportedOnly = "observed_no_runs"
			break
		}
	}
	result.Phase = "source_recheck"
	capability()
	result.Phase = "complete"
	t.Log("PASS: actual accepted single-job run catalog/detail and selected evidence, route-specific denials, no-execution fixtures; retained result states actual run counts")
}

// Guard tests exercise real request/response handling without a Lab connection.
type labRunsRoundTrip func(*http.Request) (*http.Response, error)

func (f labRunsRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestLabRunsReadOnlyTransportAndBounds(t *testing.T) {
	calls, count := 0, 0
	client := &http.Client{Transport: labRunsRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Body != nil || r.URL.Host != "dashboard.lab.test:8443" {
			t.Fatal("run acceptance escaped GET-only origin")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(`{"total":"0"}`))}, nil
	})}
	reader := labRunsReader{labMixedHTTP{client: client, origin: "https://dashboard.lab.test:8443", token: "synthetic"}, &count}
	for _, path := range []string{"https://other.test/api/v1/jobs", "//other.test/api/v1/jobs", "/api/v1/jobs#fragment", "/auth/logout", "/api/v1/jobs\n"} {
		if reader.get(t.Context(), path, new(any), 200) == nil {
			t.Fatal("invalid path admitted")
		}
	}
	if calls != 0 || count != 0 {
		t.Fatal("invalid path reached transport")
	}
	if reader.get(t.Context(), "/api/v1/jobs", new(any), 200) != nil || calls != 1 {
		t.Fatal("valid GET failed")
	}
	count = labRunsMaximumRequests
	if reader.get(t.Context(), "/api/v1/jobs", new(any), 200) == nil || calls != 1 {
		t.Fatal("request bound bypassed")
	}
	count = 0
	client.Transport = labRunsRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", (4<<20)+1)))}, nil
	})
	if reader.get(t.Context(), "/api/v1/jobs", new(any), 200) == nil {
		t.Fatal("oversized response accepted")
	}
}
func TestLabRunsCatalogProvenanceAndOrder(t *testing.T) {
	now := time.Now()
	v := api.JobRun{ID: "81000000-0000-4000-8000-000000000001", Number: "2", Phase: "terminal", DesiredState: "run", CreatedAt: now, UpdatedAt: now}
	if !labRunsValidRun(v) {
		t.Fatal("valid no-execution run rejected")
	}
	for _, change := range []func(*api.JobRun){func(v *api.JobRun) { v.Number = "9223372036854775808" }, func(v *api.JobRun) { v.Number = "01" }, func(v *api.JobRun) { v.ID = "00000000-0000-0000-0000-000000000000" }, func(v *api.JobRun) { v.ExecutionID = v.ID }, func(v *api.JobRun) { v.Backend = "subprocess" }} {
		bad := v
		change(&bad)
		if labRunsValidRun(bad) {
			t.Fatal("incomplete or noncanonical run admitted")
		}
	}
	first, e := labRunsAppend(nil, []api.JobRun{v}, "2", "next")
	if e != nil {
		t.Fatal(e)
	}
	other := v
	other.ID = "81000000-0000-4000-8000-000000000002"
	other.Number = "1"
	if out, e := labRunsAppend(first, []api.JobRun{other}, "2", ""); e != nil || len(out) != 2 {
		t.Fatal("ordered final page rejected")
	}
	for _, tc := range []struct {
		items       []api.JobRun
		total, next string
	}{{[]api.JobRun{v}, "2", ""}, {[]api.JobRun{other}, "21", "next"}, {[]api.JobRun{v}, "2", "next"}, {nil, "2", "next"}} {
		if _, e := labRunsAppend(first, tc.items, tc.total, tc.next); e == nil {
			t.Fatal("false total/order/continuation admitted")
		}
	}
}
func TestLabRunsEvidenceProvenance(t *testing.T) {
	now := time.Now()
	run := api.RunReference{ID: "81000000-0000-4000-8000-000000000001", Number: "1", ExecutionID: "82000000-0000-4000-8000-000000000001"}
	v := api.LogRange{RunID: run.ID, RunNumber: run.Number, ExecutionID: run.ExecutionID, Stream: "stderr", BytesBase64: base64.StdEncoding.EncodeToString([]byte("known")), StartOffset: "0", EndOffset: "5", NextCursor: "opaque", State: "complete", CapturedAt: &now}
	if !labRunsLog(v, run, "stderr", "known", 0) {
		t.Fatal("exact initial log rejected")
	}
	follow := v
	follow.BytesBase64, follow.StartOffset, follow.EndOffset, follow.CapturedAt = "", "5", "5", nil
	if !labRunsLog(follow, run, "stderr", "", 5) {
		t.Fatal("empty terminal follow must not invent a capture time")
	}
	for _, change := range []func(*api.LogRange){func(v *api.LogRange) { v.RunID = "" }, func(v *api.LogRange) { v.RunNumber = "2" }, func(v *api.LogRange) { v.ExecutionID = run.ID }, func(v *api.LogRange) { v.EndOffset = "4" }, func(v *api.LogRange) { v.Truncated = true }} {
		bad := v
		change(&bad)
		if labRunsLog(bad, run, "stderr", "known", 0) {
			t.Fatal("misattributed initial bytes accepted")
		}
	}
	item := api.Artifact{ID: run.ExecutionID + "/result.txt", Name: "result.txt", Checksum: "sha256:" + strings.Repeat("0", 64), RunID: run.ID, RunNumber: "1", ExecutionID: run.ExecutionID, TargetGenerationID: run.ID, SizeBytes: "1", PublishedAt: now, Availability: "metadata_only"}
	selected := api.JobRun{ID: run.ID, Number: "1", ExecutionID: run.ExecutionID, TargetGenerationID: run.ID}
	if !labRunsArtifact(item, selected) {
		t.Fatal("exact artifact rejected")
	}
	selected.ExecutionID = ""
	if labRunsArtifact(item, selected) {
		t.Fatal("artifact fabricated for unassigned run")
	}
}
func TestLabRunsReceiptsNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("test mode")
	}
	canonical, e := filepath.EvalSymlinks(dir)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(canonical, "result.json")
	if !labRunsResultPath(path) || labRunsExclusive(path, map[string]bool{"passed": false}) != nil {
		t.Fatal("new private receipt rejected")
	}
	original, _ := os.ReadFile(path)
	if labRunsExclusive(path, map[string]bool{"passed": true}) == nil {
		t.Fatal("receipt overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(original) != string(after) {
		t.Fatal("failed evidence replaced")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("receipt mode widened")
	}
}

func TestLabRunsExactSanitizedDenialsAndCancellation(t *testing.T) {
	count, calls := 0, 0
	raw := `{"code":"forbidden","message":"You do not currently have permission to view this information.","requestId":"00000000000000000000000000000000"}`
	status := 403
	client := &http.Client{Transport: labRunsRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}
	reader := labRunsReader{labMixedHTTP{client: client, origin: "https://dashboard.lab.test:8443", token: "synthetic"}, &count}
	path := "/api/v1/deployments/" + labDeployment + "/namespaces/" + labMixedOperationsNS + "/jobs/" + labRunsUnknown + "/runs"
	if reader.get(t.Context(), path, nil, 403) != nil {
		t.Fatal("exact403 run denial rejected")
	}
	if reader.get(t.Context(), path, nil, 404) == nil {
		t.Fatal("wrong404 expectation accepted")
	}
	valid := raw
	for _, bad := range []string{strings.Replace(valid, `"message":`, `"message":"private canary","message":`, 1), valid + `{}`, strings.Replace(valid, "currently authorized", "currently authorized /private/path", 1)} {
		raw = bad
		if reader.get(t.Context(), path, nil, 403) == nil {
			t.Fatal("ambiguous or private denial accepted")
		}
	}
	raw = `{"code":"not_found_or_inaccessible","message":"This item could not be found, or you do not have access to it.","requestId":"00000000000000000000000000000000"}`
	status = 404
	if reader.get(t.Context(), path, nil, 404) != nil {
		t.Fatal("exact404 absent run denied incorrectly")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := calls
	if reader.get(ctx, path, new(any), 200) == nil || calls != before+1 {
		t.Fatal("cancellation must fail once without retry")
	}
}

func TestLabRunsSourceQualification(t *testing.T) {
	now := time.Now()
	scope := api.Scope{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS}
	status := api.SourceStatus{Scope: scope, Status: "available", AsOf: &now, FetchedAt: now}
	if !labRunsFresh(scope, []api.SourceStatus{status}, "complete", now) {
		t.Fatal("exact source rejected")
	}
	for _, change := range []func(*api.SourceStatus){func(s *api.SourceStatus) { s.NamespaceID = labRunsUnknown }, func(s *api.SourceStatus) { s.DeploymentID = labSecondaryDeployment }, func(s *api.SourceStatus) { s.Status = "unavailable" }, func(s *api.SourceStatus) { s.AsOf = nil }, func(s *api.SourceStatus) { s.FetchedAt = time.Time{} }} {
		bad := status
		change(&bad)
		if labRunsFresh(scope, []api.SourceStatus{bad}, "complete", now) {
			t.Fatal("unavailable or different source accepted")
		}
	}
	if labRunsFresh(scope, []api.SourceStatus{status}, "partial", now) || labRunsFresh(scope, []api.SourceStatus{status, status}, "complete", now) {
		t.Fatal("partial or duplicate source accepted")
	}
}
