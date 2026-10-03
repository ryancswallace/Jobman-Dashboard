package control

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestBoundedManifestPinsScopeVersionAndSelectors(t *testing.T) {
	for _, mode := range []string{"tail", "offset", "sequence", "namespace", "epoch", "version", "missing_count", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			version := "9"
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					respond(w, capabilities(now))
				case "/v1/me":
					g := grants(now, version)
					g["namespaces"].([]any)[0].(map[string]any)["capabilities"] = []string{"namespace.read", "jobs.read", "logs.read"}
					respond(w, g)
				case "/v1/namespaces/lab/jobs/" + jobID + "/log-chunks":
					if r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("runNumber") != "3" || r.URL.Query().Get("stream") != "stderr" {
						t.Error("bounded selectors lost")
					}
					if mode == "tail" && r.URL.Query().Get("tailBytes") != "100" {
						t.Error("tail missing")
					}
					if mode == "offset" && r.URL.Query().Get("fromOffset") != "9007199254740993" {
						t.Error("large exact offset lost")
					}
					if mode == "sequence" && r.URL.Query().Get("afterSequence") != "4" {
						t.Error("sequence missing")
					}
					v := map[string]any{"apiVersion": contract, "kind": "LogChunkList", "asOf": now, "namespaceId": namespaceID, "namespace": "lab", "jobId": jobID, "targetGenerationId": instanceID, "runId": jobID, "runNumber": "3", "executionId": principalID, "stream": "stderr", "manifestRevision": "9223372036854775807", "recoveryEpoch": "1", "authorizationVersion": "9", "state": "open", "truncated": false, "byteLength": "9007199254740993", "lastSequence": "4", "chunks": []any{}}
					switch mode {
					case "namespace":
						v["namespaceId"] = principalID
					case "epoch":
						v["recoveryEpoch"] = "2"
					case "version":
						v["authorizationVersion"] = "10"
					case "missing_count":
						delete(v, "byteLength")
					case "revoke":
						version = "10"
					}
					respond(w, v)
				default:
					t.Errorf("used unbounded or unexpected route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			q := logs.ManifestQuery{Scope: api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, JobID: jobID, RunNumber: "3", Stream: "stderr", Limit: 1, TailBytes: 100}
			if mode == "offset" {
				n := int64(9007199254740993)
				q.Offset = &n
				q.TailBytes = 0
			}
			if mode == "sequence" {
				n := int64(4)
				q.AfterSequence = &n
				q.TailBytes = 0
			}
			m, err := c.Manifest(t.Context(), testActor, q)
			switch mode {
			case "tail", "offset", "sequence":
				if err != nil || m.ByteLength != 9007199254740993 || m.Revision != "9223372036854775807" {
					t.Fatalf("exact manifest: %+v %v", m, err)
				}
			case "revoke":
				if !errors.Is(err, monitoring.ErrAuthority) {
					t.Fatalf("revocation accepted %v", err)
				}
			default:
				if err == nil {
					t.Fatal("invalid source manifest accepted")
				}
			}
		})
	}
}
