//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
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
	"github.com/ryancswallace/jobman/protocol"
)

// Preparation is separate from scheduler submission. It creates only the new
// Alice target/agent and uses identical clean local runners/private bundle/log roots
// in the reviewed plan. No original service, source scope or log mapping changes.
func TestLabPrepareActualSlurmExecutor(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_PREPARE") != "1" {
		t.Skip("explicit reviewed Slurm preparation opt-in required")
	}
	path, build, digest := os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_PLAN"), os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_BUILD"), os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_PLAN_SHA256")
	if !filepath.IsAbs(path) || !filepath.IsAbs(build) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		t.Fatal("exact reviewed Slurm plan/build required")
	}
	raw := labExecutionFile(t, path, 32768)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		t.Fatal("reviewed Slurm plan changed")
	}
	var plan struct {
		Synthetic                                                                                                                               bool
		Mode, DeploymentID, ControlInstanceID, NamespaceID, Namespace, CoreRevision, TargetName, TargetIdempotencyKey, EnrollmentIdempotencyKey string
		TargetRequest                                                                                                                           json.RawMessage
		Runner, RunnerLayout, AgentSHA256                                                                                                       string
		RunnerCopies                                                                                                                            []string
	}
	if json.Unmarshal(raw, &plan) != nil || !plan.Synthetic || plan.Mode != "actual-slurm-execution" || plan.DeploymentID != labDeployment || plan.Namespace != "dashboard-operations" || plan.TargetName != "dashboard-execution-slurm" || plan.CoreRevision != "21701b191cd4e4e063d26cad7dae31c35db6bc8c" || !labExecutionID(plan.ControlInstanceID) || !labExecutionID(plan.NamespaceID) {
		t.Fatal("Slurm plan outside approved scope")
	}
	if plan.RunnerLayout != "identical-root-owned-local" || len(plan.RunnerCopies) != 2 || plan.RunnerCopies[0] != "submit01" || plan.RunnerCopies[1] != "compute01" || plan.AgentSHA256 != "e955472e6174a03503548137397328a4570e8e257372ac4409ed75a8130dfa72" || plan.Runner != "/usr/local/libexec/jobman-dashboard-lab/jobman-agent-execution-"+plan.AgentSHA256 {
		t.Fatal("Slurm local runner layout differs")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	call := labExecutionControl(t, ctx, alice)
	var caps struct {
		Capabilities struct{ InstanceID, RecoveryEpoch string }
	}
	call("GET", "/v1/capabilities", "", nil, &caps)
	if caps.Capabilities.InstanceID != plan.ControlInstanceID || !regexp.MustCompile(`^[1-9][0-9]{0,18}$`).MatchString(caps.Capabilities.RecoveryEpoch) {
		t.Fatal("Slurm source identity differs")
	}
	initial := caps.Capabilities
	var identity struct {
		Principal struct{ Issuer, Subject, DirectoryID string }
	}
	call("GET", "/v1/me", "", nil, &identity)
	if identity.Principal.DirectoryID != "71000000-0000-4000-8000-000000000001" || identity.Principal.Issuer != "https://oidc.lab.test:8443/realms/jobman-lab" || identity.Principal.Subject == "" {
		t.Fatal("Slurm source actor identity differs")
	}
	labSlurmPrepareCommand(t, ctx, alice.root, "provision", path, digest, build, nil)
	var target struct {
		Metadata struct{ ID, GenerationID, Namespace, Name string }
		Spec     struct{ ExecutionBackend string }
	}
	call("POST", "/v1/namespaces/dashboard-operations/targets", plan.TargetIdempotencyKey, plan.TargetRequest, &target)
	if !labExecutionID(target.Metadata.ID) || !labExecutionID(target.Metadata.GenerationID) || target.Metadata.Namespace != plan.Namespace || target.Metadata.Name != plan.TargetName || target.Spec.ExecutionBackend != "slurm" {
		t.Fatal("Slurm target admission differs")
	}
	body := map[string]any{"apiVersion": "jobman.control/v1alpha1", "kind": "AgentEnrollmentToken", "spec": map[string]any{"principal": map[string]string{"issuer": identity.Principal.Issuer, "subject": identity.Principal.Subject}, "expectedUser": "alice"}}
	var enrollment struct {
		Spec struct{ Target, TargetGenerationID, ExpectedUser, Token string }
	}
	call("POST", "/v1/namespaces/dashboard-operations/targets/dashboard-execution-slurm/enrollment-tokens", plan.EnrollmentIdempotencyKey, body, &enrollment)
	if enrollment.Spec.Target != plan.TargetName || enrollment.Spec.TargetGenerationID != target.Metadata.GenerationID || enrollment.Spec.ExpectedUser != "alice" || len(enrollment.Spec.Token) > 16384 {
		t.Fatal("Slurm enrollment admission differs")
	}
	input := map[string]string{"targetId": target.Metadata.ID, "targetGenerationId": target.Metadata.GenerationID, "recoveryEpoch": initial.RecoveryEpoch, "token": enrollment.Spec.Token}
	output := labSlurmPrepareCommand(t, ctx, alice.root, "enroll", path, digest, build, input)
	var receipt struct {
		labActualReceipt
		Runner, RunnerLayout, AgentSHA256 string
		RunnerCopies                      []string
	}
	if json.Unmarshal(output, &receipt) != nil || receipt.Mode != "actual-slurm-execution" || receipt.TargetGenerationID != target.Metadata.GenerationID || receipt.TargetID != target.Metadata.ID || receipt.ControlInstanceID != plan.ControlInstanceID || !labExecutionID(receipt.AgentID) {
		t.Fatal("Slurm enrolled-agent receipt differs")
	}
	if receipt.Runner != plan.Runner || receipt.RunnerLayout != plan.RunnerLayout || receipt.AgentSHA256 != plan.AgentSHA256 || len(receipt.RunnerCopies) != 2 || receipt.RunnerCopies[0] != "submit01" || receipt.RunnerCopies[1] != "compute01" {
		t.Fatal("Slurm enrolled-agent runner copies differ")
	}
	call("GET", "/v1/capabilities", "", nil, &caps)
	if caps.Capabilities != initial {
		t.Fatal("source changed during Slurm enrollment; preserve receipt")
	}
	t.Log("PASS: separate Alice Slurm executor enrolled against pinned source using ordinary Control APIs and exact clean local runner copies; no workloads, mappings or original enrollment changes.")
}

