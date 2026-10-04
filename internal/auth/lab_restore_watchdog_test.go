//go:build integration && (darwin || linux)

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

type labWatchdogDriver struct{ script, root, staging, planSHA, implSHA, operation string }

func (d labWatchdogDriver) call(ctx context.Context, phase string, target any) error {
	if !slices.Contains([]string{"begin", "status", "observe", "verify", "close"}, phase) {
		return errors.New("watchdog phase denied")
	}
	bound := 60 * time.Second
	if phase == "begin" {
		bound = 110 * time.Second
	}
	request, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	args := []string{d.script, phase, "--lab-root", d.root, "--staging", d.staging, "--expected-plan-sha256", d.planSHA, "--expected-implementation-sha256", d.implSHA}
	if phase == "begin" || phase == "close" {
		args = append(args, "--apply")
	}
	command := exec.CommandContext(request, "python3", args...)
	stdout, stderr := &labFaultOutput{limit: 128 << 10}, &labFaultOutput{limit: 4096}
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil || stdout.overflow || stderr.overflow || stderr.buffer.Len() != 0 {
		return errors.New("watchdog driver failed; preserve pending timer and receipts")
	}
	if json.Unmarshal(stdout.buffer.Bytes(), target) != nil {
		return errors.New("watchdog receipt invalid")
	}
	return nil
}

type labWatchdogRestart struct {
	PrimaryRestarted bool    `json:"primaryRestarted"`
	WatchdogFired    bool    `json:"watchdogFired"`
	OperationID      string  `json:"operationId"`
	Elapsed          float64 `json:"elapsedSinceArmSeconds"`
	States           map[string]struct {
		Active  bool   `json:"active"`
		PID     string `json:"pid"`
		Started string `json:"startedMonotonic"`
		UnitSHA string `json:"unitSHA256"`
	} `json:"states"`
}
type labWatchdogStatus struct {
	OperationID                      string `json:"operationId"`
	Armed, Stopped, Fired, Restarted bool
	Elapsed                          float64            `json:"elapsedSeconds"`
	Receipt                          labWatchdogRestart `json:"receipt"`
}

func (v labWatchdogStatus) valid(operation string) bool {
	return v.OperationID == operation && v.Armed && v.Stopped && v.Fired && v.Restarted &&
		v.Receipt.OperationID == operation && v.Receipt.PrimaryRestarted && v.Receipt.WatchdogFired &&
		v.Receipt.Elapsed >= 150 && v.Receipt.Elapsed <= 180 && len(v.Receipt.States) == 2 &&
		v.Receipt.States["api"].Active && v.Receipt.States["worker"].Active
}

func labWatchdogLoad(t *testing.T) labWatchdogDriver {
	t.Helper()
	d := labWatchdogDriver{script: os.Getenv("JOBMAN_DASHBOARD_LAB_WATCHDOG_DRIVER"), root: os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT"),
		staging: os.Getenv("JOBMAN_DASHBOARD_LAB_WATCHDOG_STAGING"), planSHA: os.Getenv("JOBMAN_DASHBOARD_LAB_WATCHDOG_PLAN_SHA256"), implSHA: os.Getenv("JOBMAN_DASHBOARD_LAB_WATCHDOG_IMPLEMENTATION_SHA256")}
	raw, err := labRestorePrivate(filepath.Join(d.staging, "plan.json"), 2<<20)
	if err != nil {
		t.Fatal("private reviewed watchdog plan unavailable")
	}
	sum := sha256.Sum256(raw)
	var plan struct {
		Scenario, OperationID string
		Synthetic             bool
		Implementation        map[string]string `json:"implementationSHA256"`
	}
	if json.Unmarshal(raw, &plan) != nil || hex.EncodeToString(sum[:]) != d.planSHA || plan.Scenario != "primary-restore-watchdog-intervention" || !plan.Synthetic || len(plan.OperationID) != 64 || len(plan.Implementation) != 7 {
		t.Fatal("watchdog plan identity differs")
	}
	d.operation = plan.OperationID
	for name, expected := range plan.Implementation {
		if !slices.Contains([]string{"dashboard-watchdog-plan.py", "dashboard-watchdog-guest.py", "dashboard-watchdog.py", "dashboard-restore-guest.py", "dashboard-dependency-fault-plan.py", "dashboard-dependency-fault-guest.py", "dashboard-dependency-faults.py"}, name) {
			t.Fatal("watchdog dependency outside reviewed scope")
		}
		content, e := labScaleReadFile(filepath.Join(filepath.Dir(d.script), name), 256<<10)
		digest := sha256.Sum256(content)
		if e != nil || hex.EncodeToString(digest[:]) != expected {
			t.Fatal("watchdog implementation bytes differ")
		}
	}
	if filepath.Base(d.script) != "dashboard-watchdog.py" || !filepath.IsAbs(d.script) || !filepath.IsAbs(d.root) {
		t.Fatal("fixed watchdog driver and Lab paths required")
	}
	return d
}

