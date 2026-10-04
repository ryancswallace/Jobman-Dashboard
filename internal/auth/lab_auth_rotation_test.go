//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// This is an explicitly coordinated, one-time API authentication-master
// rotation. Secrets stay in memory or the guest's private key files. No old-key
// rollback, database mutation, source change or job submission is performed by
// this harness. Failure preserves the driver receipts for forward recovery.
func TestLabAPIAuthenticationMasterRotation(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_AUTH_ROTATION") != "1" {
		t.Skip("explicit reviewed API authentication rotation opt-ins required")
	}
	driver, err := labLoadRotationDriver(os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT"), os.Getenv("JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_DRIVER"), os.Getenv("JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_STAGING"), os.Getenv("JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_PLAN_SHA256"), os.Getenv("JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_IMPLEMENTATION_SHA256"))
	if err != nil {
		t.Fatal("Reviewed staged authentication rotation is unavailable")
	}
	// Stage was completed separately, before any cookies or pending login exist.
	// The harness only applies that exact plan and verifies its forward result.
	if err := driver.staged(); err != nil {
		t.Fatal("One-time rotation must be staged but not applied before this test")
	}
	native := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	password, err := labWebPassword(native.root)
	if err != nil || native.root != driver.root {
		t.Fatal("Synthetic rotation identity credentials unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	old := labNewRotationWeb(t, native)
	pending := labNewRotationWeb(t, native)
	fresh := labNewRotationWeb(t, native)
	// Register all session and pending-login cleanup before initiating any flow.
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		for _, session := range []*labRotationWeb{old, pending, fresh} {
			if err := session.cleanup(cleanup); err != nil {
				t.Error("Rotation browser credential cleanup remains unverified; retain the private driver receipts")
			}
		}
	})
	request := func(client *http.Client, path string, header http.Header, target any) labWebResponse {
		t.Helper()
		response, err := labWebExchange(ctx, client, "GET", labWebOrigin+path, header, nil)
		if err != nil || response.status != 200 || target != nil && json.Unmarshal(response.body, target) != nil {
			t.Fatal("Rotation evidence read failed; response contents withheld")
		}
		return response
	}
	nativeHeaders := http.Header{"Authorization": {"Bearer " + native.accessToken}}
	baseline, err := labWebBootstrap(request(old.bare, "/api/v1/bootstrap", nativeHeaders, nil))
	if err != nil || baseline.CSRFToken != "" {
		t.Fatal("Independent signed account baseline invalid")
	}
	if err := old.begin(ctx); err != nil {
		t.Fatal("Old-key login initiation failed")
	}
	if err := old.finish(ctx, password, true); err != nil {
		t.Fatal("Old-key browser session establishment failed")
	}
	before, err := labWebBootstrap(request(old.client, "/api/v1/bootstrap", nil, nil))
	if err != nil || before.Account != baseline.Account || before.Preferences != baseline.Preferences || len(before.CSRFToken) != 43 || time.Until(old.expires) < 3*time.Minute {
		t.Fatal("Old session identity or remaining lifetime cannot support rotation acceptance")
	}
	old.csrf = before.CSRFToken

	// Select immutable, already accepted real subprocess evidence. No new report
	// request or workload is needed for a key-separation acceptance check.
	// A source-version upgrade may already mark the historical report outdated.
	// Preserve that flag with the entire sealed projection across key rotation;
	// report freshness is covered by the separate current-report acceptance.
	var prior struct {
		Synthetic       bool
		ObservationMode string
		Fixture         labActualReceipt
		Jobs, Reports   map[string]string
	}
	receiptPath := os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_RECEIPT")
	if !filepath.IsAbs(receiptPath) || json.Unmarshal(labExecutionFile(t, receiptPath, 65536), &prior) != nil || !prior.Synthetic || prior.ObservationMode != "actual-subprocess-agent-execution" || prior.Fixture.DeploymentID != labDeployment || prior.Fixture.Namespace != "dashboard-operations" || prior.Fixture.ControlInstanceID != "e633cf92-258d-48ff-965a-fda88d68ef3a" {
		t.Fatal("Accepted actual-execution report receipt required")
	}
	for _, id := range []string{prior.Fixture.NamespaceID, prior.Jobs["failure"], prior.Reports["metadata"], prior.Reports["include_log_tail"]} {
		if !uuid(id) {
			t.Fatal("Original evidence identity is invalid")
		}
	}
	prefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + prior.Fixture.NamespaceID + "/jobs/" + prior.Jobs["failure"]
	var job api.JobDetail
	request(old.client, prefix, nil, &job)
	if job.Job.ID != prior.Jobs["failure"] || job.Job.Outcome != "failure" || job.Job.CurrentRun == nil || !uuid(job.Job.CurrentRun.ID) || !uuid(job.Job.CurrentRun.ExecutionID) {
		t.Fatal("Completed job no longer matches retained execution evidence")
	}
	sealed := map[string]api.Report{}
	for _, profile := range []string{"metadata", "include_log_tail"} {
		var report api.Report
		path := prefix + "/reports/" + prior.Reports[profile]
		request(old.client, path, nil, &report)
		if report.TaskID != prior.Reports[profile] || report.Detail == nil || report.State != "ready" || report.Profile != profile || report.Detail.ControlInstanceID != prior.Fixture.ControlInstanceID || report.RunID != job.Job.CurrentRun.ID {
			t.Fatal("Original sealed report identity differs")
		}
		sealed[path] = report
	}
	logReport := sealed[prefix+"/reports/"+prior.Reports["include_log_tail"]]
	citations := map[string]api.Citation{}
	logCitation := false
	for _, ref := range logReport.Detail.Citations {
		if ref.ID == "" || len(citations) >= 64 {
			t.Fatal("Citation set exceeds bounded acceptance input")
		}
		path := prefix + "/reports/" + logReport.TaskID + "/citations/" + url.PathEscape(ref.ID)
		var citation api.Citation
		request(old.client, path, nil, &citation)
		if citation.ReportID != logReport.ReportID || citation.EvidenceID != logReport.EvidenceID || citation.ID != ref.ID {
			t.Fatal("Original sealed citation identity differs")
		}
		citations[path] = citation
		logCitation = logCitation || citation.BytesBase64 != nil
	}
	if len(citations) == 0 || !logCitation {
		t.Fatal("Original report has no sealed log-byte citation evidence")
	}
	var log api.LogRange
	request(old.client, prefix+"/logs?stream=stderr&limitBytes=262144", nil, &log)
	if !labRotationLog(log, job.Job, labActualFailureLog) || log.NextCursor == "" {
		t.Fatal("Original NFS log or forward cursor differs")
	}
	// The pending login has encrypted server state but no authorization code
	// yet. Obtain its fresh code only after rotation, avoiding code-expiry as a
	// possible explanation for the required old-state401.
	if err := pending.begin(ctx); err != nil {
		t.Fatal("Pending old-key browser login initiation failed")
	}
	started := time.Now()
	for _, phase := range []string{"apply", "restart", "verify"} {
		if err := driver.run(ctx, phase); err != nil {
			t.Fatal("Reviewed API rotation phase failed; preserve receipts and recover forward without restoring the retired key")
		}
	}
	if time.Since(started) > 2*time.Minute || !time.Now().Before(old.expires) {
		t.Fatal("Rotation exceeded credential-lifetime acceptance bound; do not infer retirement from expiry")
	}
	denied, err := labWebExchange(ctx, old.bare, "GET", labWebOrigin+"/api/v1/bootstrap", http.Header{"Cookie": {sessionCookie + "=" + old.cookie}}, nil)
	if err != nil || !labRotationDenied(denied) {
		t.Fatal("Retired authentication master still accepted a nonexpired old session")
	}
	if err := pending.finish(ctx, password, false); err != nil {
		t.Fatal("Retired master did not reject original unconsumed login state with a fresh authorization code")
	}
	if err := fresh.begin(ctx); err != nil {
		t.Fatal("New-key browser login initiation failed")
	}
	if err := fresh.finish(ctx, password, true); err != nil {
		t.Fatal("New-key browser session establishment failed")
	}
	after, err := labWebBootstrap(request(fresh.client, "/api/v1/bootstrap", nil, nil))
	if err != nil || after.Account != baseline.Account || after.Preferences != baseline.Preferences || len(after.CSRFToken) != 43 || fresh.cookie == old.cookie || after.CSRFToken == old.csrf {
		t.Fatal("New-key account, unchanged preferences or distinct credential proof invalid")
	}
	fresh.csrf = after.CSRFToken
	// Existing native credentials use the signed IdP contract, independently of
	// the rotated browser master. Require the same immutable account afterward.
	nativeAfter, err := labWebBootstrap(request(fresh.bare, "/api/v1/bootstrap", nativeHeaders, nil))
	if err != nil || nativeAfter.Account != baseline.Account || nativeAfter.Preferences != baseline.Preferences || nativeAfter.CSRFToken != "" {
		t.Fatal("Native identity or account data changed during browser-key rotation")
	}
	for path, before := range sealed {
		var after api.Report
		request(fresh.client, path, nil, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("Authentication-only rotation changed original report or pair identity")
		}
	}
	for path, before := range citations {
		var after api.Citation
		request(fresh.client, path, nil, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("Authentication-only rotation changed sealed citation bytes or provenance")
		}
	}
	var retained, forward api.LogRange
	request(fresh.client, prefix+"/logs?stream=stderr&limitBytes=262144", nil, &retained)
	request(fresh.client, prefix+"/logs?stream=stderr&limitBytes=262144&cursor="+url.QueryEscape(log.NextCursor), nil, &forward)
	if !labRotationLog(retained, job.Job, labActualFailureLog) || forward.State != "complete" || forward.BytesBase64 != "" || forward.StartOffset != log.EndOffset || forward.EndOffset != log.EndOffset || forward.ExecutionID != log.ExecutionID || forward.RunID != log.RunID || forward.RunNumber != log.RunNumber || forward.Stream != log.Stream {
		t.Fatal("Dedicated log cursor or original producer bytes changed during authentication rotation")
	}
	t.Log("PASS: API-only master rotation rejected nonexpired old session and old encrypted login, accepted new synthetic Keycloak web credentials and existing native identity, and preserved exact reports/citations/NFS bytes and forward cursor; no old-key rollback")
}