func labSlurmPrepareCommand(t *testing.T, ctx context.Context, root, action, plan, digest, build string, input any) []byte {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python runtime unavailable")
	}
	command := exec.CommandContext(ctx, python, filepath.Join(root, "scripts/prepare-dashboard-slurm.py"), action, "--apply", "--plan", plan, "--expected-plan-sha256", digest, "--build", build)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE=1"}
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil || len(raw) > 32768 {
			t.Fatal("bounded Slurm enrollment input invalid")
		}
		command.Stdin = bytes.NewReader(raw)
	}
	var output labExecutionOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if command.Run() != nil {
		t.Fatal("separate Slurm preparation failed; preserve retained state and inspect privately")
	}
	return output.Bytes()
}

const labSlurmFailureLog = "ACTUAL SLURM: open synthetic-output.txt: permission denied; metadata and byte delivery\n"

type labSlurmJob struct {
	labActualJob
	NativeID  string
	Scheduler struct{ Backend, State, Cluster string }
}

func (j *labSlurmJob) UnmarshalJSON(raw []byte) error {
	var base labActualJob
	var extra struct {
		Status struct {
			NativeID  string
			Scheduler struct{ Backend, State, Cluster string }
		}
	}
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &extra); err != nil {
		return err
	}
	j.labActualJob, j.NativeID, j.Scheduler = base, extra.Status.NativeID, extra.Status.Scheduler
	return nil
}

