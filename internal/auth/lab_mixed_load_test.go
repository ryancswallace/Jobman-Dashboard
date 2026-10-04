//go:build integration && (darwin || linux)

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman/protocol"
)

const labMixedRounds = 18
const labMixedPeriod = 5 * time.Second
const labMixedInflight = 28
const labMixedOperationsNS = "87ece4c0-b3e0-4357-8af8-209582bf3066"

// This is mixed contention coverage: 23 scale readers plus two investigation
// accounts. It does not replace the separate 25-scale-viewer acceptance test.
// No live phase runs without the exact explicit opt-in below. All new execution
// requests have fixed keys; failed attempts are never hidden with replacement keys.
type labMixedLimiter struct {
	slots chan struct{}
	live  atomic.Int32
	peak  atomic.Int32
}

func (g *labMixedLimiter) acquire(ctx context.Context) (func(), error) {
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, errors.New("mixed request cancelled before admission")
	}
	if ctx.Err() != nil {
		<-g.slots
		return nil, errors.New("mixed request cancelled before transport")
	}
	live := g.live.Add(1)
	for peak := g.peak.Load(); live > peak && !g.peak.CompareAndSwap(peak, live); peak = g.peak.Load() {
	}
	return func() { g.live.Add(-1); <-g.slots }, nil
}

type labMixedHTTP struct {
	client *http.Client
	token  string
	origin string
	gate   *labMixedLimiter
}

// One limiter includes source admission/polling and every Dashboard operation.
// No redirects, proxy, remote response text, or bearer enters a result/receipt.
func (c labMixedHTTP) call(ctx context.Context, method, path, key, revision string, input, output any, want ...int) (int, error) {
	if !strings.HasPrefix(path, "/api/v1/") && !(c.origin == "https://10.77.0.21:18443" && strings.HasPrefix(path, "/v1/")) || strings.ContainsAny(path, "\r\n#") {
		return 0, errors.New("mixed request endpoint invalid")
	}
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil || len(body) > 64<<10 {
			return 0, errors.New("mixed request exceeds body bound")
		}
	}
	release, err := c.gate.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	request, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("mixed request construction failed")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if revision != "" {
		request.Header.Set("If-Match", `"`+revision+`"`)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, errors.New("mixed HTTPS transport failed or cancelled")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return len(raw), errors.New("mixed response exceeded bound or was incomplete")
	}
	if !slices.Contains(want, response.StatusCode) {
		return len(raw), fmt.Errorf("mixed request returned HTTP%d", response.StatusCode)
	}
	if strings.Contains(c.origin, "dashboard.lab.test") && response.StatusCode >= 200 && response.StatusCode < 300 && response.Header.Get("Cache-Control") != "no-store" {
		return len(raw), errors.New("mixed Dashboard response permits caching")
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return len(raw), errors.New("mixed response contract invalid")
	}
	return len(raw), nil
}

