//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// Each test is a separately reviewed operation. No test installs binaries,
// configures a host, changes grants, stops PostgreSQL/NFS, or resets a feed.
func TestLabDependencyDirectory(t *testing.T) { labFaultRun(t, "directory") }
func TestLabDependencyBroker(t *testing.T)    { labFaultRun(t, "broker") }
func TestLabDependencyDatabase(t *testing.T)  { labFaultRun(t, "database") }

type labFaultDriver struct{ script, staging, root, planSHA, implementationSHA string }

func (d labFaultDriver) call(ctx context.Context, phase, fault string, output any) error {
	if !slices.Contains([]string{"begin", "recover", "status", "verify", "observe-api", "close"}, phase) {
		return errors.New("fault phase denied")
	}
	args := []string{d.script, phase, "--lab-root", d.root, "--staging", d.staging, "--expected-plan-sha256", d.planSHA, "--expected-implementation-sha256", d.implementationSHA}
	if fault != "" {
		args = append(args, "--fault", fault)
	}
	if phase == "begin" || phase == "recover" {
		args = append(args, "--apply")
	}
	bound := 45 * time.Second
	if phase == "begin" {
		bound = 155 * time.Second
	}
	call, stop := context.WithTimeout(ctx, bound)
	defer stop()
	command := exec.CommandContext(call, "python3", args...)
	command.Stderr = io.Discard
	var raw labExecutionOutput
	command.Stdout = &raw
	if command.Run() != nil || json.Unmarshal(raw.Bytes(), output) != nil {
		return errors.New("reviewed fault phase failed; retain pending receipts")
	}
	return nil
}

type labFaultSample struct {
	Label        string  `json:"label"`
	Status       int     `json:"status"`
	Code         string  `json:"code,omitempty"`
	RequestID    string  `json:"requestId,omitempty"`
	Milliseconds float64 `json:"milliseconds"`
}
type labFaultHTTP struct {
	client  *http.Client
	token   string
	mu      *sync.Mutex
	samples *[]labFaultSample
}

func (c labFaultHTTP) read(ctx context.Context, label, path string, want int, code string, output any) error {
	_, err := c.request(ctx, label, path, want, code, output, false)
	return err
}

// Recovery may retry only a bounded, sanitized dependency-unavailable response.
// Authentication rejection, changed cookies, malformed bodies and transport
// failures remain failures; a running process is not proof of authorized reads.
func (c labFaultHTTP) request(ctx context.Context, label, path string, want int, code string, output any, recovery bool) (int, error) {
	start := time.Now()
	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := labWebExchange(ctx, c.client, "GET", labWebOrigin+path, header, nil)
	sample := labFaultSample{Label: label, Status: response.status, Milliseconds: float64(time.Since(start).Microseconds()) / 1000}
	if response.status >= 400 {
		failure, validJSON := labFaultDecodeError(response.body)
		if recovery && slices.Contains([]string{"source_unavailable", "authorization_unavailable"}, failure.Code) {
			code = failure.Code
		}
		if !validJSON || !labFaultError(failure, response.status, code) {
			err = errors.New("fault response did not contain only the sanitized expected error")
		} else {
			sample.Code, sample.RequestID = failure.Code, failure.RequestID
		}
	}
	c.mu.Lock()
	if len(*c.samples) < 128 {
		*c.samples = append(*c.samples, sample)
	} else {
		err = errors.New("fault sample bound reached")
	}
	c.mu.Unlock()
	if err != nil {
		return response.status, errors.New("fault HTTP transport, bounded body, or safe error contract failed")
	}
	if (response.status != want && !(recovery && response.status == 503)) || response.header.Get("Cache-Control") != "no-store" {
		return response.status, fmt.Errorf("fault HTTP status/cache contract differs (HTTP%d)", response.status)
	}
	if strings.HasPrefix(path, "/api/") && len(response.header.Values("Set-Cookie")) > 0 {
		return response.status, errors.New("API fault cleared or replaced a valid session")
	}
	if ctx.Err() != nil {
		return response.status, errors.New("fault HTTP deadline elapsed")
	}
	if output != nil && response.status == want && json.Unmarshal(response.body, output) != nil {
		return response.status, errors.New("fault response DTO invalid")
	}
	return response.status, nil
}

func (c labFaultHTTP) recoverLog(ctx context.Context, path string, output *api.LogRange) error {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return c.recoverLogWithin(bounded, path, output, time.Second)
}

func (c labFaultHTTP) recoverLogWithin(ctx context.Context, path string, output *api.LogRange, interval time.Duration) error {
	for attempt := 0; attempt < 16; attempt++ {
		var value api.LogRange
		status, err := c.request(ctx, "restored-original-log", path, 200, "", &value, true)
		if err != nil {
			return err
		}
		if status == 200 {
			*output = value
			return nil
		}
		if attempt < 15 {
			if labMixedWait(ctx, interval) != nil {
				return errors.New("authorized broker log recovery deadline elapsed")
			}
		}
	}
	return errors.New("authorized broker log recovery attempt bound reached")
}

// Reject duplicate keys as well as unknown fields and trailing JSON. A decoder
// into a struct alone could overwrite an earlier private message with a safe one.
func labFaultDecodeError(raw []byte) (api.Error, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return api.Error{}, false
	}
	fields := map[string]string{}
	for dec.More() {
		token, err := dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || !slices.Contains([]string{"code", "message", "requestId"}, key) {
			return api.Error{}, false
		}
		if _, exists := fields[key]; exists {
			return api.Error{}, false
		}
		var value string
		if dec.Decode(&value) != nil {
			return api.Error{}, false
		}
		fields[key] = value
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') || len(fields) != 3 {
		return api.Error{}, false
	}
	if _, err = dec.Token(); err != io.EOF {
		return api.Error{}, false
	}
	return api.Error{Code: fields["code"], Message: fields["message"], RequestID: fields["requestId"]}, true
}

func labFaultError(e api.Error, status int, code string) bool {
	id, err := hex.DecodeString(e.RequestID)
	if status != 503 || e.Code != code || err != nil || len(id) != 16 || hex.EncodeToString(id) != e.RequestID {
		return false
	}
	messages := map[string][]string{
		"authorization_unavailable": {"Current source authorization could not be verified."},
		"source_unavailable":        {"The source is unavailable. Retry when private connectivity is restored.", "The request could not be completed. Retry or contact the operator with the request ID."},
	}
	return slices.Contains(messages[code], e.Message)
}

