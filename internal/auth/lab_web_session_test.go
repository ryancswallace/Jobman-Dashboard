//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const labWebOrigin = "https://dashboard.lab.test:8443"
const labWebIssuerOrigin = "https://oidc.lab.test:8443"
const labWebBodyLimit = 1 << 20

// This is HTTP protocol acceptance, not browser rendering or corporate AD FS
// acceptance. Every credential, authorization code, cookie and response stays
// in memory. No automatic redirects, page scripts or arbitrary form actions run.
func TestLabDeployedWebSession(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_WEB_SESSION") != "1" {
		t.Skip("set runtime and web-session Lab opt-ins after independent review")
	}
	native := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	password, err := labWebPassword(native.root)
	if err != nil {
		t.Fatal("Synthetic web sign-in credentials unavailable")
	}
	transport := native.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		destination := map[string]string{"dashboard.lab.test:8443": "10.77.0.10:8443", "oidc.lab.test:8443": "10.77.0.21:8443"}[address]
		if destination == "" {
			return nil, errors.New("Lab web destination denied")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, destination)
	}
	t.Cleanup(transport.CloseIdleConnections)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("Web cookie jar unavailable")
	}
	web := &http.Client{Transport: transport, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	bare := *web
	bare.Jar = nil
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	request := func(client *http.Client, method, target string, headers http.Header, body []byte, status int) labWebResponse {
		t.Helper()
		response, err := labWebExchange(ctx, client, method, target, headers, body)
		if err != nil || response.status != status {
			t.Fatalf("Web protocol %s step failed (expected HTTP %d; received %d)", method, status, response.status)
		}
		return response
	}
	nativeHeaders := http.Header{"Authorization": {"Bearer " + native.accessToken}}
	request(web, "GET", labWebOrigin+"/api/v1/bootstrap", nil, nil, 401)
	nativeBootstrap, err := labWebBootstrap(request(&bare, "GET", labWebOrigin+"/api/v1/bootstrap", nativeHeaders, nil, 200))
	if err != nil || nativeBootstrap.CSRFToken != "" || !uuid(nativeBootstrap.Account.ID) || nativeBootstrap.Account.DisplayName == "" {
		t.Fatal("Signed native identity bootstrap invalid")
	}
	login := request(web, "GET", labWebOrigin+"/auth/login?returnTo=%2Fsettings", nil, nil, 302)
	loginState, err := labWebCookie(login.header, loginCookie, false)
	if err != nil || loginState.MaxAge != 300 {
		t.Fatal("Login cookie security attributes invalid")
	}
	authorize, err := labWebAuthorization(login.header, loginState.Value)
	if err != nil {
		t.Fatal("Web authorization redirect escaped fixed S256 client contract")
	}
	form := request(web, "GET", authorize, nil, nil, 200)
	action, err := labWebForm(form.body)
	if err != nil {
		t.Fatal("Pinned synthetic login form invalid")
	}
	values := url.Values{"username": {"dashboard-alice"}, "password": {password}, "credentialId": {""}}
	posted := request(web, "POST", action, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(values.Encode()), 302)
	callback, err := labWebCallback(posted.header, loginState.Value)
	if err != nil {
		t.Fatal("Web callback redirect or state invalid")
	}
	var csrf, retainedCookie string
	var original, changed api.Preferences
	mutationMayHaveCommitted, sessionActive := false, true
	// Registered before callback admission: even a lost reply or failed cookie/
	// Location assertion must attempt cleanup and report uncertain revocation.
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()
		if mutationMayHaveCommitted {
			if err := labWebRestorePreferences(cleanup, &bare, nativeHeaders, nativeBootstrap.Account.ID, original, changed); err != nil {
				t.Error("Web preference cleanup failed; inspect the synthetic Alice settings before another run")
			}
		}
		if sessionActive {
			if err := labWebCleanupSession(cleanup, web, &bare, retainedCookie, csrf); err != nil {
				t.Error("Synthetic web session cleanup revocation remains unverified")
			}
		}
	})
	signedIn := request(web, "GET", callback, nil, nil, 303)
	if locations := signedIn.header.Values("Location"); len(locations) != 1 || locations[0] != "/settings" {
		t.Fatal("Web callback did not preserve the fixed return path")
	}
	cookie, err := labWebCookie(signedIn.header, sessionCookie, false)
	if err != nil || cookie.MaxAge != 0 || !cookie.Expires.After(time.Now()) || cookie.Expires.After(time.Now().Add(8*time.Hour+5*time.Second)) {
		t.Fatal("Session cookie security attributes or lifetime invalid")
	}
	retainedCookie = cookie.Value
	if _, err := labWebCookie(signedIn.header, loginCookie, true); err != nil {
		t.Fatal("Consumed login cookie was not cleared securely")
	}
	// A consumed code/state must not create another session. Use the original
	// login cookie outside the live jar, preserving its valid session cookie.
	replay := request(&bare, "GET", callback, http.Header{"Cookie": {loginCookie + "=" + loginState.Value}}, nil, 401)
	if !labWebCallbackReplayRejected(replay) {
		t.Fatal("Consumed callback replay issued a new web session")
	}
	bootstrap, err := labWebBootstrap(request(web, "GET", labWebOrigin+"/api/v1/bootstrap", nil, nil, 200))
	if err != nil || bootstrap.Account != nativeBootstrap.Account || len(bootstrap.CSRFToken) != 43 || bootstrap.Preferences != nativeBootstrap.Preferences {
		t.Fatal("Cookie session does not match signed immutable account or stable settings baseline")
	}
	csrf, original = bootstrap.CSRFToken, bootstrap.Preferences
	changed = original
	changed.Appearance = "dark"
	if original.Appearance == "dark" {
		changed.Appearance = "light"
	}
	if _, err := labWebNextRevision(original.Revision); err != nil {
		t.Fatal("Preference revision cannot be exercised safely")
	}
	body, _ := json.Marshal(changed)
	mutationMayHaveCommitted = true
	for _, test := range []struct{ csrf, origin string }{{"", labWebOrigin}, {strings.Repeat("x", 43), labWebOrigin}, {csrf, "https://untrusted.invalid"}} {
		headers := labWebHeaders(test.csrf, original.Revision)
		headers.Set("Origin", test.origin)
		denied := request(web, "PUT", labWebOrigin+"/api/v1/preferences", headers, body, 401)
		var failure api.Error
		if json.Unmarshal(denied.body, &failure) != nil || failure.Code != "unauthenticated" {
			t.Fatal("CSRF rejection did not return authentication failure")
		}
	}
	unchanged, err := labWebBootstrap(request(web, "GET", labWebOrigin+"/api/v1/bootstrap", nil, nil, 200))
	if err != nil || unchanged.Account != bootstrap.Account || unchanged.Preferences != original || unchanged.CSRFToken != csrf {
		t.Fatal("Rejected writes altered preferences or web session identity")
	}
	updated := request(web, "PUT", labWebOrigin+"/api/v1/preferences", labWebHeaders(csrf, original.Revision), body, 200)
	changed.Revision, _ = labWebNextRevision(original.Revision)
	if !labWebPreferenceResponse(updated, changed) {
		t.Fatal("Authenticated preference update did not advance exactly one revision")
	}
	persisted, err := labWebBootstrap(request(web, "GET", labWebOrigin+"/api/v1/bootstrap", nil, nil, 200))
	if err != nil || persisted.Account != bootstrap.Account || persisted.Preferences != changed || persisted.CSRFToken != csrf {
		t.Fatal("Authenticated settings were not persisted for the same account")
	}
	restore := original
	restore.Revision = changed.Revision
	body, _ = json.Marshal(restore)
	restored := request(web, "PUT", labWebOrigin+"/api/v1/preferences", labWebHeaders(csrf, changed.Revision), body, 200)
	restore.Revision, _ = labWebNextRevision(changed.Revision)
	if !labWebPreferenceResponse(restored, restore) {
		t.Fatal("Web settings restoration did not preserve original values")
	}
	verified, err := labWebBootstrap(request(&bare, "GET", labWebOrigin+"/api/v1/bootstrap", nativeHeaders, nil, 200))
	if err != nil || verified.Account != bootstrap.Account || verified.Preferences != restore {
		t.Fatal("Independent account view did not confirm settings restoration")
	}
	mutationMayHaveCommitted = false
	loggedOut := request(web, "POST", labWebOrigin+"/auth/logout", labWebHeaders(csrf, ""), nil, 204)
	if _, err := labWebCookie(loggedOut.header, sessionCookie, true); err != nil {
		t.Fatal("Logout did not clear the secure host session cookie")
	}
	sessionActive = false
	request(web, "GET", labWebOrigin+"/api/v1/bootstrap", nil, nil, 401)
	// Use the exact retained cookie without the now-cleared jar. This checks
	// server revocation, independently of the client deleting its cookie.
	request(&bare, "GET", labWebOrigin+"/api/v1/bootstrap", http.Header{"Cookie": {sessionCookie + "=" + cookie.Value}}, nil, 401)
	request(&bare, "POST", labWebOrigin+"/auth/logout", http.Header{"Cookie": {sessionCookie + "=" + cookie.Value}, "Origin": {labWebOrigin}, "X-CSRF-Token": {csrf}}, nil, 401)
	t.Log("PASS: actual synthetic Keycloak web BFF S256/callback, signed account equivalence, secure cookie contract, CSRF/origin rejection, CAS preference change/restoration and server-side logout revocation; HTTP protocol only")
}

