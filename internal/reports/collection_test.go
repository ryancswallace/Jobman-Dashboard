package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-diagnose/deterministic"
	"github.com/ryancswallace/jobman-diagnose/diagnosis"
	"github.com/ryancswallace/jobman/diagnostic"
)

func collectionSnapshot(t *testing.T) (Snapshot, diagnostic.SharedSelection) {
	t.Helper()
	data, err := os.ReadFile("testdata/shared-control-failure-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	core, err := diagnostic.Decode(bytes.NewReader(data), diagnostic.DecodeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := diagnostic.SharedSnapshot{
		Kind: diagnostic.SharedSnapshotKind, SchemaVersion: diagnostic.SharedSnapshotVersion,
		CapturedAt: core.CapturedAt, Source: core.Shared.Source, JobmanVersion: core.Source.JobmanVersion,
		Platform: core.Source.Platform, Job: diagnostic.SharedJob{ID: core.Subject.JobID, Revision: core.Subject.JobRevision, Phase: core.Subject.Phase, Outcome: core.Subject.Outcome},
		Runs: core.Shared.Runs, Metadata: core.Consistency.Metadata, Items: core.Items, Logs: core.Shared.Logs,
		Omissions: []diagnostic.Omission{}, RedactionNotices: []diagnostic.RedactionNotice{},
	}
	selection := diagnostic.SharedSelection{DeploymentID: snapshot.Source.DeploymentID, ControlInstanceID: snapshot.Source.ControlInstanceID, NamespaceID: snapshot.Source.NamespaceID, JobID: snapshot.Job.ID, ExpectedJobRevision: snapshot.Job.Revision}
	return Snapshot{Value: snapshot, RecoveryEpoch: "1"}, selection
}

type collectionLogReader func(context.Context, diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error)

func (reader collectionLogReader) ReadLogTail(ctx context.Context, request diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error) {
	return reader(ctx, request)
}

func TestPrepareMetadataSealsWithoutLogsAndExcludesPrivateSourceFields(t *testing.T) {
	snapshot, selection := collectionSnapshot(t)
	private, _ := diagnostic.JSONValue("private-command-env-path-canary")
	snapshot.Value.Items = append(snapshot.Value.Items,
		diagnostic.Item{ID: "private:command", Code: "jobman.command", Value: private, Source: diagnostic.ItemSource{Kind: "control", EntityID: selection.JobID}, Quality: diagnostic.QualityObserved, Disclosure: diagnostic.DisclosureLocalOnly},
		diagnostic.Item{ID: "private:unknown", Code: "control.unknown", Value: private, Source: diagnostic.ItemSource{Kind: "control", EntityID: selection.JobID}, Quality: diagnostic.QualityObserved, Disclosure: diagnostic.DisclosureMetadata})
	prepared, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{})
	if err != nil || diagnostic.Verify(prepared.Metadata) != nil || prepared.Metadata.Shared.Profile != diagnostic.SharedProfileMetadata {
		t.Fatalf("metadata preparation failed: %v", err)
	}
	core, err := prepared.Collect(t.Context(), collectionLogReader(func(context.Context, diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error) {
		t.Fatal("metadata collection read log bytes")
		return diagnostic.SharedLogTail{}, nil
	}))
	if err != nil || core.EvidenceID != prepared.Metadata.EvidenceID || len(core.Artifacts) != 0 {
		t.Fatalf("metadata changed during collection: %v", err)
	}
	encoded, _ := json.Marshal(core)
	if bytes.Contains(encoded, []byte("private-command-env-path-canary")) || len(core.Omissions) < 3 {
		t.Fatal("private source fact disclosed or missing omission")
	}
	// Private encoded input, rather than the exported Metadata or caller slices,
	// is the authority used when a delayed worker performs collection.
	prepared.Metadata.Items[0].Value[0] = '!'
	snapshot.Value.Items[0].Value[0] = '!'
	again, err := prepared.Collect(t.Context(), nil)
	if err != nil || again.EvidenceID != core.EvidenceID {
		t.Fatal("caller mutation changed prepared snapshot")
	}
}