type labFaultState struct {
	t                                 *testing.T
	ctx                               context.Context
	driver                            labFaultDriver
	alice, bob                        labFaultHTTP
	web                               labFaultHTTP
	sessions                          [2]labNativeSession
	bootstrap                         [2]api.Bootstrap
	webCookie, csrf                   string
	slurm                             labMixedSlurm
	run                               api.RunReference
	jobPath, reportPath, citationPath string
	report                            api.Report
	citation                          api.Citation
	logs                              api.LogRange
	primary, secondary                api.Scope
	primaryJob, secondaryJob          string
	inbox                             notifications.InboxItem
	active                            string
	samples                           []labFaultSample
	mu                                sync.Mutex
	ruleID                            string
	uncertainRule                     bool
	ruleStopped                       bool
	fixture                           labMultiNotificationFixture
	event                             labNotificationEvent
	started                           time.Time
	result                            *os.File
}

func (s *labFaultState) must(err error) {
	s.t.Helper()
	if err != nil {
		s.t.Fatal(err.Error())
	}
}
func (s *labFaultState) begin(fault string, reserve time.Duration) {
	s.t.Helper()
	deadline, _ := s.ctx.Deadline()
	if time.Until(deadline) < reserve {
		s.t.Fatal("insufficient remaining fault/recovery budget; no fault begun")
	}
	s.active = fault // Cleanup is already registered; even a lost begin response is uncertain.
	var result struct {
		OperationID, Fault        string
		Applied                   bool
		WatchdogDeadlineMonotonic float64
	}
	s.must(s.driver.call(s.ctx, "begin", fault, &result))
	if !result.Applied || result.Fault != fault || !uuid(result.OperationID) || result.WatchdogDeadlineMonotonic <= 0 {
		s.t.Fatal("fault admission or independent watchdog acknowledgement invalid")
	}
}
func (s *labFaultState) recover() {
	var restored struct {
		Restored bool
		Fault    string
	}
	s.must(s.driver.call(s.ctx, "recover", s.active, &restored))
	if !restored.Restored || restored.Fault != s.active {
		s.t.Fatal("fault restoration unconfirmed")
	}
	var verified struct {
		Verified bool
		Fault    string
	}
	s.must(s.driver.call(s.ctx, "verify", s.active, &verified))
	if !verified.Verified || verified.Fault != s.active {
		s.t.Fatal("fault authority/process restoration unverified")
	}
	s.active = ""
}
func (s *labFaultState) healthy() {
	for index, c := range []labFaultHTTP{s.alice, s.bob} {
		var value api.Bootstrap
		s.must(c.read(s.ctx, "restored-bootstrap", "/api/v1/bootstrap", 200, "", &value))
		if value.Account != s.bootstrap[index].Account || !labFaultGrantsSame(s.bootstrap[index], value) {
			s.t.Fatal("same credential did not regain exact prior namespace roles/capabilities")
		}
	}
	var current api.JobDetail
	s.must(s.alice.read(s.ctx, "restored-actual-job", s.jobPath, 200, "", &current))
	run, err := s.slurm.pinnedRun(current)
	s.must(err)
	if run != s.run {
		s.t.Fatal("actual accepted execution changed")
	}
	var report api.Report
	var citation api.Citation
	s.must(s.alice.read(s.ctx, "restored-report", s.reportPath, 200, "", &report))
	s.must(s.alice.read(s.ctx, "restored-citation", s.citationPath, 200, "", &citation))
	if labRestoreDigest(report) != labRestoreDigest(s.report) || !reflect.DeepEqual(citation, s.citation) {
		s.t.Fatal("sealed report or citation changed across fault")
	}
	var b api.Bootstrap
	s.must(s.web.read(s.ctx, "restored-web-session", "/api/v1/bootstrap", 200, "", &b))
	if b.Account != s.bootstrap[0].Account || b.CSRFToken != s.csrf {
		s.t.Fatal("same web session was replaced or lost")
	}
}
func labFaultGrantsSame(a, b api.Bootstrap) bool {
	if b.FixtureMode || b.Completeness != "complete" || len(a.Deployments) != len(b.Deployments) {
		return false
	}
	type grant struct {
		Deployment, Namespace string
		Roles, Capabilities   []string
	}
	project := func(v api.Bootstrap) []grant {
		out := []grant{}
		for _, d := range v.Deployments {
			for _, n := range d.Namespaces {
				out = append(out, grant{d.ID, n.ID, n.Roles, n.Capabilities})
			}
		}
		return out
	}
	return reflect.DeepEqual(project(a), project(b))
}