type labWebResponse struct {
	status int
	header http.Header
	body   []byte
}

func labWebCallbackReplayRejected(r labWebResponse) bool {
	if r.status != 401 {
		return false
	}
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == sessionCookie {
			return false
		}
	}
	return true
}

func labWebCleanupSession(ctx context.Context, web, bare *http.Client, retained, csrf string) error {
	if retained == "" && web.Jar != nil {
		origin, _ := url.Parse(labWebOrigin)
		for _, c := range web.Jar.Cookies(origin) {
			if c.Name == sessionCookie {
				if retained != "" {
					return errors.New("ambiguous cleanup session")
				}
				retained = c.Value
			}
		}
	}
	if len(retained) != 43 {
		return errors.New("callback admission uncertain; no recoverable session credential")
	}
	h := http.Header{"Cookie": {sessionCookie + "=" + retained}}
	if csrf == "" {
		r, err := labWebExchange(ctx, bare, "GET", labWebOrigin+"/api/v1/bootstrap", h, nil)
		if err == nil && r.status == 401 {
			return nil
		}
		if b, decodeErr := labWebBootstrap(r); err == nil && decodeErr == nil {
			csrf = b.CSRFToken
		}
	}
	h.Set("Origin", labWebOrigin)
	h.Set("X-CSRF-Token", csrf)
	r, err := labWebExchange(ctx, bare, "POST", labWebOrigin+"/auth/logout", h, nil)
	if err != nil || (r.status != 204 && r.status != 401) {
		return errors.New("cleanup logout unavailable")
	}
	// A401 logout may mean missing CSRF rather than revocation. Verify the
	// retained credential itself, independently of a cleared response cookie.
	r, err = labWebExchange(ctx, bare, "GET", labWebOrigin+"/api/v1/bootstrap", http.Header{"Cookie": {sessionCookie + "=" + retained}}, nil)
	if err != nil || r.status != 401 {
		return errors.New("cleanup session revocation unverified")
	}
	return nil
}