func labRotationDenied(response labWebResponse) bool {
	var failure api.Error
	return response.status == 401 && json.Unmarshal(response.body, &failure) == nil && failure.Code == "unauthenticated" && labWebCallbackReplayRejected(response)
}

func labRotationLog(value api.LogRange, job api.Job, expected string) bool {
	raw, err := base64.StdEncoding.DecodeString(value.BytesBase64)
	return err == nil && string(raw) == expected && value.State == "complete" && value.Stream == "stderr" && value.StartOffset == "0" && value.EndOffset == jsonNumber(len(raw)) && job.CurrentRun != nil && value.RunID == job.CurrentRun.ID && value.RunNumber == job.CurrentRun.Number && value.ExecutionID == job.CurrentRun.ExecutionID
}

func jsonNumber(n int) string { raw, _ := json.Marshal(n); return string(raw) }

type labRotationWeb struct {
	client, bare                   *http.Client
	authorize, state, cookie, csrf string
	requested, pending, admission  bool
	expires, initiated             time.Time
}

func labNewRotationWeb(t *testing.T, native labNativeSession) *labRotationWeb {
	t.Helper()
	transport := native.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		destination := map[string]string{"dashboard.lab.test:8443": "10.77.0.10:8443", "oidc.lab.test:8443": "10.77.0.21:8443"}[address]
		if destination == "" {
			return nil, errors.New("rotation destination denied")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, destination)
	}
	t.Cleanup(transport.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("Rotation cookie jar unavailable")
	}
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	bare := *client
	bare.Jar = nil
	return &labRotationWeb{client: client, bare: &bare}
}