func TestLabSlurmDecodingDoesNotRetainEarlierSchedulerFacts(t *testing.T) {
	var job labSlurmJob
	if json.Unmarshal([]byte(`{"metadata":{"id":"80000000-0000-4000-8000-000000000001"},"status":{"phase":"running","nativeId":"123_4","scheduler":{"backend":"slurm","state":"running"},"lifecycle":{"startedAt":"2026-10-04T01:00:00Z","startedProvenance":"scheduler.observed"}}}`), &job) != nil || job.NativeID != "123_4" || job.Status.Lifecycle.StartedAt == nil {
		t.Fatal("source scheduler identity did not decode")
	}
	if json.Unmarshal([]byte(`{"metadata":{"id":"80000000-0000-4000-8000-000000000002"},"status":{"phase":"terminal","lifecycle":{}}}`), &job) != nil || job.NativeID != "" || job.Scheduler.State != "" || job.Status.Lifecycle.StartedAt != nil {
		t.Fatal("missing source facts retained an earlier task's observations")
	}
}

type labSlurmGroup struct {
	Metadata struct{ ID string }
	Status   struct {
		Phase, Outcome, ArrayMode                              string
		Total, Terminal, Succeeded, Failed, Skipped, Cancelled int
	}
	Items []struct {
		Name, Disposition string
		Index             int
		Job               labSlurmJob
	}
}

type labSlurmPlan struct {
	Array protocol.CollectionRequest
	Graph protocol.GraphRequest
}

func labSlurmRequests(t *testing.T) labSlurmPlan {
	t.Helper()
	workload := func(name, command string) protocol.WorkloadBinding {
		sealed, err := protocol.SealWorkload(protocol.Workload{APIVersion: protocol.V1Alpha1, Kind: protocol.WorkloadKind,
			Metadata: protocol.WorkloadMetadata{Name: "dashboard-slurm-" + name}, Spec: protocol.WorkloadSpec{
				Command: protocol.Command{Executable: "/bin/sh", Args: []string{"-c", command}}, WorkingDirectory: "workspace:/", Runtime: protocol.Runtime{Kind: "native"},
				Resources: &protocol.Resources{CPU: 1, Memory: "128MiB", Nodes: 1, WallTime: "1m"},
				Policy:    protocol.ExecutionPolicy{RunTimeout: "30s", Retry: protocol.RetryPolicy{MaxRuns: 1}, DuplicateRisk: "reject"}}})
		if err != nil {
			t.Fatal("bounded Slurm workload did not seal")
		}
		return protocol.WorkloadBinding{Digest: sealed.Digest, Document: sealed.Document}
	}
	placement := protocol.Placement{Target: "dashboard-execution-slurm", Partition: "cpu"}
	failure := workload("failure", "sleep 3; printf '%s\\n' 'ACTUAL SLURM: open synthetic-output.txt: permission denied; metadata and byte delivery' >&2; exit 7")
	items := []protocol.CollectionItem{}
	for i := 0; i < 5; i++ {
		wait := "3"
		if i == 0 {
			wait = "20"
		}
		binding := workload("task-"+strconv.Itoa(i), "sleep "+wait+"; printf '%s\\n' 'ACTUAL SLURM TASK "+strconv.Itoa(i)+"'")
		if i == 2 {
			binding = failure
		}
		items = append(items, protocol.CollectionItem{Name: "task-" + strconv.Itoa(i), Workload: binding, Placement: placement})
	}
	array, err := protocol.SealCollectionRequest(protocol.CollectionRequest{APIVersion: protocol.V1Alpha1, Kind: protocol.CollectionRequestKind,
		Metadata: protocol.CollectionRequestMetadata{Namespace: "dashboard-operations", Name: "dashboard-slurm-array-v1"},
		Spec:     protocol.CollectionRequestSpec{MaxActive: 1, FailurePolicy: "continue", ArrayPolicy: "require", Items: items}})
	if err != nil {
		t.Fatal("bounded Slurm array did not seal")
	}
	success := workload("graph-success", "sleep 3; printf '%s\\n' 'ACTUAL SLURM GRAPH SUCCESS'")
	graph, err := protocol.SealGraphRequest(protocol.GraphRequest{APIVersion: protocol.V1Alpha1, Kind: protocol.GraphRequestKind,
		Metadata: protocol.GraphRequestMetadata{Namespace: "dashboard-operations", Name: "dashboard-slurm-graph-v1"}, Spec: protocol.GraphRequestSpec{
			MaxActive: 1, UnsatisfiedPolicy: "skip", Nodes: []protocol.GraphNode{{Name: "root-failure", Workload: failure, Placement: placement}, {Name: "on-failure", Workload: success, Placement: placement}, {Name: "after-terminal", Workload: success, Placement: placement}, {Name: "success-only", Workload: success, Placement: placement}},
			Edges: []protocol.GraphEdge{{From: "root-failure", To: "on-failure", Predicate: "failure"}, {From: "on-failure", To: "after-terminal", Predicate: "any-terminal"}, {From: "root-failure", To: "success-only", Predicate: "success"}}}})
	if err != nil {
		t.Fatal("bounded Slurm graph did not seal")
	}
	return labSlurmPlan{Array: array.Document, Graph: graph.Document}
}