// Only bounded, sanitized HTTP503 can be retried during read recovery. No
// service action, sign-in, rule change or source mutation is performed here.
func labWatchdogRead(ctx context.Context, client *http.Client, token, path string, want int, target any, recovery bool) error {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for attempt := 0; attempt < 16; attempt++ {
		value, err := labWebExchange(bounded, client, "GET", labWebOrigin+path, http.Header{"Authorization": {"Bearer " + token}}, nil)
		if err != nil {
			return errors.New("watchdog HTTP transport failed")
		}
		if value.header.Get("Cache-Control") != "no-store" || len(value.header.Values("Set-Cookie")) != 0 {
			return errors.New("watchdog response privacy contract changed")
		}
		if value.status == want {
			if want >= 400 {
				problem, ok := labFaultDecodeError(value.body)
				id, idErr := hex.DecodeString(problem.RequestID)
				if !ok || want != 404 || problem.Code != "not_found_or_inaccessible" || problem.Message != "The resource is absent or inaccessible." || idErr != nil || len(id) != 16 || hex.EncodeToString(id) != problem.RequestID {
					return errors.New("watchdog denial contract changed")
				}
			} else if target != nil && json.Unmarshal(value.body, target) != nil {
				return errors.New("watchdog response shape changed")
			}
			return nil
		}
		problem, ok := labFaultDecodeError(value.body)
		if !recovery || value.status != 503 || !ok || !slices.Contains([]string{"source_unavailable", "authorization_unavailable"}, problem.Code) || !labFaultError(problem, 503, problem.Code) {
			return errors.New("watchdog HTTP status or safe error changed")
		}
		if labMixedWait(bounded, time.Second) != nil {
			return errors.New("watchdog authorized read did not recover")
		}
	}
	return errors.New("watchdog recovery attempt bound")
}

