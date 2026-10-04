package reports

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman/diagnostic"
)

type manifestFunc func(context.Context, monitoring.Actor, logs.ManifestQuery) (logs.Manifest, error)

func (f manifestFunc) Manifest(ctx context.Context, a monitoring.Actor, q logs.ManifestQuery) (logs.Manifest, error) {
	return f(ctx, a, q)
}

type chunkFunc func(context.Context, monitoring.Actor, logs.Manifest, logs.Chunk) (logs.FileResult, error)

func (f chunkFunc) ReadChunk(ctx context.Context, a monitoring.Actor, m logs.Manifest, c logs.Chunk) (logs.FileResult, error) {
	return f(ctx, a, m, c)
}

func TestPinnedLogsUseRealBrokerAndRejectChangedOrIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "dense", "page-limit", "empty", "append", "revision", "epoch", "revoke", "grant-change", "corrupt", "missing", "reference", "run", "budget"} {
		t.Run(mode, func(t *testing.T) {
			const deployment = "11111111-1111-4111-8111-111111111111"
			const instance = "22222222-2222-4222-8222-222222222222"
			const ns = "33333333-3333-4333-8333-333333333333"
			const job = "44444444-4444-4444-8444-444444444444"
			const run = "55555555-5555-4555-8555-555555555555"
			const execution = "66666666-6666-4666-8666-666666666666"
			data := []byte("bounded synthetic log")
			if mode == "empty" {
				data = nil
			}
			if mode == "dense" {
				data = []byte(strings.Repeat("a", 121))
			}
			if mode == "page-limit" {
				data = []byte(strings.Repeat("a", 1700))
			}
			now := time.Now().UTC()
			ref := diagnostic.SharedLogReference{ID: "artifact:run:3:stderr", RunID: run, ExecutionID: execution, Stream: "stderr", ManifestRevision: 7, Bytes: uint64(len(data)), Complete: true}
			v := diagnostic.SharedSnapshot{Kind: diagnostic.SharedSnapshotKind, SchemaVersion: 1, CapturedAt: now, Source: diagnostic.SharedSource{Kind: "control", DeploymentID: deployment, ControlInstanceID: instance, NamespaceID: ns, ControlVersion: "test", ContractVersion: "test"}, JobmanVersion: "test", Platform: "linux/arm64", Job: diagnostic.SharedJob{ID: job, Revision: 9, Phase: "terminal", Outcome: "failure"}, Runs: []diagnostic.SharedRun{{ID: run, Number: 3, ExecutionID: execution}}, Metadata: diagnostic.MetadataTransactionalSnapshot, Items: []diagnostic.Item{}, Logs: []diagnostic.SharedLogReference{ref}, Omissions: []diagnostic.Omission{}, RedactionNotices: []diagnostic.RedactionNotice{}}
			scope := api.Scope{DeploymentID: deployment, NamespaceID: ns}
			calls, reads := 0, 0
			source := manifestFunc(func(_ context.Context, _ monitoring.Actor, q logs.ManifestQuery) (logs.Manifest, error) {
				calls++
				if q.Scope != scope || q.JobID != job || q.RunNumber != "3" || q.Stream != "stderr" {
					t.Fatal("selection changed")
				}
				m := logs.Manifest{Scope: scope, NamespaceName: "lab", JobID: job, TargetGenerationID: instance, RunID: run, RunNumber: "3", ExecutionID: execution, Stream: "stderr", InstanceID: instance, RecoveryEpoch: "1", AuthorizationVersion: "8", AuthorizationExpiresAt: now.Add(time.Minute), AsOf: now, Revision: "7", State: "complete", ByteLength: int64(len(data))}
				if len(data) > 0 {
					sum := sha256.Sum256(data)
					m.LastSequence = 1
					m.Chunks = []logs.Chunk{{Sequence: 1, ByteLength: int64(len(data)), StoreName: "logs", StoreVersion: "1", ObjectKey: "namespaces/lab/jobs/" + job + "/executions/" + execution + "/logs/stderr/00000001.chunk", Checksum: fmt.Sprintf("sha256:%x", sum), CapturedAt: now.Add(-time.Minute)}}
				}
				if mode == "dense" || mode == "page-limit" {
					m.LastSequence = int64(len(data))
					m.Chunks = nil
					start := 0
					if q.Offset != nil {
						start = int(*q.Offset)
					} else {
						start = max(0, len(data)-q.TailBytes)
					}
					for i := start; i < len(data) && len(m.Chunks) < q.Limit; i++ {
						sum := sha256.Sum256(data[i : i+1])
						m.Chunks = append(m.Chunks, logs.Chunk{Sequence: int64(i + 1), ByteOffset: int64(i), ByteLength: 1, StoreName: "logs", StoreVersion: "1", ObjectKey: fmt.Sprintf("namespaces/lab/jobs/%s/executions/%s/logs/stderr/%08d.chunk", job, execution, i+1), Checksum: fmt.Sprintf("sha256:%x", sum), CapturedAt: now.Add(-time.Minute)})
					}
				}
				if calls >= 4 {
					switch mode {
					case "append":
						m.ByteLength++
					case "revision":
						m.Revision = "8"
					case "epoch":
						m.RecoveryEpoch = "2"
					case "revoke":
						return logs.Manifest{}, monitoring.ErrForbidden
					case "grant-change":
						m.AuthorizationVersion = "9"
					}
				}
				return m, nil
			})
			chunks := chunkFunc(func(_ context.Context, _ monitoring.Actor, _ logs.Manifest, chunk logs.Chunk) (logs.FileResult, error) {
				reads++
				out := append([]byte(nil), data[chunk.ByteOffset:chunk.ByteOffset+chunk.ByteLength]...)
				if mode == "corrupt" {
					out[0] = '!'
				}
				if mode == "missing" {
					return logs.FileResult{State: "chunk_missing"}, nil
				}
				return logs.FileResult{State: "ok", Bytes: out}, nil
			})
			broker, err := logs.NewWithChunks(map[string]logs.ManifestSource{deployment: source}, chunks, []byte(strings.Repeat("k", 32)))
			if err != nil {
				t.Fatal(err)
			}
			p := PinnedLogs{Snapshot: Snapshot{Value: v, RecoveryEpoch: "1"}, Actor: monitoring.Actor{Account: api.Account{ID: job}, DirectoryID: job}, Source: source, Logs: broker}
			r := diagnostic.SharedLogRequest{Selection: diagnostic.SharedSelection{DeploymentID: deployment, ControlInstanceID: instance, NamespaceID: ns, JobID: job, ExpectedJobRevision: 9}, Reference: ref, MaxBytes: 8}
			switch mode {
			case "dense", "page-limit":
				r.MaxBytes = 65536
			case "reference":
				r.Reference.ManifestRevision++
			case "run":
				r.Selection.RunID = job
			case "budget":
				r.MaxBytes = 65537
			}
			tail, err := p.ReadLogTail(t.Context(), r)
			if mode == "valid" || mode == "empty" || mode == "dense" {
				if err != nil {
					t.Fatal(err)
				}
				want := data[max(0, len(data)-int(r.MaxBytes)):]
				wantCalls := 4
				if mode == "dense" {
					wantCalls = 6
				}
				if string(tail.Data) != string(want) || tail.ByteEnd != uint64(len(data)) || tail.Reference != ref || calls != wantCalls {
					t.Fatal("tail attribution lost")
				}
			} else if err == nil || len(tail.Data) > 0 {
				t.Fatal("invalid evidence accepted")
			}
			if mode == "page-limit" && reads != 1600 {
				t.Fatalf("page limit exceeded or not exercised: %d", reads)
			}
			if (mode == "reference" || mode == "run" || mode == "budget") && (reads != 0 || calls != 0) {
				t.Fatal("invalid selection performed reads")
			}
		})
	}
}
