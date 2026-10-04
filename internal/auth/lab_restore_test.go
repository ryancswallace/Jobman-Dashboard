//go:build integration && (darwin || linux)

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
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// These individually selected phases never restore a database, release a hold,
// edit a feed, or change a service. The reviewed operator driver runs between
// phases. Receipts contain immutable synthetic identities/hashes, never tokens.
const labRestoreInstance = "e633cf92-258d-48ff-965a-fda88d68ef3a"
const labRestoreCandidate = "b8f25afdd90f83b4602f32440a89e74dfa866b6c"

type labRestoreReport struct {
	Path         string            `json:"path"`
	Key          string            `json:"key"`
	Request      api.ReportRequest `json:"request"`
	TaskID       string            `json:"taskId"`
	ReportSHA256 string            `json:"reportSHA256"`
	Citations    map[string]string `json:"citations"`
}
type labRestoreFeed struct {
	DeploymentID     string     `json:"deploymentId"`
	InstanceID       string     `json:"instanceId"`
	Epoch            string     `json:"epoch"`
	Revision         string     `json:"revision"`
	Namespaces       []string   `json:"namespaces"`
	Status           string     `json:"status"`
	Position         string     `json:"position"`
	CheckpointSHA256 string     `json:"checkpointSHA256"`
	CursorSHA256     string     `json:"cursorSHA256"`
	SuppressThrough  *time.Time `json:"suppressThrough"`
	Gaps             int        `json:"gaps"`
}
type labRestoreEventProof struct {
	EventID    string   `json:"eventId"`
	JobID      string   `json:"jobId"`
	Suppressed bool     `json:"suppressed"`
	Processed  bool     `json:"processed"`
	Pending    int      `json:"pending"`
	InboxIDs   []string `json:"inboxIds"`
}
type labRestoreProof struct {
	Database       string                 `json:"database"`
	ObservedAt     time.Time              `json:"observedAt"`
	Held           bool                   `json:"held"`
	Generation     string                 `json:"generation"`
	RestoreThrough *time.Time             `json:"restoreThrough"`
	Feeds          []labRestoreFeed       `json:"feeds"`
	Events         []labRestoreEventProof `json:"events"`
}
type labRestoreScenario struct {
	Version   int                      `json:"version"`
	Candidate string                   `json:"candidate"`
	Nonce     string                   `json:"nonce"`
	CreatedAt time.Time                `json:"createdAt"`
	Fixtures  []labNotificationFixture `json:"fixtures"`
	RuleID    string                   `json:"ruleId"`
	E1        labNotificationEvent     `json:"e1"`
	InboxID   string                   `json:"inboxId"`
	ReadAt    time.Time                `json:"readAt"`
	Reports   []labRestoreReport       `json:"reports"`
	Baseline  labRestoreProof          `json:"baseline"`
}
type labRestoreLost struct {
	OperationID       string               `json:"operationId"`
	BackupCompletedAt time.Time            `json:"backupCompletedAt"`
	E2                labNotificationEvent `json:"e2"`
	InboxID           string               `json:"inboxId"`
	ExternalCutoff    time.Time            `json:"externalCutoff"`
	Primary           labRestoreProof      `json:"primary"`
}
type labRestoreHarness struct {
	t              *testing.T
	ctx            context.Context
	directory      string
	alice, bob     labNativeSession
	primary, clone *http.Client
}

