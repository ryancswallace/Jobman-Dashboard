import Foundation

/// A displayed page is replaced atomically with its own source/fetch metadata.
/// Earlier pages retain only bounded cursors, never stale rows with a newer date.
public struct MonitoredPage<Item: Decodable & Sendable, ID: Hashable & Sendable>: Sendable {
    public enum Load: Sendable { case refresh, restart, next(String), previous }
    public private(set) var page: Page<Item>?
    public private(set) var cursor: String?
    public private(set) var history = InboxPageHistory()
    public private(set) var requiresRestart = false
    public init() {}
    public var items: [Item] { page?.items ?? [] }
    public var nextCursor: String? { requiresRestart ? nil : page?.nextCursor }
    public var canGoBack: Bool { !requiresRestart && history.canGoBack }
    public var isFirstPage: Bool { cursor == nil }

    public func requestCursor(for load: Load) throws -> String? {
        switch load {
        case .restart: return nil
        case .refresh:
            guard !requiresRestart else { throw DashboardError.cursorExpired }
            return cursor
        case .next(let next):
            guard !requiresRestart, next == nextCursor, !next.isEmpty else { throw DashboardError.invalidResponse }
            return next
        case .previous:
            guard canGoBack else { throw DashboardError.invalidResponse }
            var copy = history
            return copy.previous()
        }
    }
    public mutating func accept(_ value: Page<Item>, for load: Load, identity: (Item) -> ID) throws {
        let requested = try requestCursor(for: load)
        guard value.items.count <= 200, Set(value.items.map(identity)).count == value.items.count,
              value.sources.count <= 320, WireDate.parse(value.fetchedAt) != nil else { throw DashboardError.invalidResponse }
        switch load {
        case .restart: history.reset()
        case .next: history.record(cursor)
        case .previous: _ = history.previous()
        case .refresh: break
        }
        page = value; cursor = requested; requiresRestart = false
    }
    public mutating func failed(_ error: any Error) {
        if let error = error as? DashboardError, [.cursorExpired, .targetChanged].contains(error) {
            // An expired snapshot is not republished as a current empty result.
            page = nil; requiresRestart = true
        }
    }
    public mutating func reset() { self = Self() }
}

public enum OverviewWindow: Int, CaseIterable, Sendable {
    case day = 24, week = 168
    public var title: String { self == .day ? "24 hours" : "7 days" }
    public var query: URLQueryItem { .init(name: "windowHours", value: String(rawValue)) }
}

/// A foreground read may replace an in-flight request. An older completion must
/// neither publish content nor release the newer request's busy state.
public struct ReadRequestGate: Sendable {
    private var current: UUID?
    public init() {}
    public var busy: Bool { current != nil }
    public mutating func begin(replacing: Bool = false) -> UUID? {
        guard replacing || current == nil else { return nil }
        let token = UUID(); current = token; return token
    }
    public func accepts(_ token: UUID) -> Bool { current == token }
    public mutating func finish(_ token: UUID) { if accepts(token) { current = nil } }
}
