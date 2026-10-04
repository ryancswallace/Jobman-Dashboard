//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// This test changes only the reviewed split worker's service state and creates
// two report requests for an already executed synthetic job. It never changes
// source jobs, grants, cursors, delivery holds or configuration. A cleanup starts
// the same worker even after an assertion failure; uncertain state is retained.
func TestLabSplitReportsSurviveWorkerRestart(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_SPLIT_RESTART") != "1" {
		t.Skip("explicit reviewed split worker restart opt-in required")
	}
	revision := os.Getenv("JOBMAN_DASHBOARD_LAB_SPLIT_COMMIT")
	path := os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_RECEIPT")
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) || !filepath.IsAbs(path) {
		t.Fatal("exact split revision and prior actual-execution receipt required")
	}
	var prior struct {
		Synthetic       bool
		ObservationMode string
		Fixture         labActualReceipt
		Jobs, Reports   map[string]string
	}
	if json.Unmarshal(labExecutionFile(t, path, 65536), &prior) != nil || !prior.Synthetic || prior.ObservationMode != "actual-subprocess-agent-execution" || prior.Fixture.DeploymentID != labDeployment || prior.Fixture.Namespace != "dashboard-operations" {
		t.Fatal("actual execution receipt differs")
	}
	for _, id := range []string{prior.Fixture.NamespaceID, prior.Fixture.ControlInstanceID, prior.Jobs["failure"], prior.Jobs["collection-failure"], prior.Reports["metadata"], prior.Reports["include_log_tail"]} {
		if !labExecutionID(id) {
			t.Fatal("actual execution receipt lacks immutable identities")
		}
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	request := labReportClient(t, ctx, alice)
	control := labExecutionControl(t, ctx, alice)
	var job labActualJob
	control("GET", "/v1/namespaces/dashboard-operations/jobs/"+prior.Jobs["failure"], "", nil, &job)
	labActualAssertJob(t, prior.Fixture, job, "failure", true)
	oldPrefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + prior.Fixture.NamespaceID + "/jobs/" + job.Metadata.ID
	oldReports := map[string]api.Report{}
	for _, profile := range []string{"metadata", "include_log_tail"} {
		var report api.Report
		raw := request("GET", oldPrefix+"/reports/"+prior.Reports[profile], alice.accessToken, "", nil, &report, 200)
		if bytes.Contains(raw, []byte(labDiagnosticCanary)) || report.State != "ready" || report.Detail == nil || report.Detail.ControlInstanceID != prior.Fixture.ControlInstanceID || report.RunID != job.Status.CurrentRun.ID {
			t.Fatal("pre-split report cannot be read through converted shared storage")
		}
		oldReports[profile] = report
		request("GET", oldPrefix+"/reports/"+report.TaskID, bob.accessToken, "", nil, nil, 404)
	}
	// Report equivalence uses the diagnosis dependency version, not the
	// Dashboard build. Request another already executed job, preserving the old
	// reports and their idempotency bindings without manufacturing new evidence.
	job = labActualJob{}
	control("GET", "/v1/namespaces/dashboard-operations/jobs/"+prior.Jobs["collection-failure"], "", nil, &job)
	labActualAssertJob(t, prior.Fixture, job, "failure", true)
	prefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + prior.Fixture.NamespaceID + "/jobs/" + job.Metadata.ID
	var catalog api.ReportPage
	request("GET", prefix+"/reports", alice.accessToken, "", nil, &catalog, 200)
	if len(catalog.Items) != 0 || catalog.NextCursor != "" {
		t.Fatal("restart scenario job already has reports; preserve prior evidence")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		if err := labSplitWorker(cleanup, alice.root, revision, "start"); err != nil {
			t.Error("reviewed split worker cleanup start failed; operator inspection required")
		}
	})
	if err := labSplitWorker(ctx, alice.root, revision, "stop"); err != nil {
		t.Fatal("reviewed split worker stop failed; inspect retained service state")
	}
	queued := map[string]api.Report{}
	for _, profile := range []string{"metadata", "include_log_tail"} {
		var report api.Report
		key := "split-" + revision + "-" + job.Metadata.ID + "-" + profile
		body := api.ReportRequest{Profile: profile, RunID: job.Status.CurrentRun.ID}
		request("POST", prefix+"/reports", alice.accessToken, key, body, &report, 202)
		if report.State != "queued" || !labExecutionID(report.TaskID) || report.TaskID == oldReports[profile].TaskID {
			t.Fatalf("new candidate report was not queued with worker stopped (state %q); preserve prior scenario evidence", report.State)
		}
		queued[profile] = report
		var retry api.Report
		request("POST", prefix+"/reports", alice.accessToken, key, body, &retry, 202)
		if retry.TaskID != report.TaskID || retry.State != "queued" {
			t.Fatal("stopped-worker report retry changed durable intent")
		}
	}
	// API mode must continue serving source-authorized original NFS bytes while
	// the worker is stopped; it must not claim and execute report work itself.
	var logs api.LogRange
	request("GET", prefix+"/logs?stream=stderr", alice.accessToken, "", nil, &logs, 200)
	rawLog, err := base64.StdEncoding.DecodeString(logs.BytesBase64)
	if err != nil || string(rawLog) != labActualFailureLog || logs.State != "complete" {
		t.Fatal("split interactive broker read changed original producer bytes")
	}
	for _, report := range queued {
		var still api.Report
		request("GET", prefix+"/reports/"+report.TaskID, alice.accessToken, "", nil, &still, 200)
		if still.State != "queued" {
			t.Fatal("API processed report work without the worker")
		}
	}
	if err := labSplitWorker(ctx, alice.root, revision, "start"); err != nil {
		t.Fatal("reviewed split worker restart failed; preserve queued intent")
	}
	for profile, admitted := range queued {
		var ready api.Report
		taskPath := prefix + "/reports/" + admitted.TaskID
		labActualPoll(t, ctx, 90*time.Second, func() bool {
			ready = api.Report{}
			raw := request("GET", taskPath, alice.accessToken, "", nil, &ready, 200)
			if bytes.Contains(raw, []byte(labDiagnosticCanary)) || ready.State == "failed" {
				t.Fatalf("split report failed or leaked a redaction canary; safe code %q", ready.FailureCode)
			}
			return ready.State == "ready"
		})
		if ready.TaskID != admitted.TaskID || ready.Detail == nil || ready.Outdated || ready.Detail.ControlInstanceID != prior.Fixture.ControlInstanceID || ready.RunID != job.Status.CurrentRun.ID || ready.Detail.Disclosure.ProviderInvoked || ready.Detail.Disclosure.GeneratedContentUsed {
			t.Fatal("restarted worker did not complete original deterministic report intent")
		}
		logCitations := 0
		for _, ref := range ready.Detail.Citations {
			var citation api.Citation
			raw := request("GET", taskPath+"/citations/"+url.PathEscape(ref.ID), alice.accessToken, "", nil, &citation, 200)
			if bytes.Contains(raw, []byte(labDiagnosticCanary)) || citation.ID != ref.ID || citation.ReportID != ready.ReportID || citation.EvidenceID != ready.EvidenceID {
				t.Fatal("split report citation identity or redaction differs")
			}
			if citation.BytesBase64 != nil {
				logCitations++
			}
		}
		if profile == "include_log_tail" && logCitations == 0 {
			t.Fatal("split worker diagnosis did not use authorized broker log evidence")
		}
		request("GET", taskPath, bob.accessToken, "", nil, nil, 404)
		var retained api.Report
		request("GET", oldPrefix+"/reports/"+oldReports[profile].TaskID, alice.accessToken, "", nil, &retained, 200)
		before, _ := json.Marshal(oldReports[profile].Detail)
		after, _ := json.Marshal(retained.Detail)
		if retained.ReportID != oldReports[profile].ReportID || retained.EvidenceID != oldReports[profile].EvidenceID || !bytes.Equal(before, after) {
			t.Fatal("new worker changed a previously sealed report")
		}
	}
	t.Logf("PASS: candidate %s API retained original reports/logs, queued two durable requests while worker stopped, and completed both through restarted worker with broker evidence and cross-namespace denial; hold/config/source unchanged", revision)
}

