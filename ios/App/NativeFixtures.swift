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

    static func data(path: String, query: [URLQueryItem] = []) throws -> Data { try JSONSerialization.data(withJSONObject: response(path: path, query: query)) }
    static func response(path: String, query: [URLQueryItem] = []) -> [String: Any] {
        func value(_ name: String) -> String? { query.first { $0.name == name }?.value }
        let stream = value("stream") ?? "stdout"
        let now = Date()
        let date: (Date) -> String = { ISO8601DateFormatter().string(from: $0) }
        let sources: [[String: Any]] = ["east", "west"].map { ["deploymentId": $0, "namespaceId": "research", "status": "available", "asOf": date(now), "fetchedAt": date(now)] }
        func page(_ items: [[String: Any]]) -> [String: Any] { ["items": items, "completeness": "complete", "sources": sources, "fetchedAt": date(now), "total": String(items.count)] }
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
            let bytes = Data("Synthetic \(stream) log fixture\nAlignment exited with status 2.\nNo production data is shown.\n".utf8)
            return ["bytesBase64":bytes.base64EncodedString(),"executionId":"execution-fixture","runId":"run-fixture","stream":stream,"startOffset":"0","endOffset":String(bytes.count),"state":"active","nextCursor":"expired-fixture-cursor","runNumber":"9007199254740993","capturedAt":date(now)]
        }
        if path.hasSuffix("/artifacts") { return page([["id":"artifact-fixture","name":"synthetic-summary.txt","sizeBytes":"120","checksum":"synthetic-checksum","availability":"metadata_only","publishedAt":date(now),"runNumber":"9007199254740993","runId":"run-fixture","executionId":"execution-fixture","targetGenerationId":"generation-fixture"]]) }
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
        if path.contains("/jobs/") { return ["job":job(path.contains("/west/") ? "west":"east", path.split(separator: "/").last.map(String.init) ?? "job-042"),"fetchedAt":date(now)] }
        if path == "/api/v1/targets" { return page([["deploymentId":"east","namespaceId":"research","targetId":"slurm-batch","name":"Synthetic Slurm","state":"active","provider":"native","backend":"slurm","generation":"1","capabilities":["collections","arrays"]]]) }
        if path.contains("/workloads/") {
            let kind = path.contains("/graph") ? "graph" : path.contains("/array") ? "array" : "collection"
            let workload: [String: Any] = ["id":"workload-fixture","kind":kind,"name":"Synthetic \(kind)","deploymentId":"east","namespaceId":"research","createdAt":date(now),"revision":"9007199254740993","asOf":date(now),"totalChildren":"5","counts":["success":"1","failure":"1","blocked":"3"],"concurrency":"4","failurePolicy":"continue","arrayPolicy":"prefer_array","arrayMode":"native","arrayId":"12345","unsatisfiedPolicy":"block"]
            if path.hasPrefix("/api/v1/workloads/") {
                var result = page([workload]); result["totals"] = [["deploymentId":"east","namespaceId":"research","total":"1","asOf":date(now)]]
                return result
            }
            let scopedSources = sources.filter { $0["deploymentId"] as? String == "east" }
            let children: [[String: Any]] = (0..<5).map { index -> [String: Any] in
                let taskIndex = index == 1 ? "9007199254740993" : String(index * 2)
                let dependencies: [String: String] = ["satisfied":"0", "unsatisfied":index == 1 ? "2" : "0"]
                return ["id":"node-\(index)", "job":job("east", "node-\(index)"), "index":String(index),
                 "disposition": index == 0 ? "ready" : "waiting", "taskIndex": taskIndex,
                 "dependencyCounts": dependencies]
            }
            func edge(_ from: Int, _ to: Int) -> [String: Any] {
                ["from":"stage-\(from)","to":"stage-\(to)","fromJobId":"node-\(from)","toJobId":"node-\(to)","predicate":"outcome_in","outcomes":["success"],"upstreamPhase":"terminal","upstreamOutcome":"failure","state":"unsatisfied"]
            }
            let edges = [edge(0,1), edge(2,1), edge(1,3), edge(1,4)]
            if path.hasSuffix("/neighborhood") {
                let center = value("nodeId") ?? "node-0"
                let index = Int(center.replacingOccurrences(of: "node-", with: "")) ?? 0
                let selected = Set([0,1,index == 0 || index == 1 ? 2 : index])
                let nodes = children.filter { selected.contains(Int($0["index"] as! String)!) }
                let ids = Set(nodes.map { $0["id"] as! String })
                let included = edges.filter { ids.contains($0["fromJobId"] as! String) && ids.contains($0["toJobId"] as! String) }
                return ["centerId":center,"nodes":nodes,"edges":included,"totalNodes":"5","totalEdges":"4","omittedNodes":String(5-nodes.count),"omittedEdges":String(4-included.count),"completeness":"complete","sources":scopedSources,"fetchedAt":date(now)]
            }
            if path.hasSuffix("/dependencies") {
                let node = value("nodeId") ?? "node-0", direction = value("direction") ?? "both"
                let filtered = edges.filter { edge in
                    let from = edge["fromJobId"] as! String, to = edge["toJobId"] as! String
                    return direction == "incoming" ? to == node : direction == "outgoing" ? from == node : from == node || to == node
                }
                let position = Int((value("cursor") ?? "edge-0").replacingOccurrences(of: "edge-", with: "")) ?? 0
                var result: [String: Any] = ["items": Array(filtered.dropFirst(position).prefix(1)), "total":String(filtered.count),"sources":scopedSources,"completeness":"complete","fetchedAt":date(now)]
                if position + 1 < filtered.count { result["nextCursor"] = "edge-\(position + 1)" }
                return result
            }
            let visible = value("cursor") == nil ? Array(children.prefix(3)) : Array(children.suffix(2))
            var result: [String: Any] = ["sources":scopedSources,"completeness":"complete","fetchedAt":date(now),"total":"5"]
            if value("cursor") == nil { result["nextCursor"] = "children-next" }
            if path.hasSuffix("/children") { result["items"] = visible }
            else { result["workload"] = workload; result["children"] = visible }
            return result
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
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
            let expired = request.url!.path.hasSuffix("/logs") && query.contains { $0.name == "cursor" }
            let status = request.httpMethod != "GET" ? 422 : expired ? 409 : 200
            let data: Data
            if expired { data = try JSONSerialization.data(withJSONObject: ["error":["code":"cursor_expired","message":"Synthetic cursor expired."]]) }
            else if request.httpMethod == "GET" { data = try NativeFixtures.data(path: request.url!.path, query: query) }
            else { data = try JSONSerialization.data(withJSONObject: ["code":"fixture_read_only","message":"Synthetic previews do not mutate real services."]) }
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() {}
}
#endif