func labWebExchange(ctx context.Context, client *http.Client, method, target string, headers http.Header, body []byte) (labWebResponse, error) {
	u, err := url.Parse(target)
	if err != nil || len(target) > 16<<10 || u.Scheme != "https" || (u.Host != "dashboard.lab.test:8443" && u.Host != "oidc.lab.test:8443") || u.User != nil || u.Fragment != "" || len(body) > 4096 {
		return labWebResponse{}, errors.New("web request outside fixed bounds")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return labWebResponse{}, errors.New("web request construction failed")
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	r, err := client.Do(req)
	if err != nil {
		return labWebResponse{}, errors.New("bounded web transport failed")
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, labWebBodyLimit+1))
	if err != nil || len(raw) > labWebBodyLimit {
		return labWebResponse{}, errors.New("web response exceeded bounds")
	}
	return labWebResponse{r.StatusCode, r.Header.Clone(), raw}, nil
}

func labWebBootstrap(r labWebResponse) (api.Bootstrap, error) {
	var b api.Bootstrap
	if r.status != 200 || json.Unmarshal(r.body, &b) != nil || b.APIVersion != api.Version || b.FixtureMode || !uuid(b.Account.ID) || b.Completeness != "complete" || r.header.Get("Cache-Control") != "no-store" {
		return b, errors.New("web bootstrap invalid")
	}
	return b, nil
}