// This test intentionally has NO restart cleanup. A lost response or failure
// leaves the acknowledged independent timer and private evidence in place.
func TestLabRestoreWatchdogIntervention(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RESTORE_WATCHDOG") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" {
		t.Skip("explicit reviewed watchdog-only plan required")
	}
	deadline, ok := t.Deadline()
	if !ok || time.Until(deadline) > 7*time.Minute || time.Until(deadline) < 6*time.Minute {
		t.Fatal("watchdog acceptance requires separate -timeout=7m")
	}
	driver := labWatchdogLoad(t)
	output := os.Getenv("JOBMAN_DASHBOARD_LAB_WATCHDOG_RESULT")
	if !filepath.IsAbs(output) || !strings.HasPrefix(output, "/private/tmp/") || filepath.Dir(output) != driver.staging {
		t.Fatal("fresh private watchdog evidence path required")
	}
	result, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("watchdog result already exists or unavailable")
	}
	if result.Chmod(0600) != nil {
		t.Fatal("watchdog evidence mode failed")
	}
	evidence := struct {
		Operation   string              `json:"operationId"`
		Passed      bool                `json:"passed"`
		TimerOnly   bool                `json:"timerOnly"`
		Stage       string              `json:"stage"`
		Receipt     *labWatchdogRestart `json:"receipt,omitempty"`
		ReportSHA   string              `json:"reportSHA256,omitempty"`
		CitationSHA string              `json:"citationSHA256,omitempty"`
		LogSHA      string              `json:"logSHA256,omitempty"`
	}{Operation: driver.operation, TimerOnly: true, Stage: "setup"}
	t.Cleanup(func() {
		raw, e := json.MarshalIndent(evidence, "", "  ")
		if e == nil {
			_, e = result.Write(append(raw, '\n'))
		}
		if e == nil {
			e = result.Sync()
		}
		e = errors.Join(e, result.Close())
		if e != nil {
			t.Error("watchdog result could not be retained")
		}
	})
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	sessions := []labNativeSession{alice, bob}
	clients := []labNotificationHTTP{newLabNotificationHTTP(t, alice), newLabNotificationHTTP(t, bob)}
	ctx, cancel := context.WithDeadline(t.Context(), deadline.Add(-10*time.Second))
	defer cancel()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	read := func(who int, path string, want int, target any, recovery bool) {
		t.Helper()
		must(labWatchdogRead(ctx, clients[who].client, sessions[who].accessToken, path, want, target, recovery))
	}
	var before [2]api.Bootstrap
	for who := range sessions {
		read(who, "/api/v1/bootstrap", 200, &before[who], false)
		if before[who].FixtureMode || before[who].Completeness != "complete" || len(before[who].Deployments) != 2 {
			t.Fatal("complete actual two-source authorization required")
		}
	}
	raw, err := labScaleReadFile(os.Getenv("JOBMAN_DASHBOARD_LAB_WATCHDOG_SLURM_RECEIPT"), 32768)
	must(err)
	digest := sha256.Sum256(raw)
	var slurm labMixedSlurm
	if hex.EncodeToString(digest[:]) != labAcceptedCurrentSlurmReceiptSHA || json.Unmarshal(raw, &slurm) != nil || !slurm.valid() {
		t.Fatal("exact accepted current report receipt required")
	}
	jobPath := labMultiPrefix(api.Scope{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS}) + "/jobs/" + slurm.Jobs["task-2"]
	var job api.JobDetail
	read(0, jobPath, 200, &job, false)
	run, err := slurm.pinnedRun(job)
	must(err)
	reportPath := jobPath + "/reports/" + slurm.Reports["include_log_tail"]
	var report api.Report
	read(0, reportPath, 200, &report, false)
	if report.State != "ready" || report.Detail == nil || report.Outdated || report.RunID != run.ID || report.Detail.ControlInstanceID != labRestoreInstance || report.Detail.Disclosure.ProviderInvoked || len(report.Detail.Citations) == 0 {
		t.Fatal("original sealed report unavailable")
	}
	citationPath := reportPath + "/citations/" + url.PathEscape(report.Detail.Citations[0].ID)
	var citation api.Citation
	read(0, citationPath, 200, &citation, false)
	if citation.ReportID != report.ReportID || citation.EvidenceID != report.EvidenceID || citation.TaskID != report.TaskID {
		t.Fatal("original citation identity differs")
	}
	var log api.LogRange
	read(0, jobPath+"/logs?stream=stderr", 200, &log, false)
	var logReader labMixedLog
	must(logReader.accept(log, labSlurmFailureLog, run))
	evidence.ReportSHA = labRestoreDigest(report)
	evidence.CitationSHA = labRestoreDigest(citation)
	evidence.LogSHA = labRestoreDigest(log.BytesBase64)
	fixtures := []labMultiFixture{labReadMultiFixture(t, filepath.Join(alice.root, ".lab/dashboard/fixture-info.json"), false), labReadMultiFixture(t, os.Getenv("JOBMAN_DASHBOARD_LAB_SECONDARY_FIXTURE"), true)}
	var research [2]api.Scope
	var researchPaths [2]string
	var inboxes [2]notifications.InboxItem
	for i, fixture := range fixtures {
		for _, ns := range fixture.Namespaces {
			if ns.Name == "dashboard-research" {
				dep := labDeployment
				if i == 1 {
					dep = labSecondaryDeployment
				}
				research[i] = api.Scope{DeploymentID: dep, NamespaceID: ns.ID}
				researchPaths[i] = labMultiPrefix(research[i]) + "/jobs/" + ns.JobIDs[0]
			}
		}
	}
	for i := range sessions {
		for _, path := range researchPaths {
			read(i, path, 200, nil, false)
		}
		var page notifications.InboxPage
		read(i, "/api/v1/inbox?"+labMultiQuery([]api.Scope{research[i]}, 20, ""), 200, &page, false)
		if page.Completeness != "complete" || len(page.Items) == 0 || !uuid(page.Items[0].ID) || page.Items[0].Job.DeploymentID != research[i].DeploymentID || page.Items[0].Job.NamespaceID != research[i].NamespaceID {
			t.Fatal("existing source-qualified owner inbox required")
		}
		inboxes[i] = page.Items[0]
		read(1-i, "/api/v1/inbox/"+inboxes[i].ID, 404, nil, false)
	}
	read(1, reportPath, 404, nil, false)
	read(1, jobPath+"/logs?stream=stderr", 404, nil, false)
	if time.Until(deadline) < 4*time.Minute {
		t.Fatal("insufficient timer and read-recovery budget; no stop attempted")
	}
	evidence.Stage = "begin_intent"
	var began struct {
		Operation   string `json:"operationId"`
		Stopped     bool   `json:"stopped"`
		Seconds     int    `json:"watchdogSeconds"`
		HostRestart bool   `json:"hostRestartAllowed"`
	}
	started := time.Now()
	must(driver.call(ctx, "begin", &began))
	if began.Operation != driver.operation || !began.Stopped || began.Seconds != 150 || began.HostRestart {
		t.Fatal("real stop/watchdog acknowledgement invalid")
	}
	evidence.Stage = "waiting_for_timer"
	var status labWatchdogStatus
	for time.Since(started) < 195*time.Second {
		status = labWatchdogStatus{}
		must(driver.call(ctx, "status", &status))
		if status.Restarted {
			break
		}
		must(labMixedWait(ctx, 2*time.Second))
	}
	if !status.valid(driver.operation) {
		t.Fatal("independent timer intervention did not produce exact restart receipts")
	}
	evidence.Receipt = &status.Receipt
	evidence.Stage = "same_credentials_recovery"
	var observed json.RawMessage
	must(driver.call(ctx, "verify", &observed))
	for i := range sessions {
		var after api.Bootstrap
		read(i, "/api/v1/bootstrap", 200, &after, true)
		if before[i].Account != after.Account || !labFaultGrantsSame(before[i], after) {
			t.Fatal("same credentials lost current source-qualified authority")
		}
		for _, path := range researchPaths {
			read(i, path, 200, nil, true)
		}
		var item notifications.InboxItem
		read(i, "/api/v1/inbox/"+inboxes[i].ID, 200, &item, true)
		if !reflect.DeepEqual(item, inboxes[i]) {
			t.Fatal("retained owner inbox changed")
		}
		read(1-i, "/api/v1/inbox/"+item.ID, 404, nil, false)
	}
	var afterJob api.JobDetail
	read(0, jobPath, 200, &afterJob, true)
	afterRun, err := slurm.pinnedRun(afterJob)
	must(err)
	if afterRun != run || afterJob.Job.Revision != job.Job.Revision {
		t.Fatal("accepted source run changed")
	}
	var afterReport api.Report
	read(0, reportPath, 200, &afterReport, true)
	var afterCitation api.Citation
	read(0, citationPath, 200, &afterCitation, true)
	if labRestoreDigest(afterReport) != evidence.ReportSHA || labRestoreDigest(afterCitation) != evidence.CitationSHA {
		t.Fatal("original report seal or citation changed")
	}
	var afterLog api.LogRange
	read(0, jobPath+"/logs?stream=stderr", 200, &afterLog, true)
	var fresh labMixedLog
	must(fresh.accept(afterLog, labSlurmFailureLog, run))
	if labRestoreDigest(afterLog.BytesBase64) != evidence.LogSHA {
		t.Fatal("original NFS bytes changed")
	}
	read(1, reportPath, 404, nil, false)
	read(1, jobPath+"/logs?stream=stderr", 404, nil, false)
	must(driver.call(ctx, "close", &observed))
	evidence.Passed = true
	evidence.Stage = "accepted"
	t.Log("PASS: original 150-second guest timer restarted only the pinned API/worker; same native credentials, two-source authority, inbox owners, sealed report/citation and NFS bytes preserved. No restore or APNs delivery exercised.")
}

