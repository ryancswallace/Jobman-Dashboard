//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// These are configured synthetic targets read through ordinary deployed source
// and Dashboard services; no scheduler execution or target health is inferred.
func TestLabDeployedTargetCatalogAndPartitions(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" {
		t.Skip("set JOBMAN_DASHBOARD_LAB_RUNTIME=1 after scoped Lab deployment")
	}
	var aliceCursor string
	var operationsTarget api.Target
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
					ID                 string `json:"id"`
					Name               string `json:"name"`
					TargetGenerationID string `json:"targetGenerationId"`
				} `json:"namespaces"`
			}
			data, err := os.ReadFile(filepath.Join(session.root, ".lab/dashboard/fixture-info.json"))
			if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &fixture) != nil || !fixture.Synthetic || len(fixture.Namespaces) != 2 {
				t.Fatal("Expected bounded public synthetic fixture identities")
			}
			diagnostic := readLabDiagnosticFixture(t, session.root, false)
			transport := session.transport.Clone()
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "dashboard.lab.test:8443" {
					return nil, ErrUnauthenticated
				}
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:8443")
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			read := func(path string, target any, status int) {
				t.Helper()
				request, err := http.NewRequestWithContext(t.Context(), "GET", "https://dashboard.lab.test:8443"+path, nil)
				if err != nil {
					t.Fatal("Invalid synthetic endpoint")
				}
				request.Header.Set("Authorization", "Bearer "+session.accessToken)
				response, err := client.Do(request)
				if err != nil {
					t.Fatal("Deployed target request failed")
				}
				defer response.Body.Close()
				body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
				if err != nil || len(body) > 4<<20 {
					t.Fatal("Deployed target response exceeded bound")
				}
				if response.StatusCode != status {
					var failure api.Error
					_ = json.Unmarshal(body, &failure)
					t.Fatalf("Deployed target request returned HTTP%d, expected%d (code %q)", response.StatusCode, status, failure.Code)
				}
				if target != nil && json.Unmarshal(body, target) != nil {
					t.Fatal("Deployed target response did not match the public contract")
				}
			}
			wantScopes := 2
			if user == "bob" {
				wantScopes = 1
			}
			allowed := make(map[string]string)
			for _, namespace := range fixture.Namespaces {
				if !uuid(namespace.ID) || !uuid(namespace.TargetGenerationID) {
					t.Fatal("Fixture target identities are missing")
				}
				if user == "alice" || namespace.Name == "dashboard-research" {
					allowed[namespace.ID] = namespace.TargetGenerationID
				}
			}
			if len(allowed) != wantScopes {
				t.Fatal("Unexpected fixture namespace names")
			}
			wantTargets := wantScopes * 2
			if diagnostic != nil && user == "alice" {
				if allowed[diagnostic.NamespaceID] == "" {
					t.Fatal("Supplemental target belongs to an unknown namespace")
				}
				wantTargets++
			}
			seen := make(map[string]bool)
			seenCursors := make(map[string]bool)
			cursor := ""
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber >= 8 || seenCursors[cursor] {
					t.Fatal("Target continuation did not terminate within the fixture bound")
				}
				seenCursors[cursor] = true
				query := url.Values{"limit": {"1"}}
				if cursor != "" {
					query.Set("cursor", cursor)
				}
				var page api.TargetPage
				read("/api/v1/targets?"+query.Encode(), &page, 200)
				if page.Completeness != "complete" || page.Total != strconv.Itoa(wantTargets) || len(page.Items) != 1 || len(page.Totals) != wantScopes || len(page.Sources) != wantScopes {
					t.Fatal("Aggregate target totals, page bounds or completeness differ")
				}
				for _, source := range page.Sources {
					if source.DeploymentID != labDeployment || allowed[source.NamespaceID] == "" || source.Status != "available" {
						t.Fatal("Target catalog returned an unauthorized or unavailable source")
					}
				}
				for _, total := range page.Totals {
					wantTotal := "2"
					if diagnostic != nil && total.NamespaceID == diagnostic.NamespaceID {
						wantTotal = "3"
					}
					if total.DeploymentID != labDeployment || allowed[total.NamespaceID] == "" || total.Total != wantTotal || total.AsOf.IsZero() {
						t.Fatal("Per-source exact target totals or provenance differ")
					}
				}
				target := page.Items[0]
				key := target.DeploymentID + "/" + target.NamespaceID + "/" + target.TargetID
				if target.DeploymentID != labDeployment || allowed[target.NamespaceID] == "" || !uuid(target.TargetID) || !uuid(target.Generation.ID) || target.AsOf.IsZero() || seen[key] {
					t.Fatal("Target identity, provenance, namespace isolation or pagination differs")
				}
				seen[key] = true
				prefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + target.NamespaceID + "/targets/" + target.TargetID
				var detail api.TargetDetail
				read(prefix, &detail, 200)
				if detail.Completeness != "complete" || detail.Target.TargetID != target.TargetID || detail.Target.Scope != target.Scope || detail.Target.Generation.ID != target.Generation.ID || detail.Target.Revision != target.Revision {
					t.Fatal("Target detail changed identity or generation")
				}
				var partitions api.TargetPartitionPage
				read(prefix+"/partitions?generationId="+target.Generation.ID+"&limit=1", &partitions, 200)
				if partitions.TargetID != target.TargetID || partitions.GenerationID != target.Generation.ID || partitions.Completeness != "complete" || partitions.NextCursor != "" {
					t.Fatal("Partition identity, generation or completeness differs")
				}
				switch target.Name {
				case "synthetic-host":
					if target.Generation.ID != allowed[target.NamespaceID] || target.Generation.ExecutionBackend != "subprocess" || target.Generation.LogStore == nil || target.Generation.LogStore.Name != "lab-nfs" || target.Generation.LogStore.Version != "1" || partitions.Total != "0" || len(partitions.Items) != 0 {
						t.Fatal("Configured host generation, store reference or empty partition set differs")
					}
				case "synthetic-slurm":
					if target.Generation.ExecutionBackend != "slurm" || partitions.Total != "1" || len(partitions.Items) != 1 || partitions.Items[0].Name != "synthetic" || !partitions.Items[0].IsDefault {
						t.Fatal("Configured Slurm partition metadata differs")
					}
				case "synthetic-diagnostics":
					if diagnostic == nil || target.NamespaceID != diagnostic.NamespaceID || target.TargetID != diagnostic.TargetID || target.Generation.ID != diagnostic.TargetGenerationID || target.Generation.ExecutionBackend != "subprocess" || target.Generation.LogStore == nil || target.Generation.LogStore.Name != "lab-nfs" || target.Generation.LogStore.Version != "1" || partitions.Total != "0" || len(partitions.Items) != 0 {
						t.Fatal("Supplemental synthetic target differs from its exact manifest identity")
					}
				default:
					t.Fatal("Unexpected synthetic target")
				}
				var stale api.Error
				read(prefix+"/partitions?generationId=79000000-0000-4000-8000-000000000001", &stale, 409)
				if stale.Code != "target_changed" {
					t.Fatal("Wrong generation did not require target refresh")
				}
				if user == "alice" && pageNumber == 0 {
					aliceCursor = page.NextCursor
				}
				for _, namespace := range fixture.Namespaces {
					if namespace.Name == "dashboard-operations" && target.NamespaceID == namespace.ID {
						operationsTarget = target
					}
				}
				cursor = page.NextCursor
				if cursor == "" {
					break
				}
			}
			if len(seen) != wantTargets {
				t.Fatal("Target pagination lost rows")
			}
			if user == "bob" {
				if aliceCursor == "" || operationsTarget.TargetID == "" {
					t.Fatal("Alice baseline is required for cross-account denial checks")
				}
				read("/api/v1/targets?limit=1&cursor="+url.QueryEscape(aliceCursor), nil, 409)
				scope, _ := json.Marshal([]api.Scope{operationsTarget.Scope})
				read("/api/v1/targets?scope="+url.QueryEscape(string(scope)), nil, 403)
				prefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + operationsTarget.NamespaceID + "/targets/" + operationsTarget.TargetID
				read(prefix, nil, 403)
				read(prefix+"/partitions?generationId="+operationsTarget.Generation.ID, nil, 403)
			}
			t.Log("PASS: exact paginated source-qualified catalogs, target generations and partition metadata; stale generation and cross-namespace/account reads denied")
		})
	}
}