func labSplitWorker(ctx context.Context, root, revision, action string) error {
	if action != "stop" && action != "start" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) {
		return fmt.Errorf("invalid split worker action")
	}
	var hosts map[string]struct {
		Host string `json:"ansible_host"`
		User string `json:"ansible_user"`
		Port int    `json:"ansible_port"`
		Key  string `json:"ansible_ssh_private_key_file"`
	}
	raw, err := os.ReadFile(filepath.Join(root, ".lab/dashboard/ssh-connections.json"))
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &hosts) != nil {
		return fmt.Errorf("private pinned Lab inventory unavailable")
	}
	host := hosts["storage01"]
	if host.User != "vagrant" || host.Host == "" || host.Port < 1 || host.Port > 65535 || !filepath.IsAbs(host.Key) {
		return fmt.Errorf("invalid storage fixture identity")
	}
	const script = `import json,os,subprocess,sys
from pathlib import Path
p=json.load(sys.stdin);assert os.getuid()==0 and p['action'] in ('start','stop')
expected='/opt/jobman-dashboard-lab/releases/'+p['revision']+'/bin/jobman-dashboard'
api='jobman-dashboard-lab-api';worker='jobman-dashboard-lab-worker'
def active(unit):return subprocess.run(['systemctl','is-active','--quiet',unit],capture_output=True,timeout=10).returncode==0
def pinned(unit,uid):
 pid=subprocess.check_output(['systemctl','show',unit,'--property=MainPID','--value'],text=True,timeout=5).strip()
 assert pid.isdigit() and int(pid)>0 and os.stat('/proc/'+pid).st_uid==uid and os.readlink('/proc/'+pid+'/exe')==expected
assert active(api) and not active('jobman-dashboard-lab-app')
pinned(api,21904)
for role in ('api','worker'):
 unit=Path('/etc/systemd/system/jobman-dashboard-lab-'+role+'.service').read_text()
 assert ('ExecStart='+expected+' --mode '+role+' --config /etc/jobman-dashboard-'+role+'-lab/config.json') in unit
if active(worker):
 pinned(worker,21905)
subprocess.run(['systemctl',p['action'],worker],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=30)
assert active(api) and active(worker)==(p['action']=='start')
pinned(api,21904)
if p['action']=='start':pinned(worker,21905)
print('verified-split-worker-'+p['action'])
`
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("SSH unavailable")
	}
	quoted := "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"
	command := exec.CommandContext(ctx, ssh, "-i", host.Key, "-p", fmt.Sprint(host.Port), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+filepath.Join(root, ".lab/dashboard/known_hosts"), "-o", "HostKeyAlgorithms=ssh-ed25519", host.User+"@"+host.Host, "sudo python3 -c "+quoted)
	payload, _ := json.Marshal(map[string]string{"action": action, "revision": revision})
	command.Stdin = bytes.NewReader(payload)
	command.Stderr = io.Discard
	var output labExecutionOutput
	command.Stdout = &output
	if command.Run() != nil || strings.TrimSpace(string(output.Bytes())) != "verified-split-worker-"+action {
		return fmt.Errorf("scoped split worker transition failed")
	}
	return nil
}
