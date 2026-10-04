//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

const labInstallOrigin = "https://dashboard.lab.test:48443"
const labInstallClient = "jobman-dashboard-install-web-v1"
const labInstallFix = "2e8f1b15c58889c52023d49d396fd600b31eecd2"

var labInstallFiles = []string{
	"dashboard-install-plan.py", "dashboard-install-guest.py", "install-dashboard-fresh.py",
	"dashboard-split-plan.py", "dashboard-dependency-fault-plan.py", "dashboard-dependency-fault-guest.py",
	"dashboard-dependency-faults.py", "dashboard-control-upgrade-plan.py", "dashboard-control-upgrade-guest.py",
}

type labInstallPlan struct {
	Format               int               `json:"format"`
	Synthetic            bool              `json:"synthetic"`
	OperationID          string            `json:"operationId"`
	PostFixRevision      string            `json:"postFixRevision"`
	ImplementationSHA256 map[string]string `json:"implementationSHA256"`
	Candidates           map[string]struct {
		Revision      string `json:"revision"`
		ArchiveSHA256 string `json:"archiveSHA256"`
	} `json:"candidates"`
	Configs struct {
		API struct {
			PublicOrigin string `json:"publicOrigin"`
			Listen       string `json:"listen"`
			OIDC         struct {
				WebClientID string `json:"webClientId"`
			} `json:"oidc"`
		} `json:"api"`
	} `json:"configs"`
}

type labInstallDriver struct {
	root, implementationRoot, staging, planSHA, implementationSHA string
	adapterPath, adapterSHA                                       string
	plan                                                          labInstallPlan
}

func labInstallDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func labInstallLoad(root, implementationRoot, staging, planSHA, implementationSHA string) (labInstallDriver, error) {
	var result labInstallDriver
	if !filepath.IsAbs(root) || !filepath.IsAbs(implementationRoot) || !filepath.IsAbs(staging) || len(planSHA) != 64 || len(implementationSHA) != 64 {
		return result, errors.New("reviewed installation paths and hashes required")
	}
	for _, path := range []string{root, implementationRoot} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			return result, errors.New("installation Lab and implementation roots must be real")
		}
	}
	raw, err := labRestorePrivate(filepath.Join(staging, "plan.json"), 8<<20)
	if err != nil || labInstallDigest(raw) != planSHA || json.Unmarshal(raw, &result.plan) != nil {
		return result, errors.New("installation plan unavailable or changed")
	}
	p := result.plan
	if p.Format != 1 || !p.Synthetic || !uuid(p.OperationID) || p.PostFixRevision != labInstallFix || p.Configs.API.PublicOrigin != labInstallOrigin || p.Configs.API.Listen != "10.77.0.10:48443" || p.Configs.API.OIDC.WebClientID != labInstallClient || len(p.Candidates) != 2 || p.Candidates["baseline"].Revision == p.Candidates["upgrade"].Revision || len(p.Candidates["baseline"].Revision) != 40 || len(p.Candidates["upgrade"].Revision) != 40 || len(p.ImplementationSHA256) != len(labInstallFiles) {
		return result, errors.New("installation plan scope invalid")
	}
	for _, name := range labInstallFiles {
		path := filepath.Join(implementationRoot, "scripts", name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() <= 0 || info.Size() > 256<<10 {
			return result, errors.New("installation implementation unavailable")
		}
		file, err := os.Open(path)
		if err != nil {
			return result, errors.New("installation implementation unreadable")
		}
		before, e1 := file.Stat()
		content, e2 := io.ReadAll(io.LimitReader(file, (256<<10)+1))
		after, e3 := file.Stat()
		file.Close()
		if e1 != nil || e2 != nil || e3 != nil || !os.SameFile(info, before) || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || len(content) > 256<<10 || labInstallDigest(content) != p.ImplementationSHA256[name] {
			return result, errors.New("installation implementation changed")
		}
	}
	encoded, err := json.MarshalIndent(p.ImplementationSHA256, "", "  ")
	if err != nil || labInstallDigest(append(encoded, '\n')) != implementationSHA {
		return result, errors.New("installation implementation set differs")
	}
	result.root, result.implementationRoot, result.staging, result.planSHA, result.implementationSHA = root, implementationRoot, staging, planSHA, implementationSHA
	return result, nil
}