func (s *labRotationWeb) begin(ctx context.Context) error {
	s.requested, s.initiated = true, time.Now()
	response, err := labWebExchange(ctx, s.client, "GET", labWebOrigin+"/auth/login?returnTo=%2Fsettings", nil, nil)
	if err != nil || response.status != 302 {
		return errors.New("rotation login start failed")
	}
	cookie, err := labWebCookie(response.header, loginCookie, false)
	if err != nil || cookie.MaxAge != 300 {
		return errors.New("rotation login cookie invalid")
	}
	s.state, s.pending = cookie.Value, true
	s.authorize, err = labWebAuthorization(response.header, s.state)
	return err
}

func (s *labRotationWeb) finish(ctx context.Context, password string, accept bool) error {
	if !s.pending || len(s.state) != 43 || time.Since(s.initiated) > 3*time.Minute {
		return errors.New("pending login timing invalid")
	}
	form, err := labWebExchange(ctx, s.client, "GET", s.authorize, nil, nil)
	if err != nil || form.status != 200 {
		return errors.New("rotation provider form unavailable")
	}
	action, err := labWebForm(form.body)
	if err != nil {
		return errors.New("rotation provider form invalid")
	}
	body := url.Values{"username": {"dashboard-alice"}, "password": {password}, "credentialId": {""}}
	posted, err := labWebExchange(ctx, s.client, "POST", action, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(body.Encode()))
	if err != nil || posted.status != 302 {
		return errors.New("rotation provider login failed")
	}
	callback, err := labWebCallback(posted.header, s.state)
	if err != nil {
		return errors.New("rotation callback invalid")
	}
	s.admission = true // cleanup must cover lost response or any failed assertion
	response, err := labWebExchange(ctx, s.client, "GET", callback, nil, nil)
	if err != nil {
		return errors.New("rotation callback unavailable")
	}
	if !accept {
		if !labRotationDenied(response) {
			return errors.New("old encrypted login not rejected")
		}
		s.pending, s.admission, s.requested = false, false, false
		return nil
	}
	if response.status != 303 || response.header.Get("Location") != "/settings" {
		return errors.New("rotation callback result invalid")
	}
	cookie, err := labWebCookie(response.header, sessionCookie, false)
	if err != nil || cookie.MaxAge != 0 || !cookie.Expires.After(time.Now()) {
		return errors.New("rotation session cookie invalid")
	}
	s.cookie, s.expires, s.pending = cookie.Value, cookie.Expires, false
	return nil
}

