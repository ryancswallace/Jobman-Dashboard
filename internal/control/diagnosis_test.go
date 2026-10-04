package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/diagnostic"
)

func diagnosticDocument(now time.Time) diagnostic.SharedSnapshot {
	return diagnostic.SharedSnapshot{
		Kind: diagnostic.SharedSnapshotKind, SchemaVersion: diagnostic.SharedSnapshotVersion,
		CapturedAt: now, Source: diagnostic.SharedSource{Kind: diagnostic.SharedSourceControl, DeploymentID: deploymentID, ControlInstanceID: instanceID, NamespaceID: namespaceID, ControlVersion: "test", ContractVersion: contract},
		JobmanVersion: "test", Platform: "linux/arm64", Job: diagnostic.SharedJob{ID: jobID, Revision: 9007199254740993, Phase: "terminal", Outcome: "failure"},
		Runs:     []diagnostic.SharedRun{{ID: principalID, Number: 3, ExecutionID: jobID}},
		Metadata: diagnostic.MetadataTransactionalSnapshot, Items: []diagnostic.Item{}, Logs: []diagnostic.SharedLogReference{}, Omissions: []diagnostic.Omission{}, RedactionNotices: []diagnostic.RedactionNotice{},
	}
}

func TestDiagnosticAdapterPinsSnapshotAndCurrentAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "feature", "namespace", "epoch", "authorization", "revoked", "expiry", "captured", "deployment", "instance", "job", "revision", "run", "invalid-fact", "changed"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			version := "9"
			reads := 0
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					v := capabilities(now)
					if mode != "feature" {
						m := v["capabilities"].(map[string]any)
						m["features"] = append(m["features"].([]string), "shared-diagnostic-snapshots")
					}
					respond(w, v)
				case "/v1/me":
					v := grants(now, version)
					v["namespaces"].([]any)[0].(map[string]any)["capabilities"] = []string{"namespace.read", "jobs.read", "evidence.read"}
					respond(w, v)
				case "/v1/namespaces/lab/jobs/" + jobID + "/diagnostic-snapshot":
					reads++
					q := r.URL.Query()
					if q.Get("deploymentId") != deploymentID || q.Get("controlInstanceId") != instanceID || q.Get("namespaceId") != namespaceID || q.Get("runId") != principalID || q.Get("expectedJobRevision") != "9007199254740993" {
						t.Error("source selection lost")
					}
					v := diagnosticDocument(now)
					response := map[string]any{"apiVersion": contract, "kind": "DiagnosticSnapshot", "namespace": "lab", "namespaceId": namespaceID, "asOf": now, "recoveryEpoch": "1", "authorizationVersion": "9", "authorizationCheckedAt": now, "authorizationExpiresAt": now.Add(time.Minute)}
					switch mode {
					case "namespace":
						response["namespaceId"] = jobID
					case "epoch":
						response["recoveryEpoch"] = "2"
					case "authorization":
						response["authorizationVersion"] = "10"
					case "revoked":
						version = "10"
					case "expiry":
						response["authorizationExpiresAt"] = now.Add(-time.Second)
					case "captured":
						v.CapturedAt = now.Add(-time.Second)
					case "deployment":
						v.Source.DeploymentID = jobID
					case "instance":
						v.Source.ControlInstanceID = jobID
					case "job":
						v.Job.ID = principalID
					case "revision":
						v.Job.Revision++
					case "run":
						v.Runs[0].ID = jobID
					case "invalid-fact":
						v.Items = []diagnostic.Item{{ID: "bad", Code: "unknown"}}
					case "changed":
						w.WriteHeader(http.StatusConflict)
						return
					}
					response["snapshot"] = v
					respond(w, response)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			selection := diagnostic.SharedSelection{DeploymentID: deploymentID, ControlInstanceID: instanceID, NamespaceID: namespaceID, JobID: jobID, ExpectedJobRevision: 9007199254740993, RunID: principalID}
			v, err := c.DiagnosticSnapshot(t.Context(), testActor, selection)
			if mode == "valid" {
				if err != nil || v.RecoveryEpoch != "1" || strconv.FormatUint(v.Value.Job.Revision, 10) != "9007199254740993" || v.Value.Runs[0].Number != 3 {
					t.Fatalf("snapshot facts lost: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid snapshot accepted")
			}
			if mode == "feature" && reads != 0 {
				t.Fatal("unsupported source queried")
			}
			selection.ControlInstanceID = jobID
			if _, err = c.DiagnosticSnapshot(t.Context(), testActor, selection); err == nil {
				t.Fatal("wrong configured instance accepted")
			}
		})
	}
}

