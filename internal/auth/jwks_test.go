package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type jwksFixture struct {
	o       *OIDC
	signer  jose.Signer
	issuer  string
	mode    atomic.Int32
	fetches atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func newJWKSFixture(t *testing.T, mode int32) *jwksFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "key"))
	if err != nil {
		t.Fatal(err)
	}
	f := &jwksFixture{signer: signer, entered: make(chan struct{}, 1), release: make(chan struct{})}
	f.mode.Store(mode)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.issuer, "authorization_endpoint": f.issuer + "/authorize", "token_endpoint": f.issuer + "/token", "jwks_uri": f.issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
			return
		}
		if r.URL.Path != "/keys" {
			http.NotFound(w, r)
			return
		}
		f.fetches.Add(1)
		switch f.mode.Load() {
		case 1:
			http.Error(w, "private provider failure", 503)
		case 2:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close() // Real transport failure after discovery succeeded.
			}
		case 3:
			_, _ = w.Write([]byte(`{"keys":`))
		case 4:
			_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
		case 5:
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-f.release:
			case <-r.Context().Done():
			}
			http.Error(w, "private slow provider failure", 503)
		default:
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "key", Algorithm: "RS256", Use: "sig"}}})
		}
	}))
	t.Cleanup(server.Close)
	f.issuer = server.URL
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := PinnedIdentityHTTPClient(f.issuer, roots)
	if err != nil {
		t.Fatal(err)
	}
	f.o, err = NewOIDC(t.Context(), OIDCOptions{Issuer: f.issuer, Audience: "api", WebClientID: "web", WebClientSecret: "synthetic", NativeClientID: "native", NativeRedirectURI: "jobman-dashboard-auth://callback", DirectoryIDClaim: "directory_id", ClientIDClaim: "appid", PublicOrigin: "https://dashboard.example", EncryptionKey: make([]byte, 32), EncryptionKeyID: "synthetic", HTTPClient: client}, &memoryIdentityStore{logins: map[string][]byte{}, sessions: map[string]Session{}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *jwksFixture) token(t *testing.T, audience string, signer jose.Signer) string {
	t.Helper()
	value, err := jwt.Signed(signer).Claims(map[string]any{"iss": f.issuer, "aud": audience, "sub": "synthetic", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "directory_id": "44444444-4444-4444-8444-444444444444", "appid": "native"}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (f *jwksFixture) authenticate(ctx context.Context, token string) error {
	r := httptest.NewRequestWithContext(ctx, "GET", "https://dashboard.example/api/v1/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	_, err := f.o.Authenticate(r)
	return err
}

func (f *jwksFixture) requireRecovery(t *testing.T, token string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		err := f.authenticate(ctx, token)
		if err == nil {
			return
		}
		if !errors.Is(err, monitoring.ErrSource) {
			t.Fatalf("same signed token did not recover with provider: %v", err)
		}
		if ctx.Err() != nil {
			t.Fatalf("provider recovery exceeded deadline: %v (last authentication error: %v)", ctx.Err(), err)
		}
		// RemoteKeySet wakes callers before clearing its completed in-flight
		// fetch. An immediate retry can still receive that fetch's source error;
		// yield until a new fetch observes recovery, without changing the token.
		runtime.Gosched()
	}
}

func TestRealJWKSFailureRecoversWithoutInvalidatingAuthentication(t *testing.T) {
	for _, mode := range []int32{1, 2, 3, 4} {
		t.Run(map[int32]string{1: "http503", 2: "transport", 3: "malformed", 4: "oversized"}[mode], func(t *testing.T) {
			f := newJWKSFixture(t, mode)
			token := f.token(t, "api", f.signer)
			if err := f.authenticate(t.Context(), token); !errors.Is(err, monitoring.ErrSource) || f.fetches.Load() == 0 {
				t.Fatal("real go-oidc key fetch failure became invalid credentials")
			}
			webToken := f.token(t, "web", f.signer)
			if _, err := f.o.webVerifier.Verify(t.Context(), webToken); !verifierUnavailable(t.Context(), err) {
				t.Fatal("browser key verifier lost dependency failure classification")
			}
			failedFetches := f.fetches.Load()
			f.mode.Store(0)
			f.requireRecovery(t, token)
			if f.fetches.Load() <= failedFetches {
				t.Fatal("provider recovery did not fetch fresh signing keys")
			}
			fetches := f.fetches.Load()
			f.mode.Store(1)
			if err := f.authenticate(t.Context(), token); err != nil || f.fetches.Load() != fetches {
				t.Fatal("verified library cache was discarded")
			}
		})
	}
}

func TestRealJWKSInvalidSignatureAndConcurrentMalformedTokenRemainUnauthorized(t *testing.T) {
	f := newJWKSFixture(t, 0)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: other}, (&jose.SignerOptions{}).WithHeader("kid", "key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.authenticate(t.Context(), f.token(t, "api", signer)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("invalid signature became dependency unavailability")
	}
	// A different fresh verifier has no cached key. Its failed fetch cannot
	// contaminate another call which rejects malformed token syntax immediately.
	f.o.apiVerifier = newIdentityVerifier(t.Context(), f.o.options, f.issuer+"/keys", "api")
	f.mode.Store(5)
	token := f.token(t, "api", f.signer)
	finished := make(chan error, 1)
	go func() { finished <- f.authenticate(t.Context(), token) }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("remote fetch did not start")
	}
	if err := f.authenticate(t.Context(), "malformed"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("another request's outage tainted an invalid token")
	}
	close(f.release)
	select {
	case err := <-finished:
		if !errors.Is(err, monitoring.ErrSource) {
			t.Fatal("in-flight unavailable key authority became invalid credentials")
		}
	case <-time.After(time.Second):
		t.Fatal("key fetch did not complete")
	}
}

func TestJWKSVerifierPreservesCallerCancellation(t *testing.T) {
	f := newJWKSFixture(t, 5)
	token := f.token(t, "api", f.signer)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := f.o.apiVerifier.Verify(ctx, token)
	close(f.release)
	if err == nil || !verifierUnavailable(ctx, err) {
		t.Fatal("canceled key verification did not retain caller deadline")
	}
}