func TestCollectionFingerprintPreservesFactsButIgnoresIncidentalCaptureTime(t *testing.T) {
	snapshot, selection := collectionSnapshot(t)
	baseline, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Value.CapturedAt = snapshot.Value.CapturedAt.Add(time.Hour + 123*time.Nanosecond)
	recaptured, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{})
	if err != nil || recaptured.SnapshotFingerprint != baseline.SnapshotFingerprint || recaptured.Metadata.EvidenceID != baseline.Metadata.EvidenceID || recaptured.Metadata.CapturedAt.Equal(baseline.Metadata.CapturedAt) {
		t.Fatalf("capture-only change affected identity: %v", err)
	}
	for name, change := range map[string]func(*Snapshot){
		"epoch":             func(s *Snapshot) { s.RecoveryEpoch = "2" },
		"manifest":          func(s *Snapshot) { s.Value.Logs[0].ManifestRevision++ },
		"content":           func(s *Snapshot) { s.Value.Items[1].Value = json.RawMessage("18") },
		"sourceObservation": func(s *Snapshot) { s.Value.Items[1].ObservedAt = &s.Value.CapturedAt },
	} {
		t.Run(name, func(t *testing.T) {
			changed, selection := collectionSnapshot(t)
			change(&changed)
			got, err := PrepareCollection(t.Context(), changed, selection, CollectionOptions{})
			if err != nil || got.SnapshotFingerprint == baseline.SnapshotFingerprint {
				t.Fatalf("factual change reused cache identity: %v", err)
			}
		})
	}
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"canary"}})
	unused, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{Redaction: policy})
	if err != nil || unused.SnapshotFingerprint != baseline.SnapshotFingerprint || unused.PolicyFingerprint != "" {
		t.Fatal("unused log policy churned metadata identity")
	}
}

