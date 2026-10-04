import Foundation
import Testing
@testable import DashboardCore

private func inboxFixture() -> NotificationInboxItem {
    .init(id: "92000000-0000-4000-8000-000000000001", job: .init(deploymentId: "10000000-0000-4000-8000-000000000001", namespaceId: "20000000-0000-4000-8000-000000000001", jobId: "40000000-0000-4000-8000-000000000001"), controlInstanceId: "93000000-0000-4000-8000-000000000001", eventId: "94000000-0000-4000-8000-000000000001", outcome: "future_terminal", eventAt: "2026-10-04T01:00:00Z", observedCompletedAt: "2026-10-04T00:55:00Z", createdAt: "2026-10-04T01:05:00Z", expiresAt: "2026-11-03T01:05:00Z", read: false, matchedRules: [.init(ruleId: "95000000-0000-4000-8000-000000000001", revision: "9007199254740993", name: "Original rule", scope: "my_jobs", outcomeMode: "all_terminal", outcomes: [])], jobName: "Current authorized name", jobAvailability: "available", delivery: .init(total: "2", byState: ["provider_accepted":"1","future_delivery_state":"1"]))
}
private let inboxNow = WireDate.parse("2026-10-04T02:00:00Z")!
@Test func inboxPreservesOriginalWideRevisionAndDistinctEventFacts() throws {
    let item = inboxFixture()
    try item.validate(expectedID: item.id, now: inboxNow)
    let decoded = try JSONDecoder().decode(NotificationInboxItem.self, from: JSONEncoder().encode(item))
    #expect(decoded.matchedRules[0].revision == "9007199254740993")
    #expect(decoded.eventAt != decoded.observedCompletedAt && decoded.outcome == "future_terminal")
    #expect(decoded.delivery.byState["future_delivery_state"] == "1")
    #expect(throws: DashboardError.invalidResponse) { try decoded.validate(expectedID: "92000000-0000-4000-8000-000000000002", now: inboxNow) }
}
@Test func inboxCannotDisplayCachedJobNameForMissingOrUnavailableJob() throws {
    var item = inboxFixture(); item.jobAvailability = "missing"
    #expect(item.displayName == item.job.jobId)
    #expect(throws: DashboardError.invalidResponse) { try item.validate(now: inboxNow) }
    item.jobName = nil; try item.validate(now: inboxNow)
    item.jobAvailability = "unavailable"; try item.validate(now: inboxNow)
    item.read = true
    #expect(throws: DashboardError.invalidResponse) { try item.validate(now: inboxNow) }
    item.readAt = "2026-10-04T01:10:00Z"; try item.validate(now: inboxNow)
    #expect(throws: DashboardError.invalidResponse) { try item.validate(now: WireDate.parse(item.expiresAt)!) }
}
@Test func inboxPageChecksPartialCountsBoundsScopeAndUnreadProjection() throws {
    let item = inboxFixture()
    var page = DashboardAPI.InboxItemPage(items: [item], unreadCount: "9007199254740993", completeness: "complete", unavailableSources: 0, inaccessibleScopes: 0, fetchedAt: "2026-10-04T02:00:00Z")
    try page.validate(unreadOnly: true, now: inboxNow)
    #expect(throws: DashboardError.invalidResponse) { try page.validate(scope: .deployments([]), now: inboxNow) }
    page.unavailableSources = 1
    #expect(throws: DashboardError.invalidResponse) { try page.validate(now: inboxNow) }
    page.completeness = "partial"; try page.validate(now: inboxNow)
    page.items[0].read = true; page.items[0].readAt = page.fetchedAt
    #expect(throws: DashboardError.invalidResponse) { try page.validate(unreadOnly: true, now: inboxNow) }
    page.items[0].delivery.total = "3"
    #expect(throws: DashboardError.invalidResponse) { try page.validate(now: inboxNow) }
}
@Test func inboxAllScopeAndExplicitEmptyRemainDistinctAndPushOnlyRoutesOpaqueID() throws {
    #expect(try DashboardScope.all.queryItems(authorized: []).isEmpty)
    #expect(try DashboardScope.deployments([]).queryItems(authorized: []).first?.value == "[]")
    let empty = DashboardAPI.InboxItemPage(items: [], unreadCount: "0", completeness: "complete", unavailableSources: 0, inaccessibleScopes: 0, fetchedAt: "2026-10-04T02:00:00Z")
    try empty.validate(scope: .deployments([]), now: inboxNow)
    var invalidEmpty = empty; invalidEmpty.unreadCount = "1"
    #expect(throws: DashboardError.invalidResponse) { try invalidEmpty.validate(scope: .deployments([]), now: inboxNow) }
    let id = inboxFixture().id
    #expect(InboxPushRoute.route(schemaVersion: 1, inboxID: id) == .inbox(id))
    #expect(InboxPushRoute.route(schemaVersion: 2, inboxID: id) == nil)
    #expect(InboxPushRoute.route(schemaVersion: 1, inboxID: "../jobs/private") == nil)
    #expect(InboxPushRoute.route(schemaVersion: 1, inboxID: "job-name") == nil)
    #expect(DashboardError.from(code: "authorization_unavailable", status: 503) == .authorizationUnavailable)
}

@Test func inboxBackPagesEvictOldestAtBoundAndRefreshResets() {
    var history = InboxPageHistory()
    history.record(nil)
    for i in 1...70 { history.record("cursor-\(i)") }
    #expect(history.count == 64 && history.discardedPages == 7)
    for i in stride(from: 70, through: 7, by: -1) { #expect(history.previous() == "cursor-\(i)") }
    #expect(!history.canGoBack && history.previous() == nil)
    history.reset(); history.record(nil)
    #expect(history.discardedPages == 0 && history.canGoBack)
    #expect(history.previous() == nil && !history.canGoBack)
}
