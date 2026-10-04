#if DEBUG
import DashboardCore
import Foundation

enum NativeInboxFixtures {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--dashboard-inbox-fixtures")
    static let server = InboxFixtureServer()
    static func item(_ number: Int, now: Date = Date()) -> NotificationInboxItem {
        let formatter = ISO8601DateFormatter(), created = now.addingTimeInterval(-Double(number) * 60)
        let unavailable = number == 2
        return .init(id: String(format: "92000000-0000-4000-8000-%012d", number), job: .init(deploymentId: NativeRuleFixtures.deployment, namespaceId: NativeRuleFixtures.namespace, jobId: NativeRuleFixtures.job),
                     controlInstanceId: "93000000-0000-4000-8000-000000000001", eventId: String(format: "94000000-0000-4000-8000-%012d", number), outcome: number == 1 ? "future_terminal" : "failure",
                     eventAt: formatter.string(from: created.addingTimeInterval(-5)), observedCompletedAt: formatter.string(from: created.addingTimeInterval(-8)), createdAt: formatter.string(from: created), expiresAt: formatter.string(from: now.addingTimeInterval(86400)), read: false,
                     matchedRules: [.init(ruleId: "95000000-0000-4000-8000-000000000001", revision: "9007199254740993", name: "Original synthetic rule", scope: "my_jobs", outcomeMode: "all_terminal", outcomes: [])],
                     jobName: number == 3 || unavailable ? nil : "Synthetic update \(number)", jobAvailability: unavailable ? "unavailable" : number == 3 ? "missing" : "available", delivery: .init(total: "2", byState: ["provider_accepted":"1", "suppressed_disabled":"1"]))
    }
}
final class InboxFixtureServer: @unchecked Sendable {
    private let lock = NSLock()
    private var rows: [NotificationInboxItem] = (1...24).map { NativeInboxFixtures.item($0) }
    private var denied: Set<String> = []
    private var detailReads: [String: Int] = [:]
    private var firstUnavailable = ProcessInfo.processInfo.arguments.contains("--dashboard-inbox-unavailable-once")
    func response(_ request: URLRequest) throws -> (Int, Data)? {
        guard let url = request.url, url.path == "/api/v1/inbox" || url.path.hasPrefix("/api/v1/inbox/") else { return nil }
        return try lock.withLock {
            func failure(_ code: String, _ status: Int) throws -> (Int, Data) { (status, try JSONSerialization.data(withJSONObject: ["code":code])) }
            if firstUnavailable { firstUnavailable = false; return try failure("authorization_unavailable", 503) }
            if ProcessInfo.processInfo.arguments.contains("--dashboard-inbox-unavailable") { return try failure("authorization_unavailable", 503) }
            if url.path == "/api/v1/inbox" {
                let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
                let scope = query.first { $0.name == "scope" }?.value
                let selected: [NamespaceRef]? = try scope.map { try JSONDecoder().decode([NamespaceRef].self, from: Data($0.utf8)) }
                let unread = query.first { $0.name == "unread" }?.value == "true"
                let filtered = rows.filter { item in selected == nil || selected!.contains { $0.deploymentId == item.job.deploymentId && $0.namespaceId == item.job.namespaceId } }
                let items = filtered.filter { !unread || !$0.read }
                let offset = query.first { $0.name == "cursor" } == nil ? 0 : 20
                let visible = Array(items.dropFirst(offset).prefix(20))
                let page = DashboardAPI.InboxItemPage(items: visible, nextCursor: items.count > offset + 20 ? "fixture-inbox-next" : nil, unreadCount: String(filtered.filter { !$0.read }.count), completeness: visible.contains { $0.jobAvailability == "unavailable" } ? "partial" : "complete", unavailableSources: 0, inaccessibleScopes: 0, fetchedAt: ISO8601DateFormatter().string(from: Date()))
                return (200, try JSONEncoder().encode(page))
            }
            let id = url.lastPathComponent
            guard !denied.contains(id), let index = rows.firstIndex(where: { $0.id == id }) else { return try failure("not_found_or_inaccessible", 404) }
            if request.httpMethod == "PATCH" {
                if ProcessInfo.processInfo.arguments.contains("--dashboard-inbox-revoke-on-read") { denied.insert(id); return try failure("not_found_or_inaccessible", 404) }
                let update = try JSONDecoder().decode(DashboardAPI.ReadUpdate.self, from: Self.body(request))
                rows[index].read = update.read; rows[index].readAt = update.read ? ISO8601DateFormatter().string(from: Date()) : nil
            }
            var result = rows[index]
            if request.httpMethod == "GET" {
                detailReads[id, default: 0] += 1
                if ProcessInfo.processInfo.arguments.contains("--dashboard-inbox-change-on-foreground"), detailReads[id, default: 0] > 1, result.jobAvailability == "available" { result.jobName = "Freshly authorized synthetic update" }
            }
            return (200, try JSONEncoder().encode(result))
        }
    }
    private static func body(_ request: URLRequest) throws -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open(); defer { stream.close() }
        var bytes = [UInt8](repeating: 0, count: 512), data = Data()
        while stream.hasBytesAvailable { let count = stream.read(&bytes, maxLength: bytes.count); guard count >= 0, data.count + max(count, 0) <= 512 else { throw DashboardError.invalidResponse }; if count == 0 { break }; data.append(contentsOf: bytes.prefix(count)) }
        return data
    }
}
#endif
