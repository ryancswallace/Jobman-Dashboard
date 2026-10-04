package reports

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-diagnose/deterministic"
	"github.com/ryancswallace/jobman-diagnose/diagnosis"
	"github.com/ryancswallace/jobman/diagnostic"
)

func projectionResult(pair Pair) Result {
	return Result{Task: Task{ID: "90000000-0000-4000-8000-000000000099", Subject: pair.Subject, State: "ready", CreatedAt: pair.Report.GeneratedAt, ExpiresAt: pair.Report.GeneratedAt.Add(24 * time.Hour), LeaseToken: "private-lease-canary", FailureCode: "private-error-canary"}, Pair: &pair, Outdated: true}
}

func TestReportProjectionRetainsFullFindingsAndOriginalIDsWithoutPrivateState(t *testing.T) {
	pair := fixturePair(t)
	result := projectionResult(pair)
	projected, err := Project(result)
	if err != nil || projected.TaskID != result.Task.ID || projected.ReportID != pair.Report.ReportID || projected.EvidenceID != pair.Evidence.Core.EvidenceID || projected.AnalysisEvidenceID != pair.Evidence.AnalysisEvidenceID || !projected.Outdated || projected.State != "ready" || projected.Detail == nil {
		t.Fatalf("report identities changed: %v", err)
	}
	detail := projected.Detail
	if len(detail.Findings) != len(pair.Report.Findings) || len(detail.Actions) != len(pair.Report.Actions) || len(detail.Citations) != len(pair.Report.Citations) || len(detail.MissingEvidence) != len(pair.Report.MissingEvidence) || detail.Retry.Rationale != pair.Report.Retry.Rationale || detail.Versions.Collector != pair.Subject.CollectorVersion {
		t.Fatal("projection omitted report explanation or provenance")
	}
	for index, finding := range detail.Findings {
		original := pair.Report.Findings[index]
		if finding.Confidence.Basis != original.Confidence.Basis || finding.Confidence.Score != original.Confidence.Score || len(finding.SupportingEvidence) != len(original.SupportingEvidence) || len(finding.ContradictingEvidence) != len(original.ContradictingEvidence) || len(finding.ContradictingFindings) != len(original.ContradictingFindings) {
			t.Fatal("finding lost confidence or evidence relationships")
		}
	}
	encoded, _ := json.Marshal(projected)
	for _, private := range []string{"private-lease-canary", "private-error-canary", "snapshotFingerprint", "leaseToken", "arguments", "safeToAutomate", "objectId"} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatalf("projection exposed private field %q", private)
		}
	}
	result.Pair = nil
	summary, err := Project(result)
	if err != nil || summary.Detail != nil || summary.ReportID != "" {
		t.Fatal("summary fabricated unverified detail")
	}
	result.Task.State = "failed"
	failed, err := Project(result)
	if err != nil || failed.FailureCode != "analysis_failed" {
		t.Fatal("failure message was not sanitized")
	}
}

func TestReportCitationResolvesOnlyTheExactVerifiedPair(t *testing.T) {
	pair := fixturePair(t)
	result := projectionResult(pair)
	for _, ref := range pair.Report.Citations {
		citation, err := ResolveCitation(result, ref.EvidenceID)
		if err != nil || citation.ID != ref.EvidenceID || citation.ReportID != pair.Report.ReportID || citation.EvidenceID != pair.Evidence.Core.EvidenceID || citation.ValueJSON == "" || citation.BytesBase64 != nil {
			t.Fatalf("item citation failed: %v", err)
		}
	}
	if _, err := ResolveCitation(result, "unlisted-citation"); err == nil {
		t.Fatal("unlisted evidence became a citation")
	}
	pair.Report.Findings[0].Explanation += " tampered"
	if _, err := ResolveCitation(result, pair.Report.Citations[0].EvidenceID); err == nil {
		t.Fatal("tampered report returned citation bytes")
	}
	result = projectionResult(fixturePair(t))
	result.Task.Subject.NamespaceID = result.Task.Subject.JobID
	if _, err := Project(result); err == nil {
		t.Fatal("pair crossed queued source subject")
	}
}