// An optional independently reviewed, self-contained continuation is explicit.
// Its single hash covers all added code; it can load only the already pinned
// original implementation. It never changes the original plan or nine hashes.
func (d labInstallDriver) withAdapter(path, digest string) (labInstallDriver, error) {
	if path == "" && digest == "" {
		return d, nil
	}
	decoded, err := hex.DecodeString(digest)
	if !filepath.IsAbs(path) || len(decoded) != 32 || err != nil || strings.ToLower(digest) != digest {
		return d, errors.New("reviewed installation adapter path and hash required together")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return d, errors.New("installation adapter must have a canonical path")
	}
	raw, err := labRestorePrivate(path, 128<<10)
	if err != nil || len(raw) == 0 || labInstallDigest(raw) != digest {
		return d, errors.New("installation adapter unavailable or changed")
	}
	d.adapterPath, d.adapterSHA = path, digest
	return d, nil
}

func (d labInstallDriver) run(ctx context.Context, phase, selected string) error {
	if phase != "verify" && phase != "upgrade" && phase != "rollback" && phase != "stop" && phase != "retire-identity" && phase != "close" {
		return errors.New("installation harness phase denied")
	}
	if _, err := labInstallLoad(d.root, d.implementationRoot, d.staging, d.planSHA, d.implementationSHA); err != nil {
		return err
	}
	entry := filepath.Join(d.implementationRoot, "scripts/install-dashboard-fresh.py")
	if _, err := d.withAdapter(d.adapterPath, d.adapterSHA); err != nil {
		return err
	}
	if d.adapterPath != "" {
		entry = d.adapterPath
	}
	args := []string{entry, phase, "--lab-root", d.root, "--staging", d.staging, "--expected-plan-sha256", d.planSHA, "--expected-implementation-sha256", d.implementationSHA}
	if d.adapterPath != "" {
		args = append(args, "--implementation-root", d.implementationRoot, "--expected-adapter-sha256", d.adapterSHA)
	}
	if phase == "verify" {
		if selected != "baseline" && selected != "upgrade" && selected != "rollback" {
			return errors.New("installation selection invalid")
		}
		args = append(args, "--selected", selected)
	} else if phase != "close" {
		args = append(args, "--apply")
	}
	command := exec.CommandContext(ctx, "python3", args...)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE=1"}
	command.WaitDelay = 3 * time.Second
	output := &labRotationOutput{maximum: 128 << 10}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil {
		return errors.New("installation phase failed; preserve pending receipts and recover explicitly")
	}
	var value struct {
		Completed, Verified, Complete bool
		OperationID                   string `json:"operationId"`
		Phase, Selected               string
	}
	if json.Unmarshal(output.Bytes(), &value) != nil {
		return errors.New("installation completion invalid")
	}
	if phase == "verify" {
		if !value.Verified || value.Selected != selected {
			return errors.New("installation verification differs")
		}
	} else if phase == "close" {
		if !value.Complete {
			return errors.New("installation closure differs")
		}
	} else if !value.Completed || value.OperationID != d.plan.OperationID || value.Phase != phase {
		return errors.New("installation completion scope differs")
	}
	return nil
}

