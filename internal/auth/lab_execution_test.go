//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
)

// Preparation is a separate explicit operation: ordinary authenticated Control
// APIs create a target and issue one enrollment token; the scoped Lab script
// receives only that single-use token over stdin. No Store observation/import,
// direct database write, original enrollment, or original service is involved.
func TestLabPrepareActualExecutionHost(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_PREPARE") != "1" {
		t.Skip("explicit reviewed actual-executor preparation is required")
	}
	planPath := os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_PLAN")
	build := os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_BUILD")
	expected := os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_PLAN_SHA256")
	if !filepath.IsAbs(planPath) || !filepath.IsAbs(build) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(expected) {
		t.Fatal("exact reviewed plan, digest and clean build directory required")
	}
	raw := labExecutionFile(t, planPath, 32768)
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expected {
		t.Fatal("reviewed executor plan changed")
	}
	var plan struct {
		Synthetic             bool            `json:"synthetic"`
		Mode                  string          `json:"mode"`
		DeploymentID          string          `json:"deploymentId"`
		ControlInstanceID     string          `json:"controlInstanceId"`
		NamespaceID           string          `json:"namespaceId"`
		Namespace             string          `json:"namespace"`
		CoreRevision          string          `json:"coreRevision"`
		TargetName            string          `json:"targetName"`
		TargetRequest         json.RawMessage `json:"targetRequest"`
		TargetIdempotencyKey  string          `json:"targetIdempotencyKey"`
		EnrollmentIdempotency string          `json:"enrollmentIdempotencyKey"`
	}
	if json.Unmarshal(raw, &plan) != nil || !plan.Synthetic || plan.Mode != "actual-subprocess-execution" || plan.CoreRevision != "21701b191cd4e4e063d26cad7dae31c35db6bc8c" || plan.DeploymentID != "72000000-0000-4000-8000-000000000001" || plan.Namespace != "dashboard-operations" || plan.TargetName != "dashboard-execution-host" || !labExecutionID(plan.ControlInstanceID) || !labExecutionID(plan.NamespaceID) {
		t.Fatal("plan is outside the approved isolated executor scope")
	}
	session := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	call := labExecutionControl(t, ctx, session)
	var capabilities struct {
		Capabilities struct {
			InstanceID    string `json:"instanceId"`
			RecoveryEpoch string `json:"recoveryEpoch"`
		} `json:"capabilities"`
	}
	call("GET", "/v1/capabilities", "", nil, &capabilities)
	if capabilities.Capabilities.InstanceID != plan.ControlInstanceID || !regexp.MustCompile(`^[1-9][0-9]{0,18}$`).MatchString(capabilities.Capabilities.RecoveryEpoch) {
		t.Fatal("current source identity differs from reviewed plan")
	}
	var identity struct {
		Principal struct {
			Issuer      string `json:"issuer"`
			Subject     string `json:"subject"`
			DirectoryID string `json:"directoryId"`
		} `json:"principal"`
	}
	call("GET", "/v1/me", "", nil, &identity)
	if identity.Principal.Issuer != "https://oidc.lab.test:8443/realms/jobman-lab" || identity.Principal.Subject == "" || identity.Principal.DirectoryID != "71000000-0000-4000-8000-000000000001" {
		t.Fatal("source did not verify the expected synthetic Alice identity")
	}
	labExecutionPrepareCommand(t, ctx, session.root, "provision", planPath, expected, build, nil)
	var target struct {
		Metadata struct {
			ID           string `json:"id"`
			GenerationID string `json:"generationId"`
			Namespace    string `json:"namespace"`
			Name         string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Backend string `json:"executionBackend"`
		} `json:"spec"`
	}
	call("POST", "/v1/namespaces/dashboard-operations/targets", plan.TargetIdempotencyKey, plan.TargetRequest, &target)
	if !labExecutionID(target.Metadata.ID) || !labExecutionID(target.Metadata.GenerationID) || target.Metadata.Namespace != plan.Namespace || target.Metadata.Name != plan.TargetName || target.Spec.Backend != "subprocess" {
		t.Fatal("new target response escaped exact approved placement")
	}
	enrollmentBody := map[string]any{"apiVersion": "jobman.control/v1alpha1", "kind": "AgentEnrollmentToken", "spec": map[string]any{"principal": map[string]string{"issuer": identity.Principal.Issuer, "subject": identity.Principal.Subject}, "expectedUser": "alice"}}
	var enrollment struct {
		Spec struct {
			Target             string `json:"target"`
			TargetGenerationID string `json:"targetGenerationId"`
			ExpectedUser       string `json:"expectedUser"`
			Token              string `json:"token"`
		} `json:"spec"`
	}
	call("POST", "/v1/namespaces/dashboard-operations/targets/dashboard-execution-host/enrollment-tokens", plan.EnrollmentIdempotency, enrollmentBody, &enrollment)
	if enrollment.Spec.Target != plan.TargetName || enrollment.Spec.TargetGenerationID != target.Metadata.GenerationID || enrollment.Spec.ExpectedUser != "alice" || len(enrollment.Spec.Token) > 16384 {
		t.Fatal("enrollment response escaped the selected target/user")
	}
	input := map[string]string{"targetId": target.Metadata.ID, "targetGenerationId": target.Metadata.GenerationID, "recoveryEpoch": capabilities.Capabilities.RecoveryEpoch, "token": enrollment.Spec.Token}
	output := labExecutionPrepareCommand(t, ctx, session.root, "enroll", planPath, expected, build, input)
	var receipt struct {
		AgentID            string `json:"agentId"`
		TargetID           string `json:"targetId"`
		TargetGenerationID string `json:"targetGenerationId"`
		ControlInstanceID  string `json:"controlInstanceId"`
	}
	if json.Unmarshal(output, &receipt) != nil || !labExecutionID(receipt.AgentID) || receipt.TargetID != target.Metadata.ID || receipt.TargetGenerationID != target.Metadata.GenerationID || receipt.ControlInstanceID != plan.ControlInstanceID {
		t.Fatal("source-qualified enrolled-agent receipt is invalid")
	}
	initial := capabilities.Capabilities
	call("GET", "/v1/capabilities", "", nil, &capabilities)
	if capabilities.Capabilities != initial {
		t.Fatal("source changed during enrollment; retain receipt and revalidate before submitting")
	}
	t.Log("PASS: exact clean Core agent enrolled using normal Control APIs; only isolated executor started. No workloads or Dashboard/broker configuration changed.")
}