func labRestorePrivate(path string, maximum int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute private receipt required")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || resolved != filepath.Dir(path) {
		return nil, errors.New("receipt parent must be real")
	}
	parent, err := os.Stat(resolved)
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		return nil, errors.New("private receipt directory required")
	}
	pstat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || pstat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("receipt owner differs")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("bounded private receipt required")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
		return nil, errors.New("private receipt identity differs")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !os.SameFile(info, before) {
		return nil, errors.New("receipt changed before open")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	after, e2 := file.Stat()
	current, e3 := os.Lstat(path)
	if err != nil || e2 != nil || e3 != nil || len(raw) > int(maximum) || int64(len(raw)) != before.Size() || !os.SameFile(before, current) || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		return nil, errors.New("receipt changed during read")
	}
	return raw, nil
}
func labRestoreLoad[T any](t *testing.T, path string) T {
	t.Helper()
	var value T
	raw, err := labRestorePrivate(path, 2<<20)
	if err != nil {
		t.Fatal("Private restore receipt unavailable or unsafe")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		t.Fatal("Restore receipt contract differs")
	}
	return value
}
func labRestoreWrite(t *testing.T, directory, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(raw) > 2<<20 {
		t.Fatal("Restore receipt exceeds bound")
	}
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("Restore phase receipt already exists or cannot be created; inspect before continuing")
	}
	if file.Chmod(0600) != nil {
		file.Close()
		t.Fatal("Private receipt mode failed")
	}
	_, err = file.Write(append(raw, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	dir, openErr := os.Open(directory)
	if openErr != nil || err != nil || syncErr != nil || closeErr != nil {
		t.Fatal("Restore receipt persistence failed; preserve partial file")
	}
	defer dir.Close()
	if dir.Sync() != nil {
		t.Fatal("Restore directory persistence failed")
	}
}
func labRestoreDigest(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func newLabRestoreHarness(t *testing.T, phase string) *labRestoreHarness {
	t.Helper()
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RESTORE") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_RESTORE_PHASE") != phase {
		t.Skip("separate reviewed restore phase opt-in required")
	}
	directory := os.Getenv("JOBMAN_DASHBOARD_LAB_RESTORE_SCENARIO")
	if !filepath.IsAbs(directory) {
		t.Fatal("New private restore scenario directory required")
	}
	info, err := os.Stat(directory)
	resolved, e2 := filepath.EvalSymlinks(directory)
	if err != nil || e2 != nil || resolved != directory || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("Real private scenario directory required")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) {
		t.Fatal("Scenario owner differs")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	t.Cleanup(cancel)
	client := func(port string) *http.Client {
		transport := alice.transport.Clone()
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "dashboard.lab.test:"+port {
				return nil, ErrUnauthenticated
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:"+port)
		}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &labRestoreHarness{t: t, ctx: ctx, directory: directory, alice: alice, bob: bob, primary: client("8443"), clone: client("38443")}
}
func labRestoreCall[T any](h *labRestoreHarness, clone bool, method, path, token, revision, key string, input any, status int) T {
	h.t.Helper()
	var result T
	var raw []byte
	var err error
	if input != nil {
		raw, err = json.Marshal(input)
		if err != nil || len(raw) > 64<<10 {
			h.t.Fatal("Restore request bound exceeded")
		}
	}
	port := "8443"
	client := h.primary
	if clone {
		port = "38443"
		client = h.clone
	}
	if !strings.HasPrefix(path, "/api/v1/") || strings.Contains(path, "#") {
		h.t.Fatal("Invalid restore API path")
	}
	request, err := http.NewRequestWithContext(h.ctx, method, "https://dashboard.lab.test:"+port+path, bytes.NewReader(raw))
	if err != nil {
		h.t.Fatal("Invalid restore request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if revision != "" {
		request.Header.Set("If-Match", `"`+revision+`"`)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := client.Do(request)
	if err != nil {
		h.t.Fatal("Restore API transport unavailable")
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		h.t.Fatal("Restore API response exceeded bound")
	}
	if response.StatusCode != status {
		h.t.Fatalf("Restore API %s returned HTTP%d, expected%d", method, response.StatusCode, status)
	}
	if status >= 200 && status < 300 && status != 204 && json.Unmarshal(raw, &result) != nil {
		h.t.Fatal("Restore API response contract differs")
	}
	return result
}
func (h *labRestoreHarness) scenario(target any, args ...string) {
	h.t.Helper()
	helper := filepath.Join(h.alice.root, "scripts/dashboard-notification-scenario.py")
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		h.t.Fatal("Reviewed normal cancellation helper unavailable")
	}
	ctx, cancel := context.WithTimeout(h.ctx, 55*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", append([]string{helper}, args...)...)
	var out labBoundedNotificationOutput
	command.Stdout = &out
	command.Stderr = io.Discard
	if command.Run() != nil || out.overflow || json.Unmarshal(out.Bytes(), target) != nil {
		h.t.Fatal("Normal cancellation helper failed; retain scenario receipts")
	}
}
func labRestoreValidateFixture(f labNotificationFixture, receipt string) bool {
	return f.Synthetic && f.FixtureVersion == 1 && f.ObservationMode == "normal-cancel-no-execution" && f.Receipt == receipt && f.DeploymentID == labDeployment && f.ControlInstanceID == labRestoreInstance && f.RecoveryEpoch == "1" && f.Namespace == "dashboard-research" && uuid(f.NamespaceID) && len(f.Jobs) == 2 && f.Jobs[0].Case == "first" && f.Jobs[1].Case == "stopped" && uuid(f.Jobs[0].JobID) && uuid(f.Jobs[1].JobID) && f.Jobs[0].JobID != f.Jobs[1].JobID
}
func (h *labRestoreHarness) complete(f labNotificationFixture, which string) labNotificationEvent {
	h.t.Helper()
	index := 0
	if which == "stopped" {
		index = 1
	} else if which != "first" {
		h.t.Fatal("Invalid fixed cancellation case")
	}
	var event labNotificationEvent
	h.scenario(&event, "complete", f.Receipt, which)
	if !event.Synthetic || event.Receipt != f.Receipt || event.Case != which || event.JobID != f.Jobs[index].JobID || event.ControlInstanceID != f.ControlInstanceID || event.DeploymentID != f.DeploymentID || event.NamespaceID != f.NamespaceID || event.RecoveryEpoch != f.RecoveryEpoch || !uuid(event.EventID) || event.Outcome != "cancelled" || event.RecordedAt.IsZero() || event.RunID != "" || event.ExecutionID != "" {
		h.t.Fatal("Original normal-cancel event differs from prepared identity")
	}
	var replay labNotificationEvent
	h.scenario(&replay, "complete", f.Receipt, which)
	if replay != event {
		h.t.Fatal("Idempotent normal cancellation changed original event")
	}
	return event
}
func (h *labRestoreHarness) primarySettled(event labNotificationEvent) {
	h.t.Helper()
	labActualPoll(h.t, h.ctx, 90*time.Second, func() bool {
		var result struct {
			Receipt, Case, EventID string
			Settled                bool
		}
		h.scenario(&result, "settled", event.Receipt, event.Case)
		if result.Receipt != event.Receipt || result.Case != event.Case || result.EventID != event.EventID {
			h.t.Fatal("Processing barrier selected another original event")
		}
		return result.Settled
	})
}
func (h *labRestoreHarness) inbox(clone bool, event labNotificationEvent) []notifications.InboxItem {
	h.t.Helper()
	scope, _ := json.Marshal([]api.Scope{{DeploymentID: event.DeploymentID, NamespaceID: event.NamespaceID}})
	q := url.Values{"scope": {string(scope)}, "limit": {"50"}}
	var found []notifications.InboxItem
	seen := map[string]bool{}
	for pageNo := 0; pageNo < 20; pageNo++ {
		page := labRestoreCall[notifications.InboxPage](h, clone, "GET", "/api/v1/inbox?"+q.Encode(), h.alice.accessToken, "", "", nil, 200)
		if page.Validate() != nil || page.Completeness != "complete" {
			h.t.Fatal("Current restored inbox is incomplete")
		}
		for _, item := range page.Items {
			if item.EventID == event.EventID {
				found = append(found, item)
			}
		}
		if page.NextCursor == "" {
			return found
		}
		if seen[page.NextCursor] {
			h.t.Fatal("Inbox cursor repeated")
		}
		seen[page.NextCursor] = true
		q.Set("cursor", page.NextCursor)
	}
	h.t.Fatal("Restore inbox traversal exceeded 1000 rows")
	return nil
}
func (h *labRestoreHarness) oneInbox(clone bool, event labNotificationEvent, rule string) notifications.InboxItem {
	h.t.Helper()
	items := h.inbox(clone, event)
	if len(items) != 1 {
		h.t.Fatal("Expected exactly one original-event inbox for restored account")
	}
	item := items[0]
	if item.Job.JobID != event.JobID || item.Job.DeploymentID != event.DeploymentID || item.Job.NamespaceID != event.NamespaceID || item.ControlInstanceID != event.ControlInstanceID || item.Outcome != event.Outcome || !item.EventAt.Equal(event.RecordedAt) || !slices.ContainsFunc(item.MatchedRules, func(m notifications.InboxMatch) bool { return m.RuleID == rule }) {
		h.t.Fatal("Inbox original identity or matching intent differs")
	}
	return item
}
func (h *labRestoreHarness) loadScenario() labRestoreScenario {
	h.t.Helper()
	s := labRestoreLoad[labRestoreScenario](h.t, filepath.Join(h.directory, "scenario.json"))
	if s.Version != 1 || s.Candidate != labRestoreCandidate || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(s.Nonce) || len(s.Fixtures) != 2 || !uuid(s.RuleID) || !uuid(s.E1.EventID) || !uuid(s.InboxID) || s.ReadAt.IsZero() || len(s.Reports) != 2 {
		h.t.Fatal("Invalid immutable restore scenario")
	}
	for _, f := range s.Fixtures {
		if !labRestoreValidateFixture(f, f.Receipt) {
			h.t.Fatal("Source scenario differs")
		}
	}
	if s.Fixtures[0].NamespaceID != s.Fixtures[1].NamespaceID || s.Fixtures[0].Receipt == s.Fixtures[1].Receipt {
		h.t.Fatal("Scenario fixtures are not independent")
	}
	return s
}

// Privileged read-only evidence is intentionally limited to these two synthetic
// databases. PostgreSQL enforces read-only and short statement/lock deadlines;
// no payload, account alias, cursor byte or credential is returned.
const labRestoreProofScript = `import json,os,re,subprocess,sys
p=json.load(sys.stdin)
assert os.geteuid()==0 and os.uname().nodename.split('.')[0]=='pg01'
assert set(p)=={'clone','events'} and isinstance(p['clone'],bool) and isinstance(p['events'],list) and 1<=len(p['events'])<=3
assert all(isinstance(v,str) and re.fullmatch('[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}',v) for v in p['events']) and len(set(p['events']))==len(p['events'])
database='jobman_dashboard_restore' if p['clone'] else 'jobman_dashboard'
ids=','.join("'"+v+"'::uuid" for v in p['events'])
query="""BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='3s'; SET LOCAL lock_timeout='500ms';
SELECT json_build_object('database',current_database(),'observedAt',clock_timestamp(),
'held',c.held,'generation',c.generation::text,'restoreThrough',c.restore_recorded_through,
'feeds',(SELECT coalesce(json_agg(row_to_json(x) ORDER BY x.\"deploymentId\"),'[]') FROM (
 SELECT f.deployment_id AS \"deploymentId\",i.control_instance_id AS \"instanceId\",i.recovery_epoch::text AS epoch,
 i.configuration_revision::text AS revision,f.namespace_ids AS namespaces,f.status,f.last_position::text AS position,
 encode(sha256(f.checkpoint),'hex') AS \"checkpointSHA256\",encode(sha256(convert_to(f.cursor,'UTF8')),'hex') AS \"cursorSHA256\",
 f.suppress_recorded_through AS \"suppressThrough\",(SELECT count(*) FROM dashboard_event_gaps g WHERE g.deployment_id=f.deployment_id AND g.resolved_at IS NULL) AS gaps
 FROM dashboard_event_feeds f FULL JOIN dashboard_source_identities i USING(deployment_id) ORDER BY f.deployment_id LIMIT 3) x),
'events',(SELECT coalesce(json_agg(row_to_json(x) ORDER BY x.\"eventId\"),'[]') FROM (
 SELECT e.event_id AS \"eventId\",e.job_id AS \"jobId\",e.notification_suppressed AS suppressed,e.processed_at IS NOT NULL AS processed,
 (SELECT count(*) FROM dashboard_notification_evaluations n WHERE (n.deployment_id,n.control_instance_id,n.event_id)=(e.deployment_id,e.control_instance_id,e.event_id) AND n.state='pending') AS pending,
 (SELECT coalesce(json_agg(n.id ORDER BY n.id),'[]') FROM dashboard_notification_inbox n JOIN dashboard_accounts a ON a.id=n.account_id
 WHERE (n.deployment_id,n.control_instance_id,n.event_id)=(e.deployment_id,e.control_instance_id,e.event_id)
 AND a.directory_id='71000000-0000-4000-8000-000000000001'::uuid) AS \"inboxIds\"
 FROM dashboard_source_events e WHERE e.deployment_id='72000000-0000-4000-8000-000000000001'::uuid
 AND e.control_instance_id='e633cf92-258d-48ff-965a-fda88d68ef3a'::uuid AND e.event_id IN ("""+ids+""") LIMIT 4) x))
FROM dashboard_notification_delivery_control c WHERE singleton;
ROLLBACK;"""
v=subprocess.run(['podman','exec','-i','--user','postgres','jobman-postgres','psql','-X','-q','-A','-t','-U','jobman_control','-v','ON_ERROR_STOP=1','-d',database],input=query.encode(),stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=8)
assert v.returncode==0 and 0<len(v.stdout)<=65536
result=json.loads(v.stdout);assert result['database']==database
print(json.dumps(result,sort_keys=True))
`

func (h *labRestoreHarness) proof(clone bool, eventIDs ...string) labRestoreProof {
	h.t.Helper()
	if len(eventIDs) < 1 || len(eventIDs) > 3 {
		h.t.Fatal("Exact bounded event proof required")
	}
	for _, id := range eventIDs {
		if !uuid(id) {
			h.t.Fatal("Canonical original event required")
		}
	}
	var inventory map[string]struct {
		Host string `json:"ansible_host"`
		User string `json:"ansible_user"`
		Port int    `json:"ansible_port"`
		Key  string `json:"ansible_ssh_private_key_file"`
	}
	raw := labExecutionFile(h.t, filepath.Join(h.alice.root, ".lab/dashboard/ssh-connections.json"), 64<<10)
	if json.Unmarshal(raw, &inventory) != nil {
		h.t.Fatal("Pinned Lab SSH inventory unavailable")
	}
	guest := inventory["pg01"]
	ip := net.ParseIP(guest.Host)
	_, subnet, _ := net.ParseCIDR("10.211.55.0/24")
	if guest.User != "vagrant" || ip == nil || !(ip.IsLoopback() || ip.Equal(net.ParseIP("10.77.0.20")) || subnet.Contains(ip)) || guest.Port < 1 || guest.Port > 65535 || !filepath.IsAbs(guest.Key) {
		h.t.Fatal("Fixed PostgreSQL Lab guest differs")
	}
	ctx, cancel := context.WithTimeout(h.ctx, 20*time.Second)
	defer cancel()
	quoted := "'" + strings.ReplaceAll(labRestoreProofScript, "'", "'\\''") + "'"
	command := exec.CommandContext(ctx, "ssh", "-i", guest.Key, "-p", strconv.Itoa(guest.Port), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+filepath.Join(h.alice.root, ".lab/dashboard/known_hosts"), "-o", "HostKeyAlgorithms=ssh-ed25519", guest.User+"@"+guest.Host, "sudo python3 -c "+quoted)
	payload, _ := json.Marshal(map[string]any{"clone": clone, "events": eventIDs})
	command.Stdin = bytes.NewReader(payload)
	command.Stderr = io.Discard
	var out labExecutionOutput
	command.Stdout = &out
	if command.Run() != nil {
		h.t.Fatal("Read-only restore proof failed")
	}
	var proof labRestoreProof
	if json.Unmarshal(out.Bytes(), &proof) != nil || !labRestoreValidProof(proof, clone) {
		h.t.Fatal("Bounded restore proof source/database contract differs")
	}
	return proof
}
func labRestoreValidProof(p labRestoreProof, clone bool) bool {
	database := "jobman_dashboard"
	if clone {
		database = "jobman_dashboard_restore"
	}
	if p.Database != database || p.ObservedAt.IsZero() || len(p.Feeds) != 2 || len(p.Events) > 3 || !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(p.Generation) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range p.Feeds {
		want := ""
		switch f.DeploymentID {
		case labDeployment:
			want = labRestoreInstance
		case labSecondaryDeployment:
			want = labSecondaryInstance
		default:
			return false
		}
		if seen[f.DeploymentID] || f.InstanceID != want || f.Epoch != "1" || len(f.Namespaces) == 0 || len(f.Namespaces) > 320 || f.Revision == "" || !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(f.Position) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f.CheckpointSHA256) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(f.CursorSHA256) {
			return false
		}
		seen[f.DeploymentID] = true
		for _, id := range f.Namespaces {
			if !uuid(id) {
				return false
			}
		}
	}
	seen = map[string]bool{}
	for _, e := range p.Events {
		if !uuid(e.EventID) || !uuid(e.JobID) || seen[e.EventID] || len(e.InboxIDs) > 1 || e.Pending < 0 {
			return false
		}
		seen[e.EventID] = true
		for _, id := range e.InboxIDs {
			if !uuid(id) {
				return false
			}
		}
	}
	return true
}
func (h *labRestoreHarness) sourceContinuity(before, after labRestoreProof, status string) {
	h.t.Helper()
	for _, old := range before.Feeds {
		index := slices.IndexFunc(after.Feeds, func(f labRestoreFeed) bool { return f.DeploymentID == old.DeploymentID })
		if index < 0 {
			h.t.Fatal("Independent restored source missing")
		}
		current := after.Feeds[index]
		a, err1 := strconv.ParseInt(old.Position, 10, 64)
		b, err2 := strconv.ParseInt(current.Position, 10, 64)
		if err1 != nil || err2 != nil || b < a || old.InstanceID != current.InstanceID || old.Epoch != current.Epoch || old.Revision != current.Revision || !slices.Equal(old.Namespaces, current.Namespaces) || current.Status != status || status == "active" && current.Gaps != 0 {
			h.t.Fatal("Independent restored source authority/checkpoint continuity differs")
		}
	}
}
func (h *labRestoreHarness) eventProof(p labRestoreProof, event labNotificationEvent, present, suppressed, processed bool, inbox string) {
	h.t.Helper()
	index := slices.IndexFunc(p.Events, func(e labRestoreEventProof) bool { return e.EventID == event.EventID })
	if !present {
		if index >= 0 {
			h.t.Fatal("Post-backup original event was already present in restored snapshot")
		}
		return
	}
	if index < 0 {
		h.t.Fatal("Original event missing from restored ledger")
	}
	e := p.Events[index]
	if e.JobID != event.JobID || e.Suppressed != suppressed || processed && (!e.Processed || e.Pending != 0) || inbox == "" && len(e.InboxIDs) != 0 || inbox != "" && !slices.Equal(e.InboxIDs, []string{inbox}) {
		h.t.Fatal("Restored event suppression, settled state or stable account inbox differs")
	}
}
func (h *labRestoreHarness) report(clone bool, record labRestoreReport, compare bool) labRestoreReport {
	h.t.Helper()
	path := record.Path + "/" + record.TaskID
	report := labRestoreCall[api.Report](h, clone, "GET", path, h.alice.accessToken, "", "", nil, 200)
	if report.State != "ready" || report.Detail == nil || report.Outdated || report.ReportID == "" || report.EvidenceID == "" || report.AnalysisEvidenceID == "" || report.TaskID != record.TaskID || report.Profile != record.Request.Profile || report.RunID != record.Request.RunID || report.Detail.ControlInstanceID != labRestoreInstance || report.Detail.Disclosure.ProviderInvoked || report.Detail.Disclosure.GeneratedContentUsed || len(report.Detail.Citations) > 256 {
		h.t.Fatal("Restored sealed pair, selected run or disclosure differs")
	}
	got := record
	got.ReportSHA256 = labRestoreDigest(report)
	got.Citations = map[string]string{}
	logCitations := 0
	for _, ref := range report.Detail.Citations {
		citation := labRestoreCall[api.Citation](h, clone, "GET", path+"/citations/"+url.PathEscape(ref.ID), h.alice.accessToken, "", "", nil, 200)
		if citation.ID != ref.ID || citation.ReportID != report.ReportID || citation.EvidenceID != report.EvidenceID || citation.AnalysisEvidenceID != report.AnalysisEvidenceID || citation.TaskID != report.TaskID || strings.Contains(string(mustLabRestoreJSON(citation)), labDiagnosticCanary) {
			h.t.Fatal("Restored citation identity or redaction differs")
		}
		if citation.BytesBase64 != nil {
			decoded, err := base64.StdEncoding.DecodeString(*citation.BytesBase64)
			if err != nil || bytes.Contains(decoded, []byte(labDiagnosticCanary)) {
				h.t.Fatal("Restored log citation leaked unredacted bytes")
			}
			logCitations++
		}
		got.Citations[ref.ID] = labRestoreDigest(citation)
	}
	if len(got.Citations) == 0 || record.Request.Profile == "include_log_tail" && logCitations == 0 || record.Request.Profile == "metadata" && logCitations != 0 {
		h.t.Fatal("Sealed report citation profile was not exercised")
	}
	if compare && (got.ReportSHA256 != record.ReportSHA256 || !reflect.DeepEqual(got.Citations, record.Citations)) {
		h.t.Fatal("Restored report or citation semantics differ from pre-backup sealed pair")
	}
	replay := labRestoreCall[api.Report](h, clone, "POST", record.Path, h.alice.accessToken, "", record.Key, record.Request, 202)
	if replay.TaskID != record.TaskID {
		h.t.Fatal("Restored idempotency key changed durable task identity")
	}
	other := record.Request
	if other.Profile == "metadata" {
		other.Profile = "include_log_tail"
	} else {
		other.Profile = "metadata"
	}
	labRestoreCall[any](h, clone, "POST", record.Path, h.alice.accessToken, "", record.Key, other, 409)
	labRestoreCall[any](h, clone, "GET", path, h.bob.accessToken, "", "", nil, 404)
	return got
}
func mustLabRestoreJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }

func TestLabRestorePrepare(t *testing.T) {
	h := newLabRestoreHarness(t, "prepare")
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("Fresh restore scenario identity unavailable")
	}
	s := labRestoreScenario{Version: 1, Candidate: labRestoreCandidate, Nonce: hex.EncodeToString(nonce[:]), CreatedAt: time.Now().UTC()}
	receipts := []string{s.Nonce, ""}
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("Second scenario identity unavailable")
	}
	receipts[1] = hex.EncodeToString(nonce[:])
	// A failed/uncertain prepare is not retried automatically. The nonce and exact
	// helper receipts allow inspection and owner-scoped cleanup without SQL edits.
	labRestoreWrite(t, h.directory, "prepare.pending.json", map[string]any{"version": 1, "nonce": s.Nonce, "receipts": receipts, "createdAt": s.CreatedAt})
	for _, receipt := range receipts {
		var f labNotificationFixture
		h.scenario(&f, "prepare", "--receipt", receipt)
		if !labRestoreValidateFixture(f, receipt) {
			t.Fatal("Prepared source jobs differ")
		}
		s.Fixtures = append(s.Fixtures, f)
	}
	if s.Fixtures[0].NamespaceID != s.Fixtures[1].NamespaceID {
		t.Fatal("Restore jobs must share the reviewed research namespace")
	}
	body := notifications.RuleInput{Name: "Restore " + s.Nonce, Enabled: true, Scope: notifications.ScopeNamespaceJobs, Namespaces: []notifications.NamespaceRef{{DeploymentID: labDeployment, NamespaceID: s.Fixtures[0].NamespaceID}}, Jobs: []notifications.JobRef{}, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"cancelled"}}
	rule := labRestoreCall[notifications.RuleView](h, false, "POST", "/api/v1/rules", h.alice.accessToken, "", "", body, 201)
	if !uuid(rule.ID) {
		t.Fatal("Restore rule identity missing")
	}
	s.RuleID = rule.ID
	labRestoreWrite(t, h.directory, "owned-rule.json", map[string]string{"ruleId": s.RuleID, "nonce": s.Nonce})
	labActualPoll(t, h.ctx, 80*time.Second, func() bool {
		rule = labRestoreCall[notifications.RuleView](h, false, "GET", "/api/v1/rules/"+s.RuleID, h.alice.accessToken, "", "", nil, 200)
		return rule.Enabled && len(rule.Scopes) == 1 && rule.Scopes[0].NamespaceID == s.Fixtures[0].NamespaceID && rule.Scopes[0].Status == "active" && rule.Scopes[0].ActivatedAt != nil
	})
	s.E1 = h.complete(s.Fixtures[0], "first")
	h.primarySettled(s.E1)
	item := h.oneInbox(false, s.E1, s.RuleID)
	s.InboxID = item.ID
	changed := labRestoreCall[notifications.InboxItem](h, false, "PATCH", "/api/v1/inbox/"+item.ID, h.alice.accessToken, "", "", map[string]bool{"read": true}, 200)
	if !changed.Read || changed.ReadAt == nil || changed.EventID != s.E1.EventID {
		t.Fatal("Pre-backup inbox read state was not persisted")
	}
	s.ReadAt = *changed.ReadAt
	fixture := readLabDiagnosticFixture(t, h.alice.root, true)
	for _, profile := range []string{"metadata", "include_log_tail"} {
		record := labRestoreReport{Path: "/api/v1/deployments/" + labDeployment + "/namespaces/" + fixture.NamespaceID + "/jobs/" + fixture.JobID + "/reports", Key: "restore-" + s.Nonce + "-" + profile, Request: api.ReportRequest{Profile: profile, RunID: fixture.RunID}}
		admitted := labRestoreCall[api.Report](h, false, "POST", record.Path, h.alice.accessToken, "", record.Key, record.Request, 202)
		if !uuid(admitted.TaskID) {
			t.Fatal("Report admission identity missing")
		}
		record.TaskID = admitted.TaskID
		labActualPoll(t, h.ctx, 90*time.Second, func() bool {
			report := labRestoreCall[api.Report](h, false, "GET", record.Path+"/"+record.TaskID, h.alice.accessToken, "", "", nil, 200)
			if report.State == "failed" {
				t.Fatal("Pre-backup report failed")
			}
			return report.State == "ready"
		})
		s.Reports = append(s.Reports, h.report(false, record, false))
	}
	s.Baseline = h.proof(false, s.E1.EventID)
	if s.Baseline.Held {
		t.Fatal("Primary must remain unheld for ordinary notification acceptance")
	}
	h.sourceContinuity(s.Baseline, s.Baseline, "active")
	h.eventProof(s.Baseline, s.E1, true, false, true, s.InboxID)
	labRestoreWrite(t, h.directory, "scenario.json", s)
	t.Log("Prepared fresh normal-cancel E1/read inbox, two private sealed report profiles and two independent active source baselines; no backup or restore performed")
}