func labFaultRun(t *testing.T, scenario string) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_DEPENDENCY_FAULT") != scenario {
		t.Skip("explicit exact reviewed dependency fault opt-in required")
	}
	deadline, ok := t.Deadline()
	if !ok || time.Until(deadline) > 6*time.Minute || time.Until(deadline) < 5*time.Minute {
		t.Fatal("each fault test requires a separate -timeout=6m invocation")
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline.Add(-65*time.Second))
	defer cancel()
	s := &labFaultState{t: t, ctx: ctx, started: time.Now()}
	s.driver = labFaultLoadDriver(t, scenario)
	output := os.Getenv("JOBMAN_DASHBOARD_LAB_FAULT_RESULT")
	if !filepath.IsAbs(output) {
		t.Fatal("fresh absolute fault result required")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil || parent != filepath.Dir(output) {
		t.Fatal("fault result parent alias denied")
	}
	s.result, err = os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	s.must(err)
	s.must(s.result.Chmod(0600))
	directory, err := os.Open(parent)
	if err != nil {
		_ = s.result.Close()
		t.Fatal("fault evidence directory unavailable")
	}
	err = directory.Sync()
	err = errors.Join(err, directory.Close())
	if err != nil {
		_ = s.result.Close()
		t.Fatal("fault evidence directory could not be synced")
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if s.active != "" {
			var value map[string]any
			if s.driver.call(cleanup, "recover", s.active, &value) != nil || value["restored"] != true {
				t.Error("fault cleanup unconfirmed; independent guest watchdog and private receipts require operator inspection")
			}
		}
		if s.ruleID != "" && !s.ruleStopped {
			s.cleanupRule(cleanup)
		}
		if s.uncertainRule {
			t.Error("rule admission was uncertain; inspect the exact private progress before creating any replacement")
		}
		if s.webCookie != "" {
			if s.webCookie == "uncertain" {
				s.webCookie = ""
			}
			bare := *s.web.client
			bare.Jar = nil
			if labWebCleanupSession(cleanup, s.web.client, &bare, s.webCookie, s.csrf) != nil {
				t.Error("test web session cleanup unconfirmed")
			}
		}
		result := map[string]any{"scenario": scenario, "startedAt": s.started.UTC(), "completedAt": time.Now().UTC(), "planSHA256": s.driver.planSHA, "samples": s.samples, "passed": !t.Failed(), "ruleId": s.ruleID, "ruleStopped": s.ruleStopped, "ruleAdmissionUncertain": s.uncertainRule, "sourceFixture": s.fixture, "sourceEvent": s.event,
			"limits": "Synthetic Lab transport faults and real HTTP only; no corporate AD FS, APNs, hard-NFS stall, or client rendering claim."}
		if json.NewEncoder(s.result).Encode(result) != nil || s.result.Sync() != nil || s.result.Close() != nil {
			t.Error("fault evidence could not be retained")
		}
	})
	s.sessions = [2]labNativeSession{labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001"), labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")}
	for index, session := range s.sessions {
		c := newLabNotificationHTTP(t, session).client
		c.Timeout = 19 * time.Second
		c.Transport.(*http.Transport).ResponseHeaderTimeout = 18 * time.Second
		c.Transport.(*http.Transport).MaxConnsPerHost = 4
		client := labFaultHTTP{client: c, token: session.accessToken, mu: &s.mu, samples: &s.samples}
		if index == 0 {
			s.alice = client
		} else {
			s.bob = client
		}
		s.must(client.read(ctx, "baseline-bootstrap", "/api/v1/bootstrap", 200, "", &s.bootstrap[index]))
		if s.bootstrap[index].FixtureMode || s.bootstrap[index].Completeness != "complete" {
			t.Fatal("complete healthy authority required")
		}
	}
	s.webSession()
	s.baseline()
	if time.Since(s.started) > 90*time.Second {
		t.Fatal("setup exceeded reserved budget; no fault begun")
	}
	switch scenario {
	case "directory":
		s.directory()
	case "broker":
		s.broker()
	case "database":
		s.database()
	}
	s.healthy()
	var closed struct{ Closed bool }
	s.must(s.driver.call(ctx, "close", "", &closed))
	if !closed.Closed {
		t.Fatal("fault operation did not close with complete recovery proofs")
	}
}

func labFaultLoadDriver(t *testing.T, scenario string) labFaultDriver {
	t.Helper()
	d := labFaultDriver{os.Getenv("JOBMAN_DASHBOARD_LAB_FAULT_DRIVER"), os.Getenv("JOBMAN_DASHBOARD_LAB_FAULT_PLAN"), os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT"), os.Getenv("JOBMAN_DASHBOARD_LAB_FAULT_PLAN_SHA256"), os.Getenv("JOBMAN_DASHBOARD_LAB_FAULT_IMPLEMENTATION_SHA256")}
	for _, path := range []string{d.script, d.staging, d.root} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("reviewed driver paths must be absolute")
		}
	}
	raw, err := labScaleReadFile(filepath.Join(d.staging, "plan.json"), 2<<20)
	if err != nil {
		t.Fatal("reviewed fault plan unavailable")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != d.planSHA {
		t.Fatal("reviewed fault plan changed")
	}
	var plan struct {
		Scenario, Revision string
		Synthetic          bool
		Implementation     map[string]string `json:"implementationSHA256"`
	}
	if json.Unmarshal(raw, &plan) != nil || !plan.Synthetic || plan.Scenario != scenario || len(plan.Revision) != 40 || len(plan.Implementation) != 3 {
		t.Fatal("fault plan differs")
	}
	for _, name := range []string{"dashboard-dependency-fault-plan.py", "dashboard-dependency-fault-guest.py", "dashboard-dependency-faults.py"} {
		bytes, err := labScaleReadFile(filepath.Join(filepath.Dir(d.script), name), 256<<10)
		hash := sha256.Sum256(bytes)
		if err != nil || hex.EncodeToString(hash[:]) != plan.Implementation[name] {
			t.Fatal("fault driver source differs from reviewed manifest")
		}
	}
	implementationBytes, marshalErr := json.MarshalIndent(plan.Implementation, "", "  ")
	implementationHash := sha256.Sum256(append(implementationBytes, '\n'))
	if marshalErr != nil || hex.EncodeToString(implementationHash[:]) != d.implementationSHA || filepath.Base(d.script) != "dashboard-dependency-faults.py" {
		t.Fatal("fault driver identity invalid")
	}
	return d
}

func (s *labFaultState) webSession() {
	transport := s.sessions[0].transport.Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 18 * time.Second
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		target := map[string]string{"dashboard.lab.test:8443": "10.77.0.10:8443", "oidc.lab.test:8443": "10.77.0.21:8443"}[address]
		if target == "" {
			return nil, errors.New("fault web destination denied")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, target)
	}
	s.t.Cleanup(transport.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	s.must(err)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 19 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.web = labFaultHTTP{client: client, mu: &s.mu, samples: &s.samples}
	exchange := func(method, path string, headers http.Header, body []byte, want int) labWebResponse {
		value, err := labWebExchange(s.ctx, client, method, path, headers, body)
		s.must(err)
		if value.status != want {
			s.t.Fatal("fault web setup protocol step failed")
		}
		return value
	}
	login := exchange("GET", labWebOrigin+"/auth/login?returnTo=%2Fsettings", nil, nil, 302)
	state, err := labWebCookie(login.header, loginCookie, false)
	s.must(err)
	authorize, err := labWebAuthorization(login.header, state.Value)
	s.must(err)
	form := exchange("GET", authorize, nil, nil, 200)
	action, err := labWebForm(form.body)
	s.must(err)
	password, err := labWebPassword(s.sessions[0].root)
	s.must(err)
	posted := exchange("POST", action, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(url.Values{"username": {"dashboard-alice"}, "password": {password}, "credentialId": {""}}.Encode()), 302)
	callback, err := labWebCallback(posted.header, state.Value)
	s.must(err)
	// A lost callback reply may still leave a usable cookie in the jar; cleanup
	// attempts extraction even if no response assertion has completed.
	s.webCookie = "uncertain"
	signed := exchange("GET", callback, nil, nil, 303)
	cookie, err := labWebCookie(signed.header, sessionCookie, false)
	s.must(err)
	s.webCookie = cookie.Value
	var b api.Bootstrap
	s.must(s.web.read(s.ctx, "baseline-web-session", "/api/v1/bootstrap", 200, "", &b))
	if b.Account != s.bootstrap[0].Account || len(b.CSRFToken) != 43 {
		s.t.Fatal("web and native immutable account differ")
	}
	s.csrf = b.CSRFToken
}

