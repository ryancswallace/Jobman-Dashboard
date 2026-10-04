import Foundation
import Testing
@testable import DashboardCore

private let observation = "2026-10-03T12:00:00Z"
private func decode<T: Decodable>(_ type: T.Type, _ json: [String: Any]) throws -> T {
    try JSONDecoder().decode(type, from: JSONSerialization.data(withJSONObject: json))
}
private func workloadJSON() -> [String: Any] {
    ["id":"same/id", "kind":"graph", "deploymentId":"east", "namespaceId":"team", "createdAt":observation,
     "revision":"9007199254740993", "asOf":observation, "totalChildren":"9007199254740993", "counts":["running":"9007199254740993"],
     "arrayMode":"future_mode", "arrayPolicy":"future_policy", "unsatisfiedPolicy":"block"]
}
private func childJSON(_ id: String, deployment: String = "east") -> [String: Any] {
    ["id":id, "index":"2", "taskIndex":"9007199254740993", "dependencyCounts":["unsatisfied":"9007199254740993"],
     "job":["deploymentId":deployment,"namespaceId":"team","id":id,"targetId":"batch","revision":"1",
            "createdAt":observation,"updatedAt":observation,"desiredState":"run","phase":"terminal","confidence":"current","labels":[:]]]
}
private func edgeJSON() -> [String: Any] {
    ["from":"prepare label", "to":"analyze label", "fromJobId":"a", "toJobId":"b", "predicate":"outcome_in", "outcomes":["success"], "upstreamPhase":"terminal", "upstreamOutcome":"failure", "state":"unsatisfied"]
}
private func neighborhoodJSON() -> [String: Any] {
    ["centerId":"b", "nodes":[childJSON("a"),childJSON("b")], "edges":[edgeJSON()], "totalNodes":"10000", "totalEdges":"20000",
     "omittedNodes":"9998", "omittedEdges":"19999", "completeness":"complete", "sources":[], "fetchedAt":observation]
}

@Test func groupFactsAndChildIndicesMatchGeneratedWireContract() throws {
    let workload = try decode(Workload.self, workloadJSON())
    let generated = try decode(DashboardAPI.Workload.self, workloadJSON())
    #expect(workload.revision == generated.revision)
    #expect(workload.asOf == generated.asOf)
    #expect(workload.totalChildren == "9007199254740993")
    #expect(workload.arrayMode == "future_mode")
    #expect(workload.arrayPolicy == "future_policy")
    #expect(workload.path.hasSuffix("same%2Fid"))
    let child = try decode(WorkloadChild.self, childJSON("a"))
    let generatedChild = try decode(DashboardAPI.WorkloadChild.self, childJSON("a"))
    #expect(child.index == "2")
    #expect(child.taskIndex == generatedChild.taskIndex)
    #expect(child.taskIndex == "9007199254740993")
    #expect(child.dependencyCounts?["unsatisfied"] == "9007199254740993")
    #expect(child.readiness == nil) // Never inferred from counts or local graph layout.
}

@Test func completeSourceTotalsStaySeparateFromDisplayedPageAndUnavailableSources() throws {
    let json: [String: Any] = ["items":[workloadJSON()], "total":"9007199254740993", "totals":[["deploymentId":"east","namespaceId":"team","total":"9007199254740993","asOf":observation]], "completeness":"partial", "sources":[["deploymentId":"west","namespaceId":"team","status":"unavailable","fetchedAt":observation]], "fetchedAt":observation, "nextCursor":"page2"]
    let page = try decode(Page<Workload>.self, json)
    let generated = try decode(DashboardAPI.WorkloadPage.self, json)
    #expect(page.items.count == 1)
    #expect(page.total == generated.total)
    #expect(page.totals?.first?.asOf == observation)
    #expect(page.completeness == "partial")
    #expect(page.sources.first?.status == "unavailable")
}

@Test func graphNeighborhoodChecksCenterScopeEdgesAndDuplicateNodesBeforeLayout() throws {
    let workload = try decode(Workload.self, workloadJSON())
    let json = neighborhoodJSON()
    let neighborhood = try decode(GraphNeighborhood.self, json)
    let generated = try decode(DashboardAPI.GraphNeighborhood.self, json)
    try neighborhood.validate(workload: workload, center: "b")
    #expect(neighborhood.omittedNodes == generated.omittedNodes)
    #expect(neighborhood.nodes[1].dependencyCounts?["unsatisfied"] == "9007199254740993")
    #expect(throws: DashboardError.invalidResponse) { try neighborhood.validate(workload: workload, center: "a") }
    var foreign = json; foreign["nodes"] = [childJSON("a", deployment: "west"), childJSON("b")]
    #expect(throws: DashboardError.invalidResponse) { try decode(GraphNeighborhood.self, foreign).validate(workload: workload, center: "b") }
    var duplicate = json; duplicate["nodes"] = [childJSON("b"),childJSON("b")]
    #expect(throws: DashboardError.invalidResponse) { try decode(GraphNeighborhood.self, duplicate).validate(workload: workload, center: "b") }
    var wrongEdge = edgeJSON(); wrongEdge["fromJobId"] = "wrong-job"
    var inconsistent = json; inconsistent["edges"] = [wrongEdge]
    #expect(throws: DashboardError.invalidResponse) { try decode(GraphNeighborhood.self, inconsistent).validate(workload: workload, center: "b") }
    var oversized = json; oversized["nodes"] = (0..<201).map { childJSON(String($0)) }
    #expect(throws: DashboardError.invalidResponse) { try decode(GraphNeighborhood.self, oversized).validate(workload: workload, center: "b") }
}

@Test func dependencyPagesValidateNodeAndDirectionWithoutInferringReadiness() throws {
    let workload = try decode(Workload.self, workloadJSON()), edge = try decode(GraphEdge.self, edgeJSON())
    try workload.validate(edges: [edge], node: "b", direction: "incoming", sources: [])
    try workload.validate(edges: [edge], node: "a", direction: "outgoing", sources: [])
    try workload.validate(edges: [edge], node: "b", direction: "", sources: [])
    #expect(throws: DashboardError.invalidResponse) { try workload.validate(edges: [edge], node: "b", direction: "outgoing", sources: []) }
    #expect(throws: DashboardError.invalidResponse) { try workload.validate(edges: [edge], node: "absent", direction: "", sources: []) }
    #expect(throws: DashboardError.invalidResponse) { try workload.validate(edges: Array(repeating: edge, count: 501), node: "b", direction: "incoming", sources: []) }
}

@Test func artifactProvenanceIsRequiredAndWideRunNumberRemainsExact() throws {
    var json: [String: Any] = ["id":"artifact1","name":"result.txt","sizeBytes":"9007199254740993","checksum":"sha256:recorded","publishedAt":observation,"availability":"metadata_only", "runId":"run1", "runNumber":"9007199254740993", "executionId":"execution1", "targetGenerationId":"generation1"]
    let artifact = try decode(Artifact.self, json)
    #expect(artifact.runNumber == "9007199254740993")
    #expect(artifact.executionId == "execution1")
    #expect(artifact.availability == "metadata_only")
    json.removeValue(forKey: "executionId")
    #expect(throws: (any Error).self) { try decode(Artifact.self, json) }
}