func labInstallExchange(ctx context.Context, client *http.Client, method, target string, headers http.Header, body []byte) (labWebResponse, error) {
	u, err := url.Parse(target)
	if err != nil || len(target) > 16<<10 || u.Scheme != "https" || (u.Host != "dashboard.lab.test:48443" && u.Host != "oidc.lab.test:8443") || u.User != nil || u.Fragment != "" || len(body) > 4096 {
		return labWebResponse{}, errors.New("installation request outside fixed bounds")
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return labWebResponse{}, errors.New("installation request invalid")
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	response, err := client.Do(request)
	if err != nil {
		return labWebResponse{}, errors.New("installation HTTPS unavailable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return labWebResponse{}, errors.New("installation response exceeds bounds")
	}
	return labWebResponse{response.StatusCode, response.Header.Clone(), raw}, nil
}

func labInstallHTTP(t *testing.T, native labNativeSession, cookies bool) *http.Client {
	t.Helper()
	transport := native.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		target := ""
		switch address {
		case "dashboard.lab.test:48443":
			target = "10.77.0.10:48443"
		case "oidc.lab.test:8443":
			target = "10.77.0.21:8443"
		default:
			return nil, errors.New("installation dial denied")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, target)
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if cookies {
		client.Jar, _ = cookiejar.New(nil)
	}
	return client
}

func labInstallAuthorization(header http.Header, state string) (string, error) {
	u, err := labWebRedirect(header, labWebIssuerOrigin, "/realms/jobman-lab/protocol/openid-connect/auth")
	if err != nil {
		return "", err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errors.New("installation authorize query invalid")
	}
	for _, values := range q {
		if len(values) != 1 {
			return "", errors.New("installation authorize duplicate query")
		}
	}
	if q.Get("client_id") != labInstallClient || q.Get("redirect_uri") != labInstallOrigin+"/auth/callback" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("nonce")) != 43 || q.Get("state") != state || q.Get("resource") != "jobman-dashboard-api" || q.Get("scope") != "openid profile" {
		return "", errors.New("installation authorize contract invalid")
	}
	return u.String(), nil
}

func labInstallSignIn(ctx context.Context, client *http.Client, password string) (string, error) {
	login, err := labInstallExchange(ctx, client, "GET", labInstallOrigin+"/auth/login?returnTo=%2Fsettings", nil, nil)
	if err != nil || login.status != 302 {
		return "", errors.New("installation login unavailable")
	}
	cookie, err := labWebCookie(login.header, loginCookie, false)
	if err != nil || cookie.MaxAge != 300 {
		return "", errors.New("installation login cookie invalid")
	}
	authorize, err := labInstallAuthorization(login.header, cookie.Value)
	if err != nil {
		return "", err
	}
	form, err := labInstallExchange(ctx, client, "GET", authorize, nil, nil)
	if err != nil || form.status != 200 {
		return "", errors.New("installation sign-in form unavailable")
	}
	action, err := labWebForm(form.body)
	if err != nil {
		return "", err
	}
	body := url.Values{"username": {"dashboard-alice"}, "password": {password}, "credentialId": {""}}
	posted, err := labInstallExchange(ctx, client, "POST", action, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(body.Encode()))
	if err != nil || posted.status != 302 {
		return "", errors.New("installation identity exchange unavailable")
	}
	callback, err := labWebRedirect(posted.header, labInstallOrigin, "/auth/callback")
	if err != nil {
		return "", err
	}
	q, err := url.ParseQuery(callback.RawQuery)
	if err != nil || len(q["state"]) != 1 || q.Get("state") != cookie.Value || len(q["code"]) != 1 || q.Get("code") == "" || q.Get("error") != "" {
		return "", errors.New("installation callback binding invalid")
	}
	response, err := labInstallExchange(ctx, client, "GET", callback.String(), nil, nil)
	if err != nil || response.status != 303 || response.header.Get("Location") != "/settings" {
		return "", errors.New("installation callback unavailable")
	}
	_, err = labWebCookie(response.header, sessionCookie, false)
	if err != nil {
		return "", err
	}
	response, err = labInstallExchange(ctx, client, "GET", labInstallOrigin+"/api/v1/bootstrap", nil, nil)
	if err != nil {
		return "", err
	}
	bootstrap, err := labWebBootstrap(response)
	if err != nil || len(bootstrap.CSRFToken) != 43 {
		return "", errors.New("installation browser session invalid")
	}
	return bootstrap.CSRFToken, nil
}

// The packaged services must already be provisioned and started by the exact
// reviewed driver through baseline. This test performs only its planned binary
// transitions, two new sealed reports and one disabled rule in the EMPTY new DB.
// It submits no Control workload and cannot send a device notification.
func TestLabFreshInstallationAndSupportedRollback(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALLATION") != "1" {
		t.Skip("explicit isolated installation opt-ins required")
	}
	driver, err := labInstallLoad(os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT"), os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALL_IMPLEMENTATION_ROOT"), os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALL_STAGING"), os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALL_PLAN_SHA256"), os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALL_IMPLEMENTATION_SHA256"))
	if err != nil {
		t.Fatal("Reviewed disposable installation is unavailable")
	}
	driver, err = driver.withAdapter(os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALL_ADAPTER"), os.Getenv("JOBMAN_DASHBOARD_LAB_INSTALL_ADAPTER_SHA256"))
	if err != nil {
		t.Fatal("Reviewed installation continuation is unavailable")
	}
	var baseline struct {
		Completed   bool
		Phase       string
		OperationID string `json:"operationId"`
		PlanSHA256  string `json:"planSHA256"`
	}
	raw, err := labRestorePrivate(filepath.Join(driver.staging, "baseline.json"), 65536)
	if err != nil || json.Unmarshal(raw, &baseline) != nil || !baseline.Completed || baseline.Phase != "baseline" || baseline.OperationID != driver.plan.OperationID || baseline.PlanSHA256 != driver.planSHA {
		t.Fatal("Exact baseline installation has not been started")
	}
	for _, name := range []string{"acceptance-started.json", "upgrade.pending.json", "rollback.pending.json", "stop.pending.json"} {
		if _, err := os.Lstat(filepath.Join(driver.staging, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("Installation acceptance was already attempted; retain evidence and review recovery")
		}
	}
	labRestoreWrite(t, driver.staging, "acceptance-started.json", map[string]any{"operationId": driver.plan.OperationID, "synthetic": true, "scope": "new-empty-db-post-fix-packages", "adapterSHA256": driver.adapterSHA})
	ctx, cancel := context.WithTimeout(t.Context(), 11*time.Minute)
	defer cancel()
	var web *http.Client
	csrf := ""
	allVerified := false
	// This is the planned close of only the new installation, never a binary
	// rollback on failure. A partial transition remains recorded for inspection.
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stop()
		if csrf != "" {
			response, err := labInstallExchange(cleanup, web, "POST", labInstallOrigin+"/auth/logout", http.Header{"Origin": {labInstallOrigin}, "X-CSRF-Token": {csrf}}, nil)
			if err != nil || response.status != 204 {
				t.Error("Fresh browser cleanup unconfirmed; disposable installation state retained")
			}
		}
		for _, phase := range []string{"stop", "retire-identity"} {
			if err := driver.run(cleanup, phase, ""); err != nil {
				t.Error("Disposable installation close remains unconfirmed; preserve receipts for explicit recovery")
				return
			}
		}
		if allVerified {
			if err := driver.run(cleanup, "close", ""); err != nil {
				t.Error("Installation final preservation receipt unavailable")
			}
		}
	})
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	client := labInstallHTTP(t, alice, false)
	web = labInstallHTTP(t, alice, true)
	request := func(method, path, token, key string, input, target any, want int) []byte {
		t.Helper()
		var body []byte
		if input != nil {
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal("Installation input invalid")
			}
		}
		headers := http.Header{}
		if token != "" {
			headers.Set("Authorization", "Bearer "+token)
		}
		if input != nil {
			headers.Set("Content-Type", "application/json")
		}
		if key != "" {
			headers.Set("Idempotency-Key", key)
		}
		response, err := labInstallExchange(ctx, client, method, labInstallOrigin+path, headers, body)
		if err != nil || response.status != want {
			t.Fatalf("Isolated installation %s returned unexpected status (wanted%d); response contents withheld", method, want)
		}
		if strings.HasPrefix(path, "/api/") && response.header.Get("Cache-Control") != "no-store" {
			t.Fatal("Sensitive install response caching contract differs")
		}
		if target != nil && json.Unmarshal(response.body, target) != nil {
			t.Fatal("Installation response contract invalid")
		}
		return response.body
	}
	var before, other api.Bootstrap
	request("GET", "/api/v1/bootstrap", alice.accessToken, "", nil, &before, 200)
	request("GET", "/api/v1/bootstrap", bob.accessToken, "", nil, &other, 200)
	if before.APIVersion != api.Version || before.FixtureMode || before.Completeness != "complete" || other.Completeness != "complete" || before.Account.ID == other.Account.ID || before.CSRFToken != "" || before.Preferences.Revision != "1" {
		t.Fatal("Fresh account/source/bootstrap differs")
	}
	for _, token := range []string{alice.accessToken, bob.accessToken} {
		var rules notifications.RulePage
		request("GET", "/api/v1/rules", token, "", nil, &rules, 200)
		var inbox notifications.InboxPage
		request("GET", "/api/v1/inbox", token, "", nil, &inbox, 200)
		if len(rules.Items) != 0 || rules.NextCursor != "" || len(inbox.Items) != 0 || inbox.NextCursor != "" || inbox.UnreadCount != "0" || inbox.Completeness != "complete" {
			t.Fatal("Fresh database copied historic rules/inbox or authorization was unavailable")
		}
	}
	page, err := labInstallExchange(ctx, client, "GET", labInstallOrigin+"/", nil, nil)
	if err != nil || page.status != 200 || !bytes.Contains(page.body, []byte("<html")) {
		t.Fatal("Fresh packaged web assets unavailable")
	}
	password, err := labWebPassword(alice.root)
	if err != nil {
		t.Fatal("Synthetic BFF credentials unavailable")
	}
	csrf, err = labInstallSignIn(ctx, web, password)
	if err != nil {
		t.Fatal("New confidential client BFF failed; protocol details withheld")
	}
	body, _ := json.Marshal(map[string]any{"timezone": "America/New_York", "appearance": "dark", "refreshSeconds": 10})
	// The new origin is a separate port. This test uses its own empty cookie jar;
	// it never loads a person's browser cookies shared by host across ports.
	denied, err := labInstallExchange(ctx, web, "PUT", labInstallOrigin+"/api/v1/preferences", http.Header{"Origin": {labInstallOrigin}, "Content-Type": {"application/json"}, "If-Match": {before.Preferences.Revision}}, body)
	if err != nil || denied.status != 401 {
		t.Fatal("Fresh browser write accepted absent CSRF")
	}
	changed, err := labInstallExchange(ctx, web, "PUT", labInstallOrigin+"/api/v1/preferences", http.Header{"Origin": {labInstallOrigin}, "X-CSRF-Token": {csrf}, "Content-Type": {"application/json"}, "If-Match": {before.Preferences.Revision}}, body)
	var preference api.Preferences
	if err != nil || changed.status != 200 || json.Unmarshal(changed.body, &preference) != nil || preference.Revision == before.Preferences.Revision || preference.Timezone != "America/New_York" {
		t.Fatal("Fresh preference persistence failed")
	}

	var accepted struct {
		Synthetic       bool
		ObservationMode string
		Fixture         labActualReceipt
		Jobs, Reports   map[string]string
	}
	raw = labExecutionFile(t, os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_RECEIPT"), 65536)
	if json.Unmarshal(raw, &accepted) != nil || !accepted.Synthetic || accepted.ObservationMode != "actual-subprocess-agent-execution" || accepted.Fixture.DeploymentID != labDeployment || accepted.Fixture.Namespace != "dashboard-operations" || accepted.Fixture.ControlInstanceID != "e633cf92-258d-48ff-965a-fda88d68ef3a" || !uuid(accepted.Jobs["failure"]) || !uuid(accepted.Fixture.NamespaceID) {
		t.Fatal("Reviewed actual execution fixture required")
	}
	prefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + accepted.Fixture.NamespaceID + "/jobs/" + accepted.Jobs["failure"]
	var job api.JobDetail
	request("GET", prefix, alice.accessToken, "", nil, &job, 200)
	if job.Job.ID != accepted.Jobs["failure"] || job.Job.NamespaceID != accepted.Fixture.NamespaceID || job.Job.DeploymentID != labDeployment || job.Job.Outcome != "failure" || job.Job.CurrentRun == nil || !uuid(job.Job.CurrentRun.ID) || !uuid(job.Job.CurrentRun.ExecutionID) || job.Job.Owner == nil || !job.Job.Owner.IsCurrentUser {
		t.Fatal("Original executed failure identity no longer matches")
	}
	request("GET", prefix, bob.accessToken, "", nil, nil, 403)
	var logs api.LogRange
	request("GET", prefix+"/logs?stream=stderr&limitBytes=262144", alice.accessToken, "", nil, &logs, 200)
	if !labRotationLog(logs, job.Job, labActualFailureLog) || logs.NextCursor == "" {
		t.Fatal("Fresh API log broker did not preserve original execution bytes")
	}
	var follow api.LogRange
	request("GET", prefix+"/logs?stream=stderr&limitBytes=262144&cursor="+url.QueryEscape(logs.NextCursor), alice.accessToken, "", nil, &follow, 200)
	if follow.BytesBase64 != "" || follow.StartOffset != logs.EndOffset || follow.EndOffset != logs.EndOffset || follow.ExecutionID != job.Job.CurrentRun.ExecutionID || follow.RunID != job.Job.CurrentRun.ID || follow.RunNumber != job.Job.CurrentRun.Number {
		t.Fatal("Fresh log continuation changed source/run identity")
	}
	request("GET", prefix+"/logs?stream=stderr", bob.accessToken, "", nil, nil, 403)
	ref := notifications.NamespaceRef{DeploymentID: labDeployment, NamespaceID: accepted.Fixture.NamespaceID}
	input := notifications.RuleInput{Name: "Fresh installation " + driver.plan.OperationID[:8], Enabled: false, Scope: notifications.ScopeWatchedJobs, Namespaces: []notifications.NamespaceRef{ref}, Jobs: []notifications.JobRef{{DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID, JobID: job.Job.ID}}, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"failure"}}
	var rule notifications.RuleView
	request("POST", "/api/v1/rules", alice.accessToken, "", input, &rule, 201)
	if !uuid(rule.ID) || rule.Enabled || rule.Name != input.Name || rule.UnavailableScopes != 0 || rule.InaccessibleScopes != 0 {
		t.Fatal("Disabled personal installation rule differs")
	}
	var sourceJob labActualJob
	sourceJob.Metadata.ID = job.Job.ID
	sourceJob.Status.CurrentRun = job.Job.CurrentRun
	reportIDs := labActualReports(t, ctx, request, alice.accessToken, bob.accessToken, accepted.Fixture, sourceJob, prefix)
	reports := map[string]api.Report{}
	citations := map[string]api.Citation{}
	for _, id := range reportIDs {
		var report api.Report
		path := prefix + "/reports/" + id
		request("GET", path, alice.accessToken, "", nil, &report, 200)
		reports[path] = report
		for _, ref := range report.Detail.Citations {
			var citation api.Citation
			path := path + "/citations/" + url.PathEscape(ref.ID)
			request("GET", path, alice.accessToken, "", nil, &citation, 200)
			citations[path] = citation
		}
	}
	verify := func(selected string) {
		t.Helper()
		var boot api.Bootstrap
		request("GET", "/api/v1/bootstrap", alice.accessToken, "", nil, &boot, 200)
		if boot.Account != before.Account || boot.Preferences != preference || boot.Completeness != "complete" {
			t.Fatal("Account or preferences changed across package transition")
		}
		response, err := labInstallExchange(ctx, web, "GET", labInstallOrigin+"/api/v1/bootstrap", nil, nil)
		browser, e := labWebBootstrap(response)
		if err != nil || e != nil || browser.Account != before.Account || browser.Preferences != preference || browser.CSRFToken != csrf {
			t.Fatal("Fresh session key or browser state changed across compatible transition")
		}
		var retained notifications.RuleView
		request("GET", "/api/v1/rules/"+rule.ID, alice.accessToken, "", nil, &retained, 200)
		if !reflect.DeepEqual(rule, retained) {
			t.Fatal("Disabled rule changed across compatible transition")
		}
		for path, original := range reports {
			var current api.Report
			request("GET", path, alice.accessToken, "", nil, &current, 200)
			if !reflect.DeepEqual(current, original) {
				t.Fatal("Sealed report changed across package transition")
			}
			request("GET", path, bob.accessToken, "", nil, nil, 404)
		}
		for path, original := range citations {
			var current api.Citation
			request("GET", path, alice.accessToken, "", nil, &current, 200)
			if !reflect.DeepEqual(current, original) {
				t.Fatal("Sealed citation changed across package transition")
			}
		}
		var retainedLog api.LogRange
		request("GET", prefix+"/logs?stream=stderr&limitBytes=262144", alice.accessToken, "", nil, &retainedLog, 200)
		if !labRotationLog(retainedLog, job.Job, labActualFailureLog) {
			t.Fatal("Source log changed across package transition")
		}
		if err := driver.run(ctx, "verify", selected); err != nil {
			t.Fatal("Database or infrastructure preservation verification failed")
		}
		labRestoreWrite(t, driver.staging, "http-"+selected+".json", map[string]any{"operationId": driver.plan.OperationID, "verified": true, "accountId": before.Account.ID, "jobId": job.Job.ID, "runId": job.Job.CurrentRun.ID, "executionId": job.Job.CurrentRun.ExecutionID, "reportIds": reportIDs, "reportDigest": labRestoreDigest(reports), "citationDigest": labRestoreDigest(citations), "ruleId": rule.ID, "preferenceRevision": preference.Revision})
	}
	verify("baseline")
	for _, phase := range []string{"upgrade", "rollback"} {
		if err := driver.run(ctx, phase, ""); err != nil {
			t.Fatal("Reviewed compatible binary transition failed; preserve exact pending receipt")
		}
		verify(phase)
	}
	allVerified = true
	t.Log("PASS: isolated empty PostgreSQL installation, fresh purpose keys/new confidential web client, actual PKCE/BFF and CSRF, role-scoped source/log/diagnosis, persisted report/citation/preferences/disabled-rule state across reviewed same-schema upgrade and rollback; Lab HTTP acceptance only")
}

