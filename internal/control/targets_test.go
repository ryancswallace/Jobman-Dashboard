package control

import (
	"encoding/json"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"net/http"
	"strings"
	"testing"
	"time"
)

func targetDocument(now time.Time) map[string]any {
	return map[string]any{"id": jobID, "name": "slurm-lab", "kind": "slurm", "state": "active", "revision": "9007199254740993", "createdAt": now.Add(-time.Hour), "updatedAt": now, "generation": map[string]any{"id": instanceID, "number": "9007199254740993", "executionBackend": "slurm", "transport": "agent-api", "runtimes": []string{"native"}, "operatingSystems": []string{"linux"}, "architectures": []string{"x86_64"}, "capabilities": []string{"batch"}, "partitions": []any{map[string]any{"name": "batch", "isDefault": true}}, "partitionCount": "201", "partitionsTruncated": true, "artifactStores": []any{}, "provider": map[string]any{"kind": "on-prem"}, "privateCredential": "never-forward"}}
}
func TestTargetAdapterPreservesBoundedFactsAndAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "long-region", "namespace", "epoch", "version", "expiry", "revoke", "feature", "cutoff", "total", "truncation", "duplicate", "bad-target", "generation", "changed", "private-field"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			version := "9"
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					v := capabilities(now)
					if mode != "feature" {
						m := v["capabilities"].(map[string]any)
						m["features"] = append(m["features"].([]string), "target-catalogs")
					}
					respond(w, v)
				case "/v1/me":
					v := grants(now, version)
					v["namespaces"].([]any)[0].(map[string]any)["capabilities"] = []string{"namespace.read", "jobs.read", "targets.read"}
					respond(w, v)
				default:
					v := map[string]any{"apiVersion": contract, "kind": "TargetCatalog", "namespace": "lab", "namespaceId": namespaceID, "asOf": now, "recoveryEpoch": "1", "authorizationVersion": "9", "authorizationCheckedAt": now, "authorizationExpiresAt": now.Add(time.Minute), "createdBefore": now, "total": "1", "items": []any{targetDocument(now)}}
					isPartitions := strings.HasSuffix(r.URL.Path, "/partitions")
					isDetail := strings.HasSuffix(r.URL.Path, "/"+jobID)
					if isPartitions {
						v["kind"] = "TargetPartitionList"
						v["targetId"] = jobID
						v["generationId"] = instanceID
						v["items"] = []any{map[string]any{"name": "batch", "isDefault": true}}
						if r.URL.Query().Get("generationId") != instanceID {
							t.Error("generation binding lost")
						}
					} else if isDetail {
						v["kind"] = "TargetSnapshot"
						v["target"] = targetDocument(now)
					}
					switch mode {
					case "long-region":
						if !isPartitions {
							raw := v["items"].([]any)[0].(map[string]any)
							if isDetail {
								raw = v["target"].(map[string]any)
							}
							g := raw["generation"].(map[string]any)
							g["provider"] = map[string]any{"kind": "aws-parallelcluster", "region": "us-" + strings.Repeat("a", 512) + "-1", "clusterName": "Research"}
						}
					case "namespace":
						v["namespaceId"] = jobID
					case "epoch":
						v["recoveryEpoch"] = "2"
					case "version":
						v["authorizationVersion"] = "10"
					case "expiry":
						v["authorizationExpiresAt"] = now.Add(-time.Second)
					case "revoke":
						version = "10"
					case "cutoff":
						v["createdBefore"] = now.Add(time.Second)
					case "total":
						v["total"] = "0"
					case "truncation":
						v["items"].([]any)[0].(map[string]any)["generation"].(map[string]any)["partitionsTruncated"] = false
					case "duplicate":
						v["items"] = []any{targetDocument(now), targetDocument(now)}
						v["total"] = "2"
					case "bad-target":
						v["targetId"] = instanceID
					case "generation":
						v["generationId"] = jobID
					case "changed":
						w.WriteHeader(409)
						return
					}
					respond(w, v)
				}
			}))
			scope := api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}
			if mode == "bad-target" || mode == "generation" || mode == "changed" {
				_, err := c.TargetPartitions(t.Context(), testActor, monitoring.TargetPartitionQuery{Scope: scope, TargetID: jobID, GenerationID: instanceID, Limit: 2})
				if err == nil {
					t.Fatal("bad partition accepted")
				}
				return
			}
			p, err := c.Targets(t.Context(), testActor, monitoring.TargetSourceQuery{NamespaceID: namespaceID, Limit: 2, CreatedBefore: now})
			if mode != "valid" && mode != "private-field" && mode != "long-region" {
				if err == nil {
					t.Fatal("accepted invalid target", mode)
				}
				return
			}
			if err != nil || len(p.Items) != 1 || p.Items[0].Revision != "9007199254740993" || p.Items[0].Generation.PartitionCount != "201" {
				t.Fatalf("facts %+v %v", p, err)
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "never-forward") {
				t.Fatal("private fields forwarded")
			}
			detail, err := c.Target(t.Context(), testActor, scope, jobID)
			if err != nil || detail.TargetID != jobID {
				t.Fatal("detail", err)
			}
			parts, err := c.TargetPartitions(t.Context(), testActor, monitoring.TargetPartitionQuery{Scope: scope, TargetID: jobID, GenerationID: instanceID, Limit: 2})
			if err != nil || len(parts.Items) != 1 || !parts.Items[0].IsDefault {
				t.Fatal("partitions", err)
			}
		})
	}
}
