import Foundation
import Testing
@testable import DashboardCore

private let jobJSON = #"{"deploymentId":"east","namespaceId":"team","id":"same-id","targetId":"batch","revision":"9007199254740993","createdAt":"2026-10-03T12:00:00Z","updatedAt":"2026-10-03T12:00:01Z","desiredState":"cancel","phase":"future_phase","outcome":"future_outcome","confidence":"stale","labels":{},"newAdditiveField":"ignored"}"#

@Test func unknownEnumsAndLargeRevisionsSurviveDecoding() throws {
    let job = try JSONDecoder().decode(Job.self, from: Data(jobJSON.utf8))
    #expect(job.phase == "future_phase")
    #expect(job.outcome == "future_outcome")
    #expect(job.revision == "9007199254740993")
    #expect(job.startedAt == nil)
    #expect(job.completedAt == nil)
    #expect(job.confidence == "stale")
}

@Test func partialSummaryDoesNotConvertUnknownContributionsToZero() throws {
    let json = #"{"active":null,"awaitingExecution":null,"terminal":{"success":null,"failure":0},"window":{"from":"2026-10-02T00:00:00Z","to":"2026-10-03T00:00:00Z"},"sources":[],"completeness":"partial","fetchedAt":"2026-10-03T12:00:00Z"}"#
    let result = try JSONDecoder().decode(Overview.self, from: Data(json.utf8))
    #expect(result.active == nil)
    #expect(result.terminal["success"]! == nil)
    #expect(result.terminal["failure"]! == 0)
    #expect(result.completeness == "partial")
}

@Test func reportConfidenceAndCitationRemainTypedReferences() throws {
    let confidence = try JSONDecoder().decode(DashboardAPI.ReportConfidence.self, from: Data(#"{"score":75,"band":"high","basis":"Fixture"}"#.utf8))
    let citation = try JSONDecoder().decode(DashboardAPI.CitationRef.self, from: Data(#"{"id":"c1","code":"exit","label":"Evidence","kind":"item"}"#.utf8))
    #expect(confidence.score == 75)
    #expect(citation.sourceEvidenceId == nil)
}

@Test func duplicateWorkloadIDsRemainSeparateAcrossDeployments() throws {
    let first = #"{"id":"same","kind":"graph","deploymentId":"east","namespaceId":"team","createdAt":"2026-10-03T12:00:00Z","totalChildren":"10000","counts":{"running":"1"},"revision":"1","asOf":"2026-10-03T12:00:00Z"}"#
    let second = first.replacingOccurrences(of: "east", with: "west")
    let a = try JSONDecoder().decode(Workload.self, from: Data(first.utf8))
    let b = try JSONDecoder().decode(Workload.self, from: Data(second.utf8))
    #expect(a.resourceId == b.resourceId)
    #expect(a.id != b.id)
    #expect(a.path != b.path)
}

@Test func explicitScopeDoesNotAddNewlyGrantedNamespaces() throws {
    let a = NamespaceRef(deploymentId: "east", namespaceId: "team")
    let b = NamespaceRef(deploymentId: "west", namespaceId: "team")
    let query = try DashboardScope.namespace(a).queryItems(authorized: [a, b])
    let decoded = try JSONDecoder().decode([NamespaceRef].self, from: Data(query[0].value!.utf8))
    #expect(decoded == [a])
    let rule = AlertRule(name: "Explicit", namespaces: [a])
    let value = try JSONSerialization.jsonObject(with: JSONEncoder().encode(rule.input)) as! [String: Any]
    #expect(value["id"] == nil)
    #expect(value["revision"] == nil)
    #expect(value["activation"] == nil)
}

@Test func wireDatesAcceptFractionalSecondsWithoutChangingLifecycleMeaning() {
    #expect(WireDate.parse("2026-10-03T12:00:00.123456Z") != nil)
    #expect(WireDate.parse("2026-10-03T12:00:00Z") != nil)
    #expect(WireDate.parse("unknown") == nil)
}

@Test func lifecycleAndRunFactsRemainDistinctFromMetadataAndGroupPosition() throws {
    var json = try JSONSerialization.jsonObject(with: Data(jobJSON.utf8)) as! [String: Any]
    json["currentRun"] = ["id": "run1", "number": "9007199254740993", "executionId": "execution1"]
    json["lifecycle"] = ["completedRecordedAt": "2026-10-03T12:00:04Z", "completedProvenance": "agent"]
    json["group"] = ["collectionId": "collection1", "collectionIndex": 42, "graphId": "graph1", "graphIndex": 7]
    json["targetGenerationId"] = "generation1"
    json["scheduler"] = ["state": "FAILED", "reason": "Synthetic reason", "observedAt": "2026-10-03T12:00:02Z"]
    let job = try JSONDecoder().decode(Job.self, from: JSONSerialization.data(withJSONObject: json))
    #expect(job.currentRun?.number == "9007199254740993")
    #expect(job.completedAt == nil)
    #expect(job.lifecycle?.completedRecordedAt != job.updatedAt)
    #expect(job.lifecycle?.completedProvenance == "agent")
    #expect(job.group?.collectionIndex == 42)
    #expect(job.group?.graphIndex == 7)
    #expect(job.targetGenerationId == "generation1")
    #expect(job.scheduler?.reason == "Synthetic reason")
}
