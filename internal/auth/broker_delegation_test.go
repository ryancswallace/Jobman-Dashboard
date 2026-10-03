package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func brokerVerifierFixture(t *testing.T) (*BrokerVerifier, *DelegationSigner, *http.Request, api.Scope, monitoring.Actor) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	scope := api.Scope{DeploymentID: "11111111-1111-4111-8111-111111111111", NamespaceID: "22222222-2222-4222-8222-222222222222"}
	signer, err := NewDelegationSigner(key, "broker-test", "dashboard-api", "urn:broker:synthetic", der, []string{scope.NamespaceID})
	if err != nil {
		t.Fatal(err)
	}
	signer.Now = func() time.Time { return now }
	sum := sha256.Sum256(der)
	verifier, err := NewBrokerVerifier([]BrokerService{{KeyID: "broker-test", ServiceID: "dashboard-api", Audience: "urn:broker:synthetic", DeploymentID: scope.DeploymentID, CertificateSHA256: base64.RawURLEncoding.EncodeToString(sum[:]), PublicKey: pub, NamespaceIDs: []string{scope.NamespaceID}}})
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = signer.Now
	verifier.issuedAfter = now.Add(-time.Second)
	actor := monitoring.Actor{Account: api.Account{ID: "not-forwarded"}, DirectoryID: "33333333-3333-4333-8333-333333333333", Issuer: "https://synthetic.invalid", Subject: "verified-subject"}
	r := httptest.NewRequest(http.MethodPost, "https://broker.invalid/v1/chunks/read", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	header, err := signer.Authorize(actor, "logs.read", scope.NamespaceID, "interactive")
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", header)
	return verifier, signer, r, scope, actor
}

func TestBrokerDelegationBindsSourceIdentityCertificateAndPurpose(t *testing.T) {
	for _, mutation := range []string{"success", "deployment", "namespace", "certificate", "chain", "no_tls", "duplicate", "expired", "startup_floor", "audience", "operation"} {
		t.Run(mutation, func(t *testing.T) {
			v, s, r, scope, original := brokerVerifierFixture(t)
			switch mutation {
			case "deployment":
				scope.DeploymentID = "44444444-4444-4444-8444-444444444444"
			case "namespace":
				scope.NamespaceID = "44444444-4444-4444-8444-444444444444"
			case "certificate":
				copy := *r.TLS.PeerCertificates[0]
				copy.Raw = []byte("another")
				r.TLS.PeerCertificates = []*x509.Certificate{&copy}
			case "chain":
				r.TLS.VerifiedChains = nil
			case "no_tls":
				r.TLS = nil
			case "duplicate":
				r.Header.Add("Authorization", r.Header.Get("Authorization"))
			case "expired":
				now := v.now()
				v.now = func() time.Time { return now.Add(66 * time.Second) }
			case "startup_floor":
				v.issuedAfter = v.now().Add(5 * time.Second)
			case "audience":
				s.audience = "urn:another"
				h, _ := s.Authorize(original, "logs.read", scope.NamespaceID, "interactive")
				r.Header.Set("Authorization", h)
			case "operation":
				h, _ := s.Authorize(original, "jobs.read", scope.NamespaceID, "interactive")
				r.Header.Set("Authorization", h)
			}
			a, err := v.Authenticate(r, scope)
			if mutation == "success" {
				if err != nil || a.DirectoryID != original.DirectoryID || a.Account.ID != original.DirectoryID || a.Issuer != original.Issuer || a.Subject != original.Subject {
					t.Fatalf("verified actor %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid assertion accepted")
			}
		})
	}
}

func TestBrokerDelegationReplayIsAtomicBoundedAndRestartFenced(t *testing.T) {
	v, _, r, scope, _ := brokerVerifierFixture(t)
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Authenticate(r, scope); err == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted replays %d", accepted.Load())
	}
	v, s, r, scope, a := brokerVerifierFixture(t)
	for i := range 10000 {
		v.used[fmt.Sprint(i)] = v.now().Add(time.Minute)
	}
	if _, err := v.Authenticate(r, scope); err == nil {
		t.Fatal("replay capacity exceeded")
	}
	for key := range v.used {
		v.used[key] = v.now().Add(-time.Second)
	}
	if _, err := v.Authenticate(r, scope); err != nil {
		t.Fatalf("expired slots not reclaimed: %v", err)
	}
	now := v.now()
	v.issuedAfter = now.Add(5 * time.Second)
	v.used = make(map[string]time.Time)
	if _, err := v.Authenticate(r, scope); err == nil {
		t.Fatal("restart resurrected old assertion")
	}
	v.now = func() time.Time { return now.Add(6 * time.Second) }
	s.Now = v.now
	header, err := s.Authorize(a, "logs.read", scope.NamespaceID, "interactive")
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", header)
	if _, err = v.Authenticate(r, scope); err != nil {
		t.Fatalf("fresh assertion after startup failed: %v", err)
	}
}

func TestBrokerDelegationExpiryBoundaryCannotReviveUsedAssertion(t *testing.T) {
	for _, test := range []struct {
		name          string
		elapsed       time.Duration
		unusedAllowed bool
	}{
		{"before", 65*time.Second - time.Nanosecond, true},
		{"equal", 65 * time.Second, false},
		{"after", 65*time.Second + time.Nanosecond, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, _, request, scope, _ := brokerVerifierFixture(t)
			if _, err := v.Authenticate(request, scope); err != nil {
				t.Fatal(err)
			}
			issued := v.now()
			v.now = func() time.Time { return issued.Add(test.elapsed) }
			if _, err := v.Authenticate(request, scope); err == nil {
				t.Fatal("used assertion became replayable at expiry cleanup")
			}

			// Assert the acceptance boundary independently of replay state: the
			// library's inclusive expiry leeway must not outlive the cache entry.
			fresh, _, unused, unusedScope, _ := brokerVerifierFixture(t)
			unusedIssued := fresh.now()
			fresh.now = func() time.Time { return unusedIssued.Add(test.elapsed) }
			_, err := fresh.Authenticate(unused, unusedScope)
			if (err == nil) != test.unusedAllowed {
				t.Fatalf("unused assertion at expiry boundary: %v", err)
			}
		})
	}
}
