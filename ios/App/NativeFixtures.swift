#if DEBUG
import DashboardCore
import Foundation

/// Activated only by an explicit DEBUG UI-test launch argument; every screen carries a fixture banner.
enum NativeFixtures {
    static func client() -> DashboardTransport {
        let config = URLSessionConfiguration.ephemeral
        config.urlCache = nil
        config.httpCookieStorage = nil
        config.protocolClasses = [FixtureProtocol.self]
        return .testing(connection: try! DashboardConnection(address: "https://dashboard-fixtures.example.test"), session: URLSession(configuration: config))
    }

    static func data(path: String) throws -> Data { try JSONSerialization.data(withJSONObject: response(path: path)) }
    static func response(path: String) -> [String: Any] {
        let now = Date()
        let date: (Date) -> String = { ISO8601DateFormatter().string(from: $0) }
        let sources: [[String: Any]] = ["east", "west"].map { ["deploymentId": $0, "namespaceId": "research", "status": "available", "asOf": date(now), "fetchedAt": date(now)] }
        func page(_ items: [[String: Any]]) -> [String: Any] { ["items": items, "completeness": "complete", "sources": sources, "fetchedAt": date(now)] }
        func job(_ deployment: String = "east", _ id: String = "job-042", phase: String = "terminal", outcome: String = "failure") -> [String: Any] {
            ["deploymentId": deployment, "namespaceId": "research", "id": id, "name": deployment == "east" ? "Synthetic alignment run" : "Synthetic index build", "targetId": "slurm-batch", "backend": "slurm", "revision": "42", "owner": ["id":"synthetic-user", "displayName":"Fixture researcher", "isCurrentUser":true], "createdAt": date(now.addingTimeInterval(-1800)), "updatedAt": date(now.addingTimeInterval(-60)), "completedAt": date(now.addingTimeInterval(-60)), "desiredState":"run", "phase":phase, "outcome":outcome, "confidence":"current", "labels":["dataset":"synthetic"], "scheduler":["state":"FAILED", "jobId":"1001"]]
        }
        let jobRef: [String: Any] = ["deploymentId":"east", "namespaceId":"research", "jobId":"job-042"]
        if path == "/api/v1/bootstrap" {
            let deployments: [[String: Any]] = ["east", "west"].map { id in
                ["id":id, "name":"Synthetic \(id.capitalized)", "status":"available", "namespaces":[["id":"research", "name":"Research", "roles":["viewer","operator"], "capabilities":["jobs.read","logs.read","reports.read"], "authorizationVersion":"1", "authorizationCheckedAt":date(now), "authorizationExpiresAt":date(now.addingTimeInterval(120))]]]
            }
            return ["apiVersion":"jobman.dashboard/v1", "account":["id":"fixture-account", "displayName":"Synthetic researcher"], "deployments":deployments,
                    "preferences":["revision":"1", "timezone":"UTC", "appearance":"system", "refreshSeconds":5],
                    "limits":["defaultPageSize":50,"maxPageSize":200,"logReadBytes":262144,"logBufferBytes":2097152,"graphNodes":200,"graphEdges":500], "completeness":"complete", "fixtureMode":true]
        }
        if path == "/api/v1/overview" { return ["active":3,"awaitingExecution":2,"running":1,"evidenceAttention":0,"missingCompletionTime":0,"terminal":["success":7,"failure":1,"cancelled":0,"timed_out":0,"aborted":0,"lost":0,"unknown":0],"window":["from":date(now.addingTimeInterval(-86400)),"to":date(now)],"sources":sources,"completeness":"complete","fetchedAt":date(now)] }
        if path == "/api/v1/jobs" { return page([job(),job("west")]) }
        if path.hasSuffix("/logs") {
            let bytes = Data("Synthetic log fixture\nAlignment exited with status 2.\nNo production data is shown.\n".utf8)
            return ["bytesBase64":bytes.base64EncodedString(),"executionId":"execution-fixture","runId":"run-fixture","stream":"stdout","startOffset":"0","endOffset":String(bytes.count),"state":"complete","capturedAt":date(now)]
        }
        if path.hasSuffix("/artifacts") { return page([["id":"artifact-fixture","name":"synthetic-summary.txt","sizeBytes":"120","checksum":"synthetic-checksum","availability":"published","publishedAt":date(now)]]) }
        if path.contains("/citations/") { return ["id":"citation-fixture","label":"Synthetic exit evidence","text":"Synthetic source observation: exited with status 2.","startOffset":"0","endOffset":"48"] }
        if path.hasSuffix("/reports") {
            let finding: [String: Any] = ["id":"finding-fixture","severity":"error","title":"Synthetic execution failed",
                                           "explanation":"This is a UI fixture for report presentation, not a generated diagnosis of a real workload.",
                                           "confidence":1.0,"confidenceBasis":"Fixture observation",
                                           "citations":[["id":"citation-fixture","label":"Synthetic exit evidence"]],
                                           "suggestions":["Inspect the authorized job log."]]
            let report: [String: Any] = ["id":"report-fixture","state":"ready","createdAt":date(now),"sourceRevision":"42",
                                          "evidenceId":"evidence-fixture","engineVersion":"synthetic-ui-fixture",
                                          "disclosure":"metadata only","findings":[finding],"missingEvidence":[]]
            return page([report])
        }
        if path.contains("/jobs/") { return ["job":job(path.contains("/west/") ? "west":"east"),"fetchedAt":date(now)] }
        if path == "/api/v1/targets" { return page([["deploymentId":"east","namespaceId":"research","targetId":"slurm-batch","name":"Synthetic Slurm","state":"active","provider":"native","backend":"slurm","generation":"1","capabilities":["collections","arrays"]]]) }
        if path.contains("/workloads/") {
            let kind = path.contains("/graph") ? "graph" : path.contains("/array") ? "array" : "collection"
            let workload: [String: Any] = ["id":"workload-fixture","kind":kind,"name":"Synthetic \(kind)","deploymentId":"east","namespaceId":"research","createdAt":date(now),"totalChildren":"3","counts":["success":"1","failure":"1","blocked":"1"],"concurrency":"4","failurePolicy":"continue"]
            if path.hasPrefix("/api/v1/workloads/") { return page([workload]) }
            let children: [[String: Any]] = (0..<3).map { index in
                var dependencies: [[String: String]] = []
                if index > 0 { dependencies.append(["upstreamNodeId":"node-\(index - 1)", "predicate":"success", "status":"unsatisfied"]) }
                let item: [String: Any] = ["id":"node-\(index)", "job":job("east", "job-\(index)"),
                                           "readiness":index == 0 ? "ready" : "waiting", "taskIndex":String(index * 2),
                                           "dependencies": dependencies]
                return item
            }
            return ["workload":workload,"children":children,"sources":sources,"completeness":"complete","fetchedAt":date(now)]
        }
        if path == "/api/v1/rules" { return page([["id":"rule-fixture","revision":"1","name":"Synthetic failures","enabled":true,"scope":"namespace_jobs","namespaces":[["deploymentId":"east","namespaceId":"research"]],"jobs":[],"outcomeMode":"selected","outcomes":["failure","timed_out","aborted","lost"],"activation":[["deploymentId":"east","status":"active"]]]]) }
        let inbox: [String: Any] = ["id":"inbox-fixture","job":jobRef,"outcome":"failure","eventAt":date(now),"createdAt":date(now),"read":false,"matchedRules":["Synthetic failures"],"deliveryStatus":"fixture only"]
        if path == "/api/v1/inbox" { return page([inbox]) }
        if path.hasPrefix("/api/v1/inbox/") { return inbox }
        if path == "/api/v1/devices" { return page([["id":"device-fixture","name":"Synthetic iPhone","enabled":false,"permission":"not_requested"]]) }
        return ["code":"not_found_or_inaccessible","message":"No synthetic fixture is defined for this request."]
    }
}

private final class FixtureProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "dashboard-fixtures.example.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        do {
            let status = request.httpMethod == "GET" ? 200 : 422
            let data = try request.httpMethod == "GET" ? NativeFixtures.data(path: request.url!.path) : JSONSerialization.data(withJSONObject: ["code":"fixture_read_only","message":"Synthetic previews do not mutate real services."])
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() {}
}
#endif
