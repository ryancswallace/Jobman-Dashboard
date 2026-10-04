import Foundation

public typealias NotificationInboxItem = DashboardAPI.InboxItem
extension DashboardAPI.InboxItem: Identifiable {
    public var jobReference: JobRef { .init(deploymentId: job.deploymentId, namespaceId: job.namespaceId, jobId: job.jobId) }
    public var displayName: String { jobAvailability == "available" ? jobName ?? job.jobId : job.jobId }
    public func validate(expectedID: String? = nil, now: Date = Date()) throws {
        guard [id, controlInstanceId, eventId, job.deploymentId, job.namespaceId, job.jobId].allSatisfy(inboxUUID),
              expectedID == nil || expectedID == id, !outcome.isEmpty, outcome.utf8.count <= 64,
              !outcome.unicodeScalars.contains(where: CharacterSet.controlCharacters.contains),
              WireDate.parse(eventAt) != nil, let created = WireDate.parse(createdAt), let expires = WireDate.parse(expiresAt), expires > created, expires > now,
              read == (readAt != nil), readAt == nil || WireDate.parse(readAt!) != nil,
              observedCompletedAt == nil || WireDate.parse(observedCompletedAt!) != nil,
              ["available", "missing", "unavailable"].contains(jobAvailability), (jobName?.utf8.count ?? 0) <= 1024,
              jobName == nil || jobAvailability == "available", !matchedRules.isEmpty, matchedRules.count <= 100,
              Set(matchedRules.map(\.ruleId)).count == matchedRules.count
        else { throw DashboardError.invalidResponse }
        for rule in matchedRules {
            guard inboxUUID(rule.ruleId), let revision = Int64(rule.revision), revision > 0, String(revision) == rule.revision,
                  !rule.name.isEmpty, rule.name.utf8.count <= 120, !rule.name.unicodeScalars.contains(where: CharacterSet.controlCharacters.contains),
                  !rule.scope.isEmpty, rule.scope.utf8.count <= 64, !rule.outcomeMode.isEmpty, rule.outcomeMode.utf8.count <= 64,
                  rule.outcomes.count <= 6, Set(rule.outcomes).count == rule.outcomes.count,
                  rule.outcomes.allSatisfy({ !$0.isEmpty && $0.utf8.count <= 64 }) else { throw DashboardError.invalidResponse }
        }
        guard let total = inboxCount(delivery.total), total <= 50, delivery.byState.count <= 50 else { throw DashboardError.invalidResponse }
        var sum: Int64 = 0
        for (state, count) in delivery.byState {
            guard !state.isEmpty, state.utf8.count <= 32, !state.unicodeScalars.contains(where: CharacterSet.controlCharacters.contains),
                  let value = inboxCount(count), value > 0, value <= 50 else { throw DashboardError.invalidResponse }
            sum += value
        }
        guard sum == total else { throw DashboardError.invalidResponse }
    }
}
extension DashboardAPI.InboxItemPage {
    public func validate(scope: DashboardScope = .all, unreadOnly: Bool = false, now: Date = Date()) throws {
        guard items.count <= 50, Set(items.map(\.id)).count == items.count, inboxCount(unreadCount) != nil,
              scope != .deployments([]) || (items.isEmpty && unreadCount == "0" && nextCursor == nil),
              ["complete", "partial"].contains(completeness), unavailableSources >= 0, unavailableSources <= 32,
              inaccessibleScopes >= 0, inaccessibleScopes <= 320, WireDate.parse(fetchedAt) != nil,
              nextCursor == nil || (!nextCursor!.isEmpty && nextCursor!.utf8.count <= 128 && !nextCursor!.unicodeScalars.contains(where: CharacterSet.whitespacesAndNewlines.contains)),
              completeness == "partial" || (unavailableSources == 0 && inaccessibleScopes == 0 && !items.contains(where: { $0.jobAvailability == "unavailable" })),
              items.allSatisfy({ scope.contains($0.jobReference.namespace) && (!unreadOnly || !$0.read) }) else { throw DashboardError.invalidResponse }
        for item in items { try item.validate(now: now) }
    }
}
/// Retains only a bounded navigation window; cursors never persist across accounts.
public struct InboxPageHistory: Sendable {
    public static let capacity = 64
    private var cursors: [String?] = []
    public private(set) var discardedPages = 0
    public var count: Int { cursors.count }
    public var canGoBack: Bool { !cursors.isEmpty }
    public init() { }
    public mutating func record(_ cursor: String?) {
        if cursors.count == Self.capacity { cursors.removeFirst(); discardedPages = min(discardedPages, Int.max - 1) + 1 }
        cursors.append(cursor)
    }
    public mutating func previous() -> String? { guard !cursors.isEmpty else { return nil }; return cursors.removeLast() }
    public mutating func reset() { cursors.removeAll(); discardedPages = 0 }
}

public enum InboxPushRoute {
    /// Payload metadata grants no authority. The only usable content is the opaque
    /// inbox UUID; all displayed fields come from its authenticated current read.
    public static func route(schemaVersion: Int?, inboxID: String?) -> DashboardRoute? {
        guard schemaVersion == 1, let inboxID, inboxUUID(inboxID) else { return nil }
        return .inbox(inboxID)
    }
}
private func inboxUUID(_ value: String) -> Bool { UUID(uuidString: value)?.uuidString.lowercased() == value && value != "00000000-0000-0000-0000-000000000000" }
private func inboxCount(_ value: String) -> Int64? { guard let number = Int64(value), number >= 0, String(number) == value else { return nil }; return number }
