package control

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
)

type noProvenanceFileRead struct{ t *testing.T }

func (r noProvenanceFileRead) Read(context.Context, logs.FileRequest) logs.FileResult {
	r.t.Error("denied source reached file reader")
	return logs.FileResult{State: "invalid_manifest"}
}

func TestBrokerForwardsVerifiedModeOverSeparateMutualTLSPools(t *testing.T) {
	modes := make(chan string, 4)
	source := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			respond(w, capabilities(time.Now().UTC()))
			return
		}
		token, e := jwt.ParseSigned(strings.TrimPrefix(r.Header.Get("Authorization"), auth.DelegationScheme+" "), []jose.SignatureAlgorithm{jose.EdDSA})
		var claims auth.DelegationClaims
		if e != nil || len(r.TLS.VerifiedChains) == 0 || token.Claims(r.TLS.PeerCertificates[0].PublicKey, &claims) != nil {
			t.Error("downstream assertion was not mTLS/signature verified")
			http.Error(w, "invalid", 401)
			return
		}
		modes <- claims.Mode
		// Stop at current namespace authorization: this test proves both signed hops,
		// while the separate broker file test covers successful authorized bytes.
		http.Error(w, "denied", 403)
	}))
	workerConfig := source.config
	workerConfig.ActorMode = auth.DelegationWorker
	worker, e := New(workerConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer worker.Close()
	cert := source.config.Certificate
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(leaf)
	digest := sha256.Sum256(cert.Certificate[0])
	signer, e := auth.NewDelegationSigner(cert.PrivateKey.(ed25519.PrivateKey), "broker-key", "dashboard-test", "urn:test-broker", cert.Certificate[0], []string{namespaceID})
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := auth.NewBrokerVerifier([]auth.BrokerService{{KeyID: "broker-key", ServiceID: "dashboard-test", Audience: "urn:test-broker", DeploymentID: deploymentID, CertificateSHA256: base64.RawURLEncoding.EncodeToString(digest[:]), PublicKey: leaf.PublicKey.(ed25519.PublicKey), NamespaceIDs: []string{namespaceID}}})
	if e != nil {
		t.Fatal(e)
	}
	local, e := logs.NewLocalChunks([]logs.Mapping{{DeploymentID: deploymentID, StoreName: "logs", StoreVersion: "1", Root: "/private/synthetic-unread"}}, noProvenanceFileRead{t})
	if e != nil {
		t.Fatal(e)
	}
	service, e := logs.NewServiceWithModes(map[string]logs.ManifestSource{deploymentID: source}, map[string]logs.ManifestSource{deploymentID: worker}, local, verifier)
	if e != nil {
		t.Fatal(e)
	}
	broker := httptest.NewUnstartedServer(service.Handler())
	broker.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}
	broker.StartTLS()
	defer broker.Close()
	roots := x509.NewCertPool()
	roots.AddCert(broker.Certificate())
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{cert}}}}
	defer client.CloseIdleConnections()
	body, e := json.Marshal(logs.ChunkRequest{Scope: api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, JobID: jobID, RunNumber: "1", ExecutionID: principalID, Stream: "stderr", Sequence: "1"})
	if e != nil {
		t.Fatal(e)
	}
	// Exercise the real broker restart floor; no verifier clock/floor override.
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-timer.C:
	}
	for _, mode := range []string{"interactive", "worker"} {
		header, e := signer.Authorize(testActor, "logs.read", namespaceID, mode)
		if e != nil {
			t.Fatal(e)
		}
		req, e := http.NewRequestWithContext(t.Context(), http.MethodPost, broker.URL+"/v1/chunks/read", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", header)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Actor-Mode", "attacker-override")
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("expected current-source denial, got %d", response.StatusCode)
		}
		select {
		case got := <-modes:
			if got != mode {
				t.Fatalf("downstream mode %s != signed %s", got, mode)
			}
		case <-time.After(time.Second):
			t.Fatal("no signed downstream request")
		}
	}
}
