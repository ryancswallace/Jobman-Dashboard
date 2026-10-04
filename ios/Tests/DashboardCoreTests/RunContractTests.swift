import Foundation
import Testing
@testable import DashboardCore

private let runDate = "2026-10-04T15:00:00Z"
private let runJob = JobRef(deploymentId: "east", namespaceId: "research", jobId: "job")
private func historicalRun(number: String = "9007199254740993", id: String = "93000000-0000-4000-8000-000000000002") -> DashboardAPI.JobRun {
    .init(id: id, number: number, phase: "future_phase", desiredState: "run", outcome: "future_outcome", createdAt: runDate, updatedAt: runDate, executionId: "94000000-0000-4000-8000-000000000002", executionPhase: "terminal", targetId: "95000000-0000-4000-8000-000000000001", targetGenerationId: "96000000-0000-4000-8000-000000000001", backend: "slurm")
}
private func runPage(_ runs: [DashboardAPI.JobRun]) -> DashboardAPI.RunPage {
    .init(items: runs, completeness: "complete", sources: [.init(deploymentId: "east", namespaceId: "research", status: "available", asOf: runDate, fetchedAt: runDate)], fetchedAt: runDate, total: "2")
}
@Test func runCatalogPreservesWideNumbersUnknownEnumsAndSourceScope() throws {
    let run = historicalRun()
    var page = runPage([run, historicalRun(number: "1", id: "93000000-0000-4000-8000-000000000001")])
    let decoded = try JSONDecoder().decode(DashboardAPI.RunPage.self, from: JSONEncoder().encode(page))
    try decoded.validate(job: runJob)
    #expect(decoded.items[0].number == "9007199254740993")
    #expect(run.logQuery == [URLQueryItem(name: "runNumber", value: "9007199254740993")])
    page.sources[0].deploymentId = "other"
    #expect(throws: DashboardError.self) { try page.validate(job: runJob) }
    #expect(throws: DashboardError.self) { try runPage([run, run]).validate(job: runJob) }
    #expect(throws: DashboardError.self) { try runPage(Array(decoded.items.reversed())).validate(job: runJob) }
    #expect(throws: DashboardError.self) { try historicalRun(number: "01").validate() }
}
@Test func selectedRunRejectsMisattributedFirstLogAndArtifacts() throws {
    let run = historicalRun()
    var wire: [String: Any] = ["bytesBase64":"YQ==", "runId":run.id, "runNumber":run.number, "executionId":run.executionId!, "stream":"stdout", "startOffset":"0", "endOffset":"1", "state":"complete"]
    func chunk() throws -> LogChunk { try JSONDecoder().decode(LogChunk.self, from: JSONSerialization.data(withJSONObject: wire)) }
    try run.validate(log: chunk())
    for field in ["runId", "runNumber", "executionId"] {
        let original = wire[field]; wire[field] = "wrong"
        #expect(throws: DashboardError.self) { try run.validate(log: chunk()) }
        wire.removeValue(forKey: field)
        #expect(throws: DashboardError.self) { try run.validate(log: chunk()) }
        wire[field] = original
    }
    var unassigned = run; unassigned.executionId = nil; unassigned.executionPhase = nil; unassigned.targetId = nil; unassigned.targetGenerationId = nil; unassigned.backend = nil
    wire["executionId"] = ""
    try unassigned.validate(log: chunk())
    wire["executionId"] = run.executionId
    #expect(throws: DashboardError.self) { try unassigned.validate(log: chunk()) }
    var artifact = Artifact(id: "artifact", name: "metadata", sizeBytes: "1", checksum: "sha256:fixture", publishedAt: runDate, availability: "metadata_only", runId: run.id, runNumber: run.number, executionId: run.executionId!, targetGenerationId: "generation")
    try run.validate(artifacts: [artifact])
    artifact.runId = "93000000-0000-4000-8000-000000000001"
    #expect(throws: DashboardError.self) { try run.validate(artifacts: [artifact]) }
}

@Test func runCatalogRejectsMalformedAuthorityAndExecutionProvenance() throws {
    let valid = historicalRun()
    let changes: [(inout DashboardAPI.JobRun) -> Void] = [
        { $0.number = "9223372036854775808" }, { $0.number = "0" },
        { $0.id = "00000000-0000-0000-0000-000000000000" },
        { $0.id = "AAAAAAAA-0000-4000-8000-000000000001" },
        { $0.phase = "" }, { $0.desiredState = String(repeating: "x", count: 65) },
        { $0.executionPhase = nil }, { $0.targetId = nil }, { $0.backend = "" },
        { $0.targetGenerationId = "00000000-0000-0000-0000-000000000000" },
        { $0.executionId = nil }, { $0.confidence = "" }
    ]
    for change in changes {
        var run = valid; change(&run)
        #expect(throws: DashboardError.self) { try run.validate() }
    }
    let pageChanges: [(inout DashboardAPI.RunPage) -> Void] = [
        { $0.completeness = "partial" }, { $0.sources[0].status = "unavailable" },
        { $0.sources[0].asOf = nil }, { $0.sources[0].fetchedAt = "bad" },
        { $0.total = "9223372036854775808" }, { $0.total = "0" },
        { $0.nextCursor = "" }, { $0.nextCursor = String(repeating: "x", count: 513) },
        { $0.nextCursor = "next"; $0.total = "1" },
        { $0.nextCursor = "next"; $0.items = [] }
    ]
    for change in pageChanges {
        var page = runPage([valid]); change(&page)
        #expect(throws: DashboardError.self) { try page.validate(job: runJob) }
    }
    let unassigned = DashboardAPI.JobRun(id: valid.id, number: valid.number, phase: "pending", desiredState: "run", createdAt: runDate, updatedAt: runDate)
    try unassigned.validate(artifacts: [])
    let unexpected = Artifact(id: "a", name: "a", sizeBytes: "1", checksum: "x", publishedAt: runDate, availability: "metadata_only", runId: valid.id, runNumber: valid.number, executionId: valid.executionId!, targetGenerationId: valid.targetGenerationId!)
    #expect(throws: DashboardError.self) { try unassigned.validate(artifacts: [unexpected]) }
}
