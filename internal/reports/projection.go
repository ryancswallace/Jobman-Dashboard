package reports

import (
	"encoding/base64"
	"slices"
	"strconv"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-diagnose/diagnosis"
	"github.com/ryancswallace/jobman/diagnostic"
)

func decimal(value uint64) string          { return strconv.FormatUint(value, 10) }
func stringsCopy(values []string) []string { return append([]string{}, values...) }

// Project exposes no lease, persistence locator or private cache fingerprint.
// The caller must have completed current authorization; this function verifies
// the stored pair again before projecting any diagnosis content.
func Project(result Result) (api.Report, error) {
	task := result.Task
	if task.Subject.Validate() != nil || !uuidPattern.MatchString(task.ID) || !slices.Contains([]string{"queued", "collecting", "analyzing", "ready", "failed"}, task.State) || task.CreatedAt.IsZero() || !task.ExpiresAt.After(task.CreatedAt) {
		return api.Report{}, ErrObject
	}
	value := api.Report{Scope: task.Subject.Scope, TaskID: task.ID, JobID: task.Subject.JobID, SourceRevision: task.Subject.Revision, RunID: task.Subject.RunID, Profile: task.Subject.Profile, State: task.State, CreatedAt: task.CreatedAt, ExpiresAt: task.ExpiresAt, Outdated: result.Outdated}
	if task.State == "failed" {
		// Only an operator-safe code is emitted, never exception text.
		value.FailureCode = "analysis_failed"
		if slices.Contains([]string{"snapshot_changed", "authorization_unavailable", "source_unavailable", "invalid_evidence", "redaction_unavailable", "timeout"}, task.FailureCode) {
			value.FailureCode = task.FailureCode
		}
	}
	if result.Pair == nil {
		return value, nil
	}
	pair := result.Pair
	if task.State != "ready" || pair.Subject != task.Subject || pair.Validate() != nil || len(pair.Evidence.SourceContext) != 0 {
		return api.Report{}, ErrObject
	}
	report, core := pair.Report, pair.Evidence.Core
	value.ReportID, value.EvidenceID, value.AnalysisEvidenceID = report.ReportID, core.EvidenceID, pair.Evidence.AnalysisEvidenceID
	detail := &api.ReportDetail{
		CapturedAt: core.CapturedAt, GeneratedAt: report.GeneratedAt, ControlInstanceID: core.Shared.Source.ControlInstanceID, ControlVersion: core.Shared.Source.ControlVersion, ContractVersion: core.Shared.Source.ContractVersion,
		Platform: core.Source.Platform, Phase: report.Subject.Phase, Outcome: report.Subject.Outcome,
		Runs: []api.RunReference{}, Mode: string(report.Mode), PrimaryFindingID: report.PrimaryFindingID,
		Versions:  api.ReportVersions{Companion: report.Versions.CompanionVersion, Engine: report.Versions.EngineVersion, Jobman: report.Versions.JobmanVersion, Collector: core.Source.CollectorVersion, EvidenceSchema: report.Versions.EvidenceSchemaVersion, ReportSchema: report.Versions.ReportSchemaVersion, GenerationSchema: report.Versions.GenerationRequestSchemaVersion, ProposalSchema: report.Versions.ProposalSchemaVersion},
		Analyzers: []api.ReportAnalyzer{}, Generators: []api.ReportGenerator{}, Findings: []api.Finding{}, Actions: []api.ReportAction{}, Citations: []api.CitationRef{}, MissingEvidence: []api.ReportMissingEvidence{}, Warnings: []api.ReportWarning{}, Omissions: []api.ReportOmission{}, RedactionNotices: []api.ReportRedaction{},
	}
	for _, run := range core.Shared.Runs {
		detail.Runs = append(detail.Runs, api.RunReference{ID: run.ID, Number: decimal(run.Number), ExecutionID: run.ExecutionID})
	}
	for _, analyzer := range report.Analyzers {
		detail.Analyzers = append(detail.Analyzers, api.ReportAnalyzer{Name: analyzer.Name, Version: analyzer.Version})
	}
	for _, generator := range report.Generators {
		detail.Generators = append(detail.Generators, api.ReportGenerator{Provider: generator.Provider, Model: generator.Model, Profile: generator.Profile, Locality: string(generator.Locality)})
	}
	for _, finding := range report.Findings {
		detail.Findings = append(detail.Findings, api.Finding{ID: finding.ID, Code: finding.Code, Category: finding.Category, Severity: string(finding.Severity), Title: finding.Summary, Explanation: finding.Explanation, Confidence: confidence(finding.Confidence), SupportingEvidence: stringsCopy(finding.SupportingEvidence), ContradictingEvidence: stringsCopy(finding.ContradictingEvidence), ContradictingFindings: stringsCopy(finding.ContradictingFindings), Analyzer: finding.Analyzer})
	}
	for _, action := range report.Actions {
		detail.Actions = append(detail.Actions, api.ReportAction{ID: action.ID, Code: action.Code, Kind: string(action.Kind), Summary: action.Summary, Description: action.Description, SupportingEvidence: stringsCopy(action.SupportingEvidence), RequiresConfirmation: action.RequiresConfirmation})
	}
	detail.Retry = api.ReportRetry{Verdict: string(report.Retry.Verdict), ExistingPolicy: string(report.Retry.ExistingPolicy), Confidence: confidence(report.Retry.Confidence), Rationale: report.Retry.Rationale, Reasons: stringsCopy(report.Retry.Reasons), SupportingEvidence: stringsCopy(report.Retry.SupportingEvidence), EarliestAt: report.Retry.EarliestAt}
	for _, citation := range report.Citations {
		detail.Citations = append(detail.Citations, citationReference(citation))
	}
	for _, missing := range report.MissingEvidence {
		detail.MissingEvidence = append(detail.MissingEvidence, api.ReportMissingEvidence{Code: missing.Code, Description: missing.Description})
	}
	for _, warning := range report.Warnings {
		detail.Warnings = append(detail.Warnings, api.ReportWarning{Code: warning.Code, Message: warning.Message})
	}
	for _, omission := range core.Omissions {
		detail.Omissions = append(detail.Omissions, api.ReportOmission{Code: omission.Code, Affects: stringsCopy(omission.Affects)})
	}
	for _, redaction := range core.RedactionNotices {
		detail.RedactionNotices = append(detail.RedactionNotices, api.ReportRedaction{Code: redaction.Code, Affects: stringsCopy(redaction.Affects), Count: decimal(redaction.Count)})
	}
	d := report.Disclosure
	detail.Disclosure = api.ReportDisclosure{ProviderInvoked: d.ProviderInvoked, GeneratedContentUsed: d.GeneratedContentUsed, Locality: string(d.Locality), Profile: d.Profile, Provider: d.Provider, Model: d.Model, RequestID: d.RequestID, Classes: stringsCopy(d.Classes), ItemIDs: stringsCopy(d.ItemIDs), ArtifactIDs: stringsCopy(d.ArtifactIDs), EnrichmentIDs: stringsCopy(d.EnrichmentIDs), ItemCount: decimal(d.ItemCount), ArtifactCount: decimal(d.ArtifactCount), EnrichmentCount: decimal(d.EnrichmentCount), ArtifactBytes: decimal(d.ArtifactBytes), EnrichmentBytes: decimal(d.EnrichmentBytes), RequestBytes: decimal(d.RequestBytes), RedactionNoticeCount: decimal(d.RedactionNoticeCount)}
	value.Detail = detail
	return value, nil
}

