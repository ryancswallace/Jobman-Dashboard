import Foundation
import Testing
@testable import DashboardCore

private let reportJob = JobRef(deploymentId: "east", namespaceId: "team", jobId: "job")
private let reportTask = "90000000-0000-4000-8000-000000000099"
private let reportHash = "sha256:" + String(repeating: "a", count: 64)
private func reportFixture() throws -> DiagnosisReport {
    let confidence: [String: Any] = ["score":85, "band":"high", "basis":"Synthetic observation"]
    let detail: [String: Any] = ["capturedAt":"2026-10-03T12:00:00Z", "generatedAt":"2026-10-03T12:01:00Z", "controlInstanceId":"control", "controlVersion":"1", "contractVersion":"1", "platform":"synthetic", "phase":"terminal", "runs":[["id":"run", "number":"9007199254740993", "executionId":"execution"]], "versions":["companion":"1", "engine":"1", "jobman":"1", "collector":"1", "evidenceSchema":2, "reportSchema":2, "generationSchema":0, "proposalSchema":0], "mode":"deterministic", "primaryFindingId":"finding", "analyzers":[], "generators":[], "findings":[["id":"finding", "code":"test", "category":"execution", "severity":"error", "title":"Synthetic failure", "explanation":"Test observation", "confidence":confidence, "supportingEvidence":["citation"], "contradictingEvidence":[], "contradictingFindings":[], "analyzer":"fixture"]], "actions":[], "retry":["verdict":"unknown", "existingPolicy":"unknown", "confidence":confidence, "rationale":"Test only", "reasons":[], "supportingEvidence":[]], "citations":[["id":"citation", "code":"exit", "label":"Synthetic evidence", "kind":"item"]], "missingEvidence":[], "warnings":[], "disclosure":["providerInvoked":false, "generatedContentUsed":false, "locality":"not_used", "classes":[], "itemIds":[], "artifactIds":[], "enrichmentIds":[], "itemCount":"0", "artifactCount":"0", "enrichmentCount":"0", "artifactBytes":"0", "enrichmentBytes":"0", "requestBytes":"0", "redactionNoticeCount":"0"], "omissions":[], "redactionNotices":[]]
    let decoded = try JSONDecoder().decode(DashboardAPI.ReportDetail.self, from: JSONSerialization.data(withJSONObject: detail))
    return DiagnosisReport(deploymentId: "east", namespaceId: "team", taskId: reportTask, jobId: "job", sourceRevision: "9007199254740993", profile: "metadata", state: "ready", createdAt: "2026-10-03T12:00:00Z", expiresAt: "2026-11-03T12:00:00Z", outdated: true, reportId: reportHash, evidenceId: reportHash, analysisEvidenceId: reportHash, detail: decoded)
}

@Test func reportContractBindsSourceTaskAndImmutableEvidenceWithoutRounding() throws {
    let report = try reportFixture(); try report.validate(job: reportJob, task: reportTask)
    #expect(report.id == reportTask)
    #expect(report.reportId != report.id)
    #expect(report.sourceRevision == "9007199254740993")
    #expect(report.detail?.runs[0].number == "9007199254740993")
    #expect(report.outdated && report.state == "ready")
    for job in [JobRef(deploymentId: "west", namespaceId: "team", jobId: "job"), JobRef(deploymentId: "east", namespaceId: "other", jobId: "job"), JobRef(deploymentId: "east", namespaceId: "team", jobId: "different")] {
        #expect(throws: DashboardError.invalidEvidence) { try report.validate(job: job) }
    }
    #expect(throws: DashboardError.invalidEvidence) { try report.validate(job: reportJob, task: "other-task") }
    var wrong = report; wrong.reportId = "invented"
    #expect(throws: DashboardError.invalidEvidence) { try wrong.validate(job: reportJob) }
    wrong = report; wrong.detail!.findings[0].supportingEvidence = ["foreign-citation"]
    #expect(throws: DashboardError.invalidEvidence) { try wrong.validate(job: reportJob) }
    wrong = report; wrong.detail!.findings[0].contradictingFindings = ["foreign-finding"]
    #expect(throws: DashboardError.invalidEvidence) { try wrong.validate(job: reportJob) }
}