func labMixedClient(t *testing.T, session labNativeSession, gate *labMixedLimiter, source bool) labMixedHTTP {
	t.Helper()
	transport := session.transport.Clone()
	transport.Proxy = nil
	origin, authority, socket := "https://dashboard.lab.test:8443", "dashboard.lab.test:8443", "10.77.0.10:8443"
	if source {
		origin, authority, socket = "https://10.77.0.21:18443", "10.77.0.21:18443", "10.77.0.21:18443"
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(labExecutionFile(t, filepath.Join(session.root, ".lab/dashboard/runtime/control/fixture-ca.crt"), 65536)) {
			t.Fatal("mixed source CA unavailable")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	transport.MaxConnsPerHost = 4
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != authority {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, socket)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return labMixedHTTP{client: &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: session.accessToken, origin: origin, gate: gate}
}

type labMixedCompletion struct {
	Index            int       `json:"index"`
	ExitCode         int       `json:"exitCode"`
	ExecutionID      string    `json:"executionId"`
	Outcome          string    `json:"outcome"`
	ObservedAt       time.Time `json:"observedAt"`
	CompletionSHA256 string    `json:"completionSHA256"`
}

type labMixedSlurm struct {
	Synthetic          bool              `json:"synthetic"`
	ObservationMode    string            `json:"observationMode"`
	Fixture            labActualReceipt  `json:"fixture"`
	Jobs               map[string]string `json:"jobs"`
	ArrayID            string            `json:"arrayId"`
	GraphID            string            `json:"graphId"`
	Reports            map[string]string `json:"reports"`
	CoreRepairRevision string            `json:"coreRepairRevision"`
	VerifiedAt         time.Time         `json:"verifiedAt"`
	RunnerCompletions  struct {
		Executions []labMixedCompletion `json:"executions"`
	} `json:"runnerCompletions"`
}

func (s labMixedSlurm) valid() bool {
	if !s.Synthetic || s.ObservationMode != "actual-slurm-agent-execution" || s.Fixture.NamespaceID != labMixedOperationsNS || s.Fixture.ControlInstanceID != labRestoreInstance || s.Fixture.DeploymentID != labDeployment || s.Fixture.RecoveryEpoch != "1" || s.CoreRepairRevision != labSlurmRepairRevision || !uuid(s.ArrayID) || !uuid(s.GraphID) || s.VerifiedAt.IsZero() || len(s.Jobs) != 9 || len(s.Reports) != 2 {
		return false
	}
	for _, name := range []string{"task-0", "task-1", "task-2", "task-3", "task-4", "graph-root-failure", "graph-on-failure", "graph-after-terminal", "graph-success-only"} {
		if !uuid(s.Jobs[name]) {
			return false
		}
	}
	return uuid(s.Reports["metadata"]) && uuid(s.Reports["include_log_tail"]) && s.failureExecution() != ""
}

// This receipt was independently accepted and its complete file hash is pinned
// before use. Never adopt whichever execution happens to answer the first read.
func (s labMixedSlurm) failureExecution() string {
	if len(s.RunnerCompletions.Executions) != 5 {
		return ""
	}
	seen := map[string]bool{}
	for index, item := range s.RunnerCompletions.Executions {
		exit, outcome := 0, "success"
		if index == 2 {
			exit, outcome = 7, "failure"
		}
		hash, err := hex.DecodeString(item.CompletionSHA256)
		if item.Index != index || item.ExitCode != exit || item.Outcome != outcome || !uuid(item.ExecutionID) || seen[item.ExecutionID] || item.ObservedAt.IsZero() || err != nil || len(hash) != 32 {
			return ""
		}
		seen[item.ExecutionID] = true
	}
	return s.RunnerCompletions.Executions[2].ExecutionID
}

func (s labMixedSlurm) pinnedRun(detail api.JobDetail) (api.RunReference, error) {
	job := detail.Job
	execution := s.failureExecution()
	if execution == "" || detail.FetchedAt.IsZero() || job.Scope != (api.Scope{DeploymentID: s.Fixture.DeploymentID, NamespaceID: s.Fixture.NamespaceID}) || job.ID != s.Jobs["task-2"] || job.TargetID != s.Fixture.TargetID || job.TargetGenerationID != s.Fixture.TargetGenerationID || job.Owner == nil || !job.Owner.IsCurrentUser || !uuid(job.Owner.ID) || job.Imported || job.Backend != "slurm" || job.Phase != "terminal" || job.Outcome != "failure" || job.CompletedAt == nil || job.CurrentRun == nil || !uuid(job.CurrentRun.ID) || job.CurrentRun.Number != "1" || job.CurrentRun.ExecutionID != execution {
		return api.RunReference{}, errors.New("mixed current Slurm job differs from accepted execution and owner")
	}
	return *job.CurrentRun, nil
}

func labMixedRequests(mode string) (protocol.CollectionRequest, error) {
	if !slices.Contains([]string{"one-control", "two-controls"}, mode) {
		return protocol.CollectionRequest{}, errors.New("fixed mixed mode required")
	}
	items := make([]protocol.CollectionItem, 0, 8)
	for i := range 8 {
		exit := "0"
		if i == 2 || i == 6 {
			exit = "7"
		}
		command := "for n in 1 2 3 4 5 6; do printf 'MIXED HOST TASK " + strconv.Itoa(i) + " LINE %s\\n' \"$n\"; sleep 1; done; exit " + exit
		sealed, err := protocol.SealWorkload(protocol.Workload{APIVersion: protocol.V1Alpha1, Kind: protocol.WorkloadKind, Metadata: protocol.WorkloadMetadata{Name: "dashboard-mixed-" + strconv.Itoa(i)}, Spec: protocol.WorkloadSpec{Command: protocol.Command{Executable: "/bin/sh", Args: []string{"-c", command}}, WorkingDirectory: "workspace:/", Runtime: protocol.Runtime{Kind: "native"}, Policy: protocol.ExecutionPolicy{RunTimeout: "30s", Retry: protocol.RetryPolicy{MaxRuns: 1}, DuplicateRisk: "reject"}}})
		if err != nil {
			return protocol.CollectionRequest{}, errors.New("mixed workload sealing failed")
		}
		items = append(items, protocol.CollectionItem{Name: "task-" + strconv.Itoa(i), Workload: protocol.WorkloadBinding{Digest: sealed.Digest, Document: sealed.Document}, Placement: protocol.Placement{Target: "dashboard-execution-host"}})
	}
	sealed, err := protocol.SealCollectionRequest(protocol.CollectionRequest{APIVersion: protocol.V1Alpha1, Kind: protocol.CollectionRequestKind, Metadata: protocol.CollectionRequestMetadata{Namespace: "dashboard-operations", Name: "dashboard-mixed-v1-" + mode}, Spec: protocol.CollectionRequestSpec{MaxActive: 2, ArrayPolicy: "never", FailurePolicy: "continue", Items: items}})
	if err != nil {
		return protocol.CollectionRequest{}, errors.New("mixed collection sealing failed")
	}
	return sealed.Document, nil
}

type labMixedMode struct {
	Mode         string                       `json:"mode"`
	StartedAt    time.Time                    `json:"startedAt"`
	CompletedAt  time.Time                    `json:"completedAt"`
	RuleID       string                       `json:"ruleId,omitempty"`
	RuleStopped  bool                         `json:"ruleStopped"`
	CollectionID string                       `json:"collectionId,omitempty"`
	Jobs         []string                     `json:"jobs,omitempty"`
	Statistics   map[string]labScaleStatistic `json:"statistics"`
	Events       []labMixedEvent              `json:"events,omitempty"`
	QueueSamples []labMixedProof              `json:"queueSamples,omitempty"`
	FreshnessMS  []float64                    `json:"freshnessUpperBoundMs,omitempty"`
	InboxMS      []float64                    `json:"inboxPersistenceMs,omitempty"`
	Error        string                       `json:"error,omitempty"`
	Passed       bool                         `json:"passed"`
}

type labMixedRun struct {
	mu      sync.Mutex
	samples []labScaleSample
	first   error
	cancel  context.CancelFunc
}

func (r *labMixedRun) sample(operation string, began time.Time, size int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.samples) >= 4096 {
		err = errors.New("mixed sample count exceeded bound")
	} else {
		r.samples = append(r.samples, labScaleSample{Operation: operation, Duration: time.Since(began), Bytes: size, Err: err})
	}
	if err != nil && r.first == nil {
		r.first = err
		r.cancel()
	}
}

func labMixedPaced(ctx context.Context, start time.Time, step time.Duration, rounds int, run func(int) error) error {
	for i := range rounds {
		wait := time.Until(start.Add(time.Duration(i) * step))
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i > 0 && time.Since(start.Add(time.Duration(i)*step)) >= step {
			return errors.New("mixed reader missed a full polling interval")
		}
		if err := run(i); err != nil {
			return err
		}
	}
	return nil
}

type labMixedLog struct {
	cursor, execution string
	offset            uint64
	started           bool
}

func (l *labMixedLog) accept(value api.LogRange, first string, expected api.RunReference) error {
	if !uuid(expected.ID) || !uuid(expected.ExecutionID) || expected.Number != "1" || value.RunID != expected.ID || value.RunNumber != expected.Number || value.ExecutionID != expected.ExecutionID {
		return errors.New("mixed log differs from the accepted run and execution")
	}
	data, err := base64.StdEncoding.DecodeString(value.BytesBase64)
	start, e1 := strconv.ParseUint(value.StartOffset, 10, 64)
	end, e2 := strconv.ParseUint(value.EndOffset, 10, 64)
	if err != nil || e1 != nil || e2 != nil || len(data) > 256<<10 || end < start || end-start != uint64(len(data)) || !uuid(value.ExecutionID) || value.Stream != "stderr" || value.Truncated || value.State != "complete" || value.NextCursor == "" {
		return errors.New("mixed immutable log byte range invalid")
	}
	if !l.started {
		if start != 0 || string(data) != first {
			return errors.New("mixed actual log content differs")
		}
	} else if value.ExecutionID != l.execution || start != l.offset || len(data) != 0 {
		return errors.New("mixed follow duplicated bytes or crossed execution")
	}
	l.started, l.execution, l.cursor, l.offset = true, value.ExecutionID, value.NextCursor, end
	return nil
}

type labMixedEvent struct {
	EventID        string     `json:"eventId"`
	JobID          string     `json:"jobId"`
	RunID          string     `json:"runId"`
	Outcome        string     `json:"outcome"`
	RecordedAt     time.Time  `json:"recordedAt"`
	PublishedAt    *time.Time `json:"publishedAt"`
	Imported       bool       `json:"imported"`
	Reconciliation bool       `json:"reconciliation"`
	Processed      bool       `json:"processed"`
	Suppressed     bool       `json:"suppressed"`
	InboxID        string     `json:"inboxId"`
	InboxCreatedAt *time.Time `json:"inboxCreatedAt"`
}
type labMixedProof struct {
	ObservedAt         time.Time       `json:"observedAt"`
	ClientBefore       time.Time       `json:"clientBefore"`
	ClientAfter        time.Time       `json:"clientAfter"`
	InstanceID         string          `json:"instanceId"`
	Epoch              string          `json:"epoch"`
	Held               bool            `json:"held"`
	FeedStatus         string          `json:"feedStatus"`
	PendingEvaluations int             `json:"pendingEvaluations"`
	PendingDeliveries  int             `json:"pendingDeliveries"`
	Events             []labMixedEvent `json:"events"`
}

// A single, fixed-host SSH call reads only exact eight newly admitted job IDs.
// PostgreSQL supplies original terminal IDs, not a reconstructed event. Both
// transactions are read-only; quota rows avoid an unbounded queue-table count.
// No tokens, DSNs, source payloads, log bytes or opaque cursors are returned.
const labMixedProofScript = `import json,os,re,subprocess,sys
p=json.load(sys.stdin)
assert os.geteuid()==0 and os.uname().nodename.split('.')[0]=='pg01'
assert set(p)=={'jobs'} and isinstance(p['jobs'],list) and len(p['jobs'])==8 and len(set(p['jobs']))==8
assert all(isinstance(v,str) and re.fullmatch('[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}',v) for v in p['jobs'])
def read(database,query):
 assert database in ('jobman_dashboard_control','jobman_dashboard')
 text="BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY; SET LOCAL statement_timeout='3s'; SET LOCAL lock_timeout='500ms'; "+query+'; ROLLBACK;'
 r=subprocess.run(['podman','exec','-i','--user','postgres','jobman-postgres','psql','-X','-q','-A','-t','-U','jobman_control','-v','ON_ERROR_STOP=1','-d',database],input=text.encode(),stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=5)
 assert r.returncode==0 and 0<len(r.stdout)<=65536
 return json.loads(r.stdout)
ids=','.join("'"+v+"'::uuid" for v in p['jobs'])
source=read('jobman_dashboard_control',"""SELECT json_build_object('instanceId',c.id,'epoch',r.restore_epoch::text,'observedAt',clock_timestamp(),
'events',(SELECT coalesce(json_agg(row_to_json(x) ORDER BY x.\"jobId\"),'[]') FROM (
SELECT o.id AS \"eventId\",o.aggregate_id AS \"jobId\",o.payload->>'runId' AS \"runId\",o.payload->>'outcome' AS outcome,
(o.payload->>'recordedAt')::timestamptz AS \"recordedAt\",f.published_at AS \"publishedAt\",
(o.payload->>'imported')::boolean AS imported,(o.payload->>'reconciliation')::boolean AS reconciliation
FROM outbox o LEFT JOIN monitoring_feed f ON f.event_id=o.id
WHERE o.namespace_id='87ece4c0-b3e0-4357-8af8-209582bf3066'::uuid AND o.topic='monitoring.job_terminal.v1'
AND o.aggregate_id IN ("""+ids+""") ORDER BY o.aggregate_id LIMIT 9) x))
FROM control_instance c CROSS JOIN service_recovery_state r WHERE c.singleton AND r.singleton""")
assert source['instanceId']=='e633cf92-258d-48ff-965a-fda88d68ef3a' and source['epoch']=='1' and len(source['events'])<=8
assert all(e['jobId'] in p['jobs'] and re.fullmatch('[0-9a-f-]{36}',e['eventId']) for e in source['events'])
eventids=','.join("'"+v['eventId']+"'::uuid" for v in source['events']) or 'NULL::uuid'
dashboard=read('jobman_dashboard',"""SELECT json_build_object('held',c.held,'feedStatus',f.status,
'pendingEvaluations',q.pending_evaluations,'pendingDeliveries',q.pending_deliveries,
'events',(SELECT coalesce(json_agg(row_to_json(x) ORDER BY x.\"eventId\"),'[]') FROM (
SELECT e.event_id AS \"eventId\",e.processed_at IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM dashboard_notification_evaluations v WHERE (v.deployment_id,v.control_instance_id,v.event_id)=(e.deployment_id,e.control_instance_id,e.event_id) AND v.state='pending') AS processed,
e.notification_suppressed AS suppressed,n.id AS \"inboxId\",n.created_at AS \"inboxCreatedAt\"
FROM dashboard_source_events e LEFT JOIN dashboard_accounts a ON a.directory_id='71000000-0000-4000-8000-000000000001'::uuid
LEFT JOIN dashboard_notification_inbox n ON n.account_id=a.id AND (n.deployment_id,n.control_instance_id,n.event_id)=(e.deployment_id,e.control_instance_id,e.event_id)
WHERE e.deployment_id='72000000-0000-4000-8000-000000000001'::uuid AND e.control_instance_id='e633cf92-258d-48ff-965a-fda88d68ef3a'::uuid AND e.event_id IN ("""+eventids+""") LIMIT 9) x))
FROM dashboard_notification_work_quota q CROSS JOIN dashboard_notification_delivery_control c CROSS JOIN dashboard_event_feeds f
WHERE q.singleton AND c.singleton AND f.deployment_id='72000000-0000-4000-8000-000000000001'::uuid""")
assert len(dashboard['events'])<=8
byid={e['eventId']:e for e in dashboard.pop('events')}
assert len(byid)<=len(source['events'])
for event in source['events']:
 event.update(byid.get(event['eventId'],{}))
 if event.get('inboxId') is None:event['inboxId']=''
source.update(dashboard)
raw=json.dumps(source,sort_keys=True);assert len(raw)<=65536
print(raw)
`

type labMixedObserver struct {
	host, user, key, known string
	port                   int
}

func labMixedObserverFor(t *testing.T, root string) labMixedObserver {
	t.Helper()
	var inventory map[string]struct {
		Host string `json:"ansible_host"`
		User string `json:"ansible_user"`
		Port int    `json:"ansible_port"`
		Key  string `json:"ansible_ssh_private_key_file"`
	}
	if json.Unmarshal(labExecutionFile(t, filepath.Join(root, ".lab/dashboard/ssh-connections.json"), 65536), &inventory) != nil {
		t.Fatal("mixed observer pinned inventory unavailable")
	}
	v := inventory["pg01"]
	ip := net.ParseIP(v.Host)
	_, subnet, _ := net.ParseCIDR("10.211.55.0/24")
	if v.User != "vagrant" || ip == nil || !(ip.IsLoopback() || ip.Equal(net.ParseIP("10.77.0.20")) || subnet.Contains(ip)) || v.Port < 1 || v.Port > 65535 || !filepath.IsAbs(v.Key) {
		t.Fatal("mixed observer fixed PostgreSQL host differs")
	}
	return labMixedObserver{v.Host, v.User, v.Key, filepath.Join(root, ".lab/dashboard/known_hosts"), v.Port}
}

func (o labMixedObserver) read(ctx context.Context, jobs []string) (labMixedProof, error) {
	var proof labMixedProof
	seen := map[string]bool{}
	for _, job := range jobs {
		if !uuid(job) || seen[job] {
			return proof, errors.New("mixed observer requires distinct exact job IDs")
		}
		seen[job] = true
	}
	if len(jobs) != 8 {
		return proof, errors.New("mixed observer requires exactly eight job IDs")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	quoted := "'" + strings.ReplaceAll(labMixedProofScript, "'", "'\\''") + "'"
	cmd := exec.CommandContext(ctx, "ssh", "-i", o.key, "-p", strconv.Itoa(o.port), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+o.known, "-o", "HostKeyAlgorithms=ssh-ed25519", o.user+"@"+o.host, "sudo python3 -c "+quoted)
	body, _ := json.Marshal(map[string]any{"jobs": jobs})
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stderr = io.Discard
	var out labExecutionOutput
	cmd.Stdout = &out
	before := time.Now().UTC()
	if cmd.Run() != nil || json.Unmarshal(out.Bytes(), &proof) != nil {
		return proof, errors.New("bounded read-only mixed observer failed")
	}
	proof.ClientBefore, proof.ClientAfter = before, time.Now().UTC()
	if proof.InstanceID != labRestoreInstance || proof.Epoch != "1" || proof.Held || proof.FeedStatus != "active" || len(proof.Events) > 8 || proof.PendingEvaluations < 0 || proof.PendingEvaluations >= 100000 || proof.PendingDeliveries < 0 || proof.PendingDeliveries >= 1000000 || proof.ObservedAt.Before(before.Add(-2*time.Second)) || proof.ObservedAt.After(proof.ClientAfter.Add(2*time.Second)) {
		return proof, errors.New("mixed source identity, clock, hold or queue capacity differs")
	}
	ids := map[string]bool{}
	for _, event := range proof.Events {
		if !seen[event.JobID] || !uuid(event.EventID) || !uuid(event.RunID) || ids[event.JobID] || event.Imported || event.Reconciliation || event.Suppressed || event.RecordedAt.IsZero() || !slices.Contains([]string{"success", "failure"}, event.Outcome) {
			return proof, errors.New("mixed original event identity or provenance differs")
		}
		ids[event.JobID] = true
	}
	return proof, nil
}

func (r *labMixedRun) do(ctx context.Context, client labMixedHTTP, operation, path string, output any, validate func() error, want int) error {
	began := time.Now()
	size, err := client.call(ctx, "GET", path, "", "", nil, output, want)
	if err == nil && validate != nil {
		err = validate()
	}
	r.sample(operation, began, size, err)
	return err
}

func labMixedScaleWorker(ctx context.Context, run *labMixedRun, client labMixedHTTP, start time.Time, viewer int, fixture labScaleFixture, two bool) error {
	scopes, namespaces := labScaleScopes(fixture, two)
	query, overviewQuery := labScaleQuery(scopes), labScaleQuery(scopes)
	overviewQuery.Del("limit")
	overviewQuery.Set("completedFrom", fixture.HistoryAt.Add(-time.Second).Format(time.RFC3339Nano))
	overviewQuery.Set("completedTo", fixture.PreparedAt.Add(time.Second).Format(time.RFC3339Nano))
	return labMixedPaced(ctx, start, labMixedPeriod, labMixedRounds, func(round int) error {
		var page api.Page[api.Job]
		if err := run.do(ctx, client, "scale-list", "/api/v1/jobs?"+query.Encode(), &page, func() error { return labScalePage(page, scopes, 50) }, 200); err != nil {
			return err
		}
		index := (viewer + round) % len(scopes)
		jobID, imported := namespaces[index].ActiveJobID, round%2 != 0
		if imported {
			jobID = namespaces[index].ImportedJobID
		}
		var detail api.JobDetail
		if err := run.do(ctx, client, "scale-detail", "/api/v1/deployments/"+scopes[index].DeploymentID+"/namespaces/"+scopes[index].NamespaceID+"/jobs/"+jobID, &detail, func() error {
			if detail.Job.ID != jobID || detail.Job.Scope != scopes[index] || detail.Job.Imported != imported || detail.FetchedAt.IsZero() {
				return errors.New("mixed scale detail identity or provenance differs")
			}
			return nil
		}, 200); err != nil {
			return err
		}
		var overview api.Overview
		return run.do(ctx, client, "scale-overview", "/api/v1/overview?"+overviewQuery.Encode(), &overview, func() error {
			if overview.Completeness != "complete" || !labScaleSourceStatus(overview.Sources, scopes) || overview.Active == nil || *overview.Active != 500 || overview.AwaitingExecution == nil || *overview.AwaitingExecution != 500 || overview.Terminal["success"] == nil || *overview.Terminal["success"] != 100000 {
				return errors.New("mixed scale aggregate silently lost a source contribution")
			}
			return nil
		}, 200)
	})
}

type labMixedInvestigation struct {
	fixture      labMixedSlurm
	run          api.RunReference
	prefix       string
	report       api.Report
	citation     api.Citation
	reportHash   string
	citationHash string
	research     api.Scope
}

func labMixedInvestigationWorker(ctx context.Context, run *labMixedRun, client labMixedHTTP, start time.Time, investigation labMixedInvestigation, burst string, terminal map[string]api.Job, seenAt map[string]time.Time) error {
	prefix, fixture := investigation.prefix, investigation.fixture
	reportPath := prefix + "/jobs/" + fixture.Jobs["task-2"] + "/reports/" + fixture.Reports["include_log_tail"]
	var log labMixedLog
	cursor := ""
	childIndex := 0
	return labMixedPaced(ctx, start, labMixedPeriod, labMixedRounds, func(_ int) error {
		var logRange api.LogRange
		q := url.Values{"stream": {"stderr"}, "limitBytes": {"262144"}}
		if log.cursor != "" {
			q.Set("cursor", log.cursor)
		}
		if err := run.do(ctx, client, "actual-log-follow", prefix+"/jobs/"+fixture.Jobs["task-2"]+"/logs?"+q.Encode(), &logRange, func() error { return log.accept(logRange, labSlurmFailureLog, investigation.run) }, 200); err != nil {
			return err
		}
		q = url.Values{"limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var children api.WorkloadChildrenPage
		if err := run.do(ctx, client, "actual-array-page", prefix+"/workloads/array/"+fixture.ArrayID+"/children?"+q.Encode(), &children, func() error {
			if children.Total != "5" || len(children.Items) < 1 || len(children.Items) > 2 || children.Completeness != "complete" || !labScaleSourceStatus(children.Sources, []api.Scope{{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS}}) {
				return errors.New("mixed actual array page incomplete")
			}
			for _, child := range children.Items {
				if childIndex >= 5 || child.Index != strconv.Itoa(childIndex) || child.TaskIndex != strconv.Itoa(childIndex) || child.Job.ID != fixture.Jobs["task-"+strconv.Itoa(childIndex)] || child.Job.Imported || child.Job.Backend != "slurm" {
					return errors.New("mixed literal array task mapping differs")
				}
				childIndex++
			}
			cursor = children.NextCursor
			if cursor == "" {
				if childIndex != 5 {
					return errors.New("mixed array pagination dropped rows")
				}
				childIndex = 0
			}
			return nil
		}, 200); err != nil {
			return err
		}
		var graph api.GraphNeighborhood
		if err := run.do(ctx, client, "actual-graph-neighborhood", prefix+"/workloads/graph/"+fixture.GraphID+"/neighborhood?nodeId="+fixture.Jobs["graph-root-failure"]+"&maxNodes=2&maxEdges=1", &graph, func() error {
			if graph.Completeness != "complete" || graph.CenterID != fixture.Jobs["graph-root-failure"] || len(graph.Nodes) != 2 || len(graph.Edges) != 1 || graph.TotalNodes != "3" || graph.TotalEdges != "2" || graph.OmittedNodes != "1" || graph.OmittedEdges != "1" {
				return errors.New("mixed bounded graph identity or omissions differ")
			}
			return nil
		}, 200); err != nil {
			return err
		}
		var report api.Report
		if err := run.do(ctx, client, "actual-report", reportPath, &report, func() error {
			if labRestoreDigest(report) != investigation.reportHash {
				return errors.New("mixed sealed actual report changed")
			}
			return nil
		}, 200); err != nil {
			return err
		}
		var citation api.Citation
		if err := run.do(ctx, client, "actual-citation", reportPath+"/citations/"+url.PathEscape(investigation.citation.ID), &citation, func() error {
			if labRestoreDigest(citation) != investigation.citationHash {
				return errors.New("mixed sealed actual citation changed")
			}
			return nil
		}, 200); err != nil {
			return err
		}
		var active api.WorkloadChildrenPage
		return run.do(ctx, client, "burst-foreground", prefix+"/workloads/collection/"+burst+"/children?limit=8", &active, func() error {
			if active.Total != "8" || len(active.Items) != 8 || active.NextCursor != "" || active.Completeness != "complete" {
				return errors.New("mixed burst projection incomplete")
			}
			seen := map[string]bool{}
			for _, child := range active.Items {
				job := child.Job
				if !uuid(job.ID) || seen[job.ID] || job.NamespaceID != labMixedOperationsNS || job.DeploymentID != labDeployment || job.Imported {
					return errors.New("mixed burst child scope or provenance differs")
				}
				seen[job.ID] = true
				if job.Phase == "terminal" {
					if _, ok := seenAt[job.ID]; !ok {
						seenAt[job.ID] = time.Now().UTC()
					}
					terminal[job.ID] = job
				}
			}
			return nil
		}, 200)
	})
}

func labMixedBobWorker(ctx context.Context, run *labMixedRun, client labMixedHTTP, start time.Time, investigation labMixedInvestigation) error {
	query := labScaleQuery([]api.Scope{investigation.research})
	query.Set("limit", "20")
	return labMixedPaced(ctx, start, labMixedPeriod, labMixedRounds, func(_ int) error {
		var page api.Page[api.Job]
		if err := run.do(ctx, client, "bob-authorized-list", "/api/v1/jobs?"+query.Encode(), &page, func() error {
			if len(page.Items) < 1 || len(page.Items) > 20 || page.Completeness != "complete" || !labScaleSourceStatus(page.Sources, []api.Scope{investigation.research}) || page.Items[0].Scope != investigation.research {
				return errors.New("mixed Bob current authorized research list differs")
			}
			return nil
		}, 200); err != nil {
			return err
		}
		var job api.JobDetail
		if err := run.do(ctx, client, "bob-authorized-detail", "/api/v1/deployments/"+labDeployment+"/namespaces/"+investigation.research.NamespaceID+"/jobs/"+page.Items[0].ID, &job, func() error {
			if job.Job.ID != page.Items[0].ID || job.Job.Scope != investigation.research {
				return errors.New("mixed Bob detail crossed namespace")
			}
			return nil
		}, 200); err != nil {
			return err
		}
		denied := investigation.prefix + "/jobs/" + investigation.fixture.Jobs["task-2"]
		if err := run.do(ctx, client, "bob-log-denial", denied+"/logs?stream=stderr", nil, nil, 403); err != nil {
			return err
		}
		return run.do(ctx, client, "bob-report-denial", denied+"/reports/"+investigation.fixture.Reports["include_log_tail"], nil, nil, 404)
	})
}

func labMixedReadyProof(proof labMixedProof, jobs []string) bool {
	if len(proof.Events) != 8 || len(jobs) != 8 {
		return false
	}
	seen := map[string]bool{}
	for _, event := range proof.Events {
		if !slices.Contains(jobs, event.JobID) || seen[event.JobID] || !event.Processed || event.Suppressed || event.Imported || event.Reconciliation || !uuid(event.InboxID) || event.InboxCreatedAt == nil || event.PublishedAt == nil || event.InboxCreatedAt.Before(event.RecordedAt) || event.PublishedAt.Before(event.RecordedAt) {
			return false
		}
		seen[event.JobID] = true
	}
	return true
}

func labMixedWait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(max(duration, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func labMixedModeRun(t *testing.T, ctx context.Context, mode string, fixture labScaleFixture, host labActualReceipt, readers []labMixedHTTP, alice, bob, source labMixedHTTP, investigation labMixedInvestigation, observer labMixedObserver, progress func(string, any)) (result labMixedMode) {
	t.Helper()
	result.Mode, result.StartedAt = mode, time.Now().UTC()
	defer func() { result.CompletedAt = time.Now().UTC() }()
	body := notifications.RuleInput{Name: "Mixed " + mode + " " + result.StartedAt.Format(time.RFC3339Nano), Enabled: true, Scope: notifications.ScopeNamespaceJobs, Namespaces: []notifications.NamespaceRef{{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS}}, Jobs: []notifications.JobRef{}, OutcomeMode: notifications.OutcomeAllTerminal, Outcomes: []string{}}
	progress("rule-create-intent", map[string]string{"mode": mode, "name": body.Name})
	var rule notifications.RuleView
	if _, err := alice.call(ctx, "POST", "/api/v1/rules", "", "", body, &rule, 201); err != nil || !uuid(rule.ID) {
		result.Error = "mixed rule creation uncertain; inspect retained intent before any retry"
		return
	}
	result.RuleID = rule.ID
	ownedRuleID := rule.ID
	progress("owned-rule", map[string]string{"mode": mode, "ruleId": rule.ID})
	defer func() {
		// Stop only this account-owned rule. Never retry an uncertain create or
		// cancel source jobs to make acceptance appear successful.
		cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		var current, stopped notifications.RuleView
		_, err := alice.call(cleanup, "GET", "/api/v1/rules/"+ownedRuleID, "", "", nil, &current, 200)
		if err == nil && current.ID == ownedRuleID {
			_, err = alice.call(cleanup, "PUT", "/api/v1/rules/"+ownedRuleID+"/enabled", "", current.Revision, map[string]bool{"enabled": false}, &stopped, 200)
			result.RuleStopped = err == nil && stopped.ID == ownedRuleID && !stopped.Enabled
		}
		if !result.RuleStopped {
			result.Passed = false
			if result.Error == "" {
				result.Error = "mixed owned rule stop unconfirmed; inspect retained rule ID"
			}
		}
		progress("rule-stop", map[string]any{"mode": mode, "ruleId": ownedRuleID, "confirmed": result.RuleStopped})
	}()
	activationDeadline := time.Now().Add(35 * time.Second)
	for {
		if _, err := alice.call(ctx, "GET", "/api/v1/rules/"+ownedRuleID, "", "", nil, &rule, 200); err != nil || rule.ID != ownedRuleID {
			result.Error = "mixed rule authorization unavailable"
			return
		}
		if rule.Enabled && len(rule.Scopes) == 1 && rule.Scopes[0].DeploymentID == labDeployment && rule.Scopes[0].NamespaceID == labMixedOperationsNS && rule.Scopes[0].Status == "active" && rule.Scopes[0].ActivatedAt != nil {
			break
		}
		if time.Now().After(activationDeadline) || labMixedWait(ctx, time.Second) != nil {
			result.Error = "mixed rule did not become active before admission"
			return
		}
	}
	request, err := labMixedRequests(mode)
	if err != nil {
		result.Error = err.Error()
		return
	}
	key := "dashboard-mixed-v1-" + mode
	progress("collection-admission-intent", map[string]string{"mode": mode, "idempotencyKey": key})
	var collection labActualGroup
	admissionAt := time.Now().UTC()
	if _, err = source.call(ctx, "POST", "/v1/namespaces/dashboard-operations/collections", key, "", request, &collection, 200, 201); err != nil || !uuid(collection.Metadata.ID) || len(collection.Items) != 8 {
		result.Error = "mixed collection admission uncertain; preserve fixed key and receipts"
		return
	}
	result.CollectionID = collection.Metadata.ID
	for i, child := range collection.Items {
		if child.Index != i || child.Name != "task-"+strconv.Itoa(i) || !uuid(child.Job.Metadata.ID) || slices.Contains(result.Jobs, child.Job.Metadata.ID) || child.Job.Status.Phase == "terminal" {
			result.Error = "mixed fixed-key workload is not a fresh bounded admission"
			return
		}
		result.Jobs = append(result.Jobs, child.Job.Metadata.ID)
	}
	progress("collection-admitted", map[string]any{"mode": mode, "collectionId": result.CollectionID, "jobs": result.Jobs})
	loadStart := time.Now()
	loadContext, stop := context.WithDeadline(ctx, loadStart.Add(90*time.Second))
	defer stop()
	run := &labMixedRun{cancel: stop}
	terminal, seenAt := map[string]api.Job{}, map[string]time.Time{}
	var workers sync.WaitGroup
	for viewer, client := range readers {
		workers.Go(func() {
			if err := labMixedScaleWorker(loadContext, run, client, loadStart, viewer, fixture, mode == "two-controls"); err != nil && loadContext.Err() == nil {
				run.sample("pacing", time.Now(), 0, err)
			}
		})
	}
	workers.Go(func() {
		if err := labMixedInvestigationWorker(loadContext, run, alice, loadStart, investigation, result.CollectionID, terminal, seenAt); err != nil && loadContext.Err() == nil {
			run.sample("pacing", time.Now(), 0, err)
		}
	})
	workers.Go(func() {
		if err := labMixedBobWorker(loadContext, run, bob, loadStart, investigation); err != nil && loadContext.Err() == nil {
			run.sample("pacing", time.Now(), 0, err)
		}
	})
	workers.Go(func() {
		_ = labMixedPaced(loadContext, loadStart, 15*time.Second, 6, func(_ int) error {
			proof, err := observer.read(loadContext, result.Jobs)
			if err != nil {
				run.sample("observer", time.Now(), 0, err)
				return err
			}
			result.QueueSamples = append(result.QueueSamples, proof)
			return nil
		})
	})
	workers.Wait()
	result.Statistics = labScaleStatistics(run.samples)
	if run.first != nil {
		result.Error = run.first.Error()
		return
	}
	for _, name := range []string{"scale-list", "scale-detail", "scale-overview"} {
		stat := result.Statistics[name]
		if stat.Samples != 23*labMixedRounds || stat.Errors != 0 || stat.P95MS > 2000 {
			result.Error = "mixed scale HTTP count, correctness or 2s p95 budget failed"
			return
		}
	}
	for _, name := range []string{"actual-log-follow", "actual-array-page", "actual-graph-neighborhood", "actual-report", "actual-citation", "burst-foreground", "bob-authorized-list", "bob-authorized-detail", "bob-log-denial", "bob-report-denial"} {
		stat := result.Statistics[name]
		if stat.Samples != labMixedRounds || stat.Errors != 0 {
			result.Error = "mixed investigation sample count or correctness failed"
			return
		}
	}
	if len(result.QueueSamples) != 6 || labMixedWait(ctx, time.Until(loadStart.Add(90*time.Second))) != nil {
		result.Error = "mixed 90-second observation window incomplete"
		return
	}
	var final labActualGroup
	if _, err = source.call(ctx, "GET", "/v1/namespaces/dashboard-operations/collections/"+result.CollectionID, "", "", nil, &final, 200); err != nil || final.Metadata.ID != result.CollectionID || final.Status.Total != 8 || final.Status.Terminal != 8 || final.Status.Succeeded != 6 || final.Status.Failed != 2 || final.Status.ArrayMode != "individual" || len(final.Items) != 8 || len(terminal) != 8 {
		result.Error = "mixed actual collection did not complete six successes and two failures"
		return
	}
	proof, err := observer.read(ctx, result.Jobs)
	if err != nil || !labMixedReadyProof(proof, result.Jobs) {
		result.Error = "mixed original events or persisted inbox did not settle"
		return
	}
	result.QueueSamples = append(result.QueueSamples, proof)
	result.Events = proof.Events
	// An upper bound on server-minus-client clock offset follows directly from
	// the observer's timestamp lying between its client send and receive. Use
	// the largest measured bound; never convert a negative latency to zero.
	clockUpper := -24 * time.Hour
	for _, sample := range result.QueueSamples {
		clockUpper = max(clockUpper, sample.ObservedAt.Sub(sample.ClientBefore))
	}
	for i, child := range final.Items {
		want := "success"
		if i == 2 || i == 6 {
			want = "failure"
		}
		job := child.Job
		shown := terminal[job.Metadata.ID]
		if child.Index != i || child.Name != "task-"+strconv.Itoa(i) || job.Metadata.ID != result.Jobs[i] || job.Metadata.NamespaceID != host.NamespaceID || job.Status.Imported || job.Status.Phase != "terminal" || job.Status.Outcome != want || job.Status.CurrentRun == nil || job.Status.Lifecycle.CompletedAt == nil || job.Spec.Placement.ExecutionBackend != "subprocess" || job.Spec.Placement.TargetID != host.TargetID || job.Spec.Placement.TargetGenerationID != host.TargetGenerationID || shown.CurrentRun == nil || *shown.CurrentRun != *job.Status.CurrentRun || shown.Outcome != want || shown.Owner == nil || !shown.Owner.IsCurrentUser {
			result.Error = "mixed executed child identity, outcome or current run differs"
			return
		}
		index := slices.IndexFunc(proof.Events, func(e labMixedEvent) bool { return e.JobID == job.Metadata.ID })
		if index < 0 {
			result.Error = "mixed original terminal event missing"
			return
		}
		event := proof.Events[index]
		if event.RunID != job.Status.CurrentRun.ID || event.Outcome != want || event.RecordedAt.Before(admissionAt.Add(-2*time.Second)) {
			result.Error = "mixed event is stale or belongs to another actual run"
			return
		}
		freshness := seenAt[job.Metadata.ID].Sub(event.RecordedAt) + clockUpper
		inboxLatency := event.InboxCreatedAt.Sub(event.RecordedAt)
		if freshness < 0 || inboxLatency < 0 {
			result.Error = "mixed timestamps cannot establish latency bounds"
			return
		}
		result.FreshnessMS = append(result.FreshnessMS, float64(freshness)/float64(time.Millisecond))
		result.InboxMS = append(result.InboxMS, float64(inboxLatency)/float64(time.Millisecond))
		var item notifications.InboxItem
		if _, err = alice.call(ctx, "GET", "/api/v1/inbox/"+event.InboxID, "", "", nil, &item, 200); err != nil || item.Validate() != nil || item.ID != event.InboxID || item.EventID != event.EventID || item.ControlInstanceID != labRestoreInstance || item.Job.JobID != job.Metadata.ID || item.Job.NamespaceID != labMixedOperationsNS || item.Outcome != want || !item.EventAt.Equal(event.RecordedAt) || !item.CreatedAt.Equal(*event.InboxCreatedAt) || !slices.ContainsFunc(item.MatchedRules, func(m notifications.InboxMatch) bool { return m.RuleID == result.RuleID }) {
			result.Error = "mixed current-authorized inbox differs from original source event"
			return
		}
	}
	fresh, inbox := slices.Clone(result.FreshnessMS), slices.Clone(result.InboxMS)
	slices.Sort(fresh)
	slices.Sort(inbox)
	if fresh[7] > 10000 || inbox[7] > 30000 {
		result.Error = "mixed eight-event nearest-rank p95 freshness or inbox budget failed"
		return
	}
	result.Passed = true
	return
}

func TestLabDeployedMixedLoad(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_MIXED_LOAD") != "1" {
		t.Skip("explicit reviewed mixed-load and actual-workload opt-in required")
	}
	if deadline, ok := t.Deadline(); !ok || time.Until(deadline) > 8*time.Minute {
		t.Fatal("live mixed test requires an outer -timeout=8m or shorter, including sign-in")
	}
	root, output := os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT"), os.Getenv("JOBMAN_DASHBOARD_LAB_MIXED_RESULT")
	slurmPath := os.Getenv("JOBMAN_DASHBOARD_LAB_MIXED_SLURM_RECEIPT")
	if !filepath.IsAbs(root) || !filepath.IsAbs(output) || !filepath.IsAbs(slurmPath) || filepath.Clean(output) != output {
		t.Fatal("mixed test requires absolute reviewed fixture and new result paths")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil || parent != filepath.Dir(output) {
		t.Fatal("mixed result parent must be a real directory")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("new result path required; preserve every previous mixed-load attempt")
	}
	if file.Chmod(0600) != nil {
		file.Close()
		t.Fatal("mixed result private mode failed")
	}
	journal, err := os.OpenFile(output+".progress.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		file.Close()
		t.Fatal("new progress path required; preserve partial evidence")
	}
	if journal.Chmod(0600) != nil {
		journal.Close()
		file.Close()
		t.Fatal("mixed progress private mode failed")
	}
	result := struct {
		StartedAt          time.Time      `json:"startedAt"`
		CompletedAt        time.Time      `json:"completedAt"`
		ScaleFixtureSHA256 string         `json:"scaleFixtureSHA256"`
		SlurmReceiptSHA256 string         `json:"slurmReceiptSHA256"`
		HostFixtureSHA256  string         `json:"hostFixtureSHA256"`
		Viewers            int            `json:"distinctSignedInViewers"`
		ScaleViewers       int            `json:"scaleViewers"`
		PeakHTTP           int32          `json:"peakHTTP"`
		Modes              []labMixedMode `json:"modes"`
		Passed             bool           `json:"passed"`
		Notes              string         `json:"notes"`
	}{StartedAt: time.Now().UTC(), ScaleViewers: 23, Modes: []labMixedMode{}, Notes: "Healthy mixed HTTP contention only: 23 scale readers plus Alice/Bob; separate 25-scale-reader acceptance remains required. Alice actual immutable Slurm stream follows/group/report reads; Bob authorized research metadata and denied operations. Maximum sixteen new bounded actual host jobs. API latency is not client rendering. Queue counts are sampled quota rows, not continuous resource profiling. No dependency fault, corporate AD FS or APNs/device-presentation acceptance."}
	gate := &labMixedLimiter{slots: make(chan struct{}, labMixedInflight)}
	defer func() {
		result.CompletedAt, result.PeakHTTP = time.Now().UTC(), gate.peak.Load()
		result.Passed = !t.Failed() && len(result.Modes) == 2 && result.Modes[0].Passed && result.Modes[1].Passed && result.Viewers == 25 && result.PeakHTTP <= labMixedInflight
		if err := json.NewEncoder(file).Encode(result); err != nil || file.Sync() != nil {
			t.Error("mixed public result could not be persisted")
		}
		if file.Close() != nil || journal.Close() != nil {
			t.Error("mixed receipt close failed")
		}
	}()
	progress := func(phase string, value any) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"phase": phase, "at": time.Now().UTC(), "value": value})
		if err != nil || len(raw) > 65536 {
			t.Fatal("mixed progress receipt exceeds bound")
		}
		if _, err = journal.Write(append(raw, '\n')); err != nil || journal.Sync() != nil {
			t.Fatal("mixed progress could not persist; stop without further mutations")
		}
	}
	directory, err := os.Open(parent)
	if err != nil {
		t.Fatal("mixed receipt parent unavailable for durability check")
	}
	syncErr, closeErr := directory.Sync(), directory.Close()
	if syncErr != nil || closeErr != nil {
		t.Fatal("mixed receipt directory could not be synced")
	}
	progress("started", map[string]any{"scaleViewers": 23, "investigationViewers": 2, "maximumHTTP": labMixedInflight, "modeSeconds": 90, "maximumNewJobs": 16})
	read := func(path string, maximum int64, value any) string {
		t.Helper()
		raw, err := labScaleReadFile(path, maximum)
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatal("mixed reviewed fixture unavailable or oversized")
		}
		hash := sha256.Sum256(raw)
		return hex.EncodeToString(hash[:])
	}
	var fixture labScaleFixture
	result.ScaleFixtureSHA256 = read(filepath.Join(root, ".lab/dashboard/scale-fixture.json"), 128<<10, &fixture)
	if fixture.validate() != nil {
		t.Fatal("complete reviewed two-source scale fixture required before mixed load")
	}
	var slurm labMixedSlurm
	result.SlurmReceiptSHA256 = read(slurmPath, 32768, &slurm)
	if !slurm.valid() || result.SlurmReceiptSHA256 != "186719a51c266e8d48157a508d5e485382811e7a60b1fbc32b1a2f1ab2107801" {
		t.Fatal("exact independently accepted actual complete Slurm receipt required")
	}
	var host labActualReceipt
	result.HostFixtureSHA256 = read(filepath.Join(root, ".lab/dashboard/execution-fixture.json"), 32768, &host)
	if !host.Synthetic || host.Mode != "actual-subprocess-execution" || host.DeploymentID != labDeployment || host.ControlInstanceID != labRestoreInstance || host.RecoveryEpoch != "1" || host.NamespaceID != labMixedOperationsNS || host.Namespace != "dashboard-operations" || host.TargetName != "dashboard-execution-host" || !uuid(host.AgentID) || !uuid(host.TargetID) || !uuid(host.TargetGenerationID) || host.CoreRevision != "21701b191cd4e4e063d26cad7dae31c35db6bc8c" {
		t.Fatal("reviewed isolated host executor identity required")
	}
	ctx, cancel := context.WithDeadline(t.Context(), result.StartedAt.Add(8*time.Minute))
	defer cancel()
	checkSources := func() {
		t.Helper()
		for _, s := range fixture.Sources {
			if err := labScaleSourceIdentity(ctx, root, s); err != nil {
				t.Fatal(err)
			}
		}
	}
	checkSources()
	accounts := map[string]bool{}
	bootstrap := func(client labMixedHTTP) api.Bootstrap {
		t.Helper()
		var b api.Bootstrap
		if _, err := client.call(ctx, "GET", "/api/v1/bootstrap", "", "", nil, &b, 200); err != nil || b.FixtureMode || b.Completeness != "complete" || b.APIVersion != api.Version || !uuid(b.Account.ID) || accounts[b.Account.ID] {
			t.Fatal("mixed fresh distinct account bootstrap failed")
		}
		accounts[b.Account.ID] = true
		result.Viewers = len(accounts)
		return b
	}
	readers := make([]labMixedHTTP, 0, 23)
	for _, user := range fixture.Users[:23] {
		if ctx.Err() != nil {
			t.Fatal("mixed overall sign-in deadline exceeded")
		}
		session := labNativeSignIn(t, user.Username, user.DirectoryID)
		if session.subject != user.Subject {
			t.Fatal("mixed signed scale identity differs from fixture")
		}
		client := labMixedClient(t, session, gate, false)
		b := bootstrap(client)
		for _, source := range fixture.Sources {
			for _, ns := range source.Namespaces {
				matched := false
				for _, d := range b.Deployments {
					for _, grant := range d.Namespaces {
						matched = matched || d.ID == source.DeploymentID && grant.ID == ns.ID && grant.Name == ns.Name && slices.Equal(grant.Roles, []string{"viewer"}) && grant.AuthorizationExpiresAt.After(time.Now())
					}
				}
				if !matched {
					t.Fatal("mixed scale viewer lacks exact fresh direct viewer grants")
				}
			}
		}
		readers = append(readers, client)
	}
	aliceSession := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bobSession := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	alice, bob := labMixedClient(t, aliceSession, gate, false), labMixedClient(t, bobSession, gate, false)
	source := labMixedClient(t, aliceSession, gate, true)
	a, b := bootstrap(alice), bootstrap(bob)
	investigation := labMixedInvestigation{fixture: slurm, prefix: "/api/v1/deployments/" + labDeployment + "/namespaces/" + labMixedOperationsNS}
	aliceOperations := false
	for _, d := range a.Deployments {
		for _, ns := range d.Namespaces {
			aliceOperations = aliceOperations || d.ID == labDeployment && ns.ID == labMixedOperationsNS && ns.AuthorizationExpiresAt.After(time.Now()) && slices.Contains(ns.Capabilities, "jobs.read") && slices.Contains(ns.Capabilities, "logs.read") && slices.Contains(ns.Capabilities, "evidence.read")
		}
	}
	for _, d := range b.Deployments {
		for _, ns := range d.Namespaces {
			if d.ID == labDeployment && ns.ID == labMixedOperationsNS {
				t.Fatal("Bob primary operations grant unexpectedly widened")
			}
			if d.ID == labDeployment && ns.Name == "dashboard-research" && ns.AuthorizationExpiresAt.After(time.Now()) {
				investigation.research = api.Scope{DeploymentID: d.ID, NamespaceID: ns.ID}
			}
		}
	}
	if !aliceOperations || !uuid(investigation.research.NamespaceID) {
		t.Fatal("mixed investigation source permissions differ")
	}
	var current api.JobDetail
	if _, err = alice.call(ctx, "GET", investigation.prefix+"/jobs/"+slurm.Jobs["task-2"], "", "", nil, &current, 200); err != nil {
		t.Fatal("accepted actual Slurm job unavailable before load")
	}
	if investigation.run, err = slurm.pinnedRun(current); err != nil {
		t.Fatal(err)
	}
	reportPath := investigation.prefix + "/jobs/" + slurm.Jobs["task-2"] + "/reports/" + slurm.Reports["include_log_tail"]
	if _, err = alice.call(ctx, "GET", reportPath, "", "", nil, &investigation.report, 200); err != nil || investigation.report.State != "ready" || investigation.report.Detail == nil || investigation.report.Outdated || investigation.report.TaskID != slurm.Reports["include_log_tail"] || investigation.report.JobID != slurm.Jobs["task-2"] || investigation.report.Scope != current.Job.Scope || investigation.report.RunID != investigation.run.ID || len(investigation.report.Detail.Runs) != 1 || investigation.report.Detail.Runs[0] != investigation.run || investigation.report.Detail.ControlInstanceID != labRestoreInstance || investigation.report.Detail.Disclosure.ProviderInvoked || len(investigation.report.Detail.Citations) < 1 {
		t.Fatal("accepted actual Slurm report unavailable before load")
	}
	investigation.reportHash = labRestoreDigest(investigation.report)
	ref := investigation.report.Detail.Citations[0]
	if _, err = alice.call(ctx, "GET", reportPath+"/citations/"+url.PathEscape(ref.ID), "", "", nil, &investigation.citation, 200); err != nil || investigation.citation.ID != ref.ID || investigation.citation.TaskID != investigation.report.TaskID || investigation.citation.EvidenceID != investigation.report.EvidenceID || investigation.citation.ReportID != investigation.report.ReportID {
		t.Fatal("accepted actual sealed citation unavailable before load")
	}
	investigation.citationHash = labRestoreDigest(investigation.citation)
	observer := labMixedObserverFor(t, root)
	progress("preflight-complete", map[string]any{"viewers": result.Viewers, "scaleFixtureSHA256": result.ScaleFixtureSHA256, "slurmReceiptSHA256": result.SlurmReceiptSHA256, "hostFixtureSHA256": result.HostFixtureSHA256, "acceptedSlurmRun": investigation.run})
	for _, mode := range []string{"one-control", "two-controls"} {
		checkSources()
		value := labMixedModeRun(t, ctx, mode, fixture, host, readers, alice, bob, source, investigation, observer, progress)
		result.Modes = append(result.Modes, value)
		progress("mode-complete", value)
		if !value.Passed {
			t.Error(value.Error)
			return
		}
		checkSources()
	}
	t.Log("PASS: healthy one/two-source mixed HTTPS load,23 scale readers plus Alice/Bob, actual investigation follows/drilldowns/sealed reports,16 bounded real host jobs and original-event/inbox latency. Separate25-viewer scale, client rendering, injected dependency faults and APNs acceptance remain distinct.")
}

func TestLabMixedRequestsAreFixedBoundedAndReal(t *testing.T) {
	var previous []byte
	for _, mode := range []string{"one-control", "two-controls"} {
		one, err := labMixedRequests(mode)
		two, err2 := labMixedRequests(mode)
		a, _ := json.Marshal(one)
		b, _ := json.Marshal(two)
		if err != nil || err2 != nil || !bytes.Equal(a, b) || len(a) > 32768 || bytes.Equal(a, previous) || len(one.Spec.Items) != 8 || one.Spec.MaxActive != 2 || one.Spec.FailurePolicy != "continue" || one.Spec.ArrayPolicy != "never" || one.Metadata.Namespace != "dashboard-operations" {
			t.Fatal("mixed immutable scenario boundary differs")
		}
		previous = a
		for index, child := range one.Spec.Items {
			w := child.Workload.Document.Spec
			if child.Name != "task-"+strconv.Itoa(index) || child.Placement.Target != "dashboard-execution-host" || w.Command.Executable != "/bin/sh" || len(w.Command.Args) != 2 || w.Command.Args[0] != "-c" || w.Policy.RunTimeout != "30s" || w.Policy.Retry.MaxRuns != 1 || w.Policy.DuplicateRisk != "reject" || w.WorkingDirectory != "workspace:/" || w.Runtime.Kind != "native" {
				t.Fatal("mixed workload escaped explicit execution scope")
			}
			want := "exit 0"
			if index == 2 || index == 6 {
				want = "exit 7"
			}
			if !strings.HasSuffix(w.Command.Args[1], want) || !strings.Contains(w.Command.Args[1], "for n in 1 2 3 4 5 6;") || !strings.Contains(w.Command.Args[1], "sleep 1;") {
				t.Fatal("mixed actual result or wall-time bound changed")
			}
		}
	}
	if _, err := labMixedRequests("arbitrary"); err == nil {
		t.Fatal("unbounded caller-selected scenario accepted")
	}
}

func TestLabMixedLimiterCancelsWaiterAndBoundsConcurrency(t *testing.T) {
	gate := &labMixedLimiter{slots: make(chan struct{}, 2)}
	one, err := gate.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	two, err := gate.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		release, err := gate.acquire(ctx)
		if release != nil {
			release()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled blocked request admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled limiter wait did not stop")
	}
	one()
	two()
	if gate.live.Load() != 0 || gate.peak.Load() != 2 || len(gate.slots) != 0 {
		t.Fatal("mixed limiter leaked slots or exceeded its capacity")
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if release, err := gate.acquire(ctx); err == nil || release != nil || len(gate.slots) != 0 {
		t.Fatal("pre-cancelled request acquired transport authority")
	}
}

func TestLabMixedFollowRejectsCrossRunAndRepeatedBytes(t *testing.T) {
	run := api.RunReference{ID: "79000000-0000-4000-8000-000000000001", ExecutionID: "79000000-0000-4000-8000-000000000002", Number: "1"}
	first := api.LogRange{RunID: run.ID, RunNumber: run.Number, ExecutionID: run.ExecutionID, Stream: "stderr", State: "complete", StartOffset: "0", EndOffset: "3", BytesBase64: base64.StdEncoding.EncodeToString([]byte("abc")), NextCursor: "opaque-one"}
	identityChanges := []func(*api.LogRange){
		func(v *api.LogRange) { v.ExecutionID = "" },
		func(v *api.LogRange) { v.ExecutionID = "79000000-0000-4000-8000-000000000003" },
		func(v *api.LogRange) { v.RunID = "" },
		func(v *api.LogRange) { v.RunID = "79000000-0000-4000-8000-000000000004" },
		func(v *api.LogRange) { v.RunNumber = "" },
		func(v *api.LogRange) { v.RunNumber = "2" },
	}
	for _, change := range identityChanges {
		value := first
		change(&value)
		var untouched labMixedLog
		if untouched.accept(value, "abc", run) == nil || untouched != (labMixedLog{}) {
			t.Fatal("misattributed identical-content first range changed state")
		}
	}
	var log labMixedLog
	if log.accept(first, "abc", run) != nil {
		t.Fatal("actual first range rejected")
	}
	empty := first
	empty.StartOffset, empty.BytesBase64, empty.NextCursor = "3", "", "opaque-two"
	if log.accept(empty, "abc", run) != nil {
		t.Fatal("complete empty follow rejected")
	}
	changes := append(identityChanges, func(v *api.LogRange) { *v = first }, func(v *api.LogRange) { v.EndOffset = "2" }, func(v *api.LogRange) { v.BytesBase64 = "invalid" }, func(v *api.LogRange) { v.Truncated = true }, func(v *api.LogRange) { v.NextCursor = "" })
	for _, change := range changes {
		value := empty
		change(&value)
		copy := log
		if copy.accept(value, "abc", run) == nil || copy != log {
			t.Fatal("invalid range changed accepted follow state")
		}
	}
}

func TestLabMixedPinsCurrentRunToAcceptedCompletion(t *testing.T) {
	s := labMixedSlurm{Fixture: labActualReceipt{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS, TargetID: "79000000-0000-4000-8000-000000000011", TargetGenerationID: "79000000-0000-4000-8000-000000000012"}, Jobs: map[string]string{"task-2": "79000000-0000-4000-8000-000000000013"}}
	now := time.Now().UTC()
	for index := range 5 {
		exit, outcome := 0, "success"
		if index == 2 {
			exit, outcome = 7, "failure"
		}
		s.RunnerCompletions.Executions = append(s.RunnerCompletions.Executions, labMixedCompletion{Index: index, ExitCode: exit, ExecutionID: fmt.Sprintf("78000000-0000-4000-8000-%012d", index+1), Outcome: outcome, ObservedAt: now, CompletionSHA256: strings.Repeat("a", 64)})
	}
	run := api.RunReference{ID: "79000000-0000-4000-8000-000000000014", Number: "1", ExecutionID: s.RunnerCompletions.Executions[2].ExecutionID}
	detail := api.JobDetail{FetchedAt: now, Job: api.Job{Scope: api.Scope{DeploymentID: s.Fixture.DeploymentID, NamespaceID: s.Fixture.NamespaceID}, ID: s.Jobs["task-2"], TargetID: s.Fixture.TargetID, TargetGenerationID: s.Fixture.TargetGenerationID, Owner: &api.Owner{ID: "79000000-0000-4000-8000-000000000015", IsCurrentUser: true}, Backend: "slurm", Phase: "terminal", Outcome: "failure", CompletedAt: &now, CurrentRun: &run}}
	if value, err := s.pinnedRun(detail); err != nil || value != run {
		t.Fatal("accepted current run rejected")
	}
	for _, change := range []func(*api.Job){
		func(j *api.Job) { j.DeploymentID = "79000000-0000-4000-8000-000000000016" },
		func(j *api.Job) { j.NamespaceID = "79000000-0000-4000-8000-000000000017" },
		func(j *api.Job) { j.ID = "79000000-0000-4000-8000-000000000018" },
		func(j *api.Job) { j.Owner = &api.Owner{ID: j.Owner.ID, IsCurrentUser: false} },
		func(j *api.Job) { j.TargetGenerationID = "79000000-0000-4000-8000-000000000019" },
		func(j *api.Job) { j.CurrentRun = nil },
		func(j *api.Job) {
			j.CurrentRun = &api.RunReference{ID: run.ID, Number: "2", ExecutionID: run.ExecutionID}
		},
		func(j *api.Job) {
			j.CurrentRun = &api.RunReference{ID: run.ID, Number: run.Number, ExecutionID: s.RunnerCompletions.Executions[0].ExecutionID}
		},
	} {
		value := detail
		change(&value.Job)
		if _, err := s.pinnedRun(value); err == nil {
			t.Fatal("unrelated current source job accepted")
		}
	}
	for _, change := range []func(*labMixedSlurm){
		func(v *labMixedSlurm) { v.RunnerCompletions.Executions = v.RunnerCompletions.Executions[:2] },
		func(v *labMixedSlurm) { v.RunnerCompletions.Executions[2].Index = 3 },
		func(v *labMixedSlurm) { v.RunnerCompletions.Executions[2].Outcome = "success" },
		func(v *labMixedSlurm) {
			v.RunnerCompletions.Executions[2].ExecutionID = v.RunnerCompletions.Executions[0].ExecutionID
		},
	} {
		value := s
		value.RunnerCompletions.Executions = slices.Clone(s.RunnerCompletions.Executions)
		change(&value)
		if _, err := value.pinnedRun(detail); err == nil {
			t.Fatal("incomplete or ambiguous accepted completion adopted")
		}
	}
}

func TestLabMixedProofRequiresEveryOriginalSettledEvent(t *testing.T) {
	now := time.Now().UTC()
	proof := labMixedProof{}
	jobs := []string{}
	for i := range 8 {
		job := fmt.Sprintf("78000000-0000-4000-8000-%012d", i+1)
		jobs = append(jobs, job)
		proof.Events = append(proof.Events, labMixedEvent{EventID: fmt.Sprintf("79000000-0000-4000-8000-%012d", i+1), JobID: job, Processed: true, RecordedAt: now, PublishedAt: &now, InboxID: fmt.Sprintf("7a000000-0000-4000-8000-%012d", i+1), InboxCreatedAt: &now})
	}
	if !labMixedReadyProof(proof, jobs) {
		t.Fatal("complete distinct proof rejected")
	}
	for _, change := range []func(*labMixedProof){func(p *labMixedProof) { p.Events = p.Events[:7] }, func(p *labMixedProof) { p.Events[7] = p.Events[0] }, func(p *labMixedProof) { p.Events[0].Processed = false }, func(p *labMixedProof) { p.Events[0].Suppressed = true }, func(p *labMixedProof) { p.Events[0].Imported = true }, func(p *labMixedProof) { p.Events[0].PublishedAt = nil }, func(p *labMixedProof) { p.Events[0].InboxID = "" }} {
		value := proof
		value.Events = slices.Clone(proof.Events)
		change(&value)
		if labMixedReadyProof(value, jobs) {
			t.Fatal("partial, replayed or imported proof accepted")
		}
	}
	for _, fragment := range []string{"REPEATABLE READ READ ONLY", "statement_timeout='3s'", "lock_timeout='500ms'", "LIMIT 9", "dashboard_notification_work_quota", "timeout=5", "len(p['jobs'])==8"} {
		if !strings.Contains(labMixedProofScript, fragment) {
			t.Fatal("observer lost an explicit safety boundary")
		}
	}
	for _, forbidden := range []string{"UPDATE ", "DELETE ", "INSERT ", "ALTER ", "CREATE ", "DROP ", "password"} {
		if strings.Contains(labMixedProofScript, forbidden) {
			t.Fatal("read-only observer acquired mutation or secret behavior")
		}
	}
}

func TestLabMixedPacingCancelsWithoutCatchUpBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	if err := labMixedPaced(ctx, time.Now(), time.Hour, 2, func(int) error { calls++; cancel(); return nil }); err == nil || calls != 1 {
		t.Fatal("cancelled polling scheduled another request")
	}
	calls = 0
	if err := labMixedPaced(t.Context(), time.Now().Add(-time.Minute), time.Second, 3, func(int) error { calls++; return nil }); err == nil || calls > 1 {
		t.Fatal("late reader produced an unbounded catch-up burst")
	}
}

type labMixedRoundTripper func(*http.Request) (*http.Response, error)

func (f labMixedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLabMixedTransportBoundsAndSanitizesRemoteFailures(t *testing.T) {
	const canary = "synthetic-bearer-must-never-enter-error"
	gate := &labMixedLimiter{slots: make(chan struct{}, 1)}
	for _, test := range []struct {
		status    int
		body      string
		cache     string
		wantError bool
	}{
		{200, `{"id":"safe"}`, "no-store", false},
		{503, canary, "no-store", true},
		{302, canary, "", true},
		{200, strings.Repeat("x", (4<<20)+1), "no-store", true},
		{200, `{"id":"safe"}`, "public", true},
		{200, canary, "no-store", true},
	} {
		client := labMixedHTTP{origin: "https://dashboard.lab.test:8443", token: canary, gate: gate, client: &http.Client{Transport: labMixedRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://dashboard.lab.test:8443/api/v1/bootstrap" || req.Header.Get("Authorization") != "Bearer "+canary {
				t.Fatal("transport changed the pinned origin or token boundary")
			}
			return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: http.Header{"Cache-Control": []string{test.cache}}}, nil
		})}}
		var output map[string]string
		_, err := client.call(t.Context(), "GET", "/api/v1/bootstrap", "", "", nil, &output, 200)
		if (err != nil) != test.wantError || err != nil && strings.Contains(err.Error(), canary) || len(gate.slots) != 0 {
			t.Fatal("transport failed bounds, sanitized-error or release contract")
		}
	}
	called := false
	client := labMixedHTTP{origin: "https://dashboard.lab.test:8443", token: canary, gate: gate, client: &http.Client{Transport: labMixedRoundTripper(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New(canary) })}}
	if _, err := client.call(t.Context(), "GET", "https://other.example/api/v1/bootstrap", "", "", nil, nil, 200); err == nil || called {
		t.Fatal("unapproved origin reached transport")
	}
}