func (s *labFaultState) baseline() {
	raw, err := labScaleReadFile(os.Getenv("JOBMAN_DASHBOARD_LAB_FAULT_SLURM_RECEIPT"), 32768)
	s.must(err)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != labAcceptedCurrentSlurmReceiptSHA || json.Unmarshal(raw, &s.slurm) != nil || !s.slurm.valid() {
		s.t.Fatal("exact accepted actual Slurm completion receipt required")
	}
	s.jobPath = labMultiPrefix(api.Scope{DeploymentID: labDeployment, NamespaceID: labMixedOperationsNS}) + "/jobs/" + s.slurm.Jobs["task-2"]
	var job api.JobDetail
	s.must(s.alice.read(s.ctx, "baseline-actual-job", s.jobPath, 200, "", &job))
	s.run, err = s.slurm.pinnedRun(job)
	s.must(err)
	s.reportPath = s.jobPath + "/reports/" + s.slurm.Reports["include_log_tail"]
	s.must(s.alice.read(s.ctx, "baseline-report", s.reportPath, 200, "", &s.report))
	if s.report.State != "ready" || s.report.Detail == nil || s.report.Outdated || s.report.RunID != s.run.ID || s.report.Detail.ControlInstanceID != labRestoreInstance || s.report.Detail.Disclosure.ProviderInvoked || len(s.report.Detail.Citations) == 0 {
		s.t.Fatal("actual sealed report unavailable")
	}
	s.citationPath = s.reportPath + "/citations/" + url.PathEscape(s.report.Detail.Citations[0].ID)
	s.must(s.alice.read(s.ctx, "baseline-citation", s.citationPath, 200, "", &s.citation))
	if s.citation.ReportID != s.report.ReportID || s.citation.EvidenceID != s.report.EvidenceID || s.citation.TaskID != s.report.TaskID {
		s.t.Fatal("sealed citation identity differs")
	}
	s.must(s.alice.read(s.ctx, "baseline-log", s.jobPath+"/logs?stream=stderr", 200, "", &s.logs))
	var log labMixedLog
	s.must(log.accept(s.logs, labSlurmFailureLog, s.run))
	primary := labReadMultiFixture(s.t, filepath.Join(s.sessions[0].root, ".lab/dashboard/fixture-info.json"), false)
	secondary := labReadMultiFixture(s.t, os.Getenv("JOBMAN_DASHBOARD_LAB_SECONDARY_FIXTURE"), true)
	for index, fixture := range []labMultiFixture{primary, secondary} {
		for _, ns := range fixture.Namespaces {
			if ns.Name == "dashboard-research" && len(ns.JobIDs) > 0 {
				if index == 0 {
					s.primary = api.Scope{DeploymentID: labDeployment, NamespaceID: ns.ID}
					s.primaryJob = labMultiPrefix(s.primary) + "/jobs/" + ns.JobIDs[0]
				} else {
					s.secondary = api.Scope{DeploymentID: labSecondaryDeployment, NamespaceID: ns.ID}
					s.secondaryJob = labMultiPrefix(s.secondary) + "/jobs/" + ns.JobIDs[0]
				}
			}
		}
	}
	if !uuid(s.primary.NamespaceID) || !uuid(s.secondary.NamespaceID) {
		s.t.Fatal("both reviewed research scopes required")
	}
	for _, c := range []labFaultHTTP{s.alice, s.bob} {
		s.must(c.read(s.ctx, "baseline-primary-research", s.primaryJob, 200, "", nil))
		s.must(c.read(s.ctx, "baseline-secondary-research", s.secondaryJob, 200, "", nil))
	}
	var inbox notifications.InboxPage
	s.must(s.alice.read(s.ctx, "baseline-inbox", "/api/v1/inbox?"+labMultiQuery([]api.Scope{s.primary}, 20, ""), 200, "", &inbox))
	if inbox.Completeness != "complete" || len(inbox.Items) == 0 || !uuid(inbox.Items[0].ID) || inbox.Items[0].Job.DeploymentID != s.primary.DeploymentID || inbox.Items[0].Job.NamespaceID != s.primary.NamespaceID {
		s.t.Fatal("an existing authorized primary inbox event is required")
	}
	s.inbox = inbox.Items[0]
}