@Test func reportPagesAreBoundedAndDoNotMixVerifiedDetailOrDuplicateTasks() throws {
    var summary = try reportFixture(); summary.detail = nil
    let page = DashboardAPI.ReportPage(items: [summary], fetchedAt: "2026-10-03T12:00:00Z")
    try page.validate(job: reportJob)
    var wrong = page; wrong.items = [summary,summary]
    #expect(throws: DashboardError.invalidResponse) { try wrong.validate(job: reportJob) }
    wrong = page; wrong.items = [try reportFixture()]
    #expect(throws: DashboardError.invalidResponse) { try wrong.validate(job: reportJob) }
    wrong = page; wrong.nextCursor = "not-a-task-cursor"
    #expect(throws: DashboardError.invalidResponse) { try wrong.validate(job: reportJob) }
}

@Test func sealedCitationValidatesOriginalIDsAndPreservesWideItemJSON() throws {
    let report = try reportFixture(), reference = report.detail!.citations[0]
    let citation = DashboardAPI.Citation(id: reference.id, code: reference.code, label: reference.label, kind: reference.kind, taskId: reportTask, reportId: reportHash, evidenceId: reportHash, analysisEvidenceId: reportHash, valueJSON: "9007199254740993", originalOffsetsExact: false, quality: "observed", disclosure: "metadata")
    #expect(try citation.validatedBytes(report: report, reference: reference) == nil)
    #expect(citation.valueJSON == "9007199254740993")
    var wrong = citation; wrong.evidenceId = "sha256:" + String(repeating: "b", count: 64)
    #expect(throws: DashboardError.invalidEvidence) { try wrong.validatedBytes(report: report, reference: reference) }
    wrong = citation; wrong.taskId = "other"
    #expect(throws: DashboardError.invalidEvidence) { try wrong.validatedBytes(report: report, reference: reference) }
}

@Test func enrichmentCitationBytesUseOnlyExactSanitizedArtifactRange() throws {
    var report = try reportFixture()
    let reference = DashboardAPI.CitationRef(id: "enrichment", code: "permission_denied", label: "Failure evidence", kind: "enrichment", sourceEvidenceId: "artifact", startOffset: "2", endOffset: "5")
    report.detail!.citations.append(reference)
    let citation = DashboardAPI.Citation(id: reference.id, code: reference.code, label: reference.label, kind: reference.kind, sourceEvidenceId: "artifact", startOffset: "2", endOffset: "5", taskId: reportTask, reportId: reportHash, evidenceId: reportHash, analysisEvidenceId: reportHash, bytesBase64: Data([0xff, 0x00, 0x41]).base64EncodedString(), rangeBasis: "sanitized_artifact_bytes", originalStartOffset: "102", originalEndOffset: "105", originalOffsetsExact: true, runId: "run", runNumber: "9007199254740993", executionId: "execution", stream: "stderr", quality: "confirmed", disclosure: "log_content")
    #expect(try citation.validatedBytes(report: report, reference: reference) == Data([0xff,0x00,0x41]))
    for change in ["end", "original", "source", "run", "bytes"] {
        var wrong = citation
        switch change {
        case "end": wrong.endOffset = "6"
        case "original": wrong.originalEndOffset = "106"
        case "source": wrong.sourceEvidenceId = "foreign-artifact"
        case "run": wrong.runId = "foreign-run"
        default: wrong.bytesBase64 = "invalid-base64"
        }
        #expect(throws: DashboardError.invalidEvidence) { try wrong.validatedBytes(report: report, reference: reference) }
    }
}

@Test func reportSourceChangesAndMissingRedactionHaveActionableErrors() {
    #expect(DashboardError.from(code: "snapshot_changed", status: 409) == .snapshotChanged)
    #expect(DashboardError.from(code: "redaction_unavailable", status: 503) == .redactionUnavailable)
    #expect(DashboardError.redactionUnavailable.localizedDescription.contains("report with job details only"))
}

@Test func reportRetryKeysDistinguishUncertainTransportFromDefinitiveRejection() {
    for error in [DashboardError.network, .sourceUnavailable, .authorizationUnavailable, .rateLimited] { #expect(error.retainsReportRequestKey) }
    for code in ["snapshot_changed", "revision_conflict", "invalid_request", "forbidden", "not_found_or_inaccessible"] {
        #expect(!DashboardError.from(code: code, status: 400).retainsReportRequestKey)
    }
}
