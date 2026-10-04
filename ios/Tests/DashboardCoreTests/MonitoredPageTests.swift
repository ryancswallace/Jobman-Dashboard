import Foundation
import Testing
@testable import DashboardCore

private struct MonitoredRow: Decodable, Sendable {
    let source: String
    let id: String
    let value: String
    var key: String { source + "/" + id }
}
private func monitoredPage(_ rows: [(String, String, String)], cursor: String? = nil, fetched: String = "2026-10-04T15:00:00Z") -> Page<MonitoredRow> {
    Page(items: rows.map { .init(source: $0.0, id: $0.1, value: $0.2) }, nextCursor: cursor, completeness: "complete", sources: [], fetchedAt: fetched, total: nil, totals: nil)
}
@Test func monitoredRefreshAddsUpdatesAndRemovesInSameSnapshot() throws {
    var state = MonitoredPage<MonitoredRow, String>()
    try state.accept(monitoredPage([("s", "a", "old")]), for: .refresh, identity: \.key)
    try state.accept(monitoredPage([("s", "b", "new"), ("s", "a", "updated")], fetched: "2026-10-04T15:01:00Z"), for: .refresh, identity: \.key)
    #expect(state.items.map(\.id) == ["b", "a"])
    #expect(state.items.last?.value == "updated")
    try state.accept(monitoredPage([("s", "b", "only")], fetched: "2026-10-04T15:02:00Z"), for: .refresh, identity: \.key)
    #expect(state.items.map(\.id) == ["b"])
    #expect(state.page?.fetchedAt == "2026-10-04T15:02:00Z")
}
@Test func monitoredPaginationRefreshKeepsCurrentCursorAndNoOldRows() throws {
    var state = MonitoredPage<MonitoredRow, String>()
    try state.accept(monitoredPage([("s", "a", "first")], cursor: "second"), for: .refresh, identity: \.key)
    try state.accept(monitoredPage([("s", "b", "second")], cursor: "third"), for: .next("second"), identity: \.key)
    #expect(state.items.map(\.id) == ["b"])
    #expect(try state.requestCursor(for: .refresh) == "second")
    try state.accept(monitoredPage([("s", "c", "replaced")]), for: .refresh, identity: \.key)
    #expect(state.items.map(\.id) == ["c"])
    #expect(state.cursor == "second")
    #expect(state.canGoBack)
    #expect(try state.requestCursor(for: .previous) == nil)
    try state.accept(monitoredPage([("s", "x", "new first")]), for: .previous, identity: \.key)
    #expect(state.isFirstPage && !state.canGoBack)
}
@Test func monitoredPageRejectsBadRefreshAtomicallyAndSeparatesSources() throws {
    var state = MonitoredPage<MonitoredRow, String>()
    try state.accept(monitoredPage([("one", "same", "first"), ("two", "same", "second")]), for: .refresh, identity: \.key)
    #expect(state.items.count == 2)
    #expect(throws: DashboardError.self) { try state.accept(monitoredPage([("one", "same", "x"), ("one", "same", "y")], fetched: "2026-10-04T15:05:00Z"), for: .refresh, identity: \.key) }
    #expect(state.items.first?.value == "first")
    #expect(state.page?.fetchedAt == "2026-10-04T15:00:00Z")
    state.failed(DashboardError.cursorExpired)
    #expect(state.page == nil && state.requiresRestart)
    #expect(throws: DashboardError.self) { try state.requestCursor(for: .refresh) }
    try state.accept(monitoredPage([]), for: .restart, identity: \.key)
    #expect(!state.requiresRestart && state.isFirstPage && state.items.isEmpty)
}
@Test func monitoredPageHistoryAndRowsStayBounded() throws {
    var state = MonitoredPage<MonitoredRow, String>()
    try state.accept(monitoredPage([], cursor: "0"), for: .refresh, identity: \.key)
    for index in 0..<70 {
        try state.accept(monitoredPage([("s", String(index), "only page")], cursor: String(index + 1)), for: .next(String(index)), identity: \.key)
    }
    #expect(state.items.count == 1)
    #expect(state.history.count == 64 && state.history.discardedPages == 6)
    try state.accept(monitoredPage([]), for: .restart, identity: \.key)
    #expect(state.history.count == 0 && state.history.discardedPages == 0)
}
@Test func overviewWindowUsesExactSupportedHours() {
    #expect(OverviewWindow.day.query == URLQueryItem(name: "windowHours", value: "24"))
    #expect(OverviewWindow.week.query == URLQueryItem(name: "windowHours", value: "168"))
}
@Test func foregroundReplacementCannotBePublishedOrFinishedByOlderRead() throws {
    var gate = ReadRequestGate()
    let started = gate.begin()
    let old = try #require(started)
    let blocked = gate.begin()
    #expect(blocked == nil)
    let replaced = gate.begin(replacing: true)
    let resumed = try #require(replaced)
    #expect(!gate.accepts(old) && gate.accepts(resumed))
    gate.finish(old)
    #expect(gate.busy)
    gate.finish(resumed)
    #expect(!gate.busy)
}