func (s *labFaultState) directory() {
	s.begin("directory_stop", 220*time.Second)
	expires := time.Time{}
	for _, b := range s.bootstrap {
		for _, d := range b.Deployments {
			if d.ID == labDeployment {
				for _, ns := range d.Namespaces {
					if ns.AuthorizationExpiresAt.After(expires) {
						expires = ns.AuthorizationExpiresAt
					}
				}
			}
		}
	}
	// Capture the last real proof after the stop is acknowledged. Setup and the
	// driver's immutable baseline checks can span a normal refresh cycle.
	for _, client := range []labFaultHTTP{s.alice, s.bob} {
		var observed api.Bootstrap
		s.must(client.read(s.ctx, "stopped-directory-proof", "/api/v1/bootstrap", 200, "", &observed))
		for _, deployment := range observed.Deployments {
			if deployment.ID == labDeployment {
				for _, grant := range deployment.Namespaces {
					if grant.AuthorizationExpiresAt.After(expires) {
						expires = grant.AuthorizationExpiresAt
					}
				}
			}
		}
	}
	if expires.IsZero() || time.Until(expires) > 125*time.Second {
		s.t.Fatal("source freshness proof outside known bound")
	}
	s.must(labMixedWait(s.ctx, max(time.Until(expires.Add(2*time.Second)), 0)))
	// A successful proof already in flight at stop may have committed afterward.
	// Poll the actual protected route for at most12s; success remains authorized
	// until a real source freshness refusal, never an invented backdated row.
	pollEnd := time.Now().Add(12 * time.Second)
	for {
		response, err := labWebExchange(s.ctx, s.alice.client, "GET", labWebOrigin+s.primaryJob, http.Header{"Authorization": {"Bearer " + s.alice.token}}, nil)
		s.must(err)
		if response.status == 503 {
			var failure api.Error
			if json.Unmarshal(response.body, &failure) != nil || !labFaultError(failure, 503, "authorization_unavailable") {
				s.t.Fatal("directory expiry returned another failure")
			}
			break
		}
		if response.status != 200 || time.Now().After(pollEnd) {
			s.t.Fatal("natural directory freshness expiry not observed before watchdog window")
		}
		s.must(labMixedWait(s.ctx, time.Second))
	}
	checks := []struct {
		c           labFaultHTTP
		label, path string
	}{
		{s.alice, "expired-primary-job", s.jobPath}, {s.alice, "expired-primary-log", s.jobPath + "/logs?stream=stderr&cursor=" + url.QueryEscape(s.logs.NextCursor)},
		{s.alice, "expired-report", s.reportPath}, {s.alice, "expired-citation", s.citationPath}, {s.alice, "expired-inbox-item", "/api/v1/inbox/" + s.inbox.ID},
		{s.bob, "expired-bob-research", s.primaryJob}, {s.web, "expired-web-research", s.primaryJob},
	}
	for _, check := range checks {
		s.must(check.c.read(s.ctx, check.label, check.path, 503, "authorization_unavailable", nil))
	}
	for _, c := range []labFaultHTTP{s.alice, s.bob, s.web} {
		s.must(c.read(s.ctx, "healthy-secondary", s.secondaryJob, 200, "", nil))
	}
	var page api.Page[api.Job]
	s.must(s.alice.read(s.ctx, "partial-two-source-jobs", "/api/v1/jobs?"+labMultiQuery([]api.Scope{s.primary, s.secondary}, 20, ""), 200, "", &page))
	if page.Completeness != "partial" || len(page.Items) == 0 {
		s.t.Fatal("healthy secondary contribution missing or not marked partial")
	}
	for _, job := range page.Items {
		if job.Scope != s.secondary {
			s.t.Fatal("stale primary job bytes leaked into aggregate")
		}
	}
	var overview api.Overview
	s.must(s.alice.read(s.ctx, "partial-two-source-overview", "/api/v1/overview?"+labMultiQuery([]api.Scope{s.primary, s.secondary}, 0, ""), 200, "", &overview))
	if overview.Completeness != "partial" || overview.Active == nil {
		s.t.Fatal("overview did not expose healthy authorized subtotal")
	}
	s.recover()
	end := time.Now().Add(60 * time.Second)
	for {
		response, err := labWebExchange(s.ctx, s.alice.client, "GET", labWebOrigin+s.primaryJob, http.Header{"Authorization": {"Bearer " + s.alice.token}}, nil)
		s.must(err)
		if response.status == 200 {
			break
		}
		if response.status != 503 || time.Now().After(end) {
			s.t.Fatal("same credential did not recover with normal directory reconciliation")
		}
		s.must(labMixedWait(s.ctx, time.Second))
	}
	var b api.Bootstrap
	s.must(s.alice.read(s.ctx, "fresh-directory-proof", "/api/v1/bootstrap", 200, "", &b))
	found := false
	for _, d := range b.Deployments {
		for _, n := range d.Namespaces {
			if d.ID == s.primary.DeploymentID && n.ID == s.primary.NamespaceID {
				found = n.AuthorizationCheckedAt.After(expires.Add(-120*time.Second)) && n.AuthorizationExpiresAt.After(time.Now())
			}
		}
	}
	if !found {
		s.t.Fatal("restored source proof was not freshly verified")
	}
	var item notifications.InboxItem
	s.must(s.alice.read(s.ctx, "restored-inbox-identity", "/api/v1/inbox/"+s.inbox.ID, 200, "", &item))
	if item.ID != s.inbox.ID || item.EventID != s.inbox.EventID {
		s.t.Fatal("historical event identity changed")
	}
}

func (s *labFaultState) broker() {
	for _, fault := range []string{"broker_stop", "broker_pause"} {
		reserve := 70 * time.Second
		if fault == "broker_stop" {
			reserve = 150 * time.Second
		}
		s.begin(fault, reserve)
		start := time.Now()
		failures := make(chan error, 4)
		for range 4 {
			go func() {
				failures <- s.alice.read(s.ctx, "broker-unavailable-log", s.jobPath+"/logs?stream=stderr", 503, "source_unavailable", nil)
			}()
		}
		// Use Bob's independent bounded client for healthy metadata while the
		// four Alice log requests occupy their own connections.
		failure := s.bob.read(s.ctx, "broker-fault-metadata", s.primaryJob, 200, "", nil)
		failure = errors.Join(failure, s.web.read(s.ctx, "broker-fault-liveness", "/healthz", 200, "", nil))
		for range 4 {
			failure = errors.Join(failure, <-failures)
		}
		s.must(failure)
		if time.Since(start) > 10*time.Second {
			s.t.Fatal("broker transport dependency did not fail within ten seconds")
		}
		s.recover()
		var reread api.LogRange
		s.must(s.alice.recoverLog(s.ctx, s.jobPath+"/logs?stream=stderr", &reread))
		var log labMixedLog
		s.must(log.accept(reread, labSlurmFailureLog, s.run))
		if reread.BytesBase64 != s.logs.BytesBase64 || reread.StartOffset != s.logs.StartOffset || reread.EndOffset != s.logs.EndOffset {
			s.t.Fatal("broker recovery changed or skipped original bytes")
		}
		// Retry the exact pre-outage continuation; unchanged key/source/run
		// identity must preserve its original contiguous offset.
		var follow api.LogRange
		s.must(s.alice.read(s.ctx, "restored-contiguous-follow", s.jobPath+"/logs?stream=stderr&cursor="+url.QueryEscape(s.logs.NextCursor), 200, "", &follow))
		s.must(log.accept(follow, labSlurmFailureLog, s.run))
	}
}

