import Foundation
import Testing
@testable import DashboardCore

@Test @MainActor func obsoleteAccountAndScopeResponsesAreRejected() {
    let boundary = SessionBoundary()
    let now = Date()
    let ns = NamespaceRef(deploymentId: "east", namespaceId: "team")
    let old = boundary.activate(accountId: "alice", scope: .all, namespaces: [ns], checkedAt: now)
    #expect(boundary.canPublish(old, namespace: ns, now: now))
    _ = boundary.activate(accountId: "alice", scope: .namespace(ns), namespaces: [ns], checkedAt: now)
    #expect(!boundary.canPublish(old, now: now))
    let selected = boundary.ticket()!
    _ = boundary.activate(accountId: "bob", scope: .namespace(ns), namespaces: [ns], checkedAt: now)
    #expect(!boundary.canPublish(selected, now: now))
    boundary.clear()
    #expect(boundary.ticket() == nil)
}

@Test @MainActor func revocationInvalidatesWorkWithoutLogin() {
    let boundary = SessionBoundary()
    let now = Date()
    let ns = NamespaceRef(deploymentId: "east", namespaceId: "team")
    let ticket = boundary.activate(accountId: "alice", scope: .all, namespaces: [ns], checkedAt: now)
    #expect(boundary.refreshAuthorization(namespaces: [], checkedAt: now))
    #expect(!boundary.canPublish(ticket, namespace: ns, now: now))
}

@Test @MainActor func cachedAuthorizationCannotOutliveFreshnessWindow() {
    let boundary = SessionBoundary()
    let now = Date()
    let ticket = boundary.activate(accountId: "alice", scope: .all, namespaces: [], checkedAt: now)
    #expect(boundary.canPublish(ticket, now: now.addingTimeInterval(119)))
    #expect(!boundary.canPublish(ticket, now: now.addingTimeInterval(120)))
    _ = boundary.refreshAuthorization(namespaces: [], checkedAt: now.addingTimeInterval(-500))
    #expect(!boundary.canPublish(ticket, now: now))
}
