package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type memoryIdentityStore struct {
	mu       sync.Mutex
	logins   map[string][]byte
	sessions map[string]Session
}

func (m *memoryIdentityStore) ResolveIdentity(_ context.Context, i Identity) (monitoring.Actor, error) {
	return monitoring.Actor{Account: api.Account{ID: "44444444-4444-4444-8444-444444444444", DisplayName: i.DisplayName}, Issuer: i.Issuer, Subject: i.Subject, DirectoryID: i.DirectoryID}, nil
}
func (m *memoryIdentityStore) PutLogin(_ context.Context, h, p []byte, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logins[string(h)] = p
	return nil
}
func (m *memoryIdentityStore) ConsumeLogin(_ context.Context, h []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.logins[string(h)]
	delete(m.logins, string(h))
	if !ok {
		return nil, ErrUnauthenticated
	}
	return p, nil
}
func (m *memoryIdentityStore) CreateSession(_ context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(s.TokenHash)] = s
	return nil
}
func (m *memoryIdentityStore) Session(_ context.Context, h []byte) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[string(h)]
	if !ok {
		return s, ErrUnauthenticated
	}
	return s, nil
}
func (m *memoryIdentityStore) RevokeSession(_ context.Context, h []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(h))
	return nil
}

type oidcFixture struct {
	o               *OIDC
	signer          jose.Signer
	issuer          string
	nonce           string
	wrongNonce      bool
	tokenCalls      int
	mutateWebClaims func(map[string]any)
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
	if err != nil {
		t.Fatal(err)
	}
	f := &oidcFixture{signer: signer}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.issuer, "authorization_endpoint": f.issuer + "/authorize", "token_endpoint": f.issuer + "/token", "jwks_uri": f.issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			f.tokenCalls++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if len(r.Form.Get("code_verifier")) < 43 || r.Form.Get("redirect_uri") != "https://dashboard.example/auth/callback" || r.Form.Get("resource") != "dashboard-api" {
				t.Error("PKCE exchange was not bound to client and API")
			}
			nonce := f.nonce
			if f.wrongNonce {
				nonce = "wrong"
			}
			claims := f.claims("web", "browser-subject", "web")
			claims["nonce"] = nonce
			if f.mutateWebClaims != nil {
				f.mutateWebClaims(claims)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "server-only-token", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(t, claims)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.issuer = server.URL
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := PinnedIdentityHTTPClient(server.URL, roots)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryIdentityStore{logins: map[string][]byte{}, sessions: map[string]Session{}}
	f.o, err = NewOIDC(context.Background(), OIDCOptions{Issuer: server.URL, Audience: "dashboard-api", WebClientID: "web", WebClientSecret: "synthetic-secret", NativeClientID: "native", NativeRedirectURI: "jobman-dashboard-auth://callback", DirectoryIDClaim: "directory_id", ClientIDClaim: "appid", PublicOrigin: "https://dashboard.example", EncryptionKey: make([]byte, 32), EncryptionKeyID: "synthetic", HTTPClient: client}, store)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *oidcFixture) claims(audience, subject, client string) map[string]any {
	return map[string]any{"iss": f.issuer, "aud": audience, "sub": subject, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "directory_id": "44444444-4444-4444-8444-444444444444", "appid": client, "name": "Synthetic Alice"}
}
func (f *oidcFixture) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	s, err := jwt.Signed(f.signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func (f *oidcFixture) begin(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "https://dashboard.example/auth/login?returnTo=%2Fjobs%3Fscope%3Dx", nil)
	w := httptest.NewRecorder()
	f.o.login(w, r)
	if w.Code != 302 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("client_id") != "web" || strings.Contains(q.Get("scope"), "offline_access") {
		t.Fatal("invalid browser flow")
	}
	f.nonce = q.Get("nonce")
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" {
		t.Fatal("weak login cookie")
	}
	return cookie, q.Get("state")
}