func TestLabSlurmRequestsBoundedAndStable(t *testing.T) {
	a, b := labSlurmRequests(t), labSlurmRequests(t)
	one, _ := json.Marshal(a)
	two, _ := json.Marshal(b)
	if !bytes.Equal(one, two) || len(one) > 32768 || len(a.Array.Spec.Items)+len(a.Graph.Spec.Nodes) != 9 || a.Array.Spec.ArrayPolicy != "require" || a.Array.Spec.MaxActive != 1 || len(a.Graph.Spec.Edges) != 3 {
		t.Fatal("Slurm scenario changed bounded/stable semantics")
	}
	bindings := []protocol.WorkloadBinding{}
	for _, item := range a.Array.Spec.Items {
		if item.Placement.Target != "dashboard-execution-slurm" || item.Placement.Partition != "cpu" {
			t.Fatal("array placement differs")
		}
		bindings = append(bindings, item.Workload)
	}
	for _, item := range a.Graph.Spec.Nodes {
		if item.Placement.Target != "dashboard-execution-slurm" || item.Placement.Partition != "cpu" {
			t.Fatal("graph placement differs")
		}
		bindings = append(bindings, item.Workload)
	}
	for _, binding := range bindings {
		w := binding.Document.Spec
		if w.Policy.RunTimeout != "30s" || w.Policy.Retry.MaxRuns != 1 || w.Policy.DuplicateRisk != "reject" || w.Resources.CPU != 1 || w.Resources.Nodes != 1 || w.Resources.Memory != "128MiB" || w.Resources.WallTime != "1m" || w.Command.Executable != "/bin/sh" {
			t.Fatal("Slurm runtime/resource boundary differs")
		}
	}
	if !strings.Contains(a.Array.Spec.Items[0].Workload.Document.Spec.Command.Args[1], "sleep 20;") {
		t.Fatal("first task must retain queued cancellation window")
	}
}

