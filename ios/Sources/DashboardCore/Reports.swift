import Foundation

extension DashboardError {
    /// An uncertain transport failure keeps its key so retry cannot duplicate
    /// admitted work. Definitive rejection requires a fresh request intent.
    public var retainsReportRequestKey: Bool {
        switch self {
        case .snapshotChanged, .revisionConflict, .forbidden, .notFound, .server("invalid_request"): false
        default: true
        }
    }
}

public typealias DiagnosisReport = DashboardAPI.Report
extension DashboardAPI.Report: Identifiable {
    public var id: String { taskId }
    public var pending: Bool { ["queued", "collecting", "analyzing"].contains(state) }
    public func validate(job: JobRef, task: String? = nil) throws {
        guard deploymentId == job.deploymentId, namespaceId == job.namespaceId, jobId == job.jobId,
              UUID(uuidString: taskId) != nil, task == nil || task == taskId,
              let revision = UInt64(sourceRevision), revision > 0, String(revision) == sourceRevision,
              runId == nil || UUID(uuidString: runId!) != nil else { throw DashboardError.invalidEvidence }
        guard let detail else { return }
        guard state == "ready", [reportId, evidenceId, analysisEvidenceId].allSatisfy({ reportDigest($0) }),
              detail.runs.count <= 32, Set(detail.runs.map(\.id)).count == detail.runs.count,
              Set(detail.findings.map(\.id)).count == detail.findings.count,
              Set(detail.citations.map(\.id)).count == detail.citations.count,
              detail.findings.contains(where: { $0.id == detail.primaryFindingId }) else { throw DashboardError.invalidEvidence }
        let citations = Set(detail.citations.map(\.id)), findings = Set(detail.findings.map(\.id))
        for finding in detail.findings {
            guard (0...100).contains(finding.confidence.score), finding.supportingEvidence.allSatisfy(citations.contains),
                  finding.contradictingEvidence.allSatisfy(citations.contains), finding.contradictingFindings.allSatisfy(findings.contains) else { throw DashboardError.invalidEvidence }
        }
        guard detail.actions.allSatisfy({ $0.supportingEvidence.allSatisfy(citations.contains) }),
              detail.retry.supportingEvidence.allSatisfy(citations.contains), (0...100).contains(detail.retry.confidence.score) else { throw DashboardError.invalidEvidence }
        for run in detail.runs {
            guard let number = UInt64(run.number), number > 0, String(number) == run.number else { throw DashboardError.invalidEvidence }
        }
    }
}
extension DashboardAPI.ReportPage {
    public func validate(job: JobRef) throws {
        guard items.count <= 20, Set(items.map(\.taskId)).count == items.count,
              nextCursor == nil || UUID(uuidString: nextCursor!) != nil else { throw DashboardError.invalidResponse }
        for item in items { try item.validate(job: job); guard item.detail == nil else { throw DashboardError.invalidResponse } }
    }
}
extension DashboardAPI.Finding: Identifiable {}
extension DashboardAPI.CitationRef: Identifiable {}
extension DashboardAPI.ReportAction: Identifiable {}

private func reportDigest(_ value: String?) -> Bool {
    guard let value, value.hasPrefix("sha256:"), value.count == 71 else { return false }
    return value.dropFirst(7).allSatisfy { ("0"..."9").contains(String($0)) || ("a"..."f").contains(String($0)) }
}
extension DashboardAPI.Citation {
    public func validatedBytes(report: DiagnosisReport, reference: DashboardAPI.CitationRef) throws -> Data? {
        guard taskId == report.taskId, reportId == report.reportId, evidenceId == report.evidenceId,
              analysisEvidenceId == report.analysisEvidenceId, id == reference.id, kind == reference.kind, code == reference.code,
              report.detail?.citations.contains(where: { $0.id == reference.id && $0.kind == reference.kind && $0.code == reference.code && $0.sourceEvidenceId == reference.sourceEvidenceId && $0.startOffset == reference.startOffset && $0.endOffset == reference.endOffset }) == true else { throw DashboardError.invalidEvidence }
        if kind == "item" {
            guard let valueJSON, bytesBase64 == nil, !valueJSON.isEmpty else { throw DashboardError.invalidEvidence }
            return nil
        }
        guard let encoded = bytesBase64, let data = Data(base64Encoded: encoded), data.count <= 65_536,
              rangeBasis == "sanitized_artifact_bytes", let startOffset, let endOffset,
              let start = UInt64(startOffset), let end = UInt64(endOffset), end >= start,
              end - start == data.count, let sourceEvidenceId,
              let detail = report.detail, detail.runs.contains(where: { $0.id == runId && $0.number == runNumber && $0.executionId == executionId }) else { throw DashboardError.invalidEvidence }
        if kind == "enrichment" {
            guard sourceEvidenceId == reference.sourceEvidenceId, startOffset == reference.startOffset, endOffset == reference.endOffset else { throw DashboardError.invalidEvidence }
        } else {
            guard kind == "artifact", sourceEvidenceId == id, start == 0 else { throw DashboardError.invalidEvidence }
        }
        if originalOffsetsExact {
            guard let originalStartOffset, let originalEndOffset, let originalStart = UInt64(originalStartOffset), let originalEnd = UInt64(originalEndOffset), originalEnd >= originalStart else { throw DashboardError.invalidEvidence }
            if kind == "enrichment" && originalEnd - originalStart != data.count { throw DashboardError.invalidEvidence }
        } else if originalStartOffset != nil || originalEndOffset != nil { throw DashboardError.invalidEvidence }
        return data
    }
}