func (s *labRotationWeb) cleanup(ctx context.Context) error {
	if s.admission {
		return labWebCleanupSession(ctx, s.client, s.bare, s.cookie, s.csrf)
	}
	if !s.requested {
		return nil
	}
	if s.state == "" && s.client.Jar != nil {
		origin, _ := url.Parse(labWebOrigin)
		for _, cookie := range s.client.Jar.Cookies(origin) {
			if cookie.Name == loginCookie {
				s.state = cookie.Value
			}
		}
	}
	if len(s.state) != 43 {
		return errors.New("pending login creation uncertain; no recoverable state cookie")
	}
	// Consume only this test's one-time state through the ordinary endpoint.
	// No valid provider code is supplied, so no session may be admitted.
	response, err := labWebExchange(ctx, s.bare, "GET", labWebOrigin+"/auth/callback?state="+url.QueryEscape(s.state)+"&code=synthetic-rotation-abandoned", http.Header{"Cookie": {loginCookie + "=" + s.state}}, nil)
	if err != nil || !labRotationDenied(response) {
		return errors.New("pending login cleanup unverified")
	}
	s.requested, s.pending = false, false
	return nil
}

type labRotationDriver struct{ root, script, staging, planSHA, implementationSHA, revision string }

func labRotationPrivate(path string, maximum int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("private rotation path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, errors.New("rotation input alias rejected")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() <= 0 || before.Size() > maximum {
		return nil, errors.New("private rotation input rejected")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("private rotation input unavailable")
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(before, current) {
		return nil, errors.New("private rotation input changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	after, afterErr := file.Stat()
	named, namedErr := os.Lstat(path)
	if err != nil || afterErr != nil || namedErr != nil || int64(len(raw)) != before.Size() || !os.SameFile(before, named) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("private rotation input changed")
	}
	return raw, nil
}

func labLoadRotationDriver(root, script, staging, planSHA, implementationSHA string) (labRotationDriver, error) {
	d := labRotationDriver{root: root, script: script, staging: staging, planSHA: planSHA, implementationSHA: implementationSHA}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !filepath.IsAbs(root) || !filepath.IsAbs(script) || !filepath.IsAbs(staging) || filepath.Base(script) != "rotate-dashboard-auth.py" || !hex64.MatchString(planSHA) || !hex64.MatchString(implementationSHA) {
		return d, errors.New("rotation driver inputs invalid")
	}
	raw, err := labRotationPrivate(filepath.Join(staging, "plan.json"), 4<<20)
	sum := sha256.Sum256(raw)
	var plan struct {
		Scenario              string
		Synthetic             bool
		Revision              string
		ConfigurationRevision int
		ImplementationSHA256  map[string]string
	}
	if err != nil || hex.EncodeToString(sum[:]) != planSHA || json.Unmarshal(raw, &plan) != nil || plan.Scenario != "dashboard-api-authentication-rotation" || !plan.Synthetic || plan.ConfigurationRevision != 8 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(plan.Revision) || len(plan.ImplementationSHA256) != 4 {
		return d, errors.New("rotation plan binding invalid")
	}
	// Python's reviewed manifest uses sorted ASCII keys, two-space indentation
	// and a final newline. All names and digest values here are ASCII.
	manifest, err := json.MarshalIndent(plan.ImplementationSHA256, "", "  ")
	manifest = append(manifest, '\n')
	manifestSum := sha256.Sum256(manifest)
	if err != nil || hex.EncodeToString(manifestSum[:]) != implementationSHA {
		return d, errors.New("rotation implementation review digest differs")
	}
	for _, name := range []string{"rotate-dashboard-auth.py", "dashboard-auth-rotation-plan.py", "dashboard-auth-rotation-guest.py", "dashboard-multisource-runtime.py"} {
		expected, ok := plan.ImplementationSHA256[name]
		if !ok || !hex64.MatchString(expected) {
			return d, errors.New("rotation implementation inventory invalid")
		}
		path := filepath.Join(filepath.Dir(script), name)
		content, err := labScaleReadFile(path, 1<<20)
		digest := sha256.Sum256(content)
		info, statErr := os.Stat(path)
		if err != nil || statErr != nil || info.Mode().Perm()&0022 != 0 || hex.EncodeToString(digest[:]) != expected {
			return d, errors.New("reviewed rotation implementation changed")
		}
	}
	d.revision = plan.Revision
	return d, nil
}

func (d labRotationDriver) staged() error {
	raw, err := labRotationPrivate(filepath.Join(d.staging, "stage.json"), 8192)
	var stage struct {
		PlanSHA256, KeyID, KeySHA256 string
		StagedAt                     int64
	}
	if err != nil || json.Unmarshal(raw, &stage) != nil || stage.PlanSHA256 != d.planSHA || stage.KeyID != "lab-auth-rotation-v1" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(stage.KeySHA256) || time.Since(time.Unix(stage.StagedAt, 0)) < 0 || time.Since(time.Unix(stage.StagedAt, 0)) > time.Hour {
		return errors.New("rotation staging receipt invalid")
	}
	for _, name := range []string{"apply.json", "apply.pending.json", "restart.json", "restart.pending.json", "verify.json"} {
		if _, err := os.Lstat(filepath.Join(d.staging, name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("rotation was already attempted")
		}
	}
	return nil
}

type labRotationOutput struct {
	buffer  bytes.Buffer
	maximum int
}

func (b *labRotationOutput) Bytes() []byte { return b.buffer.Bytes() }
func (b *labRotationOutput) Len() int      { return b.buffer.Len() }

func (b *labRotationOutput) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.maximum {
		return 0, errors.New("rotation command output exceeded bound")
	}
	return b.buffer.Write(value)
}

func (d labRotationDriver) run(ctx context.Context, phase string) error {
	if phase != "apply" && phase != "restart" && phase != "verify" {
		return errors.New("rotation command phase denied")
	}
	if _, err := labLoadRotationDriver(d.root, d.script, d.staging, d.planSHA, d.implementationSHA); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "python3", d.script, phase, "--lab-root", d.root, "--staging", d.staging, "--expected-plan-sha256", d.planSHA, "--expected-implementation-sha256", d.implementationSHA, "--apply")
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	output := &labRotationOutput{maximum: 8192}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	if cmd.Run() != nil {
		return errors.New("bounded rotation phase failed; state retained")
	}
	var result struct {
		PlanSHA256, Phase   string
		Completed, Verified bool
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || result.PlanSHA256 != d.planSHA || (phase == "verify" && !result.Verified) || (phase != "verify" && (!result.Completed || result.Phase != phase)) {
		return errors.New("rotation result binding invalid")
	}
	return nil
}

func TestLabRotationCryptoRetirementContract(t *testing.T) {
	old, _ := newSecretBox(bytes.Repeat([]byte{1}, 32), "old")
	current, _ := newSecretBox(bytes.Repeat([]byte{2}, 32), "current")
	payload, err := old.seal([]byte(`{"nonce":"synthetic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.open(payload); err == nil {
		t.Fatal("retired login state decrypted")
	}
	if old.csrf("synthetic-session") == current.csrf("synthetic-session") {
		t.Fatal("retired session MAC remained valid")
	}
	newPayload, _ := current.seal([]byte("current"))
	if raw, err := current.open(newPayload); err != nil || string(raw) != "current" {
		t.Fatal("current login state failed")
	}
	newPayload[len(newPayload)-1] ^= 1
	if _, err := current.open(newPayload); err == nil {
		t.Fatal("corrupt current state accepted")
	}
	// There is no readable key-ID envelope. Both failed cryptographic checks
	// above are invalid credentials; they do not imply a database outage.
}

func TestLabRotationOIDCRejectsRetiredCredentialsAndAcceptsCurrent(t *testing.T) {
	f := newOIDCFixture(t)
	complete := func(cookie *http.Cookie, state string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		f.o.callback(response, request)
		return response
	}
	oldLogin, oldState := f.begin(t)
	admitted := complete(oldLogin, oldState)
	if admitted.Code != 303 {
		t.Fatal("baseline callback failed")
	}
	var oldSession *http.Cookie
	for _, cookie := range admitted.Result().Cookies() {
		if cookie.Name == sessionCookie {
			oldSession = cookie
		}
	}
	if oldSession == nil {
		t.Fatal("baseline session missing")
	}
	oldRequest := httptest.NewRequest("GET", "https://dashboard.example/api/v1/bootstrap", nil)
	oldRequest.AddCookie(oldSession)
	if _, err := f.o.Authenticate(oldRequest); err != nil {
		t.Fatal("baseline session rejected")
	}
	leftCookie, leftState := f.begin(t)
	oldTokenCalls := f.tokenCalls
	f.o.box, _ = newSecretBox(bytes.Repeat([]byte{9}, 32), "rotated")
	if _, err := f.o.Authenticate(oldRequest); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("retired session did not become invalid credentials")
	}
	if rejected := complete(leftCookie, leftState); rejected.Code != 401 || f.tokenCalls != oldTokenCalls {
		t.Fatal("retired encrypted login reached provider or was not rejected401")
	}
	// A malformed record under the current key is also cryptographically
	// invalid, unlike a genuine transport/store outage handled by deadline tests.
	brokenCookie, brokenState := f.begin(t)
	store := f.o.store.(*memoryIdentityStore)
	store.mu.Lock()
	value := store.logins[string(tokenHash(brokenState))]
	value[len(value)-1] ^= 1
	store.mu.Unlock()
	if complete(brokenCookie, brokenState).Code != 401 {
		t.Fatal("invalid current ciphertext became an availability error")
	}
	currentCookie, currentState := f.begin(t)
	current := complete(currentCookie, currentState)
	if current.Code != 303 {
		t.Fatal("new-key callback failed")
	}
	for _, cookie := range current.Result().Cookies() {
		if cookie.Name == sessionCookie {
			request := httptest.NewRequest("GET", "https://dashboard.example/api/v1/bootstrap", nil)
			request.AddCookie(cookie)
			if _, err := f.o.Authenticate(request); err != nil {
				t.Fatal("new-key session failed")
			}
			return
		}
	}
	t.Fatal("new-key session missing")
}

func TestLabRotationPendingLoginCleanupUsesOnlyRetainedState(t *testing.T) {
	state := strings.Repeat("s", 43)
	called := false
	client := &http.Client{Transport: labWebRoundTripper(func(request *http.Request) (*http.Response, error) {
		called = true
		if request.URL.Host != "dashboard.lab.test:8443" || request.URL.Path != "/auth/callback" || request.URL.Query().Get("state") != state || request.URL.Query().Get("code") != "synthetic-rotation-abandoned" || request.Header.Get("Cookie") != loginCookie+"="+state {
			t.Fatal("cleanup escaped retained state")
		}
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":"unauthenticated"}`))}, nil
	})}
	flow := &labRotationWeb{client: client, bare: client, requested: true, pending: true, state: state}
	if err := flow.cleanup(t.Context()); err != nil || !called || flow.pending {
		t.Fatal("pending cleanup failed")
	}
	flow = &labRotationWeb{client: client, bare: client, requested: true}
	called = false
	if err := flow.cleanup(t.Context()); err == nil || called {
		t.Fatal("unknown pending state cleanup fabricated success")
	}
}

