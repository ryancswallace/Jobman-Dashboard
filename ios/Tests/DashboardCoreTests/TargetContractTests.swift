import Foundation
import Testing
@testable import DashboardCore

private func targetData(_ changes: [String: Any] = [:]) throws -> Data {
    var value: [String: Any] = ["deploymentId":"east", "namespaceId":"team", "targetId":"target/id", "name":"Configured target", "kind":"future-kind", "state":"future-state", "revision":"9007199254740993", "createdAt":"2026-10-03T12:00:00Z", "updatedAt":"2026-10-03T12:00:00Z", "asOf":"2026-10-03T12:00:00Z", "generation":["id":"gen-1", "number":"9007199254740993", "executionBackend":"future-backend", "transport":"agent-api", "runtimes":["native"], "operatingSystems":["linux"], "architectures":["x86_64"], "capabilities":[], "partitions":[["name":"batch", "isDefault":true]], "partitionCount":"201", "partitionsTruncated":true, "artifactStores":[], "provider":["kind":"on-prem"]]]
    value.merge(changes) { _, new in new }; return try JSONSerialization.data(withJSONObject: value)
}
private func targetFixture() throws -> Target { try JSONDecoder().decode(Target.self, from: targetData()) }
@Test func targetGeneratedContractPreservesExactGenerationAndSourceQualifiedPath() throws {
    let target = try targetFixture(); try target.validate()
    #expect(target.generation.number == "9007199254740993")
    #expect(target.revision == "9007199254740993")
    #expect(target.kind == "future-kind")
    #expect(target.path == "/api/v1/deployments/east/namespaces/team/targets/target%2Fid")
    #expect(target.generation.partitionsTruncated)
    let foreign = try JSONDecoder().decode(Target.self, from: targetData(["deploymentId":"west"]))
    #expect(target.id != foreign.id)
}
@Test func targetPartitionPagesBindGenerationAndSourceBeforePublication() throws {
    let target = try targetFixture()
    let source: [String: Any] = ["deploymentId":"east", "namespaceId":"team", "status":"available", "fetchedAt":"2026-10-03T12:00:00Z"]
    let json: [String: Any] = ["targetId":"target/id", "generationId":"gen-1", "items":[["name":"batch", "isDefault":true]], "total":"201", "sources":[source], "completeness":"complete", "fetchedAt":"2026-10-03T12:00:00Z"]
    func page(_ json: [String: Any]) throws -> TargetPartitionPage { try JSONDecoder().decode(TargetPartitionPage.self, from: JSONSerialization.data(withJSONObject: json)) }
    try page(json).validate(target: target)
    for change in [["targetId":"other"], ["generationId":"replacement"], ["total":"0"]] {
        var mutated = json; mutated.merge(change) { _, new in new }
        #expect(throws: DashboardError.invalidResponse) { try page(mutated).validate(target: target) }
    }
    var foreign = source; foreign["deploymentId"] = "west"
    var wrongScope = json; wrongScope["sources"] = [foreign]
    #expect(throws: DashboardError.invalidResponse) { try page(wrongScope).validate(target: target) }
    var duplicate = json; duplicate["items"] = [["name":"batch", "isDefault":true],["name":"batch", "isDefault":true]]
    #expect(throws: DashboardError.invalidResponse) { try page(duplicate).validate(target: target) }
}
@Test func targetReplacementHasExplicitRefreshError() {
    #expect(DashboardError.from(code: "target_changed", status: 409) == .targetChanged)
    #expect(DashboardError.targetChanged.localizedDescription.contains("Refresh the target"))
}
