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

const labSlurmRepairRevision = "807f1f2f4a89b0b400891c622d2a2cf21606e5d5"
const labSlurmRepairBinary = "9ecc9fe75c404b85eb7b4349b55db736885db5eddd024826a4d0dbf73720a47e"

// This is a distinct bounded acceptance scenario. The original queued-cancel
// experiment, failed assertions, and immutable source jobs remain unchanged.
func TestLabActualCompleteSlurmArrayAndDiagnosis(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_COMPLETE_RUN") != "1" {
		t.Skip("explicit reviewed Slurm workload opt-in required")
	}

	destination := os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_COMPLETE_RESULT")
	if !filepath.IsAbs(destination) {
		t.Fatal("new absolute complete-array result path required")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("complete-array result path must be unused before any submission")
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
	labSlurmRequireUpgrade(t)
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
	labSlurmCompleteObserve(t, ctx, alice.root, map[string]any{"operation": "preflight"})
	plan := labSlurmCompleteRequests(t)
	prefix := "/v1/namespaces/dashboard-operations"
	var array labSlurmGroup
	control("POST", prefix+"/collections", "dashboard-actual-slurm-complete-v1-array", plan.Array, &array)
	if !labExecutionID(array.Metadata.ID) {
		t.Fatal("Slurm array admission invalid")
	}
	arrayPath := prefix + "/collections/" + array.Metadata.ID
	labActualPoll(t, ctx, 90*time.Second, func() bool { control("GET", arrayPath, "", nil, &array); return array.Status.Phase == "terminal" })
	if len(array.Items) != 5 || array.Status.Total != 5 || array.Status.Terminal != 5 || array.Status.Succeeded != 4 || array.Status.Failed != 1 || array.Status.Cancelled != 0 || array.Status.ArrayMode != "slurm-array" {
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
		want := []string{"success", "success", "failure", "success", "success"}[index]
		labSlurmCompleteAssertJob(t, fixture, item.Job, want)
		jobs[item.Name] = item.Job
	}
	accounting := labSlurmCompleteObserve(t, ctx, alice.root, map[string]any{"operation": "accounting", "collectionId": array.Metadata.ID, "parentId": parent})
	labSlurmCheckCompleteAccounting(t, accounting, parent, array.Metadata.ID)
	executions := make([]map[string]any, 0, 5)
	for index, item := range array.Items {
		executions = append(executions, map[string]any{"index": index, "executionId": item.Job.Status.CurrentRun.ExecutionID})
	}
	completions := labSlurmCompleteObserve(t, ctx, alice.root, map[string]any{"operation": "completions", "executions": executions})
	var completed struct {
		Executions []struct {
			Index, ExitCode                                    int
			ExecutionID, Outcome, ObservedAt, CompletionSHA256 string
		}
	}
	if json.Unmarshal(completions, &completed) != nil || len(completed.Executions) != 5 {
		t.Fatal("actual runner completion receipt differs")
	}
	for index, item := range completed.Executions {
		exit, outcome := 0, "success"
		if index == 2 {
			exit, outcome = 7, "failure"
		}
		if item.Index != index || item.ExitCode != exit || item.Outcome != outcome || item.ExecutionID != array.Items[index].Job.Status.CurrentRun.ExecutionID || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(item.CompletionSHA256) {
			t.Fatal("runner workload result differs from the exact task identity")
		}
	}

	var graph labSlurmGroup
	control("POST", prefix+"/graphs", "dashboard-actual-slurm-complete-v1-graph", plan.Graph, &graph)
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
			labSlurmCompleteAssertJob(t, fixture, item.Job, want)
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
	for _, index := range []int{0, 1, 3, 4} {
		var result api.LogRange
		j := jobs["task-"+strconv.Itoa(index)]
		dashboard("GET", apiPrefix+"/jobs/"+j.Metadata.ID+"/logs?stream=stdout", alice.accessToken, "", nil, &result, 200)
		data, err := base64.StdEncoding.DecodeString(result.BytesBase64)
		if err != nil || string(data) != "ACTUAL SLURM TASK "+strconv.Itoa(index)+"\n" || result.ExecutionID != j.Status.CurrentRun.ExecutionID {
			t.Fatal("executed complete-array task log marker/identity differs")
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
	if destination := os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_COMPLETE_RESULT"); destination != "" {
		if !filepath.IsAbs(destination) {
			t.Fatal("public Slurm receipt requires absolute new path")
		}
		ids := map[string]string{}
		native := map[string]string{}
		for name, j := range jobs {
			ids[name] = j.Metadata.ID
			native[name] = j.NativeID
		}
		encoded, err := json.MarshalIndent(map[string]any{"synthetic": true, "observationMode": "actual-slurm-agent-execution", "fixture": fixture, "jobs": ids, "nativeJobs": native, "arrayId": array.Metadata.ID, "parentId": parent, "submittedTaskIndexes": []int{0, 1, 2, 3, 4}, "executedTaskIndexes": []int{0, 1, 2, 3, 4}, "cancelledBeforeStart": []int{}, "coreRepairRevision": labSlurmRepairRevision, "runnerCompletions": json.RawMessage(completions), "graphId": graph.Metadata.ID, "accounting": json.RawMessage(accounting), "reports": reports, "verifiedAt": time.Now().UTC()}, "", "  ")
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
	t.Log("PASS: real complete five-task Slurm array without cancellations, exact original indexes/accounting, wrapper success separated from workload failure, real scheduler graph and failed-run sealed diagnosis; source log bytes unchanged. Original sparse-cancel failure retained separately.")
}

func labSlurmCompleteRequests(t *testing.T) labSlurmPlan {
	t.Helper()
	value := labSlurmRequests(t)
	value.Array.Metadata.Name = "dashboard-slurm-complete-array-v1"
	value.Graph.Metadata.Name = "dashboard-slurm-complete-graph-v1"
	// No queued-cancellation window is required in this separate scenario.
	first := value.Array.Spec.Items[0].Workload.Document
	first.Metadata.Name = "dashboard-slurm-complete-task-0"
	first.Spec.Command.Args[1] = "sleep 3; printf '%s\\n' 'ACTUAL SLURM TASK 0'"
	sealed, err := protocol.SealWorkload(first)
	if err != nil {
		t.Fatal("complete array workload failed to seal")
	}
	value.Array.Spec.Items[0].Workload = protocol.WorkloadBinding{Digest: sealed.Digest, Document: sealed.Document}
	array, err := protocol.SealCollectionRequest(value.Array)
	if err != nil {
		t.Fatal("complete array failed to seal")
	}
	graph, err := protocol.SealGraphRequest(value.Graph)
	if err != nil {
		t.Fatal("complete graph failed to seal")
	}
	return labSlurmPlan{Array: array.Document, Graph: graph.Document}
}

func TestLabSlurmCompleteRequestsAreDistinctStableAndBounded(t *testing.T) {
	first, second, original := labSlurmCompleteRequests(t), labSlurmCompleteRequests(t), labSlurmRequests(t)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) || len(a) > 32768 || first.Array.Metadata.Name == original.Array.Metadata.Name || first.Graph.Metadata.Name == original.Graph.Metadata.Name || first.Array.Spec.ArrayPolicy != "require" || first.Array.Spec.MaxActive != 1 || len(first.Array.Spec.Items) != 5 || len(first.Graph.Spec.Nodes) != 4 {
		t.Fatal("distinct complete scenario bounds differ")
	}
	for index, item := range first.Array.Spec.Items {
		if item.Name != "task-"+strconv.Itoa(index) || item.Placement.Target != "dashboard-execution-slurm" || item.Placement.Partition != "cpu" || item.Workload.Document.Spec.Policy.RunTimeout != "30s" || item.Workload.Document.Spec.Policy.Retry.MaxRuns != 1 || !strings.HasPrefix(item.Workload.Document.Spec.Command.Args[1], "sleep 3;") {
			t.Fatal("complete-array task bounds or ordering differ")
		}
	}
	if !strings.Contains(original.Array.Spec.Items[0].Workload.Document.Spec.Command.Args[1], "sleep 20;") {
		t.Fatal("original sparse-cancel scenario changed")
	}
}

func labSlurmRequireUpgrade(t *testing.T) {
	t.Helper()
	directory := os.Getenv("JOBMAN_DASHBOARD_LAB_SLURM_UPGRADE")
	if !filepath.IsAbs(directory) {
		t.Fatal("exact completed Slurm upgrade directory required")
	}
	raw := labExecutionFile(t, filepath.Join(directory, "plan.json"), 32768)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "e03a18361b5b368722d545ce7080bc2b0b95c06900a3f2056d769c5183970d2c" {
		t.Fatal("approved upgrade plan differs")
	}
	var plan struct{ CoreRevision, AgentSHA256 string }
	if json.Unmarshal(raw, &plan) != nil || plan.CoreRevision != labSlurmRepairRevision || plan.AgentSHA256 != labSlurmRepairBinary {
		t.Fatal("upgrade binary provenance differs")
	}
	var receipt map[string]struct {
		RunnerSHA256 string
		Process      struct{ BinarySHA256, AgentID, TargetGenerationID string }
	}
	if json.Unmarshal(labExecutionFile(t, filepath.Join(directory, "apply-receipts/verify.json"), 32768), &receipt) != nil || len(receipt) != 2 || receipt["compute01"].RunnerSHA256 != labSlurmRepairBinary || receipt["submit01"].RunnerSHA256 != labSlurmRepairBinary || receipt["submit01"].Process.BinarySHA256 != labSlurmRepairBinary || receipt["submit01"].Process.AgentID != "2c4fa071-6157-495d-a332-c992593156a6" || receipt["submit01"].Process.TargetGenerationID != "ecbd7c4d-04f2-49e2-869e-2fe12bf1388c" {
		t.Fatal("both repaired runner copies and unchanged enrollment require completed verification")
	}
}

func labSlurmCheckCompleteAccounting(t *testing.T, raw []byte, parent, collection string) {
	t.Helper()
	if !labSlurmCompleteAccountingValid(raw, parent, collection) {
		t.Fatal("complete-array scheduler wrapper accounting or exact task identity differs")
	}
}

func labSlurmCompleteAccountingValid(raw []byte, parent, collection string) bool {
	var receipt struct {
		ParentID, CollectionID string
		Rows                   []struct{ JobID, State, ExitCode, NodeList, JobName, Start, End string }
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.ParentID != parent || receipt.CollectionID != collection || len(receipt.Rows) != 5 {
		return false
	}
	seen := map[string]bool{}
	for _, row := range receipt.Rows {
		index, err := strconv.Atoi(strings.TrimPrefix(row.JobID, parent+"_"))
		if err != nil || index < 0 || index > 4 || row.JobID != parent+"_"+strconv.Itoa(index) || seen[row.JobID] || row.JobName != "jobman-array-"+collection {
			return false
		}
		seen[row.JobID] = true
		// The batch wrapper successfully persisted completed.json even for exit7.
		if row.State != "COMPLETED" || row.ExitCode != "0:0" || row.NodeList != "compute01" || row.Start == "" || row.Start == "Unknown" || row.Start == "None" || row.End == "" || row.End == "Unknown" || row.End == "None" {
			return false
		}
	}
	return true
}

func labSlurmCompleteObserve(t *testing.T, ctx context.Context, root string, input map[string]any) []byte {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 8192 {
		t.Fatal("bounded complete-array read invalid")
	}
	command := exec.CommandContext(ctx, python, filepath.Join(root, "scripts/observe-dashboard-slurm-complete.py"))
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE=1"}
	command.Stdin = bytes.NewReader(raw)
	var output labExecutionOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if command.Run() != nil {
		t.Fatal("bounded complete-array read failed; retain scenario and inspect privately")
	}
	return output.Bytes()
}

func TestLabSlurmCompleteAccountingSeparatesWrapperFromWorkload(t *testing.T) {
	collection := "80000000-0000-4000-8000-000000000009"
	rows := []map[string]string{}
	for i := 0; i < 5; i++ {
		rows = append(rows, map[string]string{"jobId": "48_" + strconv.Itoa(i), "state": "COMPLETED", "exitCode": "0:0", "nodeList": "compute01", "jobName": "jobman-array-" + collection, "start": "2026-10-04T10:00:00", "end": "2026-10-04T10:00:03"})
	}
	encoded := func() []byte {
		raw, _ := json.Marshal(map[string]any{"parentId": "48", "collectionId": collection, "rows": rows})
		return raw
	}
	if !labSlurmCompleteAccountingValid(encoded(), "48", collection) {
		t.Fatal("all five persisted-result wrapper completions should be accepted")
	}
	rows[2]["exitCode"] = "7:0"
	if labSlurmCompleteAccountingValid(encoded(), "48", collection) {
		t.Fatal("workload exit must not be substituted into scheduler wrapper facts")
	}
	rows[2]["exitCode"] = "0:0"
	rows[2]["jobId"] = "48_1"
	if labSlurmCompleteAccountingValid(encoded(), "48", collection) {
		t.Fatal("duplicate task identity accepted")
	}
	rows[2]["jobId"] = "49_2"
	if labSlurmCompleteAccountingValid(encoded(), "48", collection) {
		t.Fatal("another array's task accepted")
	}
	rows[2]["jobId"] = "48_2"
	rows = rows[:4]
	if labSlurmCompleteAccountingValid(encoded(), "48", collection) {
		t.Fatal("incomplete allocation set accepted")
	}
}

// A task can finish between polls. Completed scheduler/runner evidence proves
// execution; a missing running observation must remain missing in the dashboard.
func labSlurmCompleteJobValid(f labActualReceipt, j labSlurmJob, outcome string) bool {
	if !labExecutionID(j.Metadata.ID) || j.Metadata.NamespaceID != f.NamespaceID || j.Spec.Placement.TargetID != f.TargetID || j.Spec.Placement.TargetGenerationID != f.TargetGenerationID || j.Spec.Placement.ExecutionBackend != "slurm" || j.Status.Imported || j.Status.Phase != "terminal" || j.Status.Outcome != outcome {
		return false
	}
	if j.Status.CurrentRun == nil || !labExecutionID(j.Status.CurrentRun.ID) || !labExecutionID(j.Status.CurrentRun.ExecutionID) || j.Status.CurrentRun.Number != "1" || j.Status.Lifecycle.CompletedAt == nil || j.Status.Lifecycle.CompletedProvenance != "scheduler.completed" || j.NativeID == "" || j.Scheduler.Backend != "slurm" {
		return false
	}
	if j.Status.Lifecycle.StartedAt == nil {
		return j.Status.Lifecycle.StartedProvenance == ""
	}
	return j.Status.Lifecycle.StartedProvenance == "scheduler.observed"
}

func labSlurmCompleteAssertJob(t *testing.T, f labActualReceipt, j labSlurmJob, outcome string) {
	t.Helper()
	if !labSlurmCompleteJobValid(f, j, outcome) {
		t.Fatal("complete Slurm task lacks truthful terminal placement/run/completion evidence")
	}
}

func TestLabSlurmCompleteJobAllowsUnobservedStartWithoutInventingOne(t *testing.T) {
	const id = "80000000-0000-4000-8000-000000000001"
	fixture := labActualReceipt{NamespaceID: id, TargetID: id, TargetGenerationID: id}
	var job labSlurmJob
	job.Metadata.ID = id
	job.Metadata.NamespaceID = id
	job.Spec.Placement.TargetID = id
	job.Spec.Placement.TargetGenerationID = id
	job.Spec.Placement.ExecutionBackend = "slurm"
	job.Status.Phase = "terminal"
	job.Status.Outcome = "success"
	job.Status.CurrentRun = &api.RunReference{ID: id, ExecutionID: id, Number: "1"}
	completed := time.Unix(200, 0).UTC()
	job.Status.Lifecycle.CompletedAt = &completed
	job.Status.Lifecycle.CompletedProvenance = "scheduler.completed"
	job.NativeID = "48_4"
	job.Scheduler.Backend = "slurm"
	if !labSlurmCompleteJobValid(fixture, job, "success") {
		t.Fatal("positive completion with no running observation should remain valid")
	}
	job.Status.Lifecycle.StartedProvenance = "scheduler.observed"
	if labSlurmCompleteJobValid(fixture, job, "success") {
		t.Fatal("start provenance without an actual start is invalid")
	}
	started := completed.Add(-3 * time.Second)
	job.Status.Lifecycle.StartedAt = &started
	if !labSlurmCompleteJobValid(fixture, job, "success") {
		t.Fatal("actual observed start rejected")
	}
	job.Status.Lifecycle.StartedProvenance = "inferred"
	if labSlurmCompleteJobValid(fixture, job, "success") {
		t.Fatal("inferred start accepted")
	}
	job.Status.Lifecycle.StartedAt = nil
	job.Status.Lifecycle.StartedProvenance = ""
	job.Status.Lifecycle.CompletedAt = nil
	if labSlurmCompleteJobValid(fixture, job, "success") {
		t.Fatal("missing completion proof accepted")
	}
}