func TestLabRotationDenialMustBe401WithoutIssuedSession(t *testing.T) {
	valid := labWebResponse{status: 401, header: http.Header{}, body: []byte(`{"code":"unauthenticated"}`)}
	if !labRotationDenied(valid) {
		t.Fatal("valid credential rejection failed")
	}
	for _, status := range []int{200, 400, 403, 500, 503} {
		invalid := valid
		invalid.status = status
		if labRotationDenied(invalid) {
			t.Fatal("non-401 counted as retirement")
		}
	}
	valid.header.Add("Set-Cookie", sessionCookie+"="+strings.Repeat("x", 43)+"; Path=/; Secure; HttpOnly; SameSite=Lax")
	if labRotationDenied(valid) {
		t.Fatal("session issuance counted as rejected login")
	}
}

func TestLabRotationPrivateInputAndCommandBounds(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	path, _ = filepath.EvalSymlinks(path)
	if _, err := labRotationPrivate(path, 8); err == nil {
		t.Fatal("oversized input accepted")
	}
	if _, err := labRotationPrivate(path, 32); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := labRotationPrivate(path, 32); err == nil {
		t.Fatal("public private input accepted")
	}
	output := &labRotationOutput{maximum: 8}
	if _, err := output.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("9")); err == nil {
		t.Fatal("unbounded child output")
	}
	if err := (labRotationDriver{}).run(t.Context(), "rollback"); err == nil {
		t.Fatal("retired-key rollback phase accepted")
	}
}

