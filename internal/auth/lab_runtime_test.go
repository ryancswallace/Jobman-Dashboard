//go:build integration

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

const labDeployment = "72000000-0000-4000-8000-000000000001"

// This runs against the deployed ordinary Dashboard and broker executables,
// with actual PostgreSQL, Control delegation, LDAPS and NFS. The source's job
// observations are synthetic and do not establish real Slurm/host execution.
func TestLabDeployedMonitoringAndCrossUserLogs(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" {
		t.Skip("set JOBMAN_DASHBOARD_LAB_RUNTIME=1 after scoped Lab deployment")
	}
	for _, user := range []string{"alice", "bob"} {
		t.Run(user, func(t *testing.T) {
			directory := "71000000-0000-4000-8000-000000000001"
			if user == "bob" {
				directory = "71000000-0000-4000-8000-000000000002"
			}
			session := labNativeSignIn(t, user, directory)
			var fixture struct {
				Synthetic  bool `json:"synthetic"`
				Namespaces []struct {
					ID           string   `json:"id"`
					Name         string   `json:"name"`
					JobIDs       []string `json:"jobIds"`
					CollectionID string   `json:"collectionId"`
					ArrayID      string   `json:"arrayId"`
					GraphID      string   `json:"graphId"`
				} `json:"namespaces"`
			}
			data, err := os.ReadFile(filepath.Join(session.root, ".lab/dashboard/fixture-info.json"))
			if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &fixture) != nil || !fixture.Synthetic || len(fixture.Namespaces) != 2 {
				t.Fatal("Expected bounded public synthetic fixture identities")
			}
			transport := session.transport.Clone()
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "dashboard.lab.test:8443" {
					return nil, ErrUnauthenticated
				}
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:8443")
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			read := func(path, token string, target any, status int) {
				t.Helper()
				request, err := http.NewRequestWithContext(t.Context(), "GET", "https://dashboard.lab.test:8443"+path, nil)
				if err != nil {
					t.Fatal("Invalid synthetic endpoint")
				}
				request.Header.Set("Authorization", "Bearer "+token)
				response, err := client.Do(request)
				if err != nil {
					t.Fatal("Deployed Dashboard request failed")
				}
				defer response.Body.Close()
				body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
				if err != nil || len(body) > 4<<20 {
					t.Fatal("Deployed Dashboard response exceeded bound")
				}
				if response.StatusCode != status {
					var failure api.Error
					_ = json.Unmarshal(body, &failure)
					t.Fatalf("Deployed request %s returned HTTP%d, expected%d (code %q)", path, response.StatusCode, status, failure.Code)
				}
				if target != nil && json.Unmarshal(body, target) != nil {
					t.Fatal("Deployed response did not match the public contract")
				}
			}
			var bootstrap api.Bootstrap
			read("/api/v1/bootstrap", session.idToken, nil, 401)
			read("/api/v1/bootstrap", session.accessToken, &bootstrap, 200)
			wantScopes := 2
			if user == "bob" {
				wantScopes = 1
			}
			if bootstrap.FixtureMode || bootstrap.Account.ID == "" || len(bootstrap.Deployments) != 1 || bootstrap.Deployments[0].ID != labDeployment || len(bootstrap.Deployments[0].Namespaces) != wantScopes {
				t.Fatal("Real runtime discovery or namespace isolation failed")
			}
			for i, namespace := range fixture.Namespaces {
				if len(namespace.JobIDs) < 5 || !uuid(namespace.ID) {
					t.Fatal("Incomplete synthetic source job identities")
				}
				prefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + namespace.ID
				jobPath := prefix + "/jobs/" + namespace.JobIDs[0]
				if user == "bob" && i == 1 {
					read(jobPath, session.accessToken, nil, 403)
					read(jobPath+"/logs?stream=stdout", session.accessToken, nil, 403)
					read(jobPath+"/artifacts", session.accessToken, nil, 403)
					continue
				}
				grant := bootstrap.Deployments[0].Namespaces[i]
				if grant.ID != namespace.ID {
					for _, candidate := range bootstrap.Deployments[0].Namespaces {
						if candidate.ID == namespace.ID {
							grant = candidate
						}
					}
				}
				if grant.ID != namespace.ID || !slices.Contains(grant.Capabilities, "logs.read") || !grant.AuthorizationExpiresAt.After(time.Now()) {
					t.Fatal("Fresh direct-directory read grant absent")
				}
				if user == "alice" && i == 0 && (!slices.Contains(grant.Roles, "viewer") || !slices.Contains(grant.Roles, "submitter")) {
					t.Fatal("Overlapping direct role groups did not produce their union")
				}
				scope, _ := json.Marshal([]api.Scope{{DeploymentID: labDeployment, NamespaceID: namespace.ID}})
				query := url.Values{"scope": {string(scope)}, "limit": {"200"}}.Encode()
				var jobs api.Page[api.Job]
				read("/api/v1/jobs?"+query, session.accessToken, &jobs, 200)
				if len(jobs.Items) < 5 || jobs.Completeness != "complete" {
					t.Fatal("Live source job catalog is incomplete")
				}
				var detail api.JobDetail
				read(jobPath, session.accessToken, &detail, 200)
				if detail.Job.Owner == nil || detail.Job.Owner.IsCurrentUser != (user == "alice") {
					t.Fatal("Canonical owner or cross-user job visibility differs")
				}
				var logs api.LogRange
				read(jobPath+"/logs?stream=stdout&limitBytes=262144", session.accessToken, &logs, 200)
				bytes, err := base64.StdEncoding.DecodeString(logs.BytesBase64)
				if err != nil || string(bytes) != "SYNTHETIC Dashboard Lab log: metadata and byte delivery acceptance only.\n" || logs.State != "complete" || logs.StartOffset != "0" || logs.EndOffset != strconv.Itoa(len(bytes)) || !uuid(logs.ExecutionID) || !uuid(logs.RunID) {
					t.Fatalf("Authorized NFS response differs: validEncoding=%t expectedBytes=%t state=%q start=%q end=%q actualByteCount=%d validExecutionID=%t validRunID=%t", err == nil, string(bytes) == "SYNTHETIC Dashboard Lab log: metadata and byte delivery acceptance only.\n", logs.State, logs.StartOffset, logs.EndOffset, len(bytes), uuid(logs.ExecutionID), uuid(logs.RunID))
				}
				read(prefix+"/jobs/"+namespace.JobIDs[1]+"/logs?stream=stdout", session.accessToken, &logs, 200)
				if logs.State != "complete" || logs.BytesBase64 != "" || logs.StartOffset != "0" || logs.EndOffset != "0" {
					t.Fatal("Empty terminal stream was not represented distinctly")
				}
				var artifacts api.ArtifactPage
				read(jobPath+"/artifacts", session.accessToken, &artifacts, 200)
				if artifacts.Completeness != "complete" || artifacts.Total != "0" || len(artifacts.Items) != 0 {
					t.Fatal("Actual empty artifact metadata response differs")
				}
				for kind, id := range map[string]string{"collection": namespace.CollectionID, "array": namespace.ArrayID, "graph": namespace.GraphID} {
					var catalog api.WorkloadPage
					read("/api/v1/workloads/"+kind+"?"+query, session.accessToken, &catalog, 200)
					wantCount := 1
					if kind == "collection" {
						wantCount = 2
					} // Arrays are a collection subtype at source.
					found := slices.ContainsFunc(catalog.Items, func(item api.Workload) bool { return item.ID == id })
					if catalog.Total != strconv.Itoa(wantCount) || len(catalog.Items) != wantCount || !found {
						t.Fatalf("Actual source workload catalog differs: kind=%s total=%s items=%d expectedID=%s", kind, catalog.Total, len(catalog.Items), id)
					}
					var workload api.WorkloadDetail
					read(prefix+"/workloads/"+kind+"/"+id, session.accessToken, &workload, 200)
					if len(workload.Children) == 0 || workload.Workload.ID != id {
						t.Fatal("Actual source workload children absent")
					}
					if kind == "graph" {
						var neighborhood api.GraphNeighborhood
						read(prefix+"/workloads/graph/"+id+"/neighborhood?nodeId="+url.QueryEscape(workload.Children[0].ID), session.accessToken, &neighborhood, 200)
						if len(neighborhood.Nodes) == 0 || neighborhood.CenterID != workload.Children[0].ID {
							t.Fatal("Actual graph neighborhood differs")
						}
					}
				}
			}
			t.Log("PASS: deployed authenticated discovery, direct role union and namespace denial, cross-owner monitoring/NFS logs, empty terminal streams, artifact metadata and workload/graph reads; synthetic source observations")
		})
	}
}
