package control

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

const deploymentID = "11111111-1111-4111-8111-111111111111"
const namespaceID = "22222222-2222-4222-8222-222222222222"
const instanceID = "33333333-3333-4333-8333-333333333333"
const principalID = "44444444-4444-4444-8444-444444444444"
const jobID = "55555555-5555-4555-8555-555555555555"

var testActor = monitoring.Actor{Account: api.Account{ID: principalID}, DirectoryID: principalID, Issuer: "https://identity.example/adfs", Subject: "synthetic-alice"}

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-dashboard"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(leaf)
	s := httptest.NewUnstartedServer(handler)
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}
	s.StartTLS()
	t.Cleanup(s.Close)
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	signer, err := auth.NewDelegationSigner(key, "key1", "dashboard-synthetic", "control-synthetic", der, []string{namespaceID})
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{DeploymentID: deploymentID, Name: "Synthetic", Endpoint: s.URL, InstanceID: instanceID, NamespaceIDs: []string{namespaceID}, Roots: roots, Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, Signer: signer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func capabilities(now time.Time) map[string]any {
	return map[string]any{"apiVersion": contract, "kind": "ControlCapabilities", "capabilities": map[string]any{"instanceId": instanceID, "recoveryEpoch": "1", "serviceTime": now, "contractVersions": []string{contract}, "maximumPageSize": 200, "features": []string{"namespace-discovery", "job-monitoring", "namespace-summary", "directory-authorization", "read-delegation"}}}
}
func grants(now time.Time, version string) map[string]any {
	return map[string]any{"apiVersion": contract, "kind": "CurrentPrincipal", "principal": map[string]string{"id": principalID, "directoryId": testActor.DirectoryID, "issuer": testActor.Issuer, "subject": testActor.Subject}, "namespaces": []any{map[string]any{"id": namespaceID, "name": "lab", "roles": []string{"viewer"}, "capabilities": []string{"namespace.read", "jobs.read"}, "authorizationVersion": version, "authorizationStatus": "verified", "authorizationCheckedAt": now, "lastDirectoryVerifiedAt": now.Add(-time.Second), "authorizationExpiresAt": now.Add(119 * time.Second)}}}
}
func job(now time.Time) map[string]any {
	return map[string]any{"apiVersion": contract, "kind": "Job", "metadata": map[string]any{"namespaceId": namespaceID, "namespace": "lab", "id": jobID, "name": "synthetic failure", "revision": int64(9223372036854775807), "createdAt": now.Add(-time.Hour), "updatedAt": now, "owner": map[string]string{"id": principalID, "displayName": "Alice"}}, "spec": map[string]any{"placement": map[string]any{"targetId": jobID, "targetGenerationId": instanceID, "executionBackend": "slurm"}}, "status": map[string]any{"phase": "completed", "desiredState": "running", "outcome": "future-outcome", "observationConfidence": "confirmed", "lifecycle": map[string]any{"completedAt": now, "completedProvenance": "agent"}, "currentRun": map[string]string{"id": jobID, "number": "3"}}}
}

func TestAuthorizedReadsPreserveFactsAndFilters(t *testing.T) {
	now := time.Now().UTC()
	jobReads := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 {
			t.Error("missing mutually authenticated connection")
		}
		if r.URL.Path != "/v1/capabilities" && !strings.HasPrefix(r.Header.Get("Authorization"), auth.DelegationScheme+" ") {
			t.Error("missing delegation")
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			respond(w, capabilities(now))
		case "/v1/me":
			respond(w, grants(now, "9"))
		case "/v1/namespaces/lab/jobs":
			jobReads++
			if r.URL.Query().Get("ownerPrincipalId") != principalID || r.URL.Query().Get("outcome") != "future-outcome" || r.URL.Query().Get("completedBefore") == "" || r.URL.Query().Get("confidence") != "attention" || r.URL.Query().Has("attention") {
				t.Error("filters changed")
			}
			respond(w, map[string]any{"apiVersion": contract, "kind": "JobList", "asOf": now, "items": []any{job(now)}})
		case "/v1/namespaces/lab/jobs/" + jobID:
			respond(w, job(now))
		default:
			t.Errorf("unexpected URL %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	page, err := c.Jobs(context.Background(), testActor, monitoring.SourceQuery{Query: monitoring.Query{Limit: 50, Owner: "me", Outcome: "future-outcome", Attention: true, CompletedTo: &now}, NamespaceID: namespaceID, CreatedBefore: now})
	if err != nil {
		t.Fatal(err)
	}
	if jobReads != 1 || len(page.Items) != 1 || page.Items[0].Revision != "9223372036854775807" || !page.Items[0].Owner.IsCurrentUser || page.Items[0].TargetGeneration != "" || page.Items[0].TargetGenerationID != instanceID || page.Items[0].Outcome != "future-outcome" || page.Items[0].Lifecycle.CompletedProvenance != "agent" {
		t.Fatalf("facts lost: %+v", page)
	}
	got, err := c.Job(context.Background(), testActor, api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, jobID)
	if err != nil || got.CurrentRun.Number != "3" {
		t.Fatalf("job: %+v %v", got, err)
	}
}

func TestDiscoveryFailsClosed(t *testing.T) {
	for _, mode := range []string{"instance", "feature", "stale", "unmanaged", "future", "namespace", "selector", "repeated", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					v := capabilities(now)
					p := v["capabilities"].(map[string]any)
					if mode == "instance" {
						p["instanceId"] = jobID
					}
					if mode == "feature" {
						p["features"] = []string{}
					}
					respond(w, v)
					return
				}
				v := grants(now, "2")
				ns := v["namespaces"].([]any)[0].(map[string]any)
				switch mode {
				case "stale":
					ns["authorizationExpiresAt"] = now.Add(-time.Second)
				case "unmanaged":
					delete(ns, "authorizationStatus")
				case "future":
					ns["lastDirectoryVerifiedAt"] = now.Add(time.Hour)
				case "namespace":
					ns["id"] = jobID
				case "selector":
					ns["name"] = "../other"
				case "repeated":
					v["nextPageToken"] = "repeat"
				case "oversized":
					w.Write([]byte(strings.Repeat("x", maxBody+1)))
					return
				}
				respond(w, v)
			}))
			if _, err := c.Discover(context.Background(), testActor); err == nil {
				t.Fatal("untrusted source accepted")
			}
		})
	}
}