func TestBrowserCodeFlowSessionCSRFAndLogout(t *testing.T) {
	f := newOIDCFixture(t)
	cookie, state := f.begin(t)
	r := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	f.o.callback(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/jobs?scope=x" {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil || !session.Secure || !session.HttpOnly || session.Domain != "" {
		t.Fatal("missing secure session")
	}
	request := httptest.NewRequest("GET", "https://dashboard.example/api/v1/bootstrap", nil)
	request.AddCookie(session)
	actor, err := f.o.Authenticate(request)
	if err != nil || actor.Subject != "browser-subject" || len(actor.CSRFToken) != 43 {
		t.Fatalf("session: %+v %v", actor, err)
	}
	for _, origin := range []string{"", "https://evil.example"} {
		mut := httptest.NewRequest("PATCH", "https://dashboard.example/api/v1/preferences", nil)
		mut.AddCookie(session)
		mut.Header.Set("Origin", origin)
		mut.Header.Set("X-CSRF-Token", actor.CSRFToken)
		if _, err := f.o.Authenticate(mut); err == nil {
			t.Fatal("cross-origin mutation accepted")
		}
	}
	mut := httptest.NewRequest("POST", "https://dashboard.example/auth/logout", nil)
	mut.AddCookie(session)
	mut.Header.Set("Origin", "https://dashboard.example")
	mut.Header.Set("X-CSRF-Token", actor.CSRFToken)
	w = httptest.NewRecorder()
	f.o.logout(w, mut)
	if w.Code != 204 {
		t.Fatal("logout rejected")
	}
	if _, err := f.o.Authenticate(request); err == nil {
		t.Fatal("revoked session accepted")
	}
	w = httptest.NewRecorder()
	f.o.callback(w, r)
	if w.Code != 401 || f.tokenCalls != 1 {
		t.Fatal("callback replay was not rejected before token exchange")
	}
}

func TestNativeTokensRequireAudienceClientAndDirectory(t *testing.T) {
	f := newOIDCFixture(t)
	for _, mode := range []string{"valid", "id-token", "id-token-with-api-audience", "web-audience", "wrong-client", "missing-directory", "nil-directory", "future-nbf", "forged", "mixed-cookie"} {
		t.Run(mode, func(t *testing.T) {
			claims := f.claims("dashboard-api", "native-subject", "native")
			switch mode {
			case "id-token":
				claims["aud"] = "native"
			case "id-token-with-api-audience":
				claims["aud"] = []string{"native", "dashboard-api"}
			case "web-audience":
				claims["aud"] = []string{"web", "dashboard-api"}
			case "wrong-client":
				claims["appid"] = "web"
			case "missing-directory":
				delete(claims, "directory_id")
			case "nil-directory":
				claims["directory_id"] = "00000000-0000-0000-0000-000000000000"
			case "future-nbf":
				claims["nbf"] = time.Now().Add(20 * time.Second).Unix()
			}
			token := f.sign(t, claims)
			if mode == "forged" {
				token = token[:len(token)-8] + "xxxxxxxx"
			}
			r := httptest.NewRequest("GET", "https://dashboard.example/api/v1/bootstrap", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			if mode == "mixed-cookie" {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: strings.Repeat("a", 43)})
			}
			actor, err := f.o.Authenticate(r)
			if mode == "valid" {
				if err != nil || actor.Subject != "native-subject" || actor.CSRFToken != "" {
					t.Fatalf("valid token: %+v %v", actor, err)
				}
			} else if err == nil {
				t.Fatal("invalid credential accepted")
			}
		})
	}
}