func TestCollectionPinsDisclosurePolicyBudgetAndCallerRunSelection(t *testing.T) {
	snapshot, selection := collectionSnapshot(t)
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"canary"}})
	options := CollectionOptions{IncludeLogTail: true, Redaction: policy}
	prepared, err := PrepareCollection(t.Context(), snapshot, selection, options)
	if err != nil || prepared.PolicyFingerprint != policy.Fingerprint() {
		t.Fatal("log policy was not pinned")
	}
	explicit := options
	explicit.LogBytes = diagnostic.SharedMaximumTailBytes
	defaulted, err := PrepareCollection(t.Context(), snapshot, selection, explicit)
	if err != nil || defaulted.SnapshotFingerprint != prepared.SnapshotFingerprint {
		t.Fatal("default and explicit log budget disagree")
	}
	for _, options := range []CollectionOptions{{}, {IncludeLogTail: true, LogBytes: 4096, Redaction: policy}, {IncludeLogTail: true, Redaction: testRedactionPolicy(t, RedactionConfig{Values: []string{"different"}})}} {
		changed, err := PrepareCollection(t.Context(), snapshot, selection, options)
		if err != nil || changed.SnapshotFingerprint == prepared.SnapshotFingerprint {
			t.Fatal("profile, budget or policy did not change identity")
		}
	}
	for _, selectedRun := range []string{"", snapshot.Value.Runs[0].ID} {
		selection.RunID = selectedRun
		selected, err := PrepareCollection(t.Context(), snapshot, selection, options)
		if err != nil {
			t.Fatal(err)
		}
		_, err = selected.Collect(t.Context(), collectionLogReader(func(_ context.Context, request diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error) {
			if request.Selection.RunID != selectedRun || request.Selection.ExpectedJobRevision != snapshot.Value.Job.Revision || request.Reference != snapshot.Value.Logs[0] {
				t.Fatal("collection rewrote selected run or manifest identity")
			}
			return diagnostic.SharedLogTail{Reference: request.Reference, Data: bytes.Repeat([]byte{'a'}, int(request.Reference.Bytes)), ByteEnd: request.Reference.Bytes, CapturedAt: snapshot.Value.CapturedAt}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectedRedactedBytesSealThroughPublicDeterministicEngine(t *testing.T) {
	snapshot, selection := collectionSnapshot(t)
	raw := []byte("prefix\nsecret\nvalue\nerror: permission denied\n")
	snapshot.Value.Logs[0].Bytes = uint64(len(raw) + 100)
	policy := testRedactionPolicy(t, RedactionConfig{Values: []string{"secret\nvalue"}})
	prepared, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{IncludeLogTail: true, LogBytes: uint64(len(raw)), Redaction: policy})
	if err != nil {
		t.Fatal(err)
	}
	reader := collectionLogReader(func(_ context.Context, request diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error) {
		return diagnostic.SharedLogTail{Reference: request.Reference, Data: raw, ByteStart: 100, ByteEnd: request.Reference.Bytes, CapturedAt: snapshot.Value.CapturedAt}, nil
	})
	core, err := prepared.Collect(t.Context(), reader)
	if err != nil || diagnostic.Verify(core) != nil || len(core.Artifacts) != 1 || len(core.RedactionNotices) != 1 {
		t.Fatalf("redacted evidence failed verification: %v", err)
	}
	artifact := core.Artifacts[0]
	if artifact.ByteStart != 100 || artifact.ByteEnd != uint64(len(raw)+100) || !artifact.Truncated || len(artifact.Data) != len(raw) || bytes.Contains(artifact.Data, []byte("secret")) || bytes.Contains(artifact.Data, []byte("value")) || artifact.ID != snapshot.Value.Logs[0].ID {
		t.Fatal("sealed artifact bytes lost redaction or source attribution")
	}
	evidence, err := deterministic.Prepare(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := deterministic.New("collection-test", func() time.Time { return snapshot.Value.CapturedAt })
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.Diagnose(t.Context(), evidence)
	if err != nil || diagnosis.ValidateAgainstEvidence(report, evidence) != nil {
		t.Fatalf("real deterministic report did not bind exact sealed evidence: %v", err)
	}
	encoded, _ := json.Marshal(struct {
		Evidence diagnosis.FailureEvidence
		Report   diagnosis.Report
	}{evidence, report})
	if bytes.Contains(encoded, []byte("secret")) || bytes.Contains(encoded, []byte("c2VjcmV0")) {
		t.Fatal("private pre-redaction log bytes appeared in final pair")
	}
	// Reader errors are not omissions and cannot return partial evidence.
	revoked := errors.New("synthetic revoked access")
	failed, err := prepared.Collect(t.Context(), collectionLogReader(func(context.Context, diagnostic.SharedLogRequest) (diagnostic.SharedLogTail, error) {
		return diagnostic.SharedLogTail{}, revoked
	}))
	if !errors.Is(err, revoked) || failed.EvidenceID != "" {
		t.Fatal("reader authorization error returned partial evidence")
	}
}

func TestCollectionRejectsUnconfiguredDisclosureWrongPinsBoundsAndCancellation(t *testing.T) {
	snapshot, selection := collectionSnapshot(t)
	if _, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{IncludeLogTail: true}); !errors.Is(err, ErrRedactionUnavailable) {
		t.Fatal("log profile silently downgraded without configured redaction")
	}
	for _, options := range []CollectionOptions{{LogBytes: 1}, {IncludeLogTail: true, LogBytes: diagnostic.SharedMaximumTailBytes + 1}} {
		if _, err := PrepareCollection(t.Context(), snapshot, selection, options); err == nil {
			t.Fatal("invalid collection budget accepted")
		}
	}
	for _, mutate := range []func(*diagnostic.SharedSelection){
		func(s *diagnostic.SharedSelection) { s.DeploymentID = s.JobID }, func(s *diagnostic.SharedSelection) { s.ControlInstanceID = s.JobID },
		func(s *diagnostic.SharedSelection) { s.NamespaceID = s.JobID }, func(s *diagnostic.SharedSelection) { s.ExpectedJobRevision++ },
		func(s *diagnostic.SharedSelection) { s.RunID = s.JobID },
	} {
		wrong := selection
		mutate(&wrong)
		if _, err := PrepareCollection(t.Context(), snapshot, wrong, CollectionOptions{}); err == nil {
			t.Fatal("mismatched source selection accepted")
		}
	}
	for _, epoch := range []string{"0", "-1", "01", "9223372036854775808"} {
		snapshot.RecoveryEpoch = epoch
		if _, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{}); err == nil {
			t.Fatal("invalid recovery epoch accepted")
		}
	}
	snapshot.RecoveryEpoch = "1"
	snapshot.Value.Items[0].Value = json.RawMessage(`"` + strings.Repeat("x", diagnostic.SharedMaximumBytes) + `"`)
	if _, err := PrepareCollection(t.Context(), snapshot, selection, CollectionOptions{}); err == nil {
		t.Fatal("oversized source snapshot accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PrepareCollection(canceled, snapshot, selection, CollectionOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preparation did work")
	}
	if _, err := (PreparedCollection{}).Collect(t.Context(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero preparation accepted")
	}
}
