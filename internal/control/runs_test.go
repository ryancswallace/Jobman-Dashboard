package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestRunsAdapterPinsCurrentSourceAndRun(t *testing.T) {
	for _, mode := range []string{"valid", "namespace", "job", "epoch", "grant", "expired", "revoke", "order", "execution", "number", "total", "unsupported", "cursor"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			version := "9"
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					v := capabilities(now)
					if mode != "unsupported" {
						v["capabilities"].(map[string]any)["features"] = append(v["capabilities"].(map[string]any)["features"].([]string), "bounded-run-catalog")
					}
					respond(w, v)
				case "/v1/me":
					respond(w, grants(now, version))
				case "/v1/namespaces/lab/jobs/" + jobID + "/runs", "/v1/namespaces/lab/jobs/" + jobID + "/runs/" + principalID:
					if mode == "cursor" {
						w.WriteHeader(409)
						return
					}
					item := map[string]any{"id": principalID, "number": "9007199254740993", "phase": "future", "desiredState": "run", "createdAt": now, "updatedAt": now, "workload": "private", "objectKey": "private"}
					v := map[string]any{"apiVersion": contract, "kind": "RunList", "asOf": now, "namespace": "lab", "namespaceId": namespaceID, "jobId": jobID, "recoveryEpoch": "1", "authorizationVersion": "9", "authorizationCheckedAt": now, "authorizationExpiresAt": now.Add(time.Minute), "items": []any{item}, "total": "1"}
					if strings.HasSuffix(r.URL.Path, "/"+principalID) {
						v["kind"] = "RunDetail"
						v["run"] = item
					} else if r.URL.Query().Get("limit") != "2" {
						t.Error("limit lost")
					}
					switch mode {
					case "namespace":
						v["namespaceId"] = jobID
					case "job":
						v["jobId"] = principalID
					case "epoch":
						v["recoveryEpoch"] = "2"
					case "grant":
						v["authorizationVersion"] = "2"
					case "expired":
						v["authorizationExpiresAt"] = now.Add(-time.Second)
					case "revoke":
						version = "10"
					case "order":
						v["items"] = []any{item, item}
						v["total"] = "2"
					case "execution":
						item["executionId"] = jobID
					case "number":
						item["number"] = "9223372036854775808"
					case "total":
						v["total"] = "0"
					}
					respond(w, v)
				default:
					t.Error("unexpected source path")
					http.NotFound(w, r)
				}
			}))
			scope := api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}
			page, err := c.Runs(t.Context(), testActor, monitoring.RunQuery{Scope: scope, JobID: jobID, Limit: 2})
			if mode != "valid" {
				if err == nil {
					t.Fatalf("accepted %s", mode)
				}
				if mode == "cursor" && !errors.Is(err, monitoring.ErrCursor) {
					t.Fatalf("source cursor error=%v", err)
				}
				return
			}
			if err != nil || len(page.Items) != 1 || page.Items[0].Number != "9007199254740993" {
				t.Fatalf("page=%+v,%v", page, err)
			}
			raw, _ := json.Marshal(page.Items)
			if strings.Contains(string(raw), "private") {
				t.Fatal("run exposed forbidden metadata")
			}
			detail, err := c.Run(t.Context(), testActor, scope, jobID, principalID)
			if err != nil || detail.Run.ID != principalID {
				t.Fatalf("detail=%+v,%v", detail, err)
			}
		})
	}
}