func TestLoginRejectsUnsafeReturnAndStateNonce(t *testing.T) {
	f := newOIDCFixture(t)
	for _, target := range []string{"https://evil.example", "//evil.example", "/%2f/evil.example", "/\\evil.example", "/auth/logout", "/jobs\r\nX: bad"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://dashboard.example/auth/login?returnTo="+url.QueryEscape(target), nil)
		f.o.login(w, r)
		if w.Code != 401 {
			t.Fatalf("unsafe return accepted %q", target)
		}
	}
	cookie, state := f.begin(t)
	r := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=x", nil)
	w := httptest.NewRecorder()
	f.o.callback(w, r)
	if w.Code != 401 || f.tokenCalls != 0 {
		t.Fatal("missing browser binding accepted")
	}
	r.AddCookie(cookie)
	f.wrongNonce = true
	w = httptest.NewRecorder()
	f.o.callback(w, r)
	if w.Code != 401 {
		t.Fatal("wrong nonce accepted")
	}
}

func TestWebIDTokensBindAuthorizedPartyAndRequiredIssuedAt(t *testing.T) {
	f := newOIDCFixture(t)
	for _, mode := range []string{"single-audience", "single-with-azp", "multi-with-azp", "multi-without-azp", "wrong-azp", "malformed-azp", "missing-iat"} {
		t.Run(mode, func(t *testing.T) {
			f.mutateWebClaims = func(claims map[string]any) {
				switch mode {
				case "single-with-azp":
					claims["azp"] = "web"
				case "multi-with-azp":
					claims["aud"], claims["azp"] = []string{"web", "other-resource"}, "web"
				case "multi-without-azp":
					claims["aud"] = []string{"web", "other-resource"}
				case "wrong-azp":
					claims["azp"] = "other-client"
				case "malformed-azp":
					claims["azp"] = []string{"web"}
				case "missing-iat":
					delete(claims, "iat")
				}
			}
			cookie, state := f.begin(t)
			r := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			f.o.callback(w, r)
			valid := mode == "single-audience" || mode == "single-with-azp" || mode == "multi-with-azp"
			if (valid && w.Code != http.StatusSeeOther) || (!valid && w.Code != http.StatusUnauthorized) {
				t.Fatalf("authorized-party policy %s returned %d", mode, w.Code)
			}
		})
	}
}

func TestDuplicateCookiesCannotChooseAuthenticationOrLoginState(t *testing.T) {
	f := newOIDCFixture(t)
	cookie, state := f.begin(t)
	r := httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil)
	r.AddCookie(cookie)
	r.AddCookie(&http.Cookie{Name: loginCookie, Value: strings.Repeat("b", 43)})
	w := httptest.NewRecorder()
	f.o.callback(w, r)
	if w.Code != http.StatusUnauthorized || f.tokenCalls != 0 {
		t.Fatal("ambiguous login cookie was consumed or exchanged")
	}
	// The rejection must not consume the correctly bound, single-use attempt.
	r = httptest.NewRequest("GET", "https://dashboard.example/auth/callback?state="+state+"&code=synthetic", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	f.o.callback(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatal("valid attempt was lost by cookie rejection")
	}
	var session *http.Cookie
	for _, value := range w.Result().Cookies() {
		if value.Name == sessionCookie {
			session = value
		}
	}
	if session == nil {
		t.Fatal("session was not created")
	}
	for _, reverse := range []bool{false, true} {
		request := httptest.NewRequest("GET", "https://dashboard.example/api/v1/bootstrap", nil)
		other := &http.Cookie{Name: sessionCookie, Value: strings.Repeat("b", 43)}
		if reverse {
			request.AddCookie(other)
			request.AddCookie(session)
		} else {
			request.AddCookie(session)
			request.AddCookie(other)
		}
		if _, err := f.o.Authenticate(request); err == nil {
			t.Fatal("ambiguous session cookie was accepted")
		}
	}
}

func TestNativeConfigurationKeepsUniqueScopes(t *testing.T) {
	f := newOIDCFixture(t)
	f.o.options.Scopes = []string{"openid", "profile", "offline_access", "dashboard.read"}
	mux := http.NewServeMux()
	f.o.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "https://dashboard.example/auth/native/config", nil))
	var result struct {
		Scopes []string `json:"scopes"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || strings.Join(result.Scopes, " ") != "openid profile offline_access dashboard.read" {
		t.Fatalf("native configuration repeated or lost scopes: %+v", result)
	}
}
