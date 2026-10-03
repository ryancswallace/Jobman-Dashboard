import Foundation

@main
struct ContractSmoke {
    static func main() async throws {
        let decoder = JSONDecoder()
        let overview = Data(#"{"active":null,"awaitingExecution":0,"running":0,"evidenceAttention":0,"missingCompletionTime":0,"terminal":{"failure":null},"window":{"from":"2026-10-03T00:00:00Z","to":"2026-10-04T00:00:00Z"},"completeness":"partial","sources":[],"fetchedAt":"2026-10-03T00:00:00Z"}"#.utf8)
        let value = try decoder.decode(DashboardAPI.Overview.self, from: overview)
        precondition(value.active == nil && value.awaitingExecution == 0)
        let missing = Data(String(decoding: overview, as: UTF8.self).replacingOccurrences(of: #""active":null,"#, with: "").utf8)
        do { _ = try decoder.decode(DashboardAPI.Overview.self, from: missing); fatalError("Missing required-nullable field accepted") }
        catch DecodingError.keyNotFound { }
        let job = DashboardAPI.Job(deploymentId: "east", namespaceId: "ns", id: "same", targetId: "t", revision: "9007199254740993", createdAt: "2026-10-03T00:00:00Z", updatedAt: "2026-10-03T00:00:00Z", desiredState: "cancel", phase: "future_phase", confidence: "stale", labels: [:])
        let roundTrip = try decoder.decode(DashboardAPI.Job.self, from: JSONEncoder().encode(job))
        precondition(roundTrip.phase == "future_phase" && roundTrip.outcome == nil && roundTrip.revision == "9007199254740993")
        print("Swift transport smoke: nullable fields, source enums and wide IDs passed")
    }
}