func TestLabWatchdogReceiptGuards(t *testing.T) {
	v := labWatchdogStatus{OperationID: "op", Armed: true, Stopped: true, Fired: true, Restarted: true, Receipt: labWatchdogRestart{OperationID: "op", PrimaryRestarted: true, WatchdogFired: true, Elapsed: 151}}
	v.Receipt.States = map[string]struct {
		Active  bool   `json:"active"`
		PID     string `json:"pid"`
		Started string `json:"startedMonotonic"`
		UnitSHA string `json:"unitSHA256"`
	}{"api": {Active: true}, "worker": {Active: true}}
	if !v.valid("op") {
		t.Fatal("valid timer receipt rejected")
	}
	for _, change := range []func(*labWatchdogStatus){func(v *labWatchdogStatus) { v.Fired = false }, func(v *labWatchdogStatus) { v.Stopped = false }, func(v *labWatchdogStatus) { v.Receipt.Elapsed = 149 }, func(v *labWatchdogStatus) { v.Receipt.OperationID = "other" }} {
		bad := v
		change(&bad)
		if bad.valid("op") {
			t.Fatal("incomplete or unrelated timer receipt accepted")
		}
	}
	for _, phase := range []string{"restart", "recover", "resume", "restore"} {
		if (labWatchdogDriver{}).call(context.Background(), phase, nil) == nil {
			t.Fatal("host restart/mutation route admitted")
		}
	}
}