func (s *labFaultState) sourceScenario(ctx context.Context, action, selected string, output any) error {
	root := s.sessions[0].root
	for name, want := range map[string]string{"dashboard-multisource-notification-scenario.py": "2974ca794895b8271fe78d63f75e23d3dbceb907fdcb3ec28de18d7801259b80", "dashboard-scale-source-common.py": "deb95b8dc32334cddcbcb2f7c1d24584d43de699212f26661c24499339b8cecb"} {
		raw, err := labScaleReadFile(filepath.Join(root, "scripts", name), 128<<10)
		sum := sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(sum[:]) != want {
			return errors.New("reviewed normal source helper changed")
		}
	}
	if !slices.Contains([]string{"prepare", "complete", "settled"}, action) || (action == "prepare" && selected != "") || (action != "prepare" && !slices.Contains([]string{"first", "stopped"}, selected)) {
		return errors.New("source helper selection denied")
	}
	args := []string{filepath.Join(root, "scripts/dashboard-multisource-notification-scenario.py"), "primary", action, s.fixture.Receipt}
	if selected != "" {
		args = append(args, selected)
	}
	command := exec.CommandContext(ctx, "python3", args...)
	command.Stderr = io.Discard
	var raw labExecutionOutput
	command.Stdout = &raw
	if command.Run() != nil || json.Unmarshal(raw.Bytes(), output) != nil {
		return errors.New("source helper result uncertain; preserve its original receipt")
	}
	return nil
}
func (s *labFaultState) cleanupRule(ctx context.Context) {
	c := newLabNotificationHTTP(s.t, s.sessions[0])
	var current notifications.RuleView
	status, err := c.call(ctx, "GET", "/api/v1/rules/"+s.ruleID, s.alice.token, "", nil, &current)
	if err == nil && status == 404 {
		s.ruleStopped = true
		return
	}
	if err != nil || status != 200 || current.ID != s.ruleID || len(s.fixture.Receipt) != 32 || current.Name != "Lab dependency "+s.fixture.Receipt[:12] {
		s.t.Error("test-owned rule cleanup identity unconfirmed")
		return
	}
	status, err = c.call(ctx, "DELETE", "/api/v1/rules/"+s.ruleID, s.alice.token, current.Revision, nil, nil)
	if err != nil || status != 204 {
		s.t.Error("test-owned rule deletion unconfirmed; preserve rule ID")
		return
	}
	s.ruleStopped = true
}
func (s *labFaultState) database() {
	plan, err := labScaleReadFile(filepath.Join(s.driver.staging, "plan.json"), 2<<20)
	s.must(err)
	var header struct{ OperationID string }
	s.must(json.Unmarshal(plan, &header))
	receipt := strings.ReplaceAll(header.OperationID, "-", "")
	if len(receipt) != 32 {
		s.t.Fatal("fixed operation receipt unavailable")
	}
	s.fixture.Receipt = receipt
	setup, done := context.WithTimeout(s.ctx, 35*time.Second)
	s.must(s.sourceScenario(setup, "prepare", "", &s.fixture))
	done()
	s.must(labValidateMultiNotificationFixture(s.fixture, "primary", receipt))
	input := notifications.RuleInput{Name: "Lab dependency " + receipt[:12], Enabled: true, Scope: notifications.ScopeWatchedJobs,
		Namespaces: []notifications.NamespaceRef{{DeploymentID: s.fixture.DeploymentID, NamespaceID: s.fixture.NamespaceID}},
		Jobs:       []notifications.JobRef{{DeploymentID: s.fixture.DeploymentID, NamespaceID: s.fixture.NamespaceID, JobID: s.fixture.Jobs[0].JobID}}, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"cancelled"}}
	client := newLabNotificationHTTP(s.t, s.sessions[0])
	var rule notifications.RuleView
	s.uncertainRule = true
	status, err := client.call(s.ctx, "POST", "/api/v1/rules", s.alice.token, "", input, &rule)
	if err != nil || status != 201 || !uuid(rule.ID) || rule.Name != input.Name {
		s.t.Fatal("personal rule admission uncertain; do not create a replacement")
	}
	s.ruleID = rule.ID
	s.uncertainRule = false
	activate := time.Now().Add(40 * time.Second)
	for {
		if rule.Enabled && len(rule.Scopes) == 1 && rule.Scopes[0].NamespaceRef == input.Namespaces[0] && rule.Scopes[0].Status == "active" && rule.Scopes[0].ActivatedAt != nil {
			break
		}
		if time.Now().After(activate) {
			s.t.Fatal("personal rule did not activate before fault budget")
		}
		s.must(labMixedWait(s.ctx, time.Second))
		status, err = client.call(s.ctx, "GET", "/api/v1/rules/"+s.ruleID, s.alice.token, "", nil, &rule)
		if err != nil || status != 200 {
			s.t.Fatal("personal activation could not be verified")
		}
	}
	for index, fault := range []string{"database_reject", "database_drop"} {
		s.begin(fault, 80*time.Second)
		began := time.Now()
		// The event is a normal Control transition while ONLY Dashboard SQL sockets
		// are blocked. Original UUID and recorded time come from Control's own row.
		if index == 0 {
			call, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			err = s.sourceScenario(call, "complete", "first", &s.event)
			cancel()
			s.must(err)
			s.must(labValidateMultiNotificationEvent(s.event, s.fixture, "first"))
			if s.event.RecordedAt.Before(began.Add(-2*time.Second)) || s.event.RecordedAt.After(time.Now().Add(2*time.Second)) {
				s.t.Fatal("original terminal event fell outside actual fault interval")
			}
		}
		failure := make(chan error, 3)
		for _, c := range []labFaultHTTP{s.alice, s.bob, s.web} {
			go func(c labFaultHTTP) {
				start := time.Now()
				err := c.read(s.ctx, "database-unavailable-bootstrap", "/api/v1/bootstrap", 503, "source_unavailable", nil)
				if err == nil && time.Since(start) > 17*time.Second {
					err = errors.New("SQL fault HTTP response exceeded seventeen seconds")
				}
				failure <- err
			}(c)
		}
		faultErr := s.alice.read(s.ctx, "database-fault-liveness", "/healthz", 200, "", nil)
		faultErr = errors.Join(faultErr, s.alice.read(s.ctx, "database-fault-static", "/", 200, "", nil))
		var observe struct {
			Observations []struct {
				Endpoint, State string
				Status          int
				Milliseconds    float64
			}
		}
		faultErr = errors.Join(faultErr, s.driver.call(s.ctx, "observe-api", "", &observe))
		if len(observe.Observations) != 2 || observe.Observations[0].Endpoint != "livez" || observe.Observations[0].Status != 200 || observe.Observations[1].Endpoint != "readyz" || observe.Observations[1].Status != 503 || observe.Observations[1].Milliseconds > 3000 {
			faultErr = errors.Join(faultErr, errors.New("private liveness/readiness did not separate the failed SQL dependency"))
		}
		for range 3 {
			faultErr = errors.Join(faultErr, <-failure)
		}
		s.must(faultErr)
		var evidence struct {
			Counter *struct{ Packets, Bytes uint64 }
			Fault   string
		}
		s.must(s.driver.call(s.ctx, "status", fault, &evidence))
		if evidence.Fault != fault || evidence.Counter == nil || evidence.Counter.Packets == 0 {
			s.t.Fatal("no actual Dashboard packets were rejected/dropped")
		}
		if time.Since(began) > 25*time.Second {
			s.t.Fatal("ordinary SQL fault exceeded twenty-five seconds; cleanup/watchdog remains authoritative")
		}
		s.recover()
		recovery := time.Now().Add(30 * time.Second)
		for {
			response, err := labWebExchange(s.ctx, s.alice.client, "GET", labWebOrigin+"/api/v1/bootstrap", http.Header{"Authorization": {"Bearer " + s.alice.token}}, nil)
			s.must(err)
			if response.status == 200 {
				break
			}
			if response.status != 503 || time.Now().After(recovery) {
				s.t.Fatal("same process/token did not recover SQL access")
			}
			s.must(labMixedWait(s.ctx, time.Second))
		}
	}
	settle := time.Now().Add(60 * time.Second)
	for {
		var proof labMultiNotificationBarrier
		s.must(s.sourceScenario(s.ctx, "settled", "first", &proof))
		if !proof.matches(s.event) {
			s.t.Fatal("original event settlement identity differs")
		}
		if proof.Settled {
			break
		}
		if time.Now().After(settle) {
			s.t.Fatal("original event did not settle after SQL recovery")
		}
		s.must(labMixedWait(s.ctx, time.Second))
	}
	var inbox notifications.InboxPage
	s.must(s.alice.read(s.ctx, "settled-original-event", "/api/v1/inbox?"+labMultiQuery([]api.Scope{s.primary}, 50, ""), 200, "", &inbox))
	count := 0
	for _, item := range inbox.Items {
		if item.EventID == s.event.EventID && item.ControlInstanceID == s.event.ControlInstanceID {
			count++
			s.must(labMultiNotificationMatches(item, s.event, []string{s.ruleID}, map[string]string{s.ruleID: rule.Revision}))
		}
	}
	if count != 1 {
		s.t.Fatal("original event did not yield exactly one authorized inbox identity")
	}
	s.cleanupRule(s.ctx)
	if !s.ruleStopped {
		s.t.Fatal("test rule must stop before final own-job cleanup")
	}
	var stopped labNotificationEvent
	cleanup, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	s.must(s.sourceScenario(cleanup, "complete", "stopped", &stopped))
	s.must(labValidateMultiNotificationEvent(stopped, s.fixture, "stopped"))
}