func projectionLogPair(t *testing.T, shrunk bool) Pair {
	t.Helper()
	pair := fixturePair(t)
	core := pair.Evidence.Core
	core.Shared.Profile = diagnostic.SharedProfileIncludeLogTail
	data := []byte("permission denied\n")
	originalEnd := uint64(100 + len(data))
	if shrunk {
		originalEnd += 5
	}
	core.Shared.Logs[0].Bytes = originalEnd
	core.Artifacts = []diagnostic.Artifact{{ID: core.Shared.Logs[0].ID, Role: diagnostic.ArtifactRoleLogTail, Run: core.Shared.Runs[0].Number, Stream: "stderr", MediaType: "application/octet-stream", Data: data, OriginalBytes: originalEnd, ByteStart: 100, ByteEnd: originalEnd, CapturedAt: core.CapturedAt, Quality: diagnostic.QualityConfirmed, Disclosure: diagnostic.DisclosureLogContent, Truncated: true}}
	core.Consistency.Artifacts = diagnostic.ArtifactsStable
	var err error
	core, err = diagnostic.Seal(core)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := deterministic.Prepare(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := deterministic.New(pair.Subject.CompanionVersion, func() time.Time { return core.CapturedAt })
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.Diagnose(t.Context(), evidence)
	if err != nil {
		t.Fatal(err)
	}
	// Explicitly test a whole-artifact citation as well as analyzer-selected
	// enrichment. It remains a fully sealed/validated public report.
	report.Citations = append(report.Citations, diagnosis.Citation{EvidenceID: core.Artifacts[0].ID, Code: core.Artifacts[0].Role, Summary: "Exact selected log tail", Kind: "artifact"})
	report, err = diagnosis.Seal(report)
	if err != nil {
		t.Fatal(err)
	}
	pair.Evidence, pair.Report, pair.Subject.Profile = evidence, report, diagnostic.SharedProfileIncludeLogTail
	if pair.Validate() != nil {
		t.Fatal("invalid synthetic public log pair")
	}
	return pair
}

func TestReportEnrichmentCitationsUseSanitizedRangesAndExactOriginalOffsets(t *testing.T) {
	for _, shrunk := range []bool{false, true} {
		pair := projectionLogPair(t, shrunk)
		result := projectionResult(pair)
		found := false
		for _, ref := range pair.Report.Citations {
			if ref.Kind != "enrichment" && ref.Kind != "artifact" {
				continue
			}
			citation, err := ResolveCitation(result, ref.EvidenceID)
			if err != nil || citation.BytesBase64 == nil || citation.RangeBasis != "sanitized_artifact_bytes" || citation.RunID != pair.Evidence.Core.Shared.Runs[0].ID || citation.ExecutionID != pair.Evidence.Core.Shared.Runs[0].ExecutionID {
				t.Fatalf("bad log citation: %v", err)
			}
			data, err := base64.StdEncoding.DecodeString(*citation.BytesBase64)
			if err != nil || !bytes.Contains(data, []byte("permission denied")) {
				t.Fatal("citation did not resolve exact sealed bytes")
			}
			if ref.Kind == "enrichment" {
				found = true
				if citation.OriginalOffsetsExact == shrunk || (!shrunk && citation.OriginalStartOffset != "100") || (shrunk && citation.OriginalStartOffset != "") {
					t.Fatal("inexact redacted offsets relabeled as source offsets")
				}
			} else if !citation.OriginalOffsetsExact || citation.OriginalStartOffset != "100" {
				t.Fatal("whole artifact lost its original selected range")
			}
		}
		if !found {
			t.Fatal("real deterministic fixture did not cite enrichment")
		}
	}
}

func TestReportProjectionPreservesWideRunNumbersAndRejectsExecutableAdvice(t *testing.T) {
	pair := fixturePair(t)
	core := pair.Evidence.Core
	core.Shared.Runs[0].Number = 9007199254740993
	core.Subject.SelectedRuns[0] = 9007199254740993
	core.Subject.JobRevision = 9007199254740993
	var err error
	core, err = diagnostic.Seal(core)
	if err != nil {
		t.Fatal(err)
	}
	pair.Evidence, err = deterministic.Prepare(t.Context(), core)
	if err != nil {
		t.Fatal(err)
	}
	engine, _ := deterministic.New(pair.Subject.CompanionVersion, func() time.Time { return core.CapturedAt })
	pair.Report, err = engine.Diagnose(t.Context(), pair.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	pair.Subject.Revision = "9007199254740993"
	projected, err := Project(projectionResult(pair))
	if err != nil || projected.SourceRevision != "9007199254740993" || projected.Detail.Runs[0].Number != "9007199254740993" {
		t.Fatal("wide source identity was rounded")
	}
	encoded, _ := json.Marshal(projected)
	if !strings.Contains(string(encoded), `"number":"9007199254740993"`) {
		t.Fatal("wide number used JSON number")
	}
	if len(pair.Report.Actions) == 0 {
		t.Fatal("fixture has no advice")
	}
	pair.Report.Actions[0].Execution = diagnosis.ActionExecutionReadOnly
	pair.Report.Actions[0].Arguments = []string{"jobman", "show", pair.Subject.JobID}
	if _, err := Project(projectionResult(pair)); err == nil {
		t.Fatal("executable advice accepted")
	}
}