// Five literal array indexes are submitted. Cancelling queued tasks 1 and 3
// leaves an executed sparse subset 0,2,4; this is not arbitrary sparse submission.
// All source mutations are ordinary authenticated API calls with fixed keys.
func TestLabActualSlurmArrayAndDiagnosis(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_RUN") != "1" {
		t.Skip("explicit reviewed Slurm workload opt-in required")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	var fixture labActualReceipt
	if json.Unmarshal(labExecutionFile(t, filepath.Join(alice.root, ".lab/dashboard/slurm-fixture.json"), 32768), &fixture) != nil || !fixture.Synthetic || fixture.Mode != "actual-slurm-execution" || fixture.DeploymentID != labDeployment || fixture.Namespace != "dashboard-operations" || fixture.TargetName != "dashboard-execution-slurm" || fixture.CoreRevision != "21701b191cd4e4e063d26cad7dae31c35db6bc8c" {
		t.Fatal("exact Slurm enrollment receipt required")
	}
	for _, id := range []string{fixture.ControlInstanceID, fixture.NamespaceID, fixture.TargetID, fixture.TargetGenerationID, fixture.AgentID} {
		if !labExecutionID(id) {
			t.Fatal("Slurm source identity invalid")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	control := labExecutionControl(t, ctx, alice)
	dashboard := labReportClient(t, ctx, alice)
	var capabilities struct {
		Capabilities struct{ InstanceID, RecoveryEpoch string }
	}
	verifySource := func() {
		control("GET", "/v1/capabilities", "", nil, &capabilities)
		if capabilities.Capabilities.InstanceID != fixture.ControlInstanceID || capabilities.Capabilities.RecoveryEpoch != fixture.RecoveryEpoch {
			t.Fatal("Slurm source identity/epoch changed; retain original scenario")
		}
	}
	verifySource()
	labSlurmObserve(t, ctx, alice.root, map[string]string{"operation": "preflight"})
	plan := labSlurmRequests(t)
	prefix := "/v1/namespaces/dashboard-operations"
	var array labSlurmGroup
	control("POST", prefix+"/collections", "dashboard-actual-slurm-v1-array", plan.Array, &array)
	if !labExecutionID(array.Metadata.ID) {
		t.Fatal("Slurm array admission invalid")
	}
	arrayPath := prefix + "/collections/" + array.Metadata.ID
	labActualPoll(t, ctx, 60*time.Second, func() bool {
		control("GET", arrayPath, "", nil, &array)
		if len(array.Items) != 5 {
			return false
		}
		if array.Status.Phase == "terminal" {
			return true
		} // resumable inspection of exactly the same jobs
		for _, item := range array.Items {
			if !regexp.MustCompile(`^[1-9][0-9]*_[0-4]$`).MatchString(item.Job.NativeID) {
				return false
			}
		}
		return array.Items[0].Job.Status.Phase == "running" && array.Items[0].Job.Status.Lifecycle.StartedAt != nil
	})
	for _, index := range []int{1, 3} {
		job := array.Items[index].Job
		if job.Status.Lifecycle.StartedAt != nil {
			t.Fatal("queued sparse-subset task executed before cancellation; retain failed scenario")
		}
		if job.Status.Phase != "terminal" {
			control("POST", prefix+"/jobs/"+job.Metadata.ID+"/cancel", "dashboard-actual-slurm-v1-cancel-"+strconv.Itoa(index), nil, &job)
		}
	}
	labActualPoll(t, ctx, 90*time.Second, func() bool { control("GET", arrayPath, "", nil, &array); return array.Status.Phase == "terminal" })
	if len(array.Items) != 5 || array.Status.Total != 5 || array.Status.Terminal != 5 || array.Status.Succeeded != 2 || array.Status.Failed != 1 || array.Status.Cancelled != 2 || array.Status.ArrayMode != "slurm-array" {
		t.Fatal("actual Slurm array terminal counts/mode differ")
	}
	parent := ""
	jobs := map[string]labSlurmJob{}
	for index, item := range array.Items {
		if item.Index != index || item.Name != "task-"+strconv.Itoa(index) {
			t.Fatal("source array task ordering changed")
		}
		parts := strings.Split(item.Job.NativeID, "_")
		if len(parts) != 2 || parts[1] != strconv.Itoa(index) || !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(parts[0]) {
			t.Fatal("source native task ID lost literal array index")
		}
		if parent == "" {
			parent = parts[0]
		}
		if parts[0] != parent {
			t.Fatal("array tasks did not share native scheduler parent")
		}
		want := []string{"success", "cancelled", "failure", "cancelled", "success"}[index]
		labSlurmAssertJob(t, fixture, item.Job, want, index == 0 || index == 2 || index == 4)
		jobs[item.Name] = item.Job
	}
	accounting := labSlurmObserve(t, ctx, alice.root, map[string]string{"operation": "accounting", "collectionId": array.Metadata.ID, "parentId": parent})
	labSlurmCheckAccounting(t, accounting, parent, array.Metadata.ID)
	var graph labSlurmGroup
	control("POST", prefix+"/graphs", "dashboard-actual-slurm-v1-graph", plan.Graph, &graph)
	if !labExecutionID(graph.Metadata.ID) {
		t.Fatal("Slurm graph admission invalid")
	}
	labActualPoll(t, ctx, 90*time.Second, func() bool {
		control("GET", prefix+"/graphs/"+graph.Metadata.ID, "", nil, &graph)
		return graph.Status.Phase == "terminal"
	})
	if len(graph.Items) != 4 || graph.Status.Total != 4 || graph.Status.Terminal != 4 || graph.Status.Succeeded != 2 || graph.Status.Failed != 1 || graph.Status.Skipped != 1 {
		t.Fatal("actual Slurm graph terminal counts differ")
	}
	for _, item := range graph.Items {
		if item.Name == "success-only" {
			if item.Disposition != "skipped" || item.Job.Status.CurrentRun != nil || item.Job.Status.Lifecycle.StartedAt != nil || item.Job.NativeID != "" {
				t.Fatal("unexecuted graph branch fabricated a scheduler run")
			}
		} else {
			want := "success"
			if item.Name == "root-failure" {
				want = "failure"
			}
			labSlurmAssertJob(t, fixture, item.Job, want, true)
		}
		jobs["graph-"+item.Name] = item.Job
	}
	apiPrefix := "/api/v1/deployments/" + labDeployment + "/namespaces/" + fixture.NamespaceID
	for name, source := range jobs {
		var detail api.JobDetail
		path := apiPrefix + "/jobs/" + source.Metadata.ID
		dashboard("GET", path, alice.accessToken, "", nil, &detail, 200)
		job := detail.Job
		if job.ID != source.Metadata.ID || job.Imported || job.Outcome != source.Status.Outcome || job.TargetID != fixture.TargetID || job.TargetGenerationID != fixture.TargetGenerationID || job.Backend != "slurm" || job.Owner == nil || !job.Owner.IsCurrentUser || job.Scope != (api.Scope{DeploymentID: labDeployment, NamespaceID: fixture.NamespaceID}) {
			t.Fatal("Dashboard Slurm source/owner/outcome differs")
		}
		if name != "graph-success-only" && (job.CurrentRun == nil || *job.CurrentRun != *source.Status.CurrentRun || job.Scheduler == nil || job.Scheduler.JobID != source.NativeID || job.CompletedAt == nil) {
			t.Fatal("Dashboard Slurm scheduler/run identity differs")
		}
		if (job.StartedAt == nil) != (source.Status.Lifecycle.StartedAt == nil) {
			t.Fatal("Dashboard inferred a Slurm execution start")
		}
		dashboard("GET", path, bob.accessToken, "", nil, nil, 403)
	}
	labSlurmCheckArray(t, dashboard, alice.accessToken, apiPrefix, array, parent)
	// The common graph checks use only wrapper/job facts, not backend assumptions.
	var hostShape labActualGroup
	// Preserve ordinary source job facts for the existing bounded graph checks.
	hostShape.Metadata.ID = graph.Metadata.ID
	hostShape.Status.Phase = graph.Status.Phase
	for _, item := range graph.Items {
		hostShape.Items = append(hostShape.Items, struct {
			Name, Disposition string
			Index             int
			Job               labActualJob
		}{item.Name, item.Disposition, item.Index, item.Job.labActualJob})
	}
	labActualCheckGroup(t, dashboard, alice.accessToken, apiPrefix, "graph", hostShape)
	failure := jobs["task-2"].labActualJob
	path := apiPrefix + "/jobs/" + failure.Metadata.ID
	for _, stream := range []string{"stdout", "stderr"} {
		want := ""
		if stream == "stderr" {
			want = labSlurmFailureLog
		}
		labActualCheckLog(t, control, dashboard, alice.accessToken, bob.accessToken, fixture, failure, path, stream, want)
	}
	for _, index := range []int{0, 4} {
		var result api.LogRange
		j := jobs["task-"+strconv.Itoa(index)]
		dashboard("GET", apiPrefix+"/jobs/"+j.Metadata.ID+"/logs?stream=stdout", alice.accessToken, "", nil, &result, 200)
		data, err := base64.StdEncoding.DecodeString(result.BytesBase64)
		if err != nil || string(data) != "ACTUAL SLURM TASK "+strconv.Itoa(index)+"\n" || result.ExecutionID != j.Status.CurrentRun.ExecutionID {
			t.Fatal("executed sparse task log marker/identity differs")
		}
	}
	reports := labSlurmReports(t, ctx, dashboard, alice.accessToken, bob.accessToken, fixture, failure, path)
	for _, stream := range []string{"stdout", "stderr"} {
		want := ""
		if stream == "stderr" {
			want = labSlurmFailureLog
		}
		labActualCheckLog(t, control, dashboard, alice.accessToken, bob.accessToken, fixture, failure, path, stream, want)
	}
	verifySource()
	if destination := os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_RESULT"); destination != "" {
		if !filepath.IsAbs(destination) {
			t.Fatal("public Slurm receipt requires absolute new path")
		}
		ids := map[string]string{}
		native := map[string]string{}
		for name, j := range jobs {
			ids[name] = j.Metadata.ID
			native[name] = j.NativeID
		}
		encoded, err := json.MarshalIndent(map[string]any{"synthetic": true, "observationMode": "actual-slurm-agent-execution", "fixture": fixture, "jobs": ids, "nativeJobs": native, "arrayId": array.Metadata.ID, "parentId": parent, "submittedTaskIndexes": []int{0, 1, 2, 3, 4}, "executedTaskIndexes": []int{0, 2, 4}, "cancelledBeforeStart": []int{1, 3}, "graphId": graph.Metadata.ID, "accounting": json.RawMessage(accounting), "reports": reports, "verifiedAt": time.Now().UTC()}, "", "  ")
		if err != nil || len(encoded) > 32768 {
			t.Fatal("public Slurm receipt exceeds bound")
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal("public Slurm receipt path must be new")
		}
		_, we := file.Write(append(encoded, '\n'))
		se := file.Sync()
		ce := file.Close()
		if we != nil || se != nil || ce != nil {
			t.Fatal("public Slurm receipt write failed")
		}
	}
	t.Log("PASS: real five-task Slurm array, queued cancellation of 1/3, sparse executed subset 0/2/4 with original indexes/accounting, real scheduler graph and failed-run sealed diagnosis; source log bytes unchanged. No arbitrary sparse submission, imported observations or APNs claim.")
}

func labSlurmAssertJob(t *testing.T, f labActualReceipt, j labSlurmJob, outcome string, executed bool) {
	t.Helper()
	if !labExecutionID(j.Metadata.ID) || j.Metadata.NamespaceID != f.NamespaceID || j.Spec.Placement.TargetID != f.TargetID || j.Spec.Placement.TargetGenerationID != f.TargetGenerationID || j.Spec.Placement.ExecutionBackend != "slurm" || j.Status.Imported || j.Status.Phase != "terminal" || j.Status.Outcome != outcome {
		t.Fatalf("Slurm terminal placement/outcome differs (phase %q,outcome %q,wanted %q)", j.Status.Phase, j.Status.Outcome, outcome)
	}
	if j.Status.CurrentRun == nil || !labExecutionID(j.Status.CurrentRun.ID) || !labExecutionID(j.Status.CurrentRun.ExecutionID) || j.Status.CurrentRun.Number != "1" || j.Status.Lifecycle.CompletedAt == nil || j.Status.Lifecycle.CompletedProvenance != "scheduler.completed" || j.NativeID == "" || j.Scheduler.Backend != "slurm" {
		t.Fatal("Slurm run lacks real scheduler completion identity")
	}
	if executed && (j.Status.Lifecycle.StartedAt == nil || j.Status.Lifecycle.StartedProvenance != "scheduler.observed") {
		t.Fatal("executed Slurm task lacks scheduler-observed start")
	}
	if !executed && j.Status.Lifecycle.StartedAt != nil {
		t.Fatal("queued cancellation unexpectedly executed")
	}
}

func labSlurmCheckArray(t *testing.T, request func(string, string, string, string, any, any, int) []byte, token, prefix string, source labSlurmGroup, parent string) {
	t.Helper()
	path := prefix + "/workloads/array/" + source.Metadata.ID
	var detail api.WorkloadDetail
	request("GET", path, token, "", nil, &detail, 200)
	if detail.Workload.Kind != "array" || detail.Workload.ID != source.Metadata.ID || detail.Workload.TotalChildren != "5" || detail.Workload.ArrayPolicy != "require" || detail.Workload.ArrayMode != "slurm-array" || detail.Workload.Concurrency != "1" || detail.Workload.Counts["terminal"] != "5" || detail.Completeness != "complete" {
		t.Fatal("native array detail lost source policy/count/parent identity")
	}
	cursor := ""
	seen := map[string]bool{}
	for index := 0; index < 5; index++ {
		var page api.WorkloadChildrenPage
		query := "?limit=1"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		request("GET", path+"/children"+query, token, "", nil, &page, 200)
		if page.Total != "5" || len(page.Items) != 1 {
			t.Fatal("array child page bounds differ")
		}
		item := page.Items[0]
		if item.Job.ID != source.Items[index].Job.Metadata.ID || seen[item.Job.ID] || item.Index != strconv.Itoa(index) || item.TaskIndex != strconv.Itoa(index) || item.Job.Scheduler == nil || item.Job.Scheduler.JobID != parent+"_"+strconv.Itoa(index) {
			t.Fatal("paged array task was renumbered or lost immutable scheduler identity")
		}
		seen[item.Job.ID] = true
		cursor = page.NextCursor
		if (index == 4) != (cursor == "") {
			t.Fatal("array continuation completeness differs")
		}
	}
}

func labSlurmObserve(t *testing.T, ctx context.Context, root string, input map[string]string) []byte {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Slurm observation Python unavailable")
	}
	raw, _ := json.Marshal(input)
	command := exec.CommandContext(ctx, python, filepath.Join(root, "scripts/observe-dashboard-slurm.py"))
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE=1"}
	command.Stdin = bytes.NewReader(raw)
	var output labExecutionOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if command.Run() != nil {
		t.Fatal("bounded read-only Slurm observation failed; preserve scenario and inspect privately")
	}
	return output.Bytes()
}