func TestDiagnosticAdapterRejectsAmbiguousAndOversizedSnapshotWire(t *testing.T) {
	for _, mode := range []string{"duplicate", "unknown", "case-alias", "nested-case", "escaped-key-duplicate", "oversized-dropped-field", "oversized-escaped-value"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			snapshot := diagnosticDocument(now)
			if mode == "oversized-escaped-value" {
				snapshot.Platform = strings.Repeat("x", 400000)
			}
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "duplicate":
				encoded = bytes.Replace(encoded, []byte(`"revision":"9007199254740993"`), []byte(`"revision":"2","revision":"9007199254740993"`), 1)
			case "unknown":
				encoded = bytes.Replace(encoded, []byte(`"kind":`), []byte(`"private-extra":"discarded","kind":`), 1)
			case "case-alias":
				encoded = bytes.Replace(encoded, []byte(`"kind":`), []byte(`"Kind":`), 1)
			case "nested-case":
				encoded = bytes.Replace(encoded, []byte(`"revision":`), []byte(`"Revision":`), 1)
			case "escaped-key-duplicate":
				encoded = bytes.Replace(encoded, []byte(`"revision":`), []byte(`"revis\u0069on":"2","revision":`), 1)
			case "oversized-dropped-field":
				encoded = bytes.Replace(encoded, []byte(`"kind":`), []byte(`"private-extra":"`+strings.Repeat("x", diagnostic.SharedMaximumBytes)+`","kind":`), 1)
			case "oversized-escaped-value":
				encoded = bytes.Replace(encoded, []byte(`"platform":"`+strings.Repeat("x", 400000)+`"`), []byte(`"platform":"`+strings.Repeat(`\u0078`, 400000)+`"`), 1)
			}
			if len(encoded) > maxBody-1024 {
				t.Fatal("fixture exceeded outer transport budget")
			}
			meReads := 0
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					v := capabilities(now)
					m := v["capabilities"].(map[string]any)
					m["features"] = append(m["features"].([]string), "shared-diagnostic-snapshots")
					respond(w, v)
				case "/v1/me":
					meReads++
					v := grants(now, "9")
					v["namespaces"].([]any)[0].(map[string]any)["capabilities"] = []string{"namespace.read", "evidence.read"}
					respond(w, v)
				case "/v1/namespaces/lab/jobs/" + jobID + "/diagnostic-snapshot":
					// Append raw JSON without an encoder compacting escaped values or aliases.
					authority, _ := json.Marshal(map[string]any{"apiVersion": contract, "kind": "DiagnosticSnapshot", "namespace": "lab", "namespaceId": namespaceID, "asOf": now, "recoveryEpoch": "1", "authorizationVersion": "9", "authorizationCheckedAt": now, "authorizationExpiresAt": now.Add(time.Minute)})
					body := append(bytes.Clone(authority[:len(authority)-1]), []byte(`,"snapshot":`)...)
					body = append(body, encoded...)
					body = append(body, '}')
					w.Header().Set("Content-Type", "application/json")
					if _, err := w.Write(body); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			selection := diagnostic.SharedSelection{DeploymentID: deploymentID, ControlInstanceID: instanceID, NamespaceID: namespaceID, JobID: jobID, ExpectedJobRevision: 9007199254740993, RunID: principalID}
			result, err := c.DiagnosticSnapshot(t.Context(), testActor, selection)
			if err == nil || result.Value.Job.ID != "" || meReads != 1 {
				t.Fatalf("invalid wire accepted or reauthorized after decode: %v", err)
			}
		})
	}
}
