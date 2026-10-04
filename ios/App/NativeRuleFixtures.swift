#if DEBUG
import DashboardCore
import Foundation

/// Deliberately local, synthetic state used only by the explicit rule UI tests.
/// This URLProtocol fixture cannot call a real service or request system identity.
enum NativeRuleFixtures {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--dashboard-rule-fixtures")
    static let deployment = "10000000-0000-4000-8000-000000000001"
    static let namespace = "20000000-0000-4000-8000-000000000001"
    static let job = "40000000-0000-4000-8000-000000000001"
    static let server = RuleFixtureServer()
    static func bootstrap() -> [String: Any] {
        let now = Date(), date = ISO8601DateFormatter()
        let namespaces: [[String: Any]] = ProcessInfo.processInfo.arguments.contains("--dashboard-rule-no-namespaces") ? [] : [["id":namespace,"name":"Research", "roles":["viewer"], "capabilities":["jobs.read"], "authorizationVersion":"1", "authorizationCheckedAt":date.string(from: now), "authorizationExpiresAt":date.string(from: now.addingTimeInterval(120))]]
        return ["apiVersion":"jobman.dashboard/v1", "completeness":"complete", "fixtureMode":true, "account":["id":"fixture-account", "displayName":"Synthetic researcher"],
                "deployments":[["id":deployment,"name":"Synthetic East", "status":"available", "namespaces":namespaces]],
                "preferences":["revision":"1","timezone":"UTC","appearance":"system","refreshSeconds":5],
                "limits":["defaultPageSize":50,"maxPageSize":200,"logReadBytes":262144,"logBufferBytes":2097152,"graphNodes":200,"graphEdges":500]]
    }
    static func jobResponse() -> [String: Any] {
        let now = ISO8601DateFormatter().string(from: Date())
        return ["deploymentId":deployment,"namespaceId":namespace,"id":job,"name":"Synthetic watched job","targetId":"synthetic-target","revision":"1","createdAt":now,"updatedAt":now,"desiredState":"run","phase":"running","confidence":"current","labels":[:]]
    }
}

