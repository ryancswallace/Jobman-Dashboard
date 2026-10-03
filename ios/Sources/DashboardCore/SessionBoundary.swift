import Foundation

/// A generation changes before clearing a scope, so late network completions cannot republish it.
@MainActor
public final class SessionBoundary {
    public struct Ticket: Equatable, Sendable {
        public let generation: UInt64
        public let accountId: String
        public let scope: DashboardScope
    }
    public private(set) var generation: UInt64 = 0
    public private(set) var accountId: String?
    public private(set) var scope: DashboardScope = .all
    public private(set) var authorizedNamespaces: Set<NamespaceRef> = []
    public private(set) var authorizationValidUntil: Date?

    public init() {}

    @discardableResult
    public func activate(accountId: String, scope: DashboardScope, namespaces: Set<NamespaceRef>, checkedAt: Date,
                         maximumAge: TimeInterval = 120) -> Ticket {
        generation &+= 1
        self.accountId = accountId
        self.scope = scope
        authorizedNamespaces = namespaces
        authorizationValidUntil = checkedAt.addingTimeInterval(min(max(maximumAge, 0), 120))
        return ticket()!
    }

    /// Refresh must use server verification time, never the time a cached response arrived.
    public func refreshAuthorization(namespaces: Set<NamespaceRef>, checkedAt: Date,
                                     maximumAge: TimeInterval = 120) -> Bool {
        let removed = !authorizedNamespaces.isSubset(of: namespaces)
        if removed { generation &+= 1 }
        authorizedNamespaces = namespaces
        authorizationValidUntil = checkedAt.addingTimeInterval(min(max(maximumAge, 0), 120))
        return removed
    }

    public func ticket() -> Ticket? {
        guard let accountId else { return nil }
        return Ticket(generation: generation, accountId: accountId, scope: scope)
    }

    public func canPublish(_ ticket: Ticket, namespace: NamespaceRef? = nil, now: Date = Date()) -> Bool {
        guard ticket == self.ticket(), let deadline = authorizationValidUntil, now < deadline else { return false }
        return namespace.map { authorizedNamespaces.contains($0) && scope.contains($0) } ?? true
    }

    public func clear() {
        generation &+= 1
        accountId = nil
        scope = .all
        authorizedNamespaces = []
        authorizationValidUntil = nil
    }
}

public enum RefreshInterval: Int, Codable, CaseIterable, Sendable {
    case manual = 0, fiveSeconds = 5, tenSeconds = 10, thirtySeconds = 30
}

public enum WireDate {
    public static func parse(_ value: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = fractional.date(from: value) { return date }
        return ISO8601DateFormatter().date(from: value)
    }
}
