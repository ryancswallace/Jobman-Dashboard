//go:build integration

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// This opt-in changes only the reviewed synthetic LDAPS fixture through its
// reversible helper. Both API tokens are issued before any mutation, remain in
// memory, and are reused through removal and restoration without another login.
func TestLabExistingTokensRespectDirectGroupChanges(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_REVOCATION") != "1" {
		t.Skip("set the runtime and revocation Lab opt-ins for reversible synthetic group tests")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	helper := filepath.Join(alice.root, "scripts/dashboard-directory-scenario.py")
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		t.Fatal("Reviewed reversible Lab directory helper is unavailable")
	}
	var fixture struct {
		Synthetic  bool `json:"synthetic"`
		Namespaces []struct {
			ID     string   `json:"id"`
			Name   string   `json:"name"`
			JobIDs []string `json:"jobIds"`
		} `json:"namespaces"`
	}
	raw, err := os.ReadFile(filepath.Join(alice.root, ".lab/dashboard/fixture-info.json"))
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &fixture) != nil || !fixture.Synthetic || len(fixture.Namespaces) != 2 {
		t.Fatal("Expected the approved bounded synthetic source identities")
	}
	research := fixture.Namespaces[0]
	if research.Name != "dashboard-research" || !uuid(research.ID) || len(research.JobIDs) == 0 || !uuid(research.JobIDs[0]) {
		t.Fatal("Synthetic research scope is invalid")
	}
	transport := alice.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "dashboard.lab.test:8443" {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:8443")
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	read := func(ctx context.Context, token, path string, target any) (int, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://dashboard.lab.test:8443"+path, nil)
		if err != nil {
			return 0, errors.New("invalid synthetic API path")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return 0, errors.New("synthetic API request failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		if err != nil || len(body) > 4<<20 {
			return response.StatusCode, errors.New("synthetic API response exceeded its bound")
		}
		if response.StatusCode == http.StatusOK && target != nil && json.Unmarshal(body, target) != nil {
			return response.StatusCode, errors.New("synthetic API response differs from contract")
		}
		return response.StatusCode, nil
	}
	namespace := func(ctx context.Context, token string) (api.Namespace, bool, error) {
		var result api.Bootstrap
		status, err := read(ctx, token, "/api/v1/bootstrap", &result)
		if err != nil || status != http.StatusOK || result.FixtureMode || result.Completeness != "complete" {
			return api.Namespace{}, false, errors.New("current real namespace discovery is unavailable")
		}
		for _, deployment := range result.Deployments {
			if deployment.ID == labDeployment {
				for _, entry := range deployment.Namespaces {
					if entry.ID == research.ID {
						return entry, true, nil
					}
				}
			}
		}
		return api.Namespace{}, false, nil
	}
	assertRead := func(token, path string, target any, want int) {
		t.Helper()
		status, err := read(t.Context(), token, path, target)
		if err != nil || status != want {
			t.Fatalf("Synthetic authorized API check returned HTTP%d, expected HTTP%d (requestFailed=%t)", status, want, err != nil)
		}
	}
	baselineAlice, present, err := namespace(t.Context(), alice.accessToken)
	if err != nil || !present || !slices.Contains(baselineAlice.Roles, "viewer") || !slices.Contains(baselineAlice.Roles, "submitter") || !slices.Contains(baselineAlice.Capabilities, "logs.read") {
		t.Fatal("Alice role-union baseline must be ready before any mutation")
	}
	baselineBob, present, err := namespace(t.Context(), bob.accessToken)
	if err != nil || !present || !slices.Equal(baselineBob.Roles, []string{"viewer"}) {
		t.Fatal("Bob viewer baseline must be ready before any mutation")
	}
	jobPath := "/api/v1/deployments/" + labDeployment + "/namespaces/" + research.ID + "/jobs/" + research.JobIDs[0]
	for _, session := range []labNativeSession{alice, bob} {
		assertRead(session.accessToken, jobPath, nil, 200)
		assertRead(session.accessToken, jobPath+"/logs?stream=stdout", nil, 200)
	}
	// Capture an authorized browse continuation before Bob's grant is removed.
	scope, _ := json.Marshal([]api.Scope{{DeploymentID: labDeployment, NamespaceID: research.ID}})
	query := url.Values{"scope": {string(scope)}, "limit": {"1"}}
	var page api.Page[api.Job]
	assertRead(bob.accessToken, "/api/v1/jobs?"+query.Encode(), &page, 200)
	if page.NextCursor == "" {
		t.Fatal("Revocation baseline needs an actual opaque continuation")
	}
	query.Set("cursor", page.NextCursor)

	activeReceipt := ""
	transition := func(ctx context.Context, action, scenario, receipt string) error {
		args := []string{helper, action}
		if action == "begin" {
			args = append(args, scenario, "--receipt", receipt)
		} else {
			args = append(args, receipt)
		}
		ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "python3", args...).Output()
		var result struct {
			Receipt string `json:"receipt"`
			Status  string `json:"status"`
		}
		if err != nil || len(output) > 4096 || json.Unmarshal(output, &result) != nil || result.Receipt != receipt {
			return errors.New("reversible synthetic directory transition failed; preserve its private receipt")
		}
		want := "awaiting-normal-reconciliation"
		if action == "restore" {
			want = "original-restored-awaiting-normal-reconciliation"
		}
		if result.Status != want {
			return errors.New("unexpected synthetic transition status")
		}
		return nil
	}
	t.Cleanup(func() {
		if activeReceipt != "" {
			if err := transition(context.Background(), "restore", "", activeReceipt); err != nil {
				t.Errorf("Synthetic state restoration needs inspection; private recovery receipt %s", activeReceipt)
			}
		}
	})
	waitFor := func(description string, predicate func(context.Context) bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
		defer cancel()
		for {
			if predicate(ctx) {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("Normal directory reconciliation did not reach %s within the bound", description)
			case <-time.After(2 * time.Second):
			}
		}
	}
	begin := func(scenario string) {
		t.Helper()
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			t.Fatal("Could not create private synthetic recovery identity")
		}
		activeReceipt = hex.EncodeToString(nonce[:])
		if err := transition(t.Context(), "begin", scenario, activeReceipt); err != nil {
			t.Fatal("Synthetic transition failed; cleanup will restore its private receipt")
		}
	}
	restore := func() {
		t.Helper()
		if err := transition(t.Context(), "restore", "", activeReceipt); err != nil {
			t.Fatal("Synthetic restoration failed; cleanup will retry its private receipt")
		}
		activeReceipt = ""
		waitFor("both original direct grants", func(ctx context.Context) bool {
			a, ap, ae := namespace(ctx, alice.accessToken)
			b, bp, be := namespace(ctx, bob.accessToken)
			return ae == nil && be == nil && ap && bp && slices.Contains(a.Roles, "viewer") && slices.Contains(a.Roles, "submitter") && slices.Equal(b.Roles, []string{"viewer"})
		})
	}

	begin("alice-viewer-removed")
	waitFor("Alice's remaining submitter grant", func(ctx context.Context) bool {
		entry, present, err := namespace(ctx, alice.accessToken)
		return err == nil && present && slices.Equal(entry.Roles, []string{"submitter"}) && slices.Contains(entry.Capabilities, "logs.read") && entry.AuthorizationVersion != baselineAlice.AuthorizationVersion
	})
	assertRead(alice.accessToken, jobPath, nil, 200)
	var logs api.LogRange
	assertRead(alice.accessToken, jobPath+"/logs?stream=stdout", &logs, 200)
	if logs.State != "complete" || logs.BytesBase64 == "" {
		t.Fatal("Remaining direct role did not retain authorized NFS read capability")
	}
	restore()
	t.Log("PASS: same Alice token retains namespace, job and NFS log access through the remaining direct role; original union restored")

	// Refresh the continuation under the restored current grant immediately
	// before Bob's removal, so denial cannot be attributed to Alice's earlier edit.
	query.Del("cursor")
	page = api.Page[api.Job]{}
	assertRead(bob.accessToken, "/api/v1/jobs?"+query.Encode(), &page, 200)
	if page.NextCursor == "" {
		t.Fatal("Restored baseline needs an actual opaque continuation")
	}
	query.Set("cursor", page.NextCursor)
	begin("bob-research-removed")
	waitFor("Bob's last research grant removal", func(ctx context.Context) bool {
		_, present, err := namespace(ctx, bob.accessToken)
		return err == nil && !present
	})
	for _, path := range []string{jobPath, jobPath + "/logs?stream=stdout", jobPath + "/artifacts", "/api/v1/jobs?" + query.Encode()} {
		assertRead(bob.accessToken, path, nil, 403)
	}
	assertRead(alice.accessToken, jobPath, nil, 200)
	restore()
	assertRead(bob.accessToken, jobPath, nil, 200)
	assertRead(bob.accessToken, jobPath+"/logs?stream=stdout", &logs, 200)
	if logs.State != "complete" || logs.BytesBase64 == "" {
		t.Fatal("Restored direct grant did not restore the existing token's authorized NFS read")
	}
	t.Log("PASS: same Bob token loses namespace, job, log, metadata and saved browse-cursor access after its last direct group is removed; original grants restore access without another login")
}