func labWebHeaders(csrf, revision string) http.Header {
	h := http.Header{"Origin": {labWebOrigin}, "Content-Type": {"application/json"}}
	if csrf != "" {
		h.Set("X-CSRF-Token", csrf)
	}
	if revision != "" {
		h.Set("If-Match", "\""+revision+"\"")
	}
	return h
}

func labWebCookie(h http.Header, name string, clearing bool) (*http.Cookie, error) {
	var found *http.Cookie
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name != name {
			continue
		}
		if found != nil || c.Domain != "" || c.Path != "/" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || (clearing && (c.Value != "" || c.MaxAge != -1)) || (!clearing && (len(c.Value) != 43 || c.MaxAge < 0)) {
			return nil, errors.New("web cookie contract invalid")
		}
		found = c
	}
	if found == nil {
		return nil, errors.New("web cookie missing")
	}
	return found, nil
}

func labWebRedirect(h http.Header, origin, path string) (*url.URL, error) {
	locations := h.Values("Location")
	if len(locations) != 1 || len(locations[0]) > 16<<10 {
		return nil, errors.New("web redirect missing or oversized")
	}
	u, err := url.Parse(locations[0])
	if err != nil || u.Scheme+"://"+u.Host != origin || u.User != nil || u.Fragment != "" || u.Path != path || u.RawPath != "" {
		return nil, errors.New("web redirect outside fixed destination")
	}
	return u, nil
}

func labWebAuthorization(h http.Header, state string) (string, error) {
	u, err := labWebRedirect(h, labWebIssuerOrigin, "/realms/jobman-lab/protocol/openid-connect/auth")
	if err != nil {
		return "", err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errors.New("web authorization query invalid")
	}
	for _, values := range q {
		if len(values) != 1 {
			return "", errors.New("duplicate authorization query")
		}
	}
	if q.Get("client_id") != "jobman-dashboard-web" || q.Get("redirect_uri") != labWebOrigin+"/auth/callback" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("nonce")) != 43 || q.Get("state") != state || q.Get("resource") != "jobman-dashboard-api" || q.Get("scope") != "openid profile" {
		return "", errors.New("web authorization contract invalid")
	}
	return u.String(), nil
}

func labWebCallback(h http.Header, state string) (string, error) {
	u, err := labWebRedirect(h, labWebOrigin, "/auth/callback")
	if err != nil {
		return "", err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["state"]) != 1 || q.Get("state") != state || len(q["code"]) != 1 || len(q.Get("code")) == 0 || len(q.Get("code")) > 4096 || q.Get("error") != "" {
		return "", errors.New("web callback query invalid")
	}
	return u.String(), nil
}

var labWebForms = regexp.MustCompile(`<form\b[^>]*>`)
var labWebFormID = regexp.MustCompile(`\bid="kc-form-login"`)
var labWebActions = regexp.MustCompile(`\baction="([^"]+)"`)
var labWebMethods = regexp.MustCompile(`\bmethod="([^"]+)"`)

func labWebForm(raw []byte) (string, error) {
	if len(raw) > labWebBodyLimit {
		return "", errors.New("web login form oversized")
	}
	var selected []byte
	for _, form := range labWebForms.FindAll(raw, -1) {
		if labWebFormID.Match(form) {
			if selected != nil {
				return "", errors.New("duplicate web login form")
			}
			selected = form
		}
	}
	actions, methods := labWebActions.FindAllSubmatch(selected, -1), labWebMethods.FindAllSubmatch(selected, -1)
	if len(actions) != 1 || len(methods) != 1 || !strings.EqualFold(string(methods[0][1]), "post") {
		return "", errors.New("web login form contract invalid")
	}
	action := html.UnescapeString(string(actions[0][1]))
	u, err := url.Parse(action)
	if err != nil || len(action) > 16<<10 || u.Scheme+"://"+u.Host != labWebIssuerOrigin || u.User != nil || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/realms/jobman-lab/login-actions/") {
		return "", errors.New("web login action escaped identity origin")
	}
	return action, nil
}