func TestLabWatchdogReadRecovery(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: labMixedRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer retained-test-token" {
			t.Fatal("same read credential not preserved")
		}
		status, body := 200, `{"ok":true}`
		if calls == 1 {
			status = 503
			body = `{"code":"source_unavailable","message":"The source is unavailable. Retry when private connectivity is restored.","requestId":"11111111111111111111111111111111"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	var target struct {
		OK bool `json:"ok"`
	}
	if err := labWatchdogRead(t.Context(), client, "retained-test-token", "/api/v1/bootstrap", 200, &target, true); err != nil || calls != 2 || !target.OK {
		t.Fatalf("bounded same-token recovery failed (calls=%d, failed=%t)", calls, err != nil)
	}
}

func TestLabWatchdogReadRejectsUnsafeFailure(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{401, `{"code":"unauthenticated","message":"Sign in to continue.","requestId":"11111111111111111111111111111111"}`},
		{503, `{"code":"source_unavailable","message":"private DSN secret","requestId":"11111111111111111111111111111111"}`},
		{503, `{"code":"source_unavailable","code":"authorization_unavailable","message":"Current source authorization could not be verified.","requestId":"11111111111111111111111111111111"}`},
	} {
		calls := 0
		client := &http.Client{Transport: labMixedRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		err := labWatchdogRead(t.Context(), client, "token", "/api/v1/bootstrap", 200, nil, true)
		if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe failure was retried or disclosed")
		}
	}
}

func TestLabWatchdogOwnerDenial(t *testing.T) {
	client := &http.Client{Transport: labMixedRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(`{"code":"not_found_or_inaccessible","message":"The resource is absent or inaccessible.","requestId":"11111111111111111111111111111111"}`))}, nil
	})}
	if labWatchdogRead(t.Context(), client, "bob", "/api/v1/inbox/11111111-1111-4111-8111-111111111111", 404, nil, false) != nil {
		t.Fatal("expected current-owner denial rejected")
	}
}