// These tests exercise the live harness' transport and phase admission guards
// without enabling any Lab action or opening a network connection.
type labInstallRoundTrip func(*http.Request) (*http.Response, error)

func (f labInstallRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLabInstallationRequestBoundsBeforeTransport(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: labInstallRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	for _, target := range []string{"http://dashboard.lab.test:48443/", "https://dashboard.lab.test:8443/", "https://other.test:48443/", "https://user:secret@dashboard.lab.test:48443/", "https://dashboard.lab.test:48443/#x"} {
		if _, err := labInstallExchange(t.Context(), client, "GET", target, nil, nil); err == nil {
			t.Fatalf("unsafe target accepted: %s", target)
		}
	}
	if _, err := labInstallExchange(t.Context(), client, "POST", labInstallOrigin+"/api/v1/rules", nil, make([]byte, 4097)); err == nil || calls != 0 {
		t.Fatal("invalid input reached transport")
	}
	response, err := labInstallExchange(t.Context(), client, "GET", labInstallOrigin+"/healthz", nil, nil)
	if err != nil || response.status != 200 || calls != 1 {
		t.Fatal("fixed installation origin should remain usable")
	}
	client.Transport = labInstallRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (4<<20)+1)))}, nil
	})
	if _, err := labInstallExchange(t.Context(), client, "GET", labInstallOrigin+"/api/v1/jobs", nil, nil); err == nil {
		t.Fatal("oversized reply accepted")
	}
}