func labSlurmCheckAccounting(t *testing.T, raw []byte, parent, collection string) {
	t.Helper()
	var receipt struct {
		ParentID, CollectionID string
		Rows                   []struct{ JobID, State, ExitCode, NodeList, JobName, Start, End string }
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.ParentID != parent || receipt.CollectionID != collection || len(receipt.Rows) != 5 {
		t.Fatal("bounded actual Slurm accounting receipt differs")
	}
	seen := map[string]bool{}
	for _, row := range receipt.Rows {
		index, err := strconv.Atoi(strings.TrimPrefix(row.JobID, parent+"_"))
		if err != nil || index < 0 || index > 4 || row.JobID != parent+"_"+strconv.Itoa(index) || seen[row.JobID] || row.JobName != "jobman-array-"+collection {
			t.Fatal("accounting task identity differs")
		}
		seen[row.JobID] = true
		want := []string{"COMPLETED", "CANCELLED", "FAILED", "CANCELLED", "COMPLETED"}[index]
		if len(strings.Fields(row.State)) == 0 || strings.Fields(row.State)[0] != want {
			t.Fatal("actual accounting outcome differs")
		}
		if index == 1 || index == 3 {
			if row.Start != "Unknown" && row.Start != "None" {
				t.Fatal("cancelled queued task has accounting start")
			}
		} else {
			if row.NodeList != "compute01" || row.Start == "Unknown" || row.Start == "None" || row.End == "Unknown" || row.End == "None" {
				t.Fatal("real task did not run on approved compute01")
			}
			exit := "0:0"
			if index == 2 {
				exit = "7:0"
			}
			if row.ExitCode != exit {
				t.Fatal("actual accounting exit code differs")
			}
		}
	}
}

func labSlurmReports(t *testing.T, ctx context.Context, request func(string, string, string, string, any, any, int) []byte, alice, bob string, f labActualReceipt, job labActualJob, path string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, profile := range []string{"metadata", "include_log_tail"} {
		body := api.ReportRequest{Profile: profile, RunID: job.Status.CurrentRun.ID}
		key := "actual-slurm-v1-" + job.Metadata.ID + "-" + profile
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
		masked := strings.ReplaceAll(labSlurmFailureLog, labDiagnosticCanary, strings.Repeat("*", len(labDiagnosticCanary)))
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