func confidence(value diagnosis.Confidence) api.ReportConfidence {
	return api.ReportConfidence{Score: value.Score, Band: value.Band, Basis: value.Basis}
}
func citationReference(value diagnosis.Citation) api.CitationRef {
	ref := api.CitationRef{ID: value.EvidenceID, Code: value.Code, Label: value.Summary, Kind: value.Kind, SourceEvidenceID: value.SourceEvidenceID}
	if value.Kind == "enrichment" {
		ref.StartOffset, ref.EndOffset = decimal(value.ByteStart), decimal(value.ByteEnd)
	}
	return ref
}

// ResolveCitation only returns a citation present in the validated report's
// join table. Enrichment is sliced from its exact sealed sanitized artifact;
// original source offsets are not invented when redaction changed byte lengths.
func ResolveCitation(result Result, id string) (api.Citation, error) {
	projected, err := Project(result)
	if err != nil {
		return api.Citation{}, err
	}
	if projected.Detail == nil || result.Pair == nil {
		return api.Citation{}, ErrObject
	}
	pair := result.Pair
	var ref *api.CitationRef
	for _, candidate := range projected.Detail.Citations {
		if candidate.ID == id {
			selected := candidate
			ref = &selected
			break
		}
	}
	if ref == nil {
		return api.Citation{}, &api.Error{Code: "not_found_or_inaccessible", Message: "This citation is unavailable."}
	}
	value := api.Citation{CitationRef: *ref, TaskID: result.Task.ID, ReportID: pair.Report.ReportID, EvidenceID: pair.Evidence.Core.EvidenceID, AnalysisEvidenceID: pair.Evidence.AnalysisEvidenceID}
	for _, item := range pair.Evidence.Core.Items {
		if item.ID == id {
			value.ValueJSON, value.SourceEntityID, value.Quality, value.Disclosure, value.ObservedAt = string(item.Value), item.Source.EntityID, string(item.Quality), string(item.Disclosure), item.ObservedAt
			if item.Source.Revision != 0 {
				value.SourceRevision = decimal(item.Source.Revision)
			}
			return value, nil
		}
	}
	artifactID, start, end := id, uint64(0), uint64(0)
	enriched := false
	for _, item := range pair.Evidence.Enrichment {
		if item.ID == id {
			artifactID, start, end, enriched = item.SourceArtifactID, item.ByteStart, item.ByteEnd, true
			value.ObservedAt = &item.ObservedAt
			break
		}
	}
	for _, artifact := range pair.Evidence.Core.Artifacts {
		if artifact.ID != artifactID {
			continue
		}
		if !enriched {
			end = uint64(len(artifact.Data))
		}
		if start > end || end > uint64(len(artifact.Data)) || end-start > diagnostic.SharedMaximumTailBytes {
			return api.Citation{}, ErrObject
		}
		encoded := base64.StdEncoding.EncodeToString(artifact.Data[start:end])
		value.BytesBase64, value.RangeBasis, value.StartOffset, value.EndOffset = &encoded, "sanitized_artifact_bytes", decimal(start), decimal(end)
		value.SourceEvidenceID, value.Stream, value.RunNumber, value.Quality, value.Disclosure, value.CapturedAt = artifact.ID, artifact.Stream, decimal(artifact.Run), string(artifact.Quality), string(artifact.Disclosure), &artifact.CapturedAt
		if !enriched || artifact.ByteEnd-artifact.ByteStart == uint64(len(artifact.Data)) {
			value.OriginalOffsetsExact = true
			originalStart, originalEnd := artifact.ByteStart, artifact.ByteEnd
			if enriched {
				originalStart += start
				originalEnd = artifact.ByteStart + end
			}
			value.OriginalStartOffset, value.OriginalEndOffset = decimal(originalStart), decimal(originalEnd)
		}
		for _, log := range pair.Evidence.Core.Shared.Logs {
			if log.ID == artifact.ID {
				value.RunID, value.ExecutionID = log.RunID, log.ExecutionID
				break
			}
		}
		return value, nil
	}
	return api.Citation{}, ErrObject
}