func labRestoreValidRestart(receipt map[string]json.RawMessage, operationID string) bool {
	var elapsed float64
	var receivedID string
	return json.Unmarshal(receipt["operationId"], &receivedID) == nil && receivedID == operationID &&
		bytes.Equal(bytes.TrimSpace(receipt["primaryRestarted"]), []byte("true")) &&
		bytes.Equal(bytes.TrimSpace(receipt["watchdogFired"]), []byte("false")) &&
		json.Unmarshal(receipt["elapsedSinceArmSeconds"], &elapsed) == nil && elapsed > 0 && elapsed <= 180
}

func TestLabRestoreAfterBackup(t *testing.T) {
	h := newLabRestoreHarness(t, "after-backup")
	s := h.loadScenario()
	operation := os.Getenv("JOBMAN_DASHBOARD_LAB_RESTORE_OPERATION")
	// The operation directory was made by the reviewed driver. Unknown additional
	// fields remain inside maps here because driver receipts are deliberately rich.
	complete := labRestoreLoad[map[string]json.RawMessage](t, filepath.Join(operation, "coherent-backup.json"))
	var done bool
	var operationID string
	var completed time.Time
	if json.Unmarshal(complete["complete"], &done) != nil || !done || json.Unmarshal(complete["operationId"], &operationID) != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(operationID) || json.Unmarshal(complete["completedAt"], &completed) != nil || !completed.After(s.E1.RecordedAt) || time.Since(completed) > 20*time.Minute || completed.After(time.Now()) {
		t.Fatal("Exact recent coherent backup receipt required")
	}
	restart := labRestoreLoad[map[string]json.RawMessage](t, filepath.Join(operation, "storage01-restart-primary.json"))
	if !labRestoreValidRestart(restart, operationID) {
		t.Fatal("Successful bounded primary restart receipt required")
	}
	labRestoreWrite(t, h.directory, "after-backup.pending.json", map[string]any{"operationId": operationID, "backupCompletedAt": completed})
	e2 := h.complete(s.Fixtures[0], "stopped")
	if !e2.RecordedAt.After(completed) {
		t.Fatal("E2 did not occur strictly after coordinated backup")
	}
	h.primarySettled(e2)
	inbox := h.oneInbox(false, e2, s.RuleID)
	// Only primary test intent is stopped. Restored intent will remain enabled
	// from the earlier snapshot and must produce E3 after the reviewed release.
	current := labRestoreCall[notifications.RuleView](h, false, "GET", "/api/v1/rules/"+s.RuleID, h.alice.accessToken, "", "", nil, 200)
	stopped := labRestoreCall[notifications.RuleView](h, false, "PUT", "/api/v1/rules/"+s.RuleID+"/enabled", h.alice.accessToken, current.Revision, "", map[string]bool{"enabled": false}, 200)
	if stopped.Enabled {
		t.Fatal("Primary test-owned rule did not stop")
	}
	proof := h.proof(false, s.E1.EventID, e2.EventID)
	h.sourceContinuity(s.Baseline, proof, "active")
	h.eventProof(proof, e2, true, false, true, inbox.ID)
	cutoff := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	if proof.ObservedAt.After(cutoff) || e2.RecordedAt.After(cutoff) {
		t.Fatal("Clock agreement cannot establish conservative external cutoff")
	}
	timer := time.NewTimer(time.Until(cutoff))
	defer timer.Stop()
	select {
	case <-h.ctx.Done():
		t.Fatal("Cutoff margin wait expired")
	case <-timer.C:
	}
	lost := labRestoreLost{OperationID: operationID, BackupCompletedAt: completed, E2: e2, InboxID: inbox.ID, ExternalCutoff: cutoff, Primary: proof}
	labRestoreWrite(t, h.directory, "lost-interval.json", lost)
	t.Log("E2 processed once on primary after backup; primary test rule stopped and external UTC cutoff retained. Clone restore/recovery remains a separately reviewed operator action")
}
func (h *labRestoreHarness) loadLost(s labRestoreScenario) labRestoreLost {
	h.t.Helper()
	lost := labRestoreLoad[labRestoreLost](h.t, filepath.Join(h.directory, "lost-interval.json"))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(lost.OperationID) || lost.E2.JobID != s.Fixtures[0].Jobs[1].JobID || !uuid(lost.E2.EventID) || lost.E2.EventID == s.E1.EventID || !lost.E2.RecordedAt.After(lost.BackupCompletedAt) || !lost.ExternalCutoff.After(lost.E2.RecordedAt) || !uuid(lost.InboxID) {
		h.t.Fatal("Lost-interval receipt does not bind the prepared E2 and external cutoff")
	}
	return lost
}
func (h *labRestoreHarness) assertFloor(p labRestoreProof, lost labRestoreLost, held bool) {
	h.t.Helper()
	if p.Held != held || p.RestoreThrough == nil || !p.RestoreThrough.Equal(lost.ExternalCutoff) {
		h.t.Fatal("Clone hold or monotonic external restore floor differs")
	}
	for _, f := range p.Feeds {
		if f.SuppressThrough == nil || !f.SuppressThrough.Equal(lost.ExternalCutoff) {
			h.t.Fatal("Restore floor omitted an independent source")
		}
	}
}
func (h *labRestoreHarness) preserved(s labRestoreScenario) {
	h.t.Helper()
	item := h.oneInbox(true, s.E1, s.RuleID)
	if item.ID != s.InboxID || !item.Read || item.ReadAt == nil || !item.ReadAt.Equal(s.ReadAt) {
		h.t.Fatal("Restored E1 identity/read state differs")
	}
	labRestoreCall[any](h, true, "GET", "/api/v1/inbox/"+s.InboxID, h.bob.accessToken, "", "", nil, 404)
	for _, record := range s.Reports {
		h.report(true, record, true)
	}
}
func TestLabRestoreHeld(t *testing.T) {
	h := newLabRestoreHarness(t, "held")
	s := h.loadScenario()
	lost := h.loadLost(s)
	proof := h.proof(true, s.E1.EventID, lost.E2.EventID)
	h.assertFloor(proof, lost, true)
	h.sourceContinuity(s.Baseline, proof, "paused")
	for _, feed := range proof.Feeds {
		if feed.Gaps != 1 {
			t.Fatal("Each restored source must retain its explicit recovery gap")
		}
	}
	h.eventProof(proof, s.E1, true, false, true, s.InboxID)
	h.eventProof(proof, lost.E2, false, false, false, "")
	h.preserved(s)
	if len(h.inbox(true, lost.E2)) != 0 {
		t.Fatal("E2 incorrectly existed in restored inbox before replay")
	}
	rule := labRestoreCall[notifications.RuleView](h, true, "GET", "/api/v1/rules/"+s.RuleID, h.alice.accessToken, "", "", nil, 200)
	if !rule.Enabled {
		t.Fatal("Restored pre-backup alert intent was lost")
	}
	labRestoreWrite(t, h.directory, "held-verified.json", proof)
	t.Log("Clone remains held: exact E1/read state, both report/citation profiles and idempotency survived; E2 is absent and both independent sources are paused for explicit recovery")
}
func TestLabRestoreReplayed(t *testing.T) {
	h := newLabRestoreHarness(t, "replayed")
	s := h.loadScenario()
	lost := h.loadLost(s)
	prior := labRestoreLoad[labRestoreProof](t, filepath.Join(h.directory, "held-verified.json"))
	proof := h.proof(true, s.E1.EventID, lost.E2.EventID)
	h.assertFloor(proof, lost, true)
	h.sourceContinuity(prior, proof, "active")
	if proof.Generation != prior.Generation {
		t.Fatal("Replay unexpectedly changed global hold")
	}
	h.eventProof(proof, s.E1, true, true, true, s.InboxID)
	h.eventProof(proof, lost.E2, true, true, false, "")
	h.preserved(s)
	if len(h.inbox(true, lost.E2)) != 0 {
		t.Fatal("Uncertain lost-interval event created a restored alert")
	}
	labRestoreWrite(t, h.directory, "replayed-verified.json", proof)
	t.Log("Both independently recovered feeds are active while global hold remains set; E1 retains original inbox and E2 retains original event UUID with suppression and no inbox")
}
func TestLabRestoreResumed(t *testing.T) {
	h := newLabRestoreHarness(t, "resumed")
	s := h.loadScenario()
	lost := h.loadLost(s)
	prior := labRestoreLoad[labRestoreProof](t, filepath.Join(h.directory, "replayed-verified.json"))
	proof := h.proof(true, s.E1.EventID, lost.E2.EventID)
	h.assertFloor(proof, lost, false)
	h.sourceContinuity(prior, proof, "active")
	a, e1 := strconv.ParseInt(prior.Generation, 10, 64)
	b, e2 := strconv.ParseInt(proof.Generation, 10, 64)
	if e1 != nil || e2 != nil || b != a+1 || !time.Now().After(lost.ExternalCutoff.Add(2*time.Second)) {
		t.Fatal("Separate reviewed release or post-cutoff margin not established")
	}
	labRestoreWrite(t, h.directory, "resumed.pending.json", map[string]any{"operationId": lost.OperationID, "generation": proof.Generation})
	event := h.complete(s.Fixtures[1], "first")
	if !event.RecordedAt.After(lost.ExternalCutoff.Add(2 * time.Second)) {
		t.Fatal("E3 source clock did not pass conservative cutoff margin")
	}
	labActualPoll(t, h.ctx, 90*time.Second, func() bool {
		proof = h.proof(true, s.E1.EventID, lost.E2.EventID, event.EventID)
		for _, id := range []string{lost.E2.EventID, event.EventID} {
			index := slices.IndexFunc(proof.Events, func(v labRestoreEventProof) bool { return v.EventID == id })
			if index < 0 || !proof.Events[index].Processed || proof.Events[index].Pending != 0 {
				return false
			}
		}
		return true
	})
	item := h.oneInbox(true, event, s.RuleID)
	if item.ID == s.InboxID || item.ID == lost.InboxID {
		t.Fatal("New event reused a different original inbox")
	}
	h.assertFloor(proof, lost, false)
	h.sourceContinuity(prior, proof, "active")
	h.eventProof(proof, s.E1, true, true, true, s.InboxID)
	h.eventProof(proof, lost.E2, true, true, true, "")
	h.eventProof(proof, event, true, false, true, item.ID)
	h.preserved(s)
	if len(h.inbox(true, lost.E2)) != 0 {
		t.Fatal("Suppressed original event reappeared after resume")
	}
	labRestoreWrite(t, h.directory, "resumed-verified.json", map[string]any{"event": event, "inboxId": item.ID, "proof": proof, "synthetic": true, "deliveryComponent": false, "apnsAcceptance": false})
	t.Log("Post-cutoff E3 created exactly one new clone inbox; E1/read/report/citation identities remain stable and replayed E2 remains suppressed. No provider delivery or exactly-once APNs claim")
}
func TestLabRestoreCleanup(t *testing.T) {
	h := newLabRestoreHarness(t, "cleanup")
	s := h.loadScenario()
	_ = labRestoreLoad[map[string]json.RawMessage](t, filepath.Join(h.directory, "resumed-verified.json"))
	labRestoreWrite(t, h.directory, "cleanup.pending.json", map[string]string{"ruleId": s.RuleID})
	for _, clone := range []bool{false, true} {
		current := labRestoreCall[notifications.RuleView](h, clone, "GET", "/api/v1/rules/"+s.RuleID, h.alice.accessToken, "", "", nil, 200)
		if current.ID != s.RuleID {
			t.Fatal("Cleanup owner identity differs")
		}
		labRestoreCall[any](h, clone, "DELETE", "/api/v1/rules/"+s.RuleID, h.alice.accessToken, current.Revision, "", nil, 204)
	}
	unused := h.complete(s.Fixtures[1], "stopped")
	h.primarySettled(unused)
	labActualPoll(t, h.ctx, 90*time.Second, func() bool {
		p := h.proof(true, unused.EventID)
		return len(p.Events) == 1 && p.Events[0].Processed && p.Events[0].Pending == 0
	})
	for _, clone := range []bool{false, true} {
		for _, item := range h.inbox(clone, unused) {
			if slices.ContainsFunc(item.MatchedRules, func(m notifications.InboxMatch) bool { return m.RuleID == s.RuleID }) {
				t.Fatal("Removed test intent matched unused job cancellation")
			}
		}
	}
	labRestoreWrite(t, h.directory, "cleanup-complete.json", map[string]any{"ruleId": s.RuleID, "unusedEvent": unused, "originalDataDeleted": false, "cloneServicesStopped": false})
	t.Log("Only owned personal rules removed and unused fourth job normally cancelled; all data/backup receipts retained, clone service retirement remains operator-owned")
}