func TestAccessChangeDuringReadDiscardsResult(t *testing.T) {
	now := time.Now().UTC()
	version := "1"
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			respond(w, capabilities(now))
		case "/v1/me":
			respond(w, grants(now, version))
		default:
			version = "2"
			respond(w, job(now))
		}
	}))
	if _, err := c.Job(context.Background(), testActor, api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, jobID); !errors.Is(err, monitoring.ErrAuthority) {
		t.Fatalf("changed authorization accepted: %v", err)
	}
}

func TestDiscoveryBindsDirectoryIdentityWhileAllowingCanonicalAliases(t *testing.T) {
	for _, mode := range []string{"canonical-alias", "other-directory", "missing-directory", "missing-principal"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					respond(w, capabilities(now))
					return
				}
				v := grants(now, "1")
				identity := v["principal"].(map[string]string)
				identity["issuer"], identity["subject"] = "https://canonical.example/adfs", "original-control-subject"
				switch mode {
				case "other-directory":
					identity["directoryId"] = jobID
				case "missing-directory":
					delete(identity, "directoryId")
				case "missing-principal":
					delete(identity, "id")
				}
				respond(w, v)
			}))
			_, err := c.Discover(context.Background(), testActor)
			if mode == "canonical-alias" {
				if err != nil {
					t.Fatalf("verified alias rejected: %v", err)
				}
			} else if !errors.Is(err, monitoring.ErrAuthority) {
				t.Fatalf("unbound identity accepted: %v", err)
			}
		})
	}
}

func TestPrincipalRemappingDuringReadDiscardsOwnerClassification(t *testing.T) {
	now := time.Now().UTC()
	read := false
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			respond(w, capabilities(now))
		case "/v1/me":
			v := grants(now, "1")
			if read {
				v["principal"].(map[string]string)["id"] = jobID
			}
			respond(w, v)
		default:
			read = true
			respond(w, job(now))
		}
	}))
	if _, err := c.Job(context.Background(), testActor, api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, jobID); !errors.Is(err, monitoring.ErrAuthority) {
		t.Fatalf("changed principal mapping accepted: %v", err)
	}
}

func TestRedirectCannotForwardCredentials(t *testing.T) {
	called := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer other.Close()
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	if _, err := c.Discover(context.Background(), testActor); err == nil || called {
		t.Fatalf("redirect followed: %v %v", err, called)
	}
}

func TestSummaryRejectsMalformedCountsAndWindow(t *testing.T) {
	for _, mode := range []string{"valid", "negative", "overflow", "window", "namespace"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			window := api.Window{From: now.Add(-24 * time.Hour), To: now}
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					respond(w, capabilities(now))
				case "/v1/me":
					respond(w, grants(now, "1"))
				default:
					s := map[string]any{"namespaceId": namespaceID, "namespace": "lab", "asOf": now, "completedFrom": window.From, "completedBefore": window.To, "total": "50", "active": "7", "awaitingExecution": "2", "evidenceAttention": "3", "missingCompletionTime": "1", "byPhase": map[string]string{"running": "5"}, "byOutcome": map[string]string{"future": "9"}}
					switch mode {
					case "negative":
						s["active"] = "-1"
					case "overflow":
						s["active"] = "9223372036854775808"
					case "window":
						s["completedBefore"] = now.Add(time.Second)
					case "namespace":
						s["namespaceId"] = jobID
					}
					respond(w, map[string]any{"apiVersion": contract, "kind": "NamespaceSummary", "summary": s})
				}
			}))
			result, err := c.Summary(context.Background(), testActor, api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, window)
			if mode != "valid" {
				if err == nil {
					t.Fatal("invalid summary accepted")
				}
				return
			}
			if err != nil || result.Active != 7 || result.AwaitingExecution != 2 || result.EvidenceAttention != 3 || result.MissingCompletionTime != 1 || result.Running != 5 || result.Terminal["future"] != 9 {
				t.Fatalf("wrong summary: %+v %v", result, err)
			}
		})
	}
}
