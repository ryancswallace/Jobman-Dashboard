//go:build linux || darwin

package logs

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestActualMutualTLSBrokerReauthorizesAndReadsIsolatedFile(t *testing.T) {
	_, source, files, actor, _ := brokerFixture(t)
	actor.Issuer = "https://synthetic.invalid"
	actor.Subject = "verified-subject"
	m := source.manifest
	chunk := m.Chunks[1]
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	issue := func(serial int64, usage x509.ExtKeyUsage) tls.Certificate {
		t.Helper()
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		der, err := x509.CreateCertificate(rand.Reader, template, ca, pub, key)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	clientCert, serverCert := issue(2, x509.ExtKeyUsageClientAuth), issue(3, x509.ExtKeyUsageServerAuth)
	sum := sha256.Sum256(clientCert.Certificate[0])
	verifier, err := auth.NewBrokerVerifier([]auth.BrokerService{{KeyID: "synthetic-key", ServiceID: "synthetic-dashboard", Audience: "urn:synthetic-broker", DeploymentID: m.Scope.DeploymentID, CertificateSHA256: base64.RawURLEncoding.EncodeToString(sum[:]), PublicKey: pub, NamespaceIDs: []string{m.Scope.NamespaceID}}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files.data {
		path := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewProcessReader(executable, 2, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.arguments = []string{"-test.run=^TestReaderHelper$", "--", HelperArgument}
	local, err := NewLocalChunks([]Mapping{{DeploymentID: m.Scope.DeploymentID, TargetGenerationID: m.TargetGenerationID, StoreName: chunk.StoreName, StoreVersion: chunk.StoreVersion, Root: root}}, reader)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(map[string]ManifestSource{m.Scope.DeploymentID: source}, local, verifier)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(service.Handler())
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
	server.StartTLS()
	defer server.Close()
	signer, err := auth.NewDelegationSigner(key, "synthetic-key", "synthetic-dashboard", "urn:synthetic-broker", clientCert.Certificate[0], []string{m.Scope.NamespaceID})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{DeploymentID: m.Scope.DeploymentID, Origin: server.URL, Roots: roots, Certificate: clientCert, Signer: signer})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// A broker restart fences all previously issued assertions. Exercise the
	// real startup interval rather than disabling it for an integration test.
	if _, err = client.ReadChunk(t.Context(), actor, m, chunk); err == nil {
		t.Fatal("startup fence bypassed")
	}
	time.Sleep(6 * time.Second)
	got, err := client.ReadChunk(t.Context(), actor, m, chunk)
	if err != nil || got.State != "ok" || string(got.Bytes) != "efgh" {
		t.Fatalf("actual broker read: %s %v", got.State, err)
	}
	if err = os.Remove(filepath.Join(root, chunk.ObjectKey)); err != nil {
		t.Fatal(err)
	}
	got, err = client.ReadChunk(t.Context(), actor, m, chunk)
	if err != nil || got.State != "chunk_missing" || len(got.Bytes) != 0 {
		t.Fatalf("missing file: %s %v", got.State, err)
	}
	if err = os.WriteFile(filepath.Join(root, chunk.ObjectKey), files.data[chunk.ObjectKey], 0600); err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	calls := source.calls
	source.after = func(s *testSource) {
		if s.calls == calls+2 {
			s.err = monitoring.ErrForbidden
		}
	}
	source.mu.Unlock()
	got, err = client.ReadChunk(t.Context(), actor, m, chunk)
	if err == nil || len(got.Bytes) != 0 {
		t.Fatal("final source revocation exposed bytes")
	}
}