func TestLabFaultErrorRejectsCredentialAndTransportMisclassification(t *testing.T) {
	good := api.Error{Code: "source_unavailable", Message: "The source is unavailable. Retry when private connectivity is restored.", RequestID: strings.Repeat("a", 32)}
	if !labFaultError(good, 503, "source_unavailable") {
		t.Fatal("safe unavailable error rejected")
	}
	for _, change := range []func(*api.Error){func(e *api.Error) { e.Message = "postgres://private-user:private-password@private-host" }, func(e *api.Error) { e.Code = "unauthenticated" }, func(e *api.Error) { e.RequestID = "" }, func(e *api.Error) { e.RequestID = "private-request-content" }} {
		bad := good
		change(&bad)
		if labFaultError(bad, 503, "source_unavailable") {
			t.Fatal("unsafe or misclassified failure accepted")
		}
	}
	if labFaultError(good, 401, "source_unavailable") {
		t.Fatal("database failure accepted as authentication rejection")
	}
}

func TestLabFaultHTTPRejectsUnexpectedSensitiveShapeAndCookieChanges(t *testing.T) {
	good := `{"code":"source_unavailable","message":"The source is unavailable. Retry when private connectivity is restored.","requestId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	for _, test := range []struct {
		name, body, cookie string
		status             int
		want               bool
	}{
		{"safe", good, "", 503, true}, {"extra", strings.TrimSuffix(good, "}") + `,"jobs":["private-job"]}`, "", 503, false},
		{"invalid credentials", good, "", 401, false}, {"raw failure", `{"code":"source_unavailable","message":"postgres://private-secret","requestId":"x"}`, "", 503, false},
		{"session cleared", good, "__Host-jobman-session=; Max-Age=0", 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mutex sync.Mutex
			samples := []labFaultSample{}
			client := &http.Client{Transport: labMixedRoundTripper(func(r *http.Request) (*http.Response, error) {
				h := http.Header{"Cache-Control": {"no-store"}}
				if test.cookie != "" {
					h.Set("Set-Cookie", test.cookie)
				}
				return &http.Response{StatusCode: test.status, Header: h, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			c := labFaultHTTP{client: client, token: "synthetic-token-never-persisted", mu: &mutex, samples: &samples}
			err := c.read(t.Context(), "fixture", "/api/v1/bootstrap", 503, "source_unavailable", nil)
			if (err == nil) != test.want {
				t.Fatal("fault error guard differs")
			}
			raw, _ := json.Marshal(samples)
			if bytes.Contains(raw, []byte("private-")) || bytes.Contains(raw, []byte("token-never")) {
				t.Fatal("sensitive fixture bytes reached evidence")
			}
		})
	}
}

func TestLabFaultHTTPReturnsOnCallerCancellation(t *testing.T) {
	var mutex sync.Mutex
	samples := []labFaultSample{}
	client := &http.Client{Transport: labMixedRoundTripper(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	c := labFaultHTTP{client: client, mu: &mutex, samples: &samples}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	began := time.Now()
	if c.read(ctx, "cancelled", "/api/v1/bootstrap", 503, "source_unavailable", nil) == nil || time.Since(began) > time.Second {
		t.Fatal("fault request ignored cancellation")
	}
}

func TestLabFaultSameGrantsPermitsOnlyFreshnessChanges(t *testing.T) {
	var a api.Bootstrap
	if json.Unmarshal([]byte(`{"completeness":"complete","deployments":[{"id":"source","namespaces":[{"id":"ns","roles":["viewer"],"capabilities":["jobs.read"],"authorizationCheckedAt":"2026-10-01T00:00:00Z","authorizationExpiresAt":"2026-10-01T00:02:00Z"}]}]}`), &a) != nil {
		t.Fatal("fixture")
	}
	raw, _ := json.Marshal(a)
	var b api.Bootstrap
	_ = json.Unmarshal(raw, &b)
	b.Deployments[0].Namespaces[0].AuthorizationCheckedAt = time.Now()
	if !labFaultGrantsSame(a, b) {
		t.Fatal("fresh proof rejected")
	}
	b.Deployments[0].Namespaces[0].Capabilities = append(b.Deployments[0].Namespaces[0].Capabilities, "jobs.submit")
	if labFaultGrantsSame(a, b) {
		t.Fatal("changed authority accepted as transport recovery")
	}
}

func TestLabFaultBrokerRecoveryRequiresPositiveSameCredentialRead(t *testing.T) {
	for _, code := range []string{"source_unavailable", "authorization_unavailable"} {
		t.Run(code, func(t *testing.T) {
			var mutex sync.Mutex
			samples := []labFaultSample{}
			calls := 0
			want := api.LogRange{BytesBase64: "ZXhhY3QgYnl0ZXM=", StartOffset: "0", EndOffset: "11", ExecutionID: "accepted-execution"}
			client := &http.Client{Transport: labMixedRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer unchanged-fixture-token" || r.URL.Path != "/api/v1/exact-job/logs" || r.URL.RawQuery != "stream=stderr" {
					t.Fatal("recovery changed credentials or requested range")
				}
				status := 503
				message := "The source is unavailable. Retry when private connectivity is restored."
				if code == "authorization_unavailable" {
					message = "Current source authorization could not be verified."
				}
				body, _ := json.Marshal(api.Error{Code: code, Message: message, RequestID: strings.Repeat("a", 32)})
				if calls == 3 {
					status = 200
					body, _ = json.Marshal(want)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			c := labFaultHTTP{client: client, token: "unchanged-fixture-token", mu: &mutex, samples: &samples}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var got api.LogRange
			if err := c.recoverLogWithin(ctx, "/api/v1/exact-job/logs?stream=stderr", &got, time.Millisecond); err != nil || !reflect.DeepEqual(got, want) || calls != 3 {
				t.Fatal("recovery did not require a positive unchanged range read")
			}
			if len(samples) != 3 || samples[0].Code != code || samples[1].Status != 503 || samples[2].Status != 200 {
				t.Fatal("intermediate recovery evidence was discarded")
			}
		})
	}
}

func TestLabFaultBrokerRecoveryRejectsInvalidErrorsAndKeepsOutputAtomic(t *testing.T) {
	good := `{"code":"authorization_unavailable","message":"Current source authorization could not be verified.","requestId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	for _, test := range []struct {
		name, body, cookie string
		status             int
	}{
		{"authentication", good, "", 401},
		{"unknown error", strings.Replace(good, "authorization_unavailable", "private_error", 1), "", 503},
		{"sensitive fields", strings.TrimSuffix(good, "}") + `,"logs":["private-data"]}`, "", 503},
		{"duplicate message", `{"message":"private-canary",` + strings.TrimPrefix(good, "{"), "", 503},
		{"duplicate code", `{"code":"private-canary",` + strings.TrimPrefix(good, "{"), "", 503},
		{"duplicate request ID", `{"requestId":"private-canary",` + strings.TrimPrefix(good, "{"), "", 503},
		{"trailing JSON", good + `{}`, "", 503},
		{"cookie changed", good, "session=changed", 503},
		{"malformed success", "{", "", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mutex sync.Mutex
			samples := []labFaultSample{}
			calls := 0
			client := &http.Client{Transport: labMixedRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				h := http.Header{"Cache-Control": {"no-store"}}
				if test.cookie != "" {
					h.Set("Set-Cookie", test.cookie)
				}
				return &http.Response{StatusCode: test.status, Header: h, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			c := labFaultHTTP{client: client, mu: &mutex, samples: &samples}
			before := api.LogRange{NextCursor: "original-private-cursor", ExecutionID: "original-execution"}
			got := before
			if c.recoverLogWithin(t.Context(), "/api/v1/exact-job/logs", &got, time.Millisecond) == nil || calls != 1 || !reflect.DeepEqual(got, before) {
				t.Fatal("invalid response retried or changed original log state")
			}
		})
	}
}

func TestLabFaultBrokerRecoveryDeadlineAndAttemptBounds(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			var mutex sync.Mutex
			samples := []labFaultSample{}
			calls := 0
			client := &http.Client{Transport: labMixedRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 503, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(`{"code":"source_unavailable","message":"The source is unavailable. Retry when private connectivity is restored.","requestId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))}, nil
			})}
			c := labFaultHTTP{client: client, mu: &mutex, samples: &samples}
			limit, interval := time.Second, time.Millisecond
			if deadline {
				limit, interval = 20*time.Millisecond, time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), limit)
			defer cancel()
			before := api.LogRange{ExecutionID: "original-execution"}
			got := before
			start := time.Now()
			if c.recoverLogWithin(ctx, "/api/v1/exact-job/logs", &got, interval) == nil || !reflect.DeepEqual(got, before) || time.Since(start) > time.Second {
				t.Fatal("unavailable broker exceeded bound or changed original state")
			}
			if (!deadline && calls != 16) || (deadline && calls != 1) || len(samples) != calls {
				t.Fatal("bounded recovery evidence or request count differs")
			}
		})
	}
}