func TestLabInstallationAuthorizationPinsFreshClientAndCallback(t *testing.T) {
	state := strings.Repeat("s", 43)
	q := url.Values{"client_id": {labInstallClient}, "redirect_uri": {labInstallOrigin + "/auth/callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {strings.Repeat("c", 43)}, "nonce": {strings.Repeat("n", 43)}, "state": {state}, "resource": {"jobman-dashboard-api"}, "scope": {"openid profile"}}
	header := func(values url.Values) http.Header {
		return http.Header{"Location": {labWebIssuerOrigin + "/realms/jobman-lab/protocol/openid-connect/auth?" + values.Encode()}}
	}
	if _, err := labInstallAuthorization(header(q), state); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ key, value string }{{"client_id", "jobman-dashboard-web"}, {"redirect_uri", "https://dashboard.lab.test:8443/auth/callback"}, {"state", "changed"}, {"code_challenge_method", "plain"}, {"resource", "jobman-control"}} {
		copy := url.Values{}
		for k, v := range q {
			copy[k] = append([]string{}, v...)
		}
		copy.Set(change.key, change.value)
		if _, err := labInstallAuthorization(header(copy), state); err == nil {
			t.Fatalf("changed %s accepted", change.key)
		}
	}
	q.Add("client_id", labInstallClient)
	if _, err := labInstallAuthorization(header(q), state); err == nil {
		t.Fatal("duplicate protocol parameter accepted")
	}
}

func TestLabInstallationRejectsUnreviewedPhaseBeforeImplementation(t *testing.T) {
	for _, phase := range []string{"snapshot", "migrate", "database", "identity", "stage", "drop", "restart"} {
		if err := (labInstallDriver{}).run(t.Context(), phase, ""); err == nil || err.Error() != "installation harness phase denied" {
			t.Fatalf("phase %s did not fail at the allowlist", phase)
		}
	}
}

func TestLabInstallationImplementationPins(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	labRoot := filepath.Join(root, "actual-lab")
	if err := os.Mkdir(labRoot, 0700); err != nil {
		t.Fatal(err)
	}
	scripts, staging := filepath.Join(root, "scripts"), filepath.Join(root, "prepared")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	p := labInstallPlan{Format: 1, Synthetic: true, OperationID: "11000000-0000-4000-8000-000000000001", PostFixRevision: labInstallFix, ImplementationSHA256: map[string]string{}}
	p.Configs.API.PublicOrigin, p.Configs.API.Listen, p.Configs.API.OIDC.WebClientID = labInstallOrigin, "10.77.0.10:48443", labInstallClient
	p.Candidates = map[string]struct {
		Revision      string `json:"revision"`
		ArchiveSHA256 string `json:"archiveSHA256"`
	}{"baseline": {strings.Repeat("a", 40), strings.Repeat("c", 64)}, "upgrade": {strings.Repeat("b", 40), strings.Repeat("d", 64)}}
	for _, name := range labInstallFiles {
		content := []byte("# reviewed " + name + "\n")
		if name == "install-dashboard-fresh.py" {
			content = []byte("import json,sys\nassert sys.argv[1] == 'stop'\nassert sys.argv[sys.argv.index('--lab-root')+1] == " + strconv.Quote(labRoot) + "\nprint(json.dumps({'completed': True, 'phase': 'stop', 'operationId': '" + p.OperationID + "'}))\n")
		}
		if err := os.WriteFile(filepath.Join(scripts, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		p.ImplementationSHA256[name] = labInstallDigest(content)
	}
	raw, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(staging, "plan.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	set, _ := json.MarshalIndent(p.ImplementationSHA256, "", "  ")
	planHash, implementationHash := labInstallDigest(raw), labInstallDigest(append(set, '\n'))
	if driver, err := labInstallLoad(labRoot, root, staging, planHash, implementationHash); err != nil {
		t.Fatal(err)
	} else if driver.root != labRoot || driver.implementationRoot != root {
		t.Fatal("driver archive and live Lab roots conflated")
	} else if err := driver.run(t.Context(), "stop", ""); err != nil {
		t.Fatal("isolated archived-script invocation failed: ", err)
	}
	driver, err := labInstallLoad(labRoot, root, staging, planHash, implementationHash)
	if err != nil {
		t.Fatal(err)
	}
	adapterRoot := filepath.Join(root, "private-adapter")
	if err := os.Mkdir(adapterRoot, 0700); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(adapterRoot, "reviewed-continuation.py")
	adapterRaw := []byte("import json,sys\nassert sys.argv[1] == 'stop'\nassert sys.argv[sys.argv.index('--implementation-root')+1] == " + strconv.Quote(root) + "\nassert sys.argv[sys.argv.index('--lab-root')+1] == " + strconv.Quote(labRoot) + "\nassert len(sys.argv[sys.argv.index('--expected-adapter-sha256')+1]) == 64\nassert '--apply' in sys.argv\nprint(json.dumps({'completed': True, 'phase': 'stop', 'operationId': '" + p.OperationID + "'}))\n")
	if err := os.WriteFile(adapter, adapterRaw, 0600); err != nil {
		t.Fatal(err)
	}
	driver, err = driver.withAdapter(adapter, labInstallDigest(adapterRaw))
	if err != nil || driver.run(t.Context(), "stop", "") != nil {
		t.Fatal("explicit hash-bound continuation invocation failed", err)
	}
	if err := os.WriteFile(adapter, []byte("raise Exception('changed')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := driver.run(t.Context(), "stop", ""); err == nil || err.Error() != "installation adapter unavailable or changed" {
		t.Fatal("changed adapter was executed", err)
	}
	if err := os.WriteFile(filepath.Join(scripts, labInstallFiles[0]), []byte("# altered\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := labInstallLoad(labRoot, root, staging, planHash, implementationHash); err == nil {
		t.Fatal("changed reviewed implementation accepted")
	}
}

func TestLabInstallationAdapterAdmission(t *testing.T) {
	driver := labInstallDriver{}
	if result, err := driver.withAdapter("", ""); err != nil || result.adapterPath != "" {
		t.Fatal("unmodified original driver should remain the default")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "adapter.py")
	raw := []byte("# independently reviewed self-contained adapter\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	digest := labInstallDigest(raw)
	if _, err := driver.withAdapter(path, digest); err != nil {
		t.Fatal("valid private adapter rejected", err)
	}
	for _, test := range [][2]string{{path, ""}, {"", digest}, {"relative.py", digest}, {path, strings.ToUpper(digest)}, {path, strings.Repeat("g", 64)}, {path, strings.Repeat("0", 64)}} {
		if _, err := driver.withAdapter(test[0], test[1]); err == nil {
			t.Fatal("unreviewed adapter accepted")
		}
	}
	link := filepath.Join(root, "alias.py")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.withAdapter(link, digest); err == nil {
		t.Fatal("adapter symlink accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.withAdapter(path, digest); err == nil {
		t.Fatal("non-private adapter accepted")
	}
}
