package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func TestJobExecutionIsAuthorizedDetailOnly(t *testing.T) {
	for _, mode := range []string{"available", "old-source", "missing", "revoked", "wrong-namespace"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			reads := 0
			args := []string{"-c", "printf '%s\\n' \"$1\"", "", "two words", "line\nbreak", "<script>synthetic</script>", "日本語"}
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/capabilities":
					value := capabilities(now)
					if mode != "old-source" {
						capabilities := value["capabilities"].(map[string]any)
						capabilities["features"] = append(capabilities["features"].([]string), "job-execution-detail")
					}
					respond(w, value)
				case "/v1/me":
					version := "9"
					if mode == "revoked" && reads > 0 {
						version = "10"
					}
					respond(w, grants(now, version))
				case "/v1/namespaces/lab/jobs/" + jobID, "/v1/namespaces/lab/jobs":
					reads++
					value := job(now)
					spec := value["spec"].(map[string]any)
					placement := spec["placement"].(map[string]any)
					placement["target"], placement["partition"] = "Research cluster", "batch"
					spec["workloadDigest"] = "sha256:" + strings.Repeat("a", 64)
					value["status"].(map[string]any)["confidenceUpdatedAt"] = now
					if mode == "missing" {
						spec["executionUnavailableReason"] = "missing"
					} else {
						spec["execution"] = map[string]any{"command": map[string]any{"executable": "/bin/sh", "args": args}, "workingDirectory": "/srv/synthetic work", "environment": map[string]string{"TOKEN": "synthetic-environment-must-not-leak"}}
					}
					if mode == "wrong-namespace" {
						value["metadata"].(map[string]any)["namespaceId"] = instanceID
					}
					if r.URL.Path == "/v1/namespaces/lab/jobs" {
						respond(w, map[string]any{"apiVersion": contract, "kind": "JobList", "asOf": now, "items": []any{value}})
					} else {
						respond(w, value)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			scope := api.Scope{DeploymentID: deploymentID, NamespaceID: namespaceID}
			detail, err := c.JobDetail(context.Background(), testActor, scope, jobID)
			if mode == "revoked" || mode == "wrong-namespace" {
				if err == nil || detail.Execution != nil || detail.Job.ID != "" {
					t.Fatal("untrusted or revoked detail escaped authorization")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if detail.Job.TargetName != "Research cluster" || detail.Job.Partition != "batch" || detail.Job.WorkloadDigest == "" || detail.Job.ConfidenceUpdatedAt == nil || !detail.Job.ConfidenceUpdatedAt.Equal(now) {
				t.Fatal("status metadata was lost")
			}
			if mode == "available" {
				if detail.Execution == nil || detail.Execution.Command.Executable != "/bin/sh" || !reflect.DeepEqual(detail.Execution.Command.Args, args) || detail.Execution.WorkingDirectory != "/srv/synthetic work" {
					t.Fatal("literal command or argument boundaries changed")
				}
			} else {
				want := "missing"
				if mode == "old-source" {
					want = "unsupported"
				}
				if detail.Execution != nil || detail.ExecutionUnavailableReason != want {
					t.Fatal("missing command did not retain an explicit reason")
				}
			}
			encoded, err := json.Marshal(detail)
			if err != nil || strings.Contains(string(encoded), "synthetic-environment-must-not-leak") {
				t.Fatal("environment escaped detail projection")
			}
			page, err := c.Jobs(context.Background(), testActor, monitoring.SourceQuery{Query: monitoring.Query{Limit: 10}, NamespaceID: namespaceID, CreatedBefore: now})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err = json.Marshal(page)
			if err != nil || strings.Contains(string(encoded), "executable") || strings.Contains(string(encoded), "synthetic-environment-must-not-leak") {
				t.Fatal("command escaped into paginated jobs")
			}
			ordinary, err := c.Job(context.Background(), testActor, scope, jobID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err = json.Marshal(ordinary)
			if err != nil || strings.Contains(string(encoded), "executable") {
				t.Fatal("command escaped ordinary job metadata")
			}
		})
	}
}

func TestExecutionProjectionBoundsAndMalformedSource(t *testing.T) {
	valid := `{"command":{"executable":"printf","args":[]},"workingDirectory":"/tmp"}`
	for _, tc := range []struct {
		name, raw, reason, wantReason string
		wantError                     bool
	}{
		{name: "empty-args", raw: valid},
		{name: "future-unavailable", reason: "future_reason", wantReason: "future_reason"},
		{name: "missing", wantReason: "missing"},
		{name: "source-too-large", reason: "too_large", wantReason: "too_large"},
		{name: "conflicting-state", raw: valid, reason: "missing", wantError: true},
		{name: "null-args", raw: `{"command":{"executable":"printf","args":null},"workingDirectory":"/tmp"}`, wantError: true},
		{name: "null-argument", raw: `{"command":{"executable":"printf","args":[null]},"workingDirectory":"workspace:/synthetic"}`, wantError: true},
		{name: "missing-cwd", raw: `{"command":{"executable":"printf","args":[]}}`, wantError: true},
		{name: "missing-executable", raw: `{"command":{"args":[]},"workingDirectory":"/tmp"}`, wantError: true},
		{name: "nul-argument", raw: `{"command":{"executable":"printf","args":["\u0000"]},"workingDirectory":"/tmp"}`, wantError: true},
		{name: "oversized-field", raw: `{"command":{"executable":"` + strings.Repeat("a", 65537) + `","args":[]},"workingDirectory":"/tmp"}`, wantReason: "too_large"},
		{name: "oversized-body", raw: strings.Repeat(" ", 2<<20) + valid, wantReason: "too_large"},
		{name: "oversized-reason", reason: strings.Repeat("x", 129), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, reason, err := normalizeExecution(json.RawMessage(tc.raw), tc.reason)
			if tc.wantError {
				if !errors.Is(err, monitoring.ErrSource) || value != nil {
					t.Fatal("malformed source accepted")
				}
				return
			}
			if err != nil || reason != tc.wantReason {
				t.Fatalf("unexpected result: reason=%q err=%v", reason, err)
			}
			if tc.wantReason != "" && value != nil {
				t.Fatal("unavailable projection retained command")
			}
			if tc.name == "empty-args" && (value == nil || value.Command.Args == nil || len(value.Command.Args) != 0) {
				t.Fatal("empty argument vector lost")
			}
		})
	}
	tooMany := api.JobExecution{Command: api.JobCommand{Executable: "printf", Args: make([]string, 4097)}, WorkingDirectory: "/tmp"}
	raw, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	if value, reason, err := normalizeExecution(raw, ""); err != nil || value != nil || reason != "too_large" {
		t.Fatal("argument count was not bounded")
	}
}

func TestExecutionProjectionBoundsEncodedOutput(t *testing.T) {
	value := api.JobExecution{Command: api.JobCommand{Executable: "printf", Args: make([]string, 32)}, WorkingDirectory: "workspace:/synthetic"}
	for i := range value.Command.Args {
		value.Command.Args[i] = strings.Repeat("<", 60000)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() > 2<<20 {
		t.Fatal("test input must fit source bound")
	}
	if result, reason, err := normalizeExecution(buffer.Bytes(), ""); err != nil || result != nil || reason != "too_large" {
		t.Fatal("HTML escaping exceeded encoded projection limit")
	}
}