func labWebPassword(root string) (string, error) {
	path := filepath.Join(root, ".lab/credentials/dashboard.env")
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 {
		return "", errors.New("private Lab credential file required")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("private Lab credential open failed")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0o077 != 0 {
		return "", errors.New("private Lab credential file changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return "", errors.New("private Lab credentials exceed bounds")
	}
	password := ""
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && key == "JOBMAN_LAB_DASHBOARD_ALICE_PASSWORD" {
			if password != "" || len(strings.TrimSpace(value)) != 64 {
				return "", errors.New("private Lab password ambiguous")
			}
			password = strings.TrimSpace(value)
		}
	}
	if password == "" {
		return "", errors.New("private Lab password missing")
	}
	return password, nil
}

func labWebNextRevision(value string) (string, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || n > 1<<63-3 || strconv.FormatInt(n, 10) != value {
		return "", errors.New("preference revision invalid")
	}
	return strconv.FormatInt(n+1, 10), nil
}

func labWebPreferenceResponse(r labWebResponse, expected api.Preferences) bool {
	var p api.Preferences
	return r.status == 200 && json.Unmarshal(r.body, &p) == nil && p == expected && r.header.Get("ETag") == "\""+expected.Revision+"\""
}

// Only restore the exact attempted successor. An unrelated concurrent update
// must never be overwritten, even if it happens to select the same appearance.
func labWebRestoration(current, original, changed api.Preferences) (*api.Preferences, error) {
	next, err := labWebNextRevision(original.Revision)
	if err != nil {
		return nil, err
	}
	changed.Revision = next
	restored := original
	restored.Revision, err = labWebNextRevision(next)
	if err != nil {
		return nil, err
	}
	if current == original || current == restored {
		return nil, nil
	}
	if current != changed {
		return nil, errors.New("preferences changed concurrently; refusing overwrite")
	}
	result := original
	result.Revision = current.Revision
	return &result, nil
}

func labWebRestorePreferences(ctx context.Context, client *http.Client, headers http.Header, accountID string, original, changed api.Preferences) error {
	r, err := labWebExchange(ctx, client, "GET", labWebOrigin+"/api/v1/bootstrap", headers, nil)
	b, decodeErr := labWebBootstrap(r)
	if err != nil || decodeErr != nil || b.Account.ID != accountID {
		return errors.New("cleanup identity unavailable")
	}
	restore, err := labWebRestoration(b.Preferences, original, changed)
	if err != nil || restore == nil {
		return err
	}
	body, _ := json.Marshal(restore)
	h := headers.Clone()
	h.Set("Content-Type", "application/json")
	h.Set("If-Match", "\""+restore.Revision+"\"")
	r, err = labWebExchange(ctx, client, "PUT", labWebOrigin+"/api/v1/preferences", h, body)
	expected := *restore
	expected.Revision, _ = labWebNextRevision(restore.Revision)
	if err != nil || !labWebPreferenceResponse(r, expected) {
		return errors.New("cleanup preference CAS failed")
	}
	r, err = labWebExchange(ctx, client, "GET", labWebOrigin+"/api/v1/bootstrap", headers, nil)
	b, decodeErr = labWebBootstrap(r)
	if err != nil || decodeErr != nil || b.Account.ID != accountID || b.Preferences != expected {
		return errors.New("cleanup preference verification failed")
	}
	return nil
}

func TestLabWebSessionCookieGuards(t *testing.T) {
	valid := http.Cookie{Name: sessionCookie, Value: strings.Repeat("s", 43), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	for _, test := range []struct {
		name   string
		mutate func(*http.Cookie)
	}{
		{"domain", func(c *http.Cookie) { c.Domain = "lab.test" }},
		{"path", func(c *http.Cookie) { c.Path = "/api" }},
		{"secure", func(c *http.Cookie) { c.Secure = false }},
		{"http_only", func(c *http.Cookie) { c.HttpOnly = false }},
		{"same_site", func(c *http.Cookie) { c.SameSite = http.SameSiteNoneMode }},
		{"short", func(c *http.Cookie) { c.Value = "short" }},
		{"deleted", func(c *http.Cookie) { c.MaxAge = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := valid
			test.mutate(&c)
			if _, err := labWebCookie(http.Header{"Set-Cookie": {c.String()}}, sessionCookie, false); err == nil {
				t.Fatal("Unsafe session cookie accepted")
			}
		})
	}
	h := http.Header{"Set-Cookie": {valid.String()}}
	if _, err := labWebCookie(h, sessionCookie, false); err != nil {
		t.Fatal("Valid session cookie rejected")
	}
	h.Add("Set-Cookie", valid.String())
	if _, err := labWebCookie(h, sessionCookie, false); err == nil {
		t.Fatal("Duplicate session cookie accepted")
	}
	valid.Value, valid.MaxAge = "", -1
	if _, err := labWebCookie(http.Header{"Set-Cookie": {valid.String()}}, sessionCookie, true); err != nil {
		t.Fatal("Valid clearing cookie rejected")
	}
	valid.MaxAge = 0
	if _, err := labWebCookie(http.Header{"Set-Cookie": {valid.String()}}, sessionCookie, true); err == nil {
		t.Fatal("Empty cookie without expiry accepted as cleared")
	}
}

func TestLabWebSessionEarlyCallbackFailureCleanup(t *testing.T) {
	value, csrf := strings.Repeat("s", 43), strings.Repeat("c", 43)
	revoked, calls := false, []string{}
	jar, _ := cookiejar.New(nil)
	web := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: labWebRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		headers, status, body := make(http.Header), 200, []byte(`{}`)
		if r.URL.Path == "/auth/callback" {
			// The server admitted a session but supplied an invalid Location.
			// Cleanup must not depend on successful callback assertions.
			status = 303
			headers.Set("Location", "/unexpected")
			headers.Set("Set-Cookie", (&http.Cookie{Name: sessionCookie, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}).String())
		} else {
			if r.Header.Get("Cookie") != sessionCookie+"="+value {
				t.Fatal("Cleanup did not recover the admitted cookie from its jar")
			}
			if r.URL.Path == "/auth/logout" {
				if r.Header.Get("Origin") != labWebOrigin || r.Header.Get("X-CSRF-Token") != csrf {
					t.Fatal("Cleanup logout lacks recovered CSRF/origin")
				}
				revoked, status = true, 204
			} else if revoked {
				status = 401
			} else {
				headers.Set("Cache-Control", "no-store")
				body, _ = json.Marshal(api.Bootstrap{APIVersion: api.Version, Account: api.Account{ID: "44444444-4444-4444-8444-444444444444"}, Completeness: "complete", CSRFToken: csrf})
			}
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	bare := *web
	bare.Jar = nil
	err := func() error {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := labWebCleanupSession(ctx, web, &bare, "", ""); err != nil {
				t.Fatal("Early callback assertion failure was not cleaned up")
			}
		}()
		r, err := labWebExchange(t.Context(), web, "GET", labWebOrigin+"/auth/callback?code=synthetic", nil, nil)
		if err != nil || r.header.Get("Location") != "/settings" {
			return errors.New("expected callback assertion failure")
		}
		return nil
	}()
	if err == nil || !revoked || strings.Join(calls, ",") != "GET /auth/callback,GET /api/v1/bootstrap,POST /auth/logout,GET /api/v1/bootstrap" {
		t.Fatal("Early assertion failure did not revoke and independently verify its admitted session")
	}
	emptyJar, _ := cookiejar.New(nil)
	web.Jar = emptyJar
	if err := labWebCleanupSession(t.Context(), web, &bare, "", ""); err == nil {
		t.Fatal("Missing admission credential did not report revocation uncertainty")
	}
}

func TestLabWebSessionCallbackReplayAssertions(t *testing.T) {
	if !labWebCallbackReplayRejected(labWebResponse{status: 401, header: make(http.Header)}) {
		t.Fatal("Consumed callback denial rejected")
	}
	if labWebCallbackReplayRejected(labWebResponse{status: 303, header: make(http.Header)}) {
		t.Fatal("Callback replay success accepted")
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: strings.Repeat("s", 43), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if labWebCallbackReplayRejected(labWebResponse{status: 401, header: http.Header{"Set-Cookie": {cookie.String()}}}) {
		t.Fatal("Callback replay denial that still issues a session accepted")
	}
}

func TestLabWebSessionFormAndRedirectGuards(t *testing.T) {
	form := `<form id="kc-form-login" action="https://oidc.lab.test:8443/realms/jobman-lab/login-actions/authenticate?a=1&amp;b=2" method="post">`
	if action, err := labWebForm([]byte(form)); err != nil || !strings.HasSuffix(action, "?a=1&b=2") {
		t.Fatal("Pinned form entity decoding failed")
	}
	for _, value := range []string{
		form + form,
		strings.Replace(form, `method="post"`, `method="get"`, 1),
		strings.Replace(form, `method="post"`, `method="post" action="https://oidc.lab.test:8443/other"`, 1),
		strings.Replace(form, "https://oidc.lab.test:8443", "http://oidc.lab.test:8443", 1),
		strings.Replace(form, "https://oidc.lab.test:8443", "https://oidc.lab.test:8443@untrusted.invalid", 1),
		strings.Replace(form, "/realms/jobman-lab/login-actions/", "/other/", 1),
		strings.Replace(form, "/realms/", "/%72ealms/", 1),
		strings.Repeat("x", labWebBodyLimit+1),
	} {
		if _, err := labWebForm([]byte(value)); err == nil {
			t.Fatal("Unsafe or ambiguous credential form accepted")
		}
	}
	state := strings.Repeat("s", 43)
	q := url.Values{"state": {state}, "code": {"synthetic-code"}}
	callback := labWebOrigin + "/auth/callback?" + q.Encode()
	if _, err := labWebCallback(http.Header{"Location": {callback}}, state); err != nil {
		t.Fatal("Fixed callback rejected")
	}
	for _, locations := range [][]string{
		{callback, callback}, {callback + "&state=duplicate"}, {callback + "&error=failed"}, {callback + "#fragment"},
		{strings.Replace(callback, "https:", "http:", 1)}, {strings.Replace(callback, "/auth/", "/%61uth/", 1)},
		{strings.Replace(callback, "dashboard.lab.test:8443", "untrusted.invalid", 1)}, {strings.Repeat("x", 16<<10+1)},
	} {
		if _, err := labWebCallback(http.Header{"Location": locations}, state); err == nil {
			t.Fatal("Unsafe or ambiguous callback accepted")
		}
	}
	authorize := url.Values{"client_id": {"jobman-dashboard-web"}, "redirect_uri": {labWebOrigin + "/auth/callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {strings.Repeat("c", 43)}, "nonce": {strings.Repeat("n", 43)}, "state": {state}, "resource": {"jobman-dashboard-api"}, "scope": {"openid profile"}}
	authorizationURL := func() string {
		return labWebIssuerOrigin + "/realms/jobman-lab/protocol/openid-connect/auth?" + authorize.Encode()
	}
	if _, err := labWebAuthorization(http.Header{"Location": {authorizationURL()}}, state); err != nil {
		t.Fatal("Fixed web S256 authorization rejected")
	}
	authorize.Set("code_challenge_method", "plain")
	if _, err := labWebAuthorization(http.Header{"Location": {authorizationURL()}}, state); err == nil {
		t.Fatal("PKCE downgrade accepted")
	}
}

type labWebRoundTripper func(*http.Request) (*http.Response, error)

func (f labWebRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLabWebSessionTransportBoundsAndSanitization(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: labWebRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("private-cookie-and-code-must-not-escape")
	})}
	for _, target := range []string{"http://dashboard.lab.test:8443/api/v1/bootstrap", "https://untrusted.invalid/", labWebOrigin + "/#fragment", labWebOrigin + "/?" + strings.Repeat("x", 16<<10)} {
		if _, err := labWebExchange(t.Context(), client, "GET", target, nil, nil); err == nil {
			t.Fatal("Out-of-bound request accepted")
		}
	}
	if calls != 0 {
		t.Fatal("Rejected destinations reached transport")
	}
	_, err := labWebExchange(t.Context(), client, "GET", labWebOrigin+"/", nil, nil)
	if err == nil || strings.Contains(err.Error(), "private-cookie") || strings.Contains(err.Error(), labWebOrigin) {
		t.Fatal("Transport errors leak private request context")
	}
	client.Transport = labWebRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", labWebBodyLimit+1)))}, nil
	})
	if _, err := labWebExchange(t.Context(), client, "GET", labWebOrigin+"/", nil, nil); err == nil {
		t.Fatal("Oversized response accepted")
	}
}

func TestLabWebSessionRestorationDoesNotOverwriteConcurrentSettings(t *testing.T) {
	original := api.Preferences{Revision: "7", Timezone: "UTC", Appearance: "system", RefreshSeconds: 5}
	changed := original
	changed.Appearance, changed.Revision = "dark", "8"
	restored := original
	restored.Revision = "9"
	for _, current := range []api.Preferences{original, restored} {
		if result, err := labWebRestoration(current, original, changed); err != nil || result != nil {
			t.Fatal("Uncommitted or already restored preference write was not recognized")
		}
	}
	result, err := labWebRestoration(changed, original, changed)
	if err != nil || result == nil || result.Revision != "8" || result.Appearance != original.Appearance {
		t.Fatal("Exact uncertain committed successor was not restored with CAS")
	}
	for _, current := range []api.Preferences{
		{Revision: "8", Timezone: "Europe/London", Appearance: "dark", RefreshSeconds: 5},
		{Revision: "9", Timezone: "UTC", Appearance: "dark", RefreshSeconds: 5},
		{Revision: "10", Timezone: "UTC", Appearance: "system", RefreshSeconds: 5},
	} {
		if _, err := labWebRestoration(current, original, changed); err == nil {
			t.Fatal("Concurrent preference update would be overwritten")
		}
	}
	for _, revision := range []string{"0", "01", "-1", "9223372036854775807"} {
		if _, err := labWebNextRevision(revision); err == nil {
			t.Fatal("Invalid or overflowing revision accepted")
		}
	}
}

func TestLabWebSessionCleanupCASAndOwnership(t *testing.T) {
	original := api.Preferences{Revision: "7", Timezone: "UTC", Appearance: "system", RefreshSeconds: 5}
	changed := original
	changed.Appearance, changed.Revision = "dark", "8"
	accountID := "44444444-4444-4444-8444-444444444444"
	for _, wrongAccount := range []bool{false, true} {
		current, writes := changed, 0
		client := &http.Client{Transport: labWebRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Context().Err() != nil || r.Header.Get("Authorization") != "Bearer synthetic-native" || r.Header.Get("Cookie") != "" {
				t.Fatal("Cleanup did not use the bounded independent account credential")
			}
			headers := http.Header{"Cache-Control": {"no-store"}}
			var body []byte
			if r.Method == "PUT" {
				writes++
				if r.Header.Get("If-Match") != `"8"` || json.NewDecoder(r.Body).Decode(&current) != nil || current.Revision != "8" || current.Appearance != "system" {
					t.Fatal("Cleanup CAS or restored fields invalid")
				}
				current.Revision = "9"
				headers.Set("ETag", `"9"`)
				body, _ = json.Marshal(current)
			} else {
				id := accountID
				if wrongAccount {
					id = "44444444-4444-4444-8444-444444444445"
				}
				body, _ = json.Marshal(api.Bootstrap{APIVersion: api.Version, Account: api.Account{ID: id}, Preferences: current, Completeness: "complete"})
			}
			return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})}
		err := labWebRestorePreferences(t.Context(), client, http.Header{"Authorization": {"Bearer synthetic-native"}}, accountID, original, changed)
		if wrongAccount && (err == nil || writes != 0) {
			t.Fatal("Cleanup wrote a different account")
		}
		if !wrongAccount && (err != nil || writes != 1 || current.Appearance != "system") {
			t.Fatal("Cleanup did not restore and independently verify settings")
		}
	}
}

func TestLabWebSessionPrivatePasswordLoader(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".lab/credentials")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal("Private test directory unavailable")
	}
	path := filepath.Join(directory, "dashboard.env")
	line := "JOBMAN_LAB_DASHBOARD_ALICE_PASSWORD=" + strings.Repeat("a", 64) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal("Private test credential unavailable")
	}
	if _, err := labWebPassword(root); err != nil {
		t.Fatal("Valid bounded private credential rejected")
	}
	if err := os.WriteFile(path, []byte(line+line), 0o600); err != nil {
		t.Fatal("Duplicate credential fixture unavailable")
	}
	if _, err := labWebPassword(root); err == nil {
		t.Fatal("Duplicate private password accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal("Private fixture replacement failed")
	}
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal("Symlink fixture unavailable")
	}
	if _, err := labWebPassword(root); err == nil {
		t.Fatal("Symlink credential file accepted")
	}
}
