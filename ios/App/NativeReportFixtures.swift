#if DEBUG
import Foundation

/// Explicit synthetic UI data only; no provider or authentication is simulated.
enum NativeReportFixtures {
    static let taskID = "90000000-0000-4000-8000-000000000099"
    static func response(path: String, query: [URLQueryItem], timestamp: String) -> [String: Any] {
        let digest = "sha256:" + String(repeating: "a", count: 64)
        let source: [String: Any] = ["deploymentId":"east", "namespaceId":"research", "jobId":"job-042"]
        let confidence: [String: Any] = ["score":85, "band":"high", "basis":"Synthetic fixture observation; not a calibrated probability."]
        let citation: [String: Any] = ["id":"citation-fixture", "code":"jobman.run.exit.code", "label":"Synthetic exit evidence", "kind":"item"]
        var denied = citation; denied["id"] = "citation-denied"; denied["label"] = "Synthetic revoked evidence"
        let finding: [String: Any] = ["id":"finding-fixture", "code":"synthetic_failure", "category":"execution", "severity":"error", "title":"Synthetic execution failed", "explanation":"This is a UI fixture, not an analysis of a real workload.", "confidence":confidence, "supportingEvidence":["citation-fixture"], "contradictingEvidence":["citation-denied"], "contradictingFindings":[], "analyzer":"synthetic-ui-fixture"]
        let disclosure: [String: Any] = ["providerInvoked":false, "generatedContentUsed":false, "locality":"not_used", "classes":[], "itemIds":[], "artifactIds":[], "enrichmentIds":[], "itemCount":"0", "artifactCount":"0", "enrichmentCount":"0", "artifactBytes":"0", "enrichmentBytes":"0", "requestBytes":"0", "redactionNoticeCount":"0"]
        let detail: [String: Any] = ["capturedAt":timestamp, "generatedAt":timestamp, "controlInstanceId":"90000000-0000-4000-8000-000000000003", "controlVersion":"fixture", "contractVersion":"fixture", "platform":"synthetic", "phase":"terminal", "outcome":"failure", "runs":[["id":"run-fixture", "number":"9007199254740993", "executionId":"execution-fixture"]], "versions":["companion":"fixture", "engine":"fixture", "jobman":"fixture", "collector":"fixture", "evidenceSchema":2, "reportSchema":2, "generationSchema":0, "proposalSchema":0], "mode":"deterministic", "primaryFindingId":"finding-fixture", "analyzers":[["name":"synthetic-ui-fixture", "version":"1"]], "generators":[], "findings":[finding], "actions":[["id":"action-fixture", "code":"inspect", "kind":"inspect", "summary":"Inspect authorized logs", "description":"Synthetic advice, shown as text only.", "supportingEvidence":["citation-fixture"], "requiresConfirmation":false]], "retry":["verdict":"after_change", "existingPolicy":"unknown", "confidence":confidence, "rationale":"Resolve the synthetic failure before retrying.", "reasons":["synthetic_failure"], "supportingEvidence":["citation-fixture"]], "citations":[citation,denied], "missingEvidence":[["code":"synthetic_missing", "description":"No actual workload is analyzed in this preview."]], "warnings":[], "disclosure":disclosure, "omissions":[["code":"log_content_not_requested", "affects":["logs"]]], "redactionNotices":[]]
        var report = source
        report.merge(["taskId":taskID, "sourceRevision":"9007199254740993", "profile":"metadata", "state":"ready", "createdAt":timestamp, "expiresAt":"2099-01-01T00:00:00Z", "outdated":true, "reportId":digest, "evidenceId":digest, "analysisEvidenceId":digest, "detail":detail]) { _, new in new }
        if path.contains("/citations/") {
            var value = citation
            value.merge(["taskId":taskID, "reportId":digest, "evidenceId":digest, "analysisEvidenceId":digest, "valueJSON":"17", "quality":"observed", "disclosure":"metadata", "originalOffsetsExact":false, "sourceEntityId":"run-fixture"]) { _, new in new }
            return value
        }
        if path.hasSuffix("/reports") {
            report.removeValue(forKey: "detail")
            let second = query.contains { $0.name == "cursor" }
            if second { report["taskId"] = "90000000-0000-4000-8000-000000000098"; report["state"] = "failed"; report["failureCode"] = "source_unavailable" }
            var result: [String: Any] = ["items":[report], "fetchedAt":timestamp]
            if !second { result["nextCursor"] = "90000000-0000-4000-8000-000000000098" }
            return result
        }
        return report
    }
}
#endif
