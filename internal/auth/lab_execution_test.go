//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman/protocol"
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

type labExecutionOutput struct{ buffer bytes.Buffer }

func (w *labExecutionOutput) Bytes() []byte { return w.buffer.Bytes() }
func (w *labExecutionOutput) Len() int      { return w.buffer.Len() }

func labExecutionID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && regexp.MustCompile(`^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(value)
}

func (w *labExecutionOutput) Write(value []byte) (int, error) {
	if w.Len()+len(value) > 32768 {
		return 0, io.ErrShortBuffer
	}
	return w.buffer.Write(value)
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

const labActualFailureLog = "ACTUAL SUBPROCESS: open synthetic-output.txt: permission denied; metadata and byte delivery\n"

type labActualReceipt struct {
	Synthetic          bool   `json:"synthetic"`
	Mode               string `json:"mode"`
	DeploymentID       string `json:"deploymentId"`
	ControlInstanceID  string `json:"controlInstanceId"`
	RecoveryEpoch      string `json:"recoveryEpoch"`
	NamespaceID        string `json:"namespaceId"`
	Namespace          string `json:"namespace"`
	TargetID           string `json:"targetId"`
	TargetGenerationID string `json:"targetGenerationId"`
	TargetName         string `json:"targetName"`
	AgentID            string `json:"agentId"`
	CoreRevision       string `json:"coreRevision"`
}

type labActualJob struct {
	Metadata struct{ ID, Name, NamespaceID string } `json:"metadata"`
	Spec     struct {
		Placement struct{ TargetID, TargetGenerationID, ExecutionBackend string } `json:"placement"`
	} `json:"spec"`
	Status struct {
		Imported       bool `json:"imported"`
		Phase, Outcome string
		CurrentRun     *api.RunReference `json:"currentRun"`
		Lifecycle      struct {
			StartedAt, CompletedAt                 *time.Time
			StartedProvenance, CompletedProvenance string
		} `json:"lifecycle"`
	} `json:"status"`
}

type labActualGroup struct {
	Metadata struct{ ID string } `json:"metadata"`
	Status   struct {
		Phase, Outcome, ArrayMode                   string
		Total, Terminal, Succeeded, Failed, Skipped int
	} `json:"status"`
	Items []struct {
		Name, Disposition string
		Index             int
		Job               labActualJob
	} `json:"items"`
}

type labActualPlan struct {
	Singles    []protocol.JobRequest
	Collection protocol.CollectionRequest
	Graph      protocol.GraphRequest
}

// Stable sealed requests make an interrupted run resumable without creating a
// new target, workload set, or executable side effect under a different key.
func labActualRequests(t *testing.T) labActualPlan {
	t.Helper()
	workload := func(name, executable string, args []string, timeout string) protocol.WorkloadBinding {
		sealed, err := protocol.SealWorkload(protocol.Workload{APIVersion: protocol.V1Alpha1, Kind: protocol.WorkloadKind,
			Metadata: protocol.WorkloadMetadata{Name: "dashboard-actual-" + name}, Spec: protocol.WorkloadSpec{
				Command: protocol.Command{Executable: executable, Args: args}, WorkingDirectory: "workspace:/", Runtime: protocol.Runtime{Kind: "native"},
				Policy: protocol.ExecutionPolicy{RunTimeout: timeout, Retry: protocol.RetryPolicy{MaxRuns: 1}, DuplicateRisk: "reject"}}})
		if err != nil {
			t.Fatal("bounded actual workload did not seal")
		}
		return protocol.WorkloadBinding{Digest: sealed.Digest, Document: sealed.Document}
	}
	placement := protocol.Placement{Target: "dashboard-execution-host"}
	success := workload("success", "/usr/bin/printf", []string{"ACTUAL SUBPROCESS SUCCESS\n"}, "30s")
	failure := workload("failure", "/bin/sh", []string{"-c", "printf '%s\\n' 'ACTUAL SUBPROCESS: open synthetic-output.txt: permission denied; metadata and byte delivery' >&2; exit 7"}, "30s")
	timeout := workload("timeout", "/bin/sleep", []string{"20"}, "2s")
	cancel := workload("cancel", "/bin/sleep", []string{"25"}, "30s")
	result := labActualPlan{}
	for i, binding := range []protocol.WorkloadBinding{success, failure, timeout, cancel} {
		name := []string{"success", "failure", "timeout", "cancel"}[i]
		sealed, err := protocol.SealJobRequest(protocol.JobRequest{APIVersion: protocol.V1Alpha1, Kind: protocol.JobRequestKind, Metadata: protocol.JobRequestMetadata{Namespace: "dashboard-operations", Name: "dashboard-actual-" + name + "-v1", Labels: map[string]string{"dashboard-acceptance": "actual-host-v1"}}, Spec: protocol.JobRequestSpec{Workload: binding, Placement: placement}})
		if err != nil {
			t.Fatal("actual job request did not seal")
		}
		result.Singles = append(result.Singles, sealed.Document)
	}
	collection, err := protocol.SealCollectionRequest(protocol.CollectionRequest{APIVersion: protocol.V1Alpha1, Kind: protocol.CollectionRequestKind, Metadata: protocol.CollectionRequestMetadata{Namespace: "dashboard-operations", Name: "dashboard-actual-collection-v1"}, Spec: protocol.CollectionRequestSpec{MaxActive: 2, FailurePolicy: "continue", ArrayPolicy: "never", Items: []protocol.CollectionItem{{Name: "first", Workload: success, Placement: placement}, {Name: "failure", Workload: failure, Placement: placement}, {Name: "last", Workload: success, Placement: placement}}}})
	if err != nil {
		t.Fatal("actual collection request did not seal")
	}
	result.Collection = collection.Document
	graph, err := protocol.SealGraphRequest(protocol.GraphRequest{APIVersion: protocol.V1Alpha1, Kind: protocol.GraphRequestKind, Metadata: protocol.GraphRequestMetadata{Namespace: "dashboard-operations", Name: "dashboard-actual-graph-v1"}, Spec: protocol.GraphRequestSpec{MaxActive: 2, UnsatisfiedPolicy: "skip", Nodes: []protocol.GraphNode{{Name: "root-failure", Workload: failure, Placement: placement}, {Name: "on-failure", Workload: success, Placement: placement}, {Name: "after-terminal", Workload: success, Placement: placement}, {Name: "success-only", Workload: success, Placement: placement}}, Edges: []protocol.GraphEdge{{From: "root-failure", To: "on-failure", Predicate: "failure"}, {From: "on-failure", To: "after-terminal", Predicate: "any-terminal"}, {From: "root-failure", To: "success-only", Predicate: "success"}}}})
	if err != nil {
		t.Fatal("actual graph request did not seal")
	}
	result.Graph = graph.Document
	return result
}

func TestLabActualExecutionRequestsBoundedAndStable(t *testing.T) {
	one, two := labActualRequests(t), labActualRequests(t)
	first, _ := json.Marshal(one)
	second, _ := json.Marshal(two)
	if !bytes.Equal(first, second) || len(first) > 32768 || len(one.Singles)+len(one.Collection.Spec.Items)+len(one.Graph.Spec.Nodes) != 11 {
		t.Fatal("actual execution plan changed its stable bounded request set")
	}
	for _, request := range one.Singles {
		w := request.Spec.Workload.Document.Spec
		timeout, err := time.ParseDuration(w.Policy.RunTimeout)
		if err != nil || timeout > 30*time.Second || w.Policy.Retry.MaxRuns != 1 || w.Policy.DuplicateRisk != "reject" || request.Spec.Placement.Target != "dashboard-execution-host" {
			t.Fatal("actual execution plan exceeded target/runtime/retry boundary")
		}
	}
	if one.Collection.Spec.ArrayPolicy != "never" || one.Collection.Spec.MaxActive != 2 || len(one.Graph.Spec.Edges) != 3 || one.Graph.Spec.UnsatisfiedPolicy != "skip" {
		t.Fatal("collection/graph semantics differ")
	}
}

// Opt-in submits exactly eleven bounded ordinary jobs (including wrapper
// children); it never imports observations, writes SQL, or rewrites manifests.
// Source requests use fixed scenario keys; logs/citations are inspected only in
// memory. The final optional receipt contains public IDs, never commands/bytes.
func TestLabActualHostWorkloadsAndDiagnosis(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_RUN") != "1" {
		t.Skip("explicit reviewed actual-workload submission opt-in required")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	var fixture labActualReceipt
	if json.Unmarshal(labExecutionFile(t, filepath.Join(alice.root, ".lab/dashboard/execution-fixture.json"), 32768), &fixture) != nil || !fixture.Synthetic || fixture.Mode != "actual-subprocess-execution" || fixture.DeploymentID != labDeployment || fixture.Namespace != "dashboard-operations" || fixture.TargetName != "dashboard-execution-host" || fixture.CoreRevision != "21701b191cd4e4e063d26cad7dae31c35db6bc8c" {
		t.Fatal("exact actual executor receipt is unavailable")
	}
	for _, id := range []string{fixture.ControlInstanceID, fixture.NamespaceID, fixture.TargetID, fixture.TargetGenerationID, fixture.AgentID} {
		if !labExecutionID(id) {
			t.Fatal("actual executor identity invalid")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	control := labExecutionControl(t, ctx, alice)
	dashboard := labReportClient(t, ctx, alice)
	var caps struct {
		Capabilities struct{ InstanceID, RecoveryEpoch string }
	}
	verifySource := func() {
		control("GET", "/v1/capabilities", "", nil, &caps)
		if caps.Capabilities.InstanceID != fixture.ControlInstanceID || caps.Capabilities.RecoveryEpoch != fixture.RecoveryEpoch {
			t.Fatal("actual source identity/epoch changed; stop without submitting another set")
		}
	}
	verifySource()
	plan := labActualRequests(t)
	prefix := "/v1/namespaces/dashboard-operations"
	jobs := map[string]labActualJob{}
	for i, body := range plan.Singles {
		name := []string{"success", "failure", "timeout", "cancel"}[i]
		var job labActualJob
		control("POST", prefix+"/jobs", "dashboard-actual-host-v1-"+name, body, &job)
		if !labExecutionID(job.Metadata.ID) {
			t.Fatal("actual job admission lacks immutable ID")
		}
		if name == "cancel" && job.Status.Phase != "terminal" {
			labActualPoll(t, ctx, 45*time.Second, func() bool {
				control("GET", prefix+"/jobs/"+job.Metadata.ID, "", nil, &job)
				if job.Status.Phase == "terminal" {
					t.Fatal("cancellation test completed before a running observation")
				}
				return job.Status.Phase == "running" && job.Status.CurrentRun != nil && job.Status.Lifecycle.StartedAt != nil
			})
			control("POST", prefix+"/jobs/"+job.Metadata.ID+"/cancel", "dashboard-actual-host-v1-cancel-action", nil, &job)
		}
		want := []string{"success", "failure", "timed_out", "cancelled"}[i]
		labActualPoll(t, ctx, 60*time.Second, func() bool {
			control("GET", prefix+"/jobs/"+job.Metadata.ID, "", nil, &job)
			return job.Status.Phase == "terminal"
		})
		labActualAssertJob(t, fixture, job, want, true)
		jobs[name] = job
	}
	var collection, graph labActualGroup
	control("POST", prefix+"/collections", "dashboard-actual-host-v1-collection", plan.Collection, &collection)
	if !labExecutionID(collection.Metadata.ID) {
		t.Fatal("collection admission invalid")
	}
	labActualPoll(t, ctx, 90*time.Second, func() bool {
		control("GET", prefix+"/collections/"+collection.Metadata.ID, "", nil, &collection)
		return collection.Status.Phase == "terminal"
	})
	if len(collection.Items) != 3 || collection.Status.Total != 3 || collection.Status.Terminal != 3 || collection.Status.Succeeded != 2 || collection.Status.Failed != 1 || collection.Status.ArrayMode != "individual" {
		t.Fatal("actual portable collection terminal counts/mode differ")
	}
	for _, item := range collection.Items {
		want := "success"
		if item.Name == "failure" {
			want = "failure"
		}
		labActualAssertJob(t, fixture, item.Job, want, true)
		jobs["collection-"+item.Name] = item.Job
	}
	control("POST", prefix+"/graphs", "dashboard-actual-host-v1-graph", plan.Graph, &graph)
	if !labExecutionID(graph.Metadata.ID) {
		t.Fatal("graph admission invalid")
	}
	labActualPoll(t, ctx, 90*time.Second, func() bool {
		control("GET", prefix+"/graphs/"+graph.Metadata.ID, "", nil, &graph)
		return graph.Status.Phase == "terminal"
	})
	if len(graph.Items) != 4 || graph.Status.Total != 4 || graph.Status.Terminal != 4 || graph.Status.Succeeded != 2 || graph.Status.Failed != 1 || graph.Status.Skipped != 1 {
		t.Fatal("actual dependency graph counts differ")
	}
	for _, item := range graph.Items {
		if item.Name == "success-only" {
			if item.Disposition != "skipped" || item.Job.Status.CurrentRun != nil || item.Job.Status.Lifecycle.StartedAt != nil {
				t.Fatal("unsatisfied branch fabricated execution")
			}
		} else {
			want := "success"
			if item.Name == "root-failure" {
				want = "failure"
			}
			labActualAssertJob(t, fixture, item.Job, want, true)
		}
		jobs["graph-"+item.Name] = item.Job
	}
	apiPrefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + fixture.NamespaceID
	for name, source := range jobs {
		var detail api.JobDetail
		path := apiPrefix + "/jobs/" + source.Metadata.ID
		dashboard("GET", path, alice.accessToken, "", nil, &detail, 200)
		job := detail.Job
		if job.ID != source.Metadata.ID || job.Imported || job.Outcome != source.Status.Outcome || job.TargetID != fixture.TargetID || job.TargetGenerationID != fixture.TargetGenerationID || job.Owner == nil || !job.Owner.IsCurrentUser || job.Scope != (api.Scope{DeploymentID: labDeployment, NamespaceID: fixture.NamespaceID}) {
			t.Fatal("Dashboard actual job source/owner/lifecycle projection differs")
		}
		if name != "graph-success-only" && (job.CurrentRun == nil || *job.CurrentRun != *source.Status.CurrentRun || job.StartedAt == nil || job.CompletedAt == nil) {
			t.Fatal("Dashboard actual run/lifecycle provenance missing")
		}
		dashboard("GET", path, bob.accessToken, "", nil, nil, 403)
	}
	labActualCheckGroup(t, dashboard, alice.accessToken, apiPrefix, "collection", collection)
	labActualCheckGroup(t, dashboard, alice.accessToken, apiPrefix, "graph", graph)
	failure := jobs["failure"]
	path := apiPrefix + "/jobs/" + failure.Metadata.ID
	// Empty stdout and non-empty stderr both traverse the deployed mTLS broker
	// and producer-published NFS chunks. Exact bytes prove real command output.
	for _, stream := range []string{"stdout", "stderr"} {
		want := ""
		if stream == "stderr" {
			want = labActualFailureLog
		}
		labActualCheckLog(t, control, dashboard, alice.accessToken, bob.accessToken, fixture, failure, path, stream, want)
	}
	var successLog api.LogRange
	dashboard("GET", apiPrefix+"/jobs/"+jobs["success"].Metadata.ID+"/logs?stream=stdout", alice.accessToken, "", nil, &successLog, 200)
	if decoded, err := base64.StdEncoding.DecodeString(successLog.BytesBase64); err != nil || string(decoded) != "ACTUAL SUBPROCESS SUCCESS\n" || successLog.ExecutionID != jobs["success"].Status.CurrentRun.ExecutionID {
		t.Fatal("actual success marker missing from broker stream")
	}
	reports := labActualReports(t, ctx, dashboard, alice.accessToken, bob.accessToken, fixture, failure, path)
	// Analysis must not mutate source manifests or original unredacted bytes.
	for _, stream := range []string{"stdout", "stderr"} {
		want := ""
		if stream == "stderr" {
			want = labActualFailureLog
		}
		labActualCheckLog(t, control, dashboard, alice.accessToken, bob.accessToken, fixture, failure, path, stream, want)
	}
	verifySource()
	if destination := os.Getenv("JOBMAN_DASHBOARD_LAB_EXECUTION_RESULT"); destination != "" {
		if !filepath.IsAbs(destination) {
			t.Fatal("result receipt path must be absolute")
		}
		ids := map[string]string{}
		for name, job := range jobs {
			ids[name] = job.Metadata.ID
		}
		encoded, err := json.MarshalIndent(map[string]any{"synthetic": true, "observationMode": "actual-subprocess-agent-execution", "fixture": fixture, "jobs": ids, "collectionId": collection.Metadata.ID, "graphId": graph.Metadata.ID, "reports": reports, "verifiedAt": time.Now().UTC()}, "", "  ")
		if err != nil || len(encoded) > 32768 {
			t.Fatal("public execution receipt exceeds bound")
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal("public execution receipt path must be new")
		}
		_, writeErr := file.Write(append(encoded, '\n'))
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			t.Fatal("public execution receipt write failed")
		}
	}
	t.Log("PASS: actual isolated subprocess success/failure/timeout/cancellation, portable collection, dependency graph with unexecuted skipped branch, source-qualified Dashboard monitoring, NFS log bytes/checksums, deterministic sealed metadata/redacted-log reports and Bob denial. No imported observations, APNs or Slurm acceptance claimed.")
}

func labActualPoll(t *testing.T, ctx context.Context, maximum time.Duration, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual execution overall deadline exceeded")
		case <-timer.C:
			t.Fatal("actual execution did not reach expected state within bounded wait")
		case <-ticker.C:
		}
	}
}

func labActualAssertJob(t *testing.T, f labActualReceipt, j labActualJob, outcome string, executed bool) {
	t.Helper()
	if !labExecutionID(j.Metadata.ID) || j.Metadata.NamespaceID != f.NamespaceID || j.Spec.Placement.TargetID != f.TargetID || j.Spec.Placement.TargetGenerationID != f.TargetGenerationID || j.Spec.Placement.ExecutionBackend != "subprocess" || j.Status.Imported || j.Status.Phase != "terminal" || j.Status.Outcome != outcome {
		t.Fatalf("actual job placement/lifecycle differs (phase %q, outcome %q; expected %q)", j.Status.Phase, j.Status.Outcome, outcome)
	}
	if executed && (j.Status.CurrentRun == nil || !labExecutionID(j.Status.CurrentRun.ID) || !labExecutionID(j.Status.CurrentRun.ExecutionID) || j.Status.CurrentRun.Number != "1" || j.Status.Lifecycle.StartedAt == nil || j.Status.Lifecycle.CompletedAt == nil || j.Status.Lifecycle.StartedProvenance != "process.started" || j.Status.Lifecycle.CompletedProvenance != "process.completed") {
		t.Fatal("terminal job lacks actual process run/observation provenance")
	}
}

func labActualCheckGroup(t *testing.T, request func(string, string, string, string, any, any, int) []byte, token, prefix, kind string, source labActualGroup) {
	t.Helper()
	path := prefix + "/workloads/" + kind + "/" + source.Metadata.ID
	var detail api.WorkloadDetail
	request("GET", path, token, "", nil, &detail, 200)
	if detail.Workload.ID != source.Metadata.ID || detail.Workload.TotalChildren != strconv.Itoa(len(source.Items)) || detail.Workload.Phase != "terminal" || detail.Workload.Counts["terminal"] != strconv.Itoa(len(source.Items)) || detail.Workload.AsOf.IsZero() || detail.Completeness != "complete" {
		t.Fatal("actual workload projection counts/freshness differ")
	}
	if kind == "collection" && (detail.Workload.ArrayPolicy != "never" || detail.Workload.ArrayMode != "individual" || detail.Workload.Concurrency != "2") {
		t.Fatal("actual portable policy/mode not preserved")
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		var children api.WorkloadChildrenPage
		query := "?limit=1"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		request("GET", path+"/children"+query, token, "", nil, &children, 200)
		if children.Total != strconv.Itoa(len(source.Items)) || len(children.Items) != 1 {
			t.Fatal("actual child paging lost total/bound")
		}
		item := children.Items[0]
		if page >= len(source.Items) || seen[item.Job.ID] || item.Job.ID != source.Items[page].Job.Metadata.ID || item.Index != strconv.Itoa(source.Items[page].Index) {
			t.Fatal("actual child cursor repeated/lost/source-mismatched rows")
		}
		seen[item.Job.ID] = true
		cursor = children.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != len(source.Items) {
		t.Fatal("actual child traversal did not terminate completely")
	}
	if kind != "graph" {
		return
	}
	edges := map[string]bool{}
	cursor = ""
	for page := 0; page < 4; page++ {
		var result api.GraphEdgePage
		query := "?limit=1"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		request("GET", path+"/dependencies"+query, token, "", nil, &result, 200)
		if len(result.Items) != 1 || result.Total != "3" {
			t.Fatal("actual graph edge pagination bound differs")
		}
		edge := result.Items[0]
		key := edge.From + "/" + edge.To
		if edges[key] || !seen[edge.FromJobID] || !seen[edge.ToJobID] {
			t.Fatal("actual graph edge identity differs")
		}
		edges[key] = true
		if edge.To == "success-only" {
			if edge.State != "unsatisfied" {
				t.Fatal("unsatisfied graph edge projected as ready")
			}
		} else if edge.State != "satisfied" {
			t.Fatal("actual satisfied graph edge is missing")
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(edges) != 3 {
		t.Fatal("graph edge continuation lost rows")
	}
	center := ""
	for _, item := range source.Items {
		if item.Name == "root-failure" {
			center = item.Job.Metadata.ID
		}
	}
	if center == "" {
		t.Fatal("graph center identity missing")
	}
	var neighborhood api.GraphNeighborhood
	request("GET", path+"/neighborhood?nodeId="+url.QueryEscape(center)+"&maxNodes=2&maxEdges=1", token, "", nil, &neighborhood, 200)
	if neighborhood.CenterID != center || len(neighborhood.Nodes) != 2 || len(neighborhood.Edges) != 1 || neighborhood.TotalNodes != "3" || neighborhood.TotalEdges != "2" || neighborhood.OmittedNodes != "1" || neighborhood.OmittedEdges != "1" {
		t.Fatal("bounded graph neighborhood omitted-count disclosure differs")
	}
}

func labActualCheckLog(t *testing.T, control func(string, string, string, any, any), request func(string, string, string, string, any, any, int) []byte, alice, bob string, f labActualReceipt, job labActualJob, path, stream, want string) {
	t.Helper()
	var manifest struct {
		NamespaceID, JobID, TargetGenerationID, RunID, RunNumber, ExecutionID, State, ByteLength string
		Chunks                                                                                   []struct{ ByteOffset, ByteLength, Checksum, StoreName, StoreVersion string }
	}
	control("GET", "/v1/namespaces/dashboard-operations/jobs/"+job.Metadata.ID+"/log-chunks?stream="+stream+"&limit=100", "", nil, &manifest)
	if manifest.NamespaceID != f.NamespaceID || manifest.JobID != job.Metadata.ID || manifest.TargetGenerationID != f.TargetGenerationID || manifest.RunID != job.Status.CurrentRun.ID || manifest.ExecutionID != job.Status.CurrentRun.ExecutionID || manifest.State != "complete" || manifest.ByteLength != strconv.Itoa(len(want)) || len(manifest.Chunks) < 1 {
		t.Fatal("actual producer manifest source/run/completion differs")
	}
	for _, chunk := range manifest.Chunks {
		start, e1 := strconv.Atoi(chunk.ByteOffset)
		count, e2 := strconv.Atoi(chunk.ByteLength)
		if e1 != nil || e2 != nil || start < 0 || count < 0 || start+count > len(want) || chunk.StoreName != "lab-nfs" || chunk.StoreVersion != "1" {
			t.Fatal("actual chunk manifest bounds/store differ")
		}
		digest := sha256.Sum256([]byte(want[start : start+count]))
		if chunk.Checksum != "sha256:"+hex.EncodeToString(digest[:]) {
			t.Fatal("actual producer chunk checksum differs from known command bytes")
		}
	}
	var logs api.LogRange
	request("GET", path+"/logs?stream="+stream, alice, "", nil, &logs, 200)
	decoded, err := base64.StdEncoding.DecodeString(logs.BytesBase64)
	if err != nil || string(decoded) != want || logs.StartOffset != "0" || logs.EndOffset != strconv.Itoa(len(want)) || logs.RunID != job.Status.CurrentRun.ID || logs.ExecutionID != job.Status.CurrentRun.ExecutionID || logs.State != "complete" || logs.Truncated {
		t.Fatal("actual NFS/broker log bytes or identity differ")
	}
	request("GET", path+"/logs?stream="+stream, bob, "", nil, nil, 403)
}

func labActualReports(t *testing.T, ctx context.Context, request func(string, string, string, string, any, any, int) []byte, alice, bob string, f labActualReceipt, job labActualJob, path string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, profile := range []string{"metadata", "include_log_tail"} {
		body := api.ReportRequest{Profile: profile, RunID: job.Status.CurrentRun.ID}
		key := "actual-host-v1-" + job.Metadata.ID + "-" + profile
		var admitted api.Report
		request("POST", path+"/reports", alice, key, body, &admitted, 202)
		if !labExecutionID(admitted.TaskID) || admitted.Detail != nil {
			t.Fatal("actual report admission differs")
		}
		var replay api.Report
		request("POST", path+"/reports", alice, key, body, &replay, 202)
		if replay.TaskID != admitted.TaskID {
			t.Fatal("actual report retry duplicated task")
		}
		var ready api.Report
		taskPath := path + "/reports/" + admitted.TaskID
		labActualPoll(t, ctx, 90*time.Second, func() bool {
			raw := request("GET", taskPath, alice, "", nil, &ready, 200)
			if bytes.Contains(raw, []byte(labDiagnosticCanary)) {
				t.Fatal("actual report leaked redaction canary")
			}
			if ready.State == "failed" {
				t.Fatalf("actual report failed with safe code %q", ready.FailureCode)
			}
			return ready.State == "ready"
		})
		d := ready.Detail
		if d == nil || ready.JobID != job.Metadata.ID || ready.RunID != job.Status.CurrentRun.ID || ready.Outdated || ready.ReportID == "" || ready.EvidenceID == "" || ready.AnalysisEvidenceID == "" || d.ControlInstanceID != f.ControlInstanceID || d.Disclosure.ProviderInvoked || d.Disclosure.GeneratedContentUsed || len(d.Generators) != 0 || len(d.Runs) != 1 || d.Runs[0].ID != job.Status.CurrentRun.ID || d.Runs[0].ExecutionID != job.Status.CurrentRun.ExecutionID {
			t.Fatal("actual deterministic report lacks sealed original identities/disclosure")
		}
		masked := strings.ReplaceAll(labActualFailureLog, labDiagnosticCanary, strings.Repeat("*", len(labDiagnosticCanary)))
		logCitations, items := 0, 0
		for _, ref := range d.Citations {
			var citation api.Citation
			raw := request("GET", taskPath+"/citations/"+url.PathEscape(ref.ID), alice, "", nil, &citation, 200)
			if bytes.Contains(raw, []byte(labDiagnosticCanary)) || citation.ReportID != ready.ReportID || citation.EvidenceID != ready.EvidenceID || citation.AnalysisEvidenceID != ready.AnalysisEvidenceID || citation.ID != ref.ID {
				t.Fatal("actual sealed citation identity/redaction differs")
			}
			if citation.BytesBase64 == nil {
				if !json.Valid([]byte(citation.ValueJSON)) {
					t.Fatal("actual evidence item invalid")
				}
				items++
			} else {
				data, e1 := base64.StdEncoding.DecodeString(*citation.BytesBase64)
				start, e2 := strconv.Atoi(citation.StartOffset)
				end, e3 := strconv.Atoi(citation.EndOffset)
				if profile != "include_log_tail" || e1 != nil || e2 != nil || e3 != nil || start < 0 || end < start || end > len(masked) || string(data) != masked[start:end] || citation.Stream != "stderr" || citation.RunID != job.Status.CurrentRun.ID || citation.ExecutionID != job.Status.CurrentRun.ExecutionID || !citation.OriginalOffsetsExact || citation.OriginalStartOffset != citation.StartOffset || citation.OriginalEndOffset != citation.EndOffset {
					t.Fatal("actual redacted citation bytes/offsets differ")
				}
				logCitations++
			}
		}
		if profile == "metadata" && items == 0 {
			t.Fatal("actual metadata diagnosis lacks item citation")
		}
		if profile == "include_log_tail" {
			recognized := false
			for _, finding := range d.Findings {
				recognized = recognized || finding.Code == "target.permission_message"
			}
			if !recognized || logCitations == 0 || len(d.RedactionNotices) == 0 {
				t.Fatal("actual permission message did not produce redacted log diagnosis")
			}
		}
		request("GET", taskPath, bob, "", nil, nil, 404)
		result[profile] = ready.TaskID
	}
	return result
}