func labExecutionFile(t *testing.T, path string, maximum int64) []byte {
	t.Helper()
	data, err := config.ReadPublicFile(path, maximum)
	if err != nil || len(data) == 0 {
		t.Fatal("bounded regular execution acceptance input unreadable")
	}
	return data
}

func labExecutionControl(t *testing.T, ctx context.Context, session labNativeSession) func(string, string, string, any, any) {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(labExecutionFile(t, filepath.Join(session.root, ".lab/dashboard/runtime/control/fixture-ca.crt"), 65536)) {
		t.Fatal("isolated source public trust unavailable")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32768}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "10.77.0.21:18443" {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(method, path, key string, input, output any) {
		t.Helper()
		var body []byte
		var err error
		if input != nil {
			body, err = json.Marshal(input)
			if err != nil || len(body) > 256<<10 {
				t.Fatal("bounded execution request encoding failed")
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, "https://10.77.0.21:18443"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("execution request is invalid")
		}
		request.Header.Set("Authorization", "Bearer "+session.accessToken)
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("isolated execution source request failed")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		response.Body.Close()
		if readErr != nil || len(data) > 2<<20 || response.StatusCode != 200 && response.StatusCode != 201 || json.Unmarshal(data, output) != nil {
			t.Fatalf("isolated execution source returned invalid response (HTTP %d); no response body disclosed", response.StatusCode)
		}
	}
}

type labExecutionOutput struct{ bytes.Buffer }

func labExecutionID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && regexp.MustCompile(`^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(value)
}

func (w *labExecutionOutput) Write(value []byte) (int, error) {
	if w.Len()+len(value) > 32768 {
		return 0, io.ErrShortBuffer
	}
	return w.Buffer.Write(value)
}

func labExecutionPrepareCommand(t *testing.T, ctx context.Context, root, action, plan, digest, build string, input any) []byte {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python runtime unavailable for scoped Lab preparation")
	}
	command := exec.CommandContext(ctx, python, filepath.Join(root, "scripts/prepare-dashboard-execution.py"), action, "--apply", "--plan", plan, "--expected-plan-sha256", digest, "--build", build)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE=1"}
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil || len(encoded) > 32768 {
			t.Fatal("bounded enrollment input invalid")
		}
		command.Stdin = bytes.NewReader(encoded)
	}
	var output labExecutionOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if command.Run() != nil {
		t.Fatal("scoped executor preparation failed; preserve pending receipts and inspect privately before retry")
	}
	return output.Bytes()
}
