#if DEBUG
import Foundation

/// Explicit DEBUG-only, changing responses exercise the real native transport.
final class NativeRefreshFixtures: @unchecked Sendable {
    static let server = NativeRefreshFixtures()
    private let lock = NSLock()
    private var counts: [String: Int] = [:]
    private var delayedPageStarted = false
    private var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--dashboard-refresh-fixtures") }
    func response(_ request: URLRequest) throws -> Data? {
        guard (enabled || ProcessInfo.processInfo.arguments.contains("--dashboard-page-authorization-change")), request.httpMethod == "GET", let url = request.url else { return nil }
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let cursor = query.first { $0.name == "cursor" }?.value
        let path = url.path
        if ProcessInfo.processInfo.arguments.contains("--dashboard-page-authorization-change") {
            if path == "/api/v1/jobs", cursor != nil { lock.withLock { delayedPageStarted = true }; return nil }
            let changed = lock.withLock { delayedPageStarted }
            if path == "/api/v1/bootstrap" {
                var value = NativeFixtures.response(path: path, query: query)
                var deployments = value["deployments"] as! [[String: Any]]
                for index in deployments.indices {
                    var namespaces = deployments[index]["namespaces"] as! [[String: Any]]
                    for ns in namespaces.indices { namespaces[ns]["authorizationVersion"] = changed ? "2" : "1" }
                    deployments[index]["namespaces"] = namespaces
                }
                value["deployments"] = deployments
                return try JSONSerialization.data(withJSONObject: value)
            }
            if path == "/api/v1/jobs", changed {
                var value = NativeFixtures.response(path: path, query: query)
                var jobs = value["items"] as! [[String: Any]]
                jobs[0]["name"] = "Authorized refreshed job"; value["items"] = jobs
                return try JSONSerialization.data(withJSONObject: value)
            }
            return nil
        }
        guard path.hasPrefix("/api/v1/workloads/") || path.hasSuffix("/logs") || (path.contains("/jobs/") && !path.hasSuffix("/artifacts") && !path.contains("/reports")) else { return nil }
        let count = lock.withLock { counts[path, default: 0] += 1; return counts[path]! }
        var response = NativeFixtures.response(path: path, query: query)
        if path.hasPrefix("/api/v1/workloads/") {
            var a = (response["items"] as! [[String: Any]])[0]
            a["id"] = "item-a"; a["name"] = count == 1 ? "Refresh row A" : "Refresh row A updated"
            var b = a; b["id"] = "item-b"; b["name"] = "Refresh row B"
            var c = a; c["id"] = "item-c"; c["name"] = "Later page row C"
            response["items"] = cursor == nil ? (count == 1 ? [a] : count == 2 ? [b, a] : [b]) : [c]
            response["total"] = "3"
            if cursor == nil { response["nextCursor"] = "refresh-page-two" }
        } else if path.hasSuffix("/logs") {
            let data = Data("Manual log read \(count)\n".utf8)
            response["bytesBase64"] = data.base64EncodedString(); response["endOffset"] = String(data.count)
            response["state"] = "complete"; response.removeValue(forKey: "nextCursor")
            response["capturedAt"] = "2025-01-01T00:00:00Z"
        } else if var job = response["job"] as? [String: Any] {
            job["name"] = "Foreground job read \(count)"; response["job"] = job
        }
        return try JSONSerialization.data(withJSONObject: response)
    }
}
#endif
