package control

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestArtifactMetadataAdapterPinsAuthorityAndOmitsStorageCoordinates(t *testing.T) {
	for _, mode := range []string{"valid", "namespace", "job", "epoch", "version", "run", "checksum", "missing_total", "zero_total", "short_total", "next_at_total", "expired", "revoke", "order"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			version := "9"
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					respond(w, capabilities(now))
				case "/v1/me":
					g := grants(now, version)
					g["namespaces"].([]any)[0].(map[string]any)["capabilities"] = []string{"namespace.read", "jobs.read", "artifacts.read"}
					respond(w, g)
				case "/v1/namespaces/lab/jobs/" + jobID + "/artifact-metadata":
					if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("runNumber") != "3" || r.URL.Query().Get("pageToken") != "opaque" {
						t.Error("source selectors lost")
					}
					item := map[string]any{"runId": jobID, "runNumber": "3", "executionId": principalID, "targetGenerationId": instanceID, "name": "result", "byteLength": "9007199254740993", "checksum": "sha256:" + strings.Repeat("a", 64), "publishedAt": now, "objectKey": "private/location", "storeName": "private-store", "storeVersion": "1"}
					v := map[string]any{"apiVersion": contract, "kind": "ArtifactList", "asOf": now, "namespaceId": namespaceID, "namespace": "lab", "jobId": jobID, "recoveryEpoch": "1", "authorizationVersion": "9", "authorizationCheckedAt": now, "authorizationExpiresAt": now.Add(time.Minute), "total": "9007199254740993", "items": []any{item}}
					switch mode {
					case "namespace":
						v["namespaceId"] = jobID
					case "job":
						v["jobId"] = principalID
					case "epoch":
						v["recoveryEpoch"] = "2"
					case "version":
						v["authorizationVersion"] = "10"
					case "run":
						item["runNumber"] = "4"
					case "checksum":
						item["checksum"] = "unverified"
					case "missing_total":
						delete(v, "total")
					case "zero_total":
						v["total"] = "0"
					case "short_total":
						second := make(map[string]any)
						for k, value := range item {
							second[k] = value
						}
						second["name"] = "result-z"
						v["items"] = []any{item, second}
						v["total"] = "1"
					case "next_at_total":
						v["total"] = "1"
						v["nextPageToken"] = "next"
					case "expired":
						v["authorizationExpiresAt"] = now.Add(-time.Second)
					case "revoke":
						version = "10"
					case "order":
						v["items"] = []any{item, item}
					}
					respond(w, v)
				default:
					t.Errorf("unexpected or unbounded path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			q := monitoring.ArtifactQuery{Scope: api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}, JobID: jobID, RunNumber: "3", Limit: 2, Cursor: "opaque"}
			out, err := c.Artifacts(t.Context(), testActor, q)
			if mode != "valid" {
				if err == nil {
					t.Fatalf("accepted %s", mode)
				}
				return
			}
			if err != nil || out.Total != "9007199254740993" || len(out.Items) != 1 || out.Items[0].SizeBytes != "9007199254740993" || out.Items[0].Availability != "metadata_only" {
				t.Fatalf("metadata %+v %v", out, err)
			}
		})
	}
}