final class RuleFixtureServer: @unchecked Sendable {
    private let lock = NSLock()
    private var rows: [String: AlertRule] = [:]
    private var created = 0
    private var conflicted = false
    init() {
        let timestamp = ISO8601DateFormatter().string(from: Date())
        for i in 100...120 {
            let id = String(format: "70000000-0000-4000-8000-%012d", i)
            let name = i == 100 ? "Synthetic personal failures" : i == 101 ? "Synthetic unavailable scope" : i == 102 ? "Synthetic conflict rule" : i == 103 ? "Synthetic access revoked" : i == 120 ? "Page two rule" : "Synthetic rule \(i)"
            let hidden = i == 101
            rows[id] = .init(id: id, revision: "1", name: name, enabled: true, scope: "my_jobs", outcomeMode: "selected", outcomes: RuleDraft.unsuccessful,
                             scopes: hidden ? [] : [.init(deploymentId: NativeRuleFixtures.deployment, namespaceId: NativeRuleFixtures.namespace, status: "pending")], jobs: [], inaccessibleScopes: 0, unavailableScopes: hidden ? 1 : 0, createdAt: timestamp, updatedAt: timestamp)
        }
    }
    func response(_ request: URLRequest) throws -> (Int, Data)? {
        guard NativeRuleFixtures.enabled, let url = request.url else { return nil }
        if url.path == "/api/v1/bootstrap" { return (200, try JSONSerialization.data(withJSONObject: NativeRuleFixtures.bootstrap())) }
        if url.path == "/api/v1/jobs" {
            return (200, try JSONSerialization.data(withJSONObject: ["items":[NativeRuleFixtures.jobResponse()],"completeness":"complete","sources":[],"fetchedAt":ISO8601DateFormatter().string(from: Date())]))
        }
        if url.path.hasSuffix("/jobs/\(NativeRuleFixtures.job)") { return (200, try JSONSerialization.data(withJSONObject: ["job":NativeRuleFixtures.jobResponse(),"fetchedAt":ISO8601DateFormatter().string(from: Date())])) }
        guard url.path == "/api/v1/rules" || url.path.hasPrefix("/api/v1/rules/") else { return nil }
        return try lock.withLock {
            func failure(_ code: String, status: Int) throws -> (Int, Data) { (status, try JSONSerialization.data(withJSONObject: ["code":code,"message":"Synthetic rule fixture response"])) }
            func result(_ rule: AlertRule) throws -> (Int, Data) { (200, try JSONEncoder().encode(rule)) }
            let method = request.httpMethod ?? "GET"
            let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
            if url.path == "/api/v1/rules" && method == "GET" {
                let cursor = query.first { $0.name == "cursor" }?.value
                let all = rows.values.sorted { $0.id < $1.id }, offset = cursor == nil ? 0 : 20
                return (200, try JSONEncoder().encode(DashboardAPI.RulePage(items: Array(all.dropFirst(offset).prefix(20)), nextCursor: offset + 20 < all.count ? "synthetic-rule-page-two" : nil)))
            }
            if url.path == "/api/v1/rules" && method == "POST" {
                let input = try JSONDecoder().decode(DashboardAPI.RuleInput.self, from: body(request))
                created += 1
                let id = String(format: "70000000-0000-4000-8000-%012d", created), timestamp = ISO8601DateFormatter().string(from: Date())
                let rule = AlertRule(id: id, revision: "1", name: input.name, enabled: input.enabled, scope: input.scope, outcomeMode: input.outcomeMode, outcomes: input.outcomes,
                                     scopes: input.namespaces.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, status: input.enabled ? "pending" : "disabled") }, jobs: input.jobs, inaccessibleScopes: 0, unavailableScopes: 0, createdAt: timestamp, updatedAt: timestamp)
                rows[id] = rule
                if input.name == "Deferred create" { return (503, try JSONSerialization.data(withJSONObject: ["code":"source_unavailable", "fixtureDelay":true])) }
                if input.name == "Uncertain create" { return try failure("source_unavailable", status: 503) }
                return try result(rule)
            }
            let parts = url.path.split(separator: "/")
            guard parts.count >= 4, var rule = rows[String(parts[3])] else { return try failure("not_found_or_inaccessible", status: 404) }
            if rule.name == "Synthetic access revoked" { return try failure("forbidden", status: 403) }
            if method == "GET" { return try result(rule) }
            guard request.value(forHTTPHeaderField: "If-Match") == rule.revision else { return try failure("revision_conflict", status: 409) }
            if method == "DELETE" { rows.removeValue(forKey: rule.id); return (204, Data()) }
            if url.path.hasSuffix("/enabled") {
                rule.enabled = try JSONDecoder().decode(DashboardAPI.RuleEnabledInput.self, from: body(request)).enabled
                rule.scopes = rule.scopes.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, status: rule.enabled ? "pending" : "disabled") }
            } else if url.path.hasSuffix("/revalidate") {
                rule.scopes = rule.scopes.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, status: "active", activatedAt: ISO8601DateFormatter().string(from: Date())) }
            } else if method == "PUT" {
                if rule.name == "Synthetic conflict rule" && !conflicted {
                    conflicted = true; rule.revision = "2"; rule.name = "Updated elsewhere"; rows[rule.id] = rule
                    return try failure("revision_conflict", status: 409)
                }
                let input = try JSONDecoder().decode(DashboardAPI.RuleInput.self, from: body(request))
                rule.name = input.name; rule.enabled = input.enabled; rule.scope = input.scope; rule.outcomeMode = input.outcomeMode; rule.outcomes = input.outcomes; rule.jobs = input.jobs
                rule.scopes = input.namespaces.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, status: "pending") }
            } else { return try failure("invalid_request", status: 400) }
            rule.revision = String((Int64(rule.revision) ?? 0) + 1); rows[rule.id] = rule
            return try result(rule)
        }
    }
    private func body(_ request: URLRequest) throws -> Data {
        if let data = request.httpBody { return data }
        guard let stream = request.httpBodyStream else { throw DashboardError.invalidResponse }
        stream.open(); defer { stream.close() }
        var data = Data(), buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count >= 0 else { throw DashboardError.invalidResponse }
            if count == 0 { break }
            data.append(contentsOf: buffer.prefix(count))
            guard data.count <= 65_536 else { throw DashboardError.responseTooLarge }
        }
        return data
    }
}
#endif
