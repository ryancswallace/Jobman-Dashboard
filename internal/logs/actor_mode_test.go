package logs

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestBrokerClientConstructorBindsVerifiedActorMode(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(leaf)
	var calls atomic.Int64
	modes := make(chan string, 2)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		token, err := jwt.ParseSigned(strings.TrimPrefix(r.Header.Get("Authorization"), auth.DelegationScheme+" "), []jose.SignatureAlgorithm{jose.EdDSA})
		var claims auth.DelegationClaims
		if err != nil || len(r.TLS.VerifiedChains) == 0 || token.Claims(pub, &claims) != nil {
			t.Error("unverified delegation")
			http.Error(w, "invalid", 401)
			return
		}
		modes <- claims.Mode
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"chunk_missing"}`))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	ns := "33333333-3333-4333-8333-333333333333"
	deployment := "11111111-1111-4111-8111-111111111111"
	signer, err := auth.NewDelegationSigner(key, "test-key", "synthetic-worker", "urn:synthetic-broker", der, []string{ns})
	if err != nil {
		t.Fatal(err)
	}
	actor := monitoring.Actor{DirectoryID: "44444444-4444-4444-8444-444444444444", Issuer: "https://synthetic.example", Subject: "alice"}
	config := ClientConfig{DeploymentID: deployment, Origin: server.URL, Roots: roots, Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, Signer: signer}
	for _, mode := range []auth.DelegationMode{"", auth.DelegationWorker} {
		config.ActorMode = mode
		client, err := NewClient(config)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.ReadChunk(t.Context(), actor, Manifest{Scope: api.Scope{DeploymentID: deployment, NamespaceID: ns}}, Chunk{Sequence: 1})
		client.Close()
		if err != nil || result.State != "chunk_missing" {
			t.Fatal(result, err)
		}
		want, _ := mode.Canonical()
		if got := <-modes; got != string(want) {
			t.Fatal("signed mode differs", got, want)
		}
	}
	config.ActorMode = "arbitrary-caller-mode"
	if _, err = NewClient(config); err == nil || calls.Load() != 2 {
		t.Fatal("invalid constructor mode reached network", err)
	}
}
