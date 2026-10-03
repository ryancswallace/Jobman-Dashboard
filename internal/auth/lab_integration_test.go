//go:build integration

package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// This explicit opt-in uses only the named synthetic Lab account. It verifies
// the real code/PKCE/JWKS/token path with Dashboard's production validator,
// while keeping all credentials and tokens out of output and persisted files.
// It does not establish AD FS, a physical iPhone or browser-app acceptance.
func TestLabNativePKCEAndDashboardTokenValidation(t *testing.T) {
	root := os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT")
	if root == "" {
		t.Skip("set JOBMAN_DASHBOARD_LAB_ROOT to the authorized synthetic Lab")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("Lab root must be absolute")
	}
	issuer := "https://oidc.lab.test:8443/realms/jobman-lab"
	ca, err := os.ReadFile(filepath.Join(root, ".lab/certs/lab-ca.crt"))
	if err != nil {
		t.Fatal("Lab public CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("Lab public CA invalid")
	}
	credentialsPath := filepath.Join(root, ".lab/credentials/dashboard.env")
	info, err := os.Lstat(credentialsPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("Lab credentials must be a private regular file")
	}
	data, err := os.ReadFile(credentialsPath)
	if err != nil || len(data) > 64<<10 {
		t.Fatal("Lab credentials unavailable or oversized")
	}
	secrets := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok {
			secrets[name] = strings.TrimSpace(value)
		}
	}
	password, webSecret := secrets["JOBMAN_LAB_DASHBOARD_ALICE_PASSWORD"], secrets["JOBMAN_LAB_DASHBOARD_WEB_SECRET"]
	if len(password) != 64 || len(webSecret) != 64 {
		t.Fatal("Synthetic Dashboard credentials missing")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	// Change only this test client's destination socket. TLS SNI, hostname/CA
	// verification, issuer identity, cookies and URL authority remain unchanged.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "oidc.lab.test:8443" {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.21:8443")
	}
	t.Cleanup(transport.CloseIdleConnections)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: identityTransport{base: transport, origin: "https://oidc.lab.test:8443"}, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	store := &memoryIdentityStore{logins: map[string][]byte{}, sessions: map[string]Session{}}
	o, err := NewOIDC(ctx, OIDCOptions{Issuer: issuer, Audience: "jobman-dashboard-api", WebClientID: "jobman-dashboard-web", WebClientSecret: webSecret, NativeClientID: "jobman-dashboard-native", NativeRedirectURI: "jobman-dashboard-auth://callback", DirectoryIDClaim: "directory_guid", ClientIDClaim: "azp", PublicOrigin: "https://dashboard.lab.test:8443", EncryptionKey: make([]byte, 32), EncryptionKeyID: "synthetic-test", HTTPClient: client}, store)
	if err != nil {
		t.Fatal("Real Lab OIDC discovery rejected")
	}
	state, _ := randomToken()
	nonce, _ := randomToken()
	verifier := oauth2.GenerateVerifier()
	oauth := o.oauth
	oauth.ClientID = o.options.NativeClientID
	oauth.ClientSecret = ""
	oauth.RedirectURL = o.options.NativeRedirectURI
	oauth.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	authorize := oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce), oauth2.SetAuthURLParam("resource", o.options.Audience))
	request, _ := http.NewRequestWithContext(ctx, "GET", authorize, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("Real Lab authorization request failed")
	}
	page, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if readErr != nil || response.StatusCode != 200 {
		t.Fatal("Real Lab sign-in form unavailable")
	}
	// The fixture uses Keycloak's named login form; never execute page scripts
	// or follow arbitrary content URLs. Its action must stay on the pinned origin.
	form := regexp.MustCompile(`<form\b[^>]*\bid="kc-form-login"[^>]*>`).Find(page)
	action := regexp.MustCompile(`\baction="([^"]+)"`).FindSubmatch(form)
	if len(action) != 2 {
		t.Fatal("Expected synthetic sign-in form not found")
	}
	u, err := url.Parse(html.UnescapeString(string(action[1])))
	if err != nil || u.Scheme != "https" || u.Host != "oidc.lab.test:8443" || u.User != nil || !strings.HasPrefix(u.Path, "/realms/jobman-lab/login-actions/") {
		t.Fatal("Synthetic sign-in form action escaped pinned identity origin")
	}
	values := url.Values{"username": {"dashboard-alice"}, "password": {password}, "credentialId": {""}}
	request, _ = http.NewRequestWithContext(ctx, "POST", u.String(), strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal("Synthetic sign-in submission failed")
	}
	response.Body.Close()
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil || response.StatusCode != 302 || callback.Scheme != "jobman-dashboard-auth" || callback.Host != "callback" || callback.Path != "" || callback.User != nil {
		t.Fatal("Synthetic sign-in did not return the exact native callback")
	}
	query, err := url.ParseQuery(callback.RawQuery)
	if err != nil || len(query["state"]) != 1 || query.Get("state") != state || len(query["code"]) != 1 || query.Get("code") == "" || query.Get("error") != "" {
		t.Fatal("Synthetic authorization code/state invalid")
	}
	clientContext := oidc.ClientContext(ctx, client)
	token, err := oauth.Exchange(clientContext, query.Get("code"), oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", o.options.Audience))
	if err != nil {
		t.Fatal("Real Lab PKCE code exchange rejected")
	}
	if token.AccessToken == "" {
		t.Fatal("No API access token")
	}
	native := httptest.NewRequest("GET", o.options.PublicOrigin+"/api/v1/bootstrap", nil).WithContext(ctx)
	native.Header.Set("Authorization", "Bearer "+token.AccessToken)
	actor, err := o.Authenticate(native)
	if err != nil || actor.Issuer != issuer || actor.Subject == "" || actor.DirectoryID != "71000000-0000-4000-8000-000000000001" {
		t.Fatal("Dashboard rejected signed Lab API identity")
	}
	id, ok := token.Extra("id_token").(string)
	if !ok || id == "" {
		t.Fatal("No native ID token")
	}
	provider, err := oidc.NewProvider(clientContext, issuer)
	if err != nil {
		t.Fatal("Native verification discovery failed")
	}
	verified, err := provider.VerifierContext(clientContext, &oidc.Config{ClientID: oauth.ClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, id)
	if err != nil || verified.Nonce != nonce || verified.Subject != actor.Subject || slices.Contains(verified.Audience, o.options.Audience) {
		t.Fatal("Native ID token audience/nonce/subject invalid")
	}
	var claims map[string]json.RawMessage
	if verified.Claims(&claims) != nil {
		t.Fatal("Native signed claims invalid")
	}
	var directory string
	if json.Unmarshal(claims["directory_guid"], &directory) != nil || directory != actor.DirectoryID {
		t.Fatal("Native immutable directory claim differs")
	}
	native.Header.Set("Authorization", "Bearer "+id)
	if _, err = o.Authenticate(native); err == nil {
		t.Fatal("ID token was accepted as an API access token")
	}
	if _, err = oauth.Exchange(clientContext, query.Get("code"), oauth2.VerifierOption(verifier)); err == nil {
		t.Fatal("Authorization code replay accepted")
	}
	t.Log("PASS: verified TLS, native S256 code exchange, signed API/client/GUID identity, nonce, ID-token rejection and code replay denial; synthetic Keycloak only")
}