func TestLabRotationReviewDigestAndOneTimeStage(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, name := range []string{"rotate-dashboard-auth.py", "dashboard-auth-rotation-plan.py", "dashboard-auth-rotation-guest.py", "dashboard-multisource-runtime.py"} {
		content := []byte("# offline synthetic fixture\n")
		if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		sources[name] = hex.EncodeToString(sum[:])
	}
	manifest, _ := json.MarshalIndent(sources, "", "  ")
	manifest = append(manifest, '\n')
	manifestSum := sha256.Sum256(manifest)
	plan, _ := json.Marshal(map[string]any{"scenario": "dashboard-api-authentication-rotation", "synthetic": true, "configurationRevision": 8, "revision": strings.Repeat("a", 40), "implementationSHA256": sources})
	planSum := sha256.Sum256(plan)
	planSHA, implementationSHA := hex.EncodeToString(planSum[:]), hex.EncodeToString(manifestSum[:])
	if err := os.WriteFile(filepath.Join(root, "plan.json"), plan, 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "rotate-dashboard-auth.py")
	driver, err := labLoadRotationDriver(root, script, root, planSHA, implementationSHA)
	if err != nil {
		t.Fatal(err)
	}
	for _, hashes := range [][2]string{{strings.Repeat("b", 64), implementationSHA}, {planSHA, strings.Repeat("b", 64)}} {
		if _, err := labLoadRotationDriver(root, script, root, hashes[0], hashes[1]); err == nil {
			t.Fatal("unreviewed hash accepted")
		}
	}
	stage, _ := json.Marshal(map[string]any{"planSHA256": planSHA, "keyId": "lab-auth-rotation-v1", "keySHA256": strings.Repeat("c", 64), "stagedAt": time.Now().Unix()})
	if err := os.WriteFile(filepath.Join(root, "stage.json"), stage, 0600); err != nil {
		t.Fatal(err)
	}
	if err := driver.staged(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apply.pending.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := driver.staged(); err == nil {
		t.Fatal("uncertain prior mutation permitted new baseline")
	}
	if err := os.WriteFile(script, []byte("# changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := labLoadRotationDriver(root, script, root, planSHA, implementationSHA); err == nil {
		t.Fatal("changed implementation accepted")
	}
}

func TestLabRotationEarlyAdmissionFailureRecoversJarForCleanup(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(labWebOrigin)
	credential := strings.Repeat("s", 43)
	jar.SetCookies(origin, []*http.Cookie{{Name: sessionCookie, Value: credential, Path: "/", Secure: true}})
	loggedOut := false
	client := &http.Client{Jar: jar, Transport: labWebRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Cookie") != sessionCookie+"="+credential {
			t.Fatal("cleanup lost callback-issued cookie")
		}
		status, body := 200, `{"apiVersion":"jobman.dashboard/v1","account":{"id":"44444444-4444-4444-8444-444444444444","displayName":"Synthetic"},"completeness":"complete","csrfToken":"`+strings.Repeat("c", 43)+`"}`
		if request.Method == "POST" {
			if request.URL.Path != "/auth/logout" || request.Header.Get("X-CSRF-Token") != strings.Repeat("c", 43) {
				t.Fatal("cleanup lacked recovered CSRF")
			}
			loggedOut = true
			status, body = 204, ""
		} else if loggedOut {
			status, body = 401, `{"code":"unauthenticated"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	bare := *client
	bare.Jar = nil
	flow := &labRotationWeb{client: client, bare: &bare, requested: true, pending: true, admission: true}
	if err := flow.cleanup(t.Context()); err != nil || !loggedOut {
		t.Fatal("early callback assertion failure left recoverable session active")
	}
}