func TestLabRestorePrivateReceiptBoundaries(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "receipt.json")
	if err = os.WriteFile(path, []byte(`{"safe":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := labRestorePrivate(path, 128); err != nil || string(raw) != `{"safe":true}` {
		t.Fatal("valid receipt rejected", err)
	}
	if _, err = labRestorePrivate(path, 2); err == nil {
		t.Fatal("oversize receipt accepted")
	}
	link := filepath.Join(root, "symlink")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = labRestorePrivate(link, 128); err == nil {
		t.Fatal("symbolic receipt accepted")
	}
	hard := filepath.Join(root, "hardlink")
	if err = os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	if _, err = labRestorePrivate(path, 128); err == nil {
		t.Fatal("multiply linked receipt accepted")
	}
	if err = os.Remove(hard); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = labRestorePrivate(path, 128); err == nil {
		t.Fatal("public receipt accepted")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = labRestorePrivate(path, 128); err == nil {
		t.Fatal("public parent accepted")
	}
}
func TestLabRestoreProofRejectsRebindingAndAmbiguity(t *testing.T) {
	f := func(id, instance string) labRestoreFeed {
		return labRestoreFeed{DeploymentID: id, InstanceID: instance, Epoch: "1", Revision: "7", Namespaces: []string{"4156b832-9be8-40ff-a471-cb3061b6001d"}, Position: "1", CheckpointSHA256: strings.Repeat("a", 64), CursorSHA256: strings.Repeat("b", 64)}
	}
	valid := labRestoreProof{Database: "jobman_dashboard_restore", ObservedAt: time.Now(), Generation: "4", Feeds: []labRestoreFeed{f(labDeployment, labRestoreInstance), f(labSecondaryDeployment, labSecondaryInstance)}}
	if !labRestoreValidProof(valid, true) {
		t.Fatal("valid fixed clone proof rejected")
	}
	tests := []func(*labRestoreProof){func(p *labRestoreProof) { p.Database = "jobman_dashboard" }, func(p *labRestoreProof) { p.Feeds = p.Feeds[:1] }, func(p *labRestoreProof) { p.Feeds[1] = p.Feeds[0] }, func(p *labRestoreProof) { p.Feeds[1].InstanceID = labRestoreInstance }, func(p *labRestoreProof) { p.Feeds[1].Epoch = "2" }, func(p *labRestoreProof) { p.Feeds[0].CheckpointSHA256 = "opaque-cursor" }, func(p *labRestoreProof) { p.Feeds[0].Position = "-1" }, func(p *labRestoreProof) { p.Feeds[0].Namespaces = []string{"not-uuid"} }, func(p *labRestoreProof) { p.Events = []labRestoreEventProof{{EventID: "not-uuid"}} }}
	for n, change := range tests {
		test := valid
		test.Feeds = slices.Clone(valid.Feeds)
		change(&test)
		if labRestoreValidProof(test, true) {
			t.Fatalf("invalid proof%d accepted", n)
		}
	}
	if labRestoreValidProof(valid, false) {
		t.Fatal("clone proof accepted for primary")
	}
	// The proof is SQL-enforced read-only, bounded and tied to exact two database
	// names. Destructive/DDL verbs are absent; values are only canonical UUIDs.
	for _, required := range []string{"REPEATABLE READ READ ONLY", "statement_timeout='3s'", "lock_timeout='500ms'", "LIMIT 3", "LIMIT 4", "sha256(f.checkpoint)", "timeout=8"} {
		if !strings.Contains(labRestoreProofScript, required) {
			t.Fatal("proof safety invariant missing", required)
		}
	}
	for _, forbidden := range []string{"UPDATE ", "DELETE ", "INSERT ", "ALTER ", "DROP ", "CREATE ", "password"} {
		if strings.Contains(labRestoreProofScript, forbidden) {
			t.Fatal("read proof acquired mutation or secret handling")
		}
	}
}

func TestLabRestoreRestartReceiptRequiresExactCompletedOperation(t *testing.T) {
	id := strings.Repeat("a", 64)
	value := map[string]json.RawMessage{"operationId": json.RawMessage(`"` + id + `"`), "primaryRestarted": json.RawMessage(`true`), "watchdogFired": json.RawMessage(`false`), "elapsedSinceArmSeconds": json.RawMessage(`12.5`)}
	if !labRestoreValidRestart(value, id) {
		t.Fatal("valid completed restart rejected")
	}
	for _, change := range []struct{ key, value string }{{"operationId", `"` + strings.Repeat("b", 64) + `"`}, {"operationId", ""}, {"primaryRestarted", `false`}, {"primaryRestarted", ""}, {"watchdogFired", `true`}, {"watchdogFired", ""}, {"watchdogFired", `null`}, {"elapsedSinceArmSeconds", `0`}, {"elapsedSinceArmSeconds", `181`}, {"elapsedSinceArmSeconds", `"12"`}} {
		copy := map[string]json.RawMessage{}
		for key, raw := range value {
			copy[key] = raw
		}
		copy[change.key] = json.RawMessage(change.value)
		if labRestoreValidRestart(copy, id) {
			t.Fatal("incomplete or unrelated restart accepted", change.key)
		}
	}
}
