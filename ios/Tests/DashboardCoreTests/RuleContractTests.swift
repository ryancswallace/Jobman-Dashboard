import Foundation
import Testing
@testable import DashboardCore

private let ruleNamespace = NamespaceRef(deploymentId: "10000000-0000-4000-8000-000000000001", namespaceId: "20000000-0000-4000-8000-000000000001")
private let ruleID = "30000000-0000-4000-8000-000000000001"
private let jobID = "40000000-0000-4000-8000-000000000001"
private func ruleFixture() -> AlertRule {
    .init(id: ruleID, revision: "9007199254740993", name: "My failures", enabled: true, scope: "my_jobs", outcomeMode: "selected", outcomes: RuleDraft.unsuccessful,
          scopes: [.init(deploymentId: ruleNamespace.deploymentId, namespaceId: ruleNamespace.namespaceId, status: "active", activatedAt: "2026-10-04T01:00:00Z")], jobs: [], inaccessibleScopes: 0, unavailableScopes: 0, createdAt: "2026-10-04T01:00:00Z", updatedAt: "2026-10-04T01:00:00Z")
}
@Test func ruleDefaultsAreExplicitOptInWithCancellationExcluded() throws {
    var draft = RuleDraft()
    #expect(draft.namespaces.isEmpty && draft.jobs.isEmpty)
    #expect(draft.scope == "my_jobs")
    #expect(draft.outcomes == ["failure", "timed_out", "aborted", "lost"])
    #expect(throws: RuleDraftError.name) { try draft.input() }
    draft.name = "Future submitted jobs"; draft.namespaces = [ruleNamespace]
    let input = try draft.input()
    #expect(input.scope == "my_jobs" && input.jobs.isEmpty)
    #expect(!input.outcomes.contains("cancelled"))
    let object = try JSONSerialization.jsonObject(with: JSONEncoder().encode(input)) as! [String: Any]
    #expect(Set(object.keys) == Set(["name", "enabled", "scope", "namespaces", "jobs", "outcomeMode", "outcomes"]))
}
@Test func ruleEditorCannotReconstructHiddenOrUnknownIntent() throws {
    var rule = ruleFixture()
    try rule.validate()
    #expect(try RuleDraft(rule: rule).input().name == rule.name)
    rule.unavailableScopes = 1
    #expect(!rule.canEditFullRule)
    #expect(throws: RuleDraftError.hiddenReferences) { try RuleDraft(rule: rule) }
    rule.unavailableScopes = 0; rule.scope = "future_scope"
    try rule.validate()
    #expect(throws: RuleDraftError.unsupported) { try RuleDraft(rule: rule) }
    rule.scope = "my_jobs"; rule.outcomes = ["future_terminal"]
    try rule.validate()
    #expect(!rule.canEditFullRule)
    rule.outcomeMode = "all_terminal"; rule.outcomes = []
    #expect(try RuleDraft(rule: rule).input().outcomes.isEmpty)
}
@Test func watchedJobsRemainSourceQualifiedAndBoundsAreExact() throws {
    let other = NamespaceRef(deploymentId: "10000000-0000-4000-8000-000000000002", namespaceId: ruleNamespace.namespaceId)
    var draft = RuleDraft(watching: JobRef(deploymentId: ruleNamespace.deploymentId, namespaceId: ruleNamespace.namespaceId, jobId: jobID))
    draft.selectNamespace(other, selected: true)
    try draft.addJob(id: jobID.uppercased(), namespace: other)
    try draft.addJob(id: jobID, namespace: other)
    #expect(draft.jobs.count == 2)
    #expect(try draft.input().jobs.count == 2)
    draft.selectNamespace(ruleNamespace, selected: false)
    #expect(draft.jobs.count == 1 && draft.jobs[0].deploymentId == other.deploymentId)
    #expect(throws: RuleDraftError.watchedJobs) { try draft.addJob(id: "job-42", namespace: other) }
    for index in 2...100 { try draft.addJob(id: String(format: "40000000-0000-4000-8000-%012d", index), namespace: other) }
    #expect(draft.jobs.count == 100)
    #expect(throws: RuleDraftError.watchedJobs) { try draft.addJob(id: "40000000-0000-4000-8000-000000000101", namespace: other) }
    draft.selectScope("namespace_jobs")
    let namespaceInput = try draft.input()
    #expect(draft.jobs.isEmpty && namespaceInput.jobs.isEmpty)
}
@Test func ruleNameUsesUTF8AndOutcomeModesClearInactiveValues() throws {
    var draft = try RuleDraft(rule: ruleFixture())
    draft.name = String(repeating: "é", count: 60)
    #expect(try draft.input().name.utf8.count == 120)
    draft.name += "a"
    #expect(throws: RuleDraftError.name) { try draft.input() }
    draft.name = "valid\u{0000}invalid"
    #expect(throws: RuleDraftError.name) { try draft.input() }
    draft.name = "Selected future outcomes"
    draft.selectAllTerminal(true)
    #expect(try draft.input().outcomes.isEmpty)
    draft.selectAllTerminal(false)
    #expect(draft.outcomes == RuleDraft.unsuccessful)
    draft.outcomes = []
    #expect(throws: RuleDraftError.outcomes) { try draft.input() }
    draft.outcomes = ["cancelled"]
    #expect(try draft.input().outcomes == ["cancelled"])
}
@Test func rulePageValidatesWideRevisionsIdentityAndPageLimits() throws {
    let rule = ruleFixture()
    try rule.validate(expectedID: ruleID)
    #expect(rule.revision == "9007199254740993")
    #expect(throws: DashboardError.invalidResponse) { try rule.validate(expectedID: jobID) }
    var page = DashboardAPI.RulePage(items: [rule], nextCursor: "opaque-continuation")
    try page.validate()
    page.items.append(rule)
    #expect(throws: DashboardError.invalidResponse) { try page.validate() }
    var bad = rule; bad.revision = "9223372036854775808"
    #expect(throws: DashboardError.invalidResponse) { try bad.validate() }
    bad = rule; bad.jobs = [.init(deploymentId: jobID, namespaceId: ruleNamespace.namespaceId, jobId: jobID)]
    #expect(throws: DashboardError.invalidResponse) { try bad.validate() }
}
@Test func uncertainRuleCreateAndConflictingEditRequireDifferentRecovery() {
    var uncertain = RuleSubmissionGuard()
    uncertain.failed(DashboardError.network, creating: true)
    #expect(uncertain.uncertainCreation && uncertain.blocksSubmission)
    uncertain.reviewedReload()
    #expect(uncertain.blocksSubmission)
    for failure in [DashboardError.server("invalid_request"), .rateLimited, .ruleCapacity, .unsupportedOutcome] {
        var state = RuleSubmissionGuard(); state.failed(failure, creating: true)
        #expect(!state.blocksSubmission)
    }
    #expect(DashboardError.from(code: "rule_capacity", status: 429) == .ruleCapacity)
    #expect(DashboardError.from(code: "unsupported_outcome", status: 422) == .unsupportedOutcome)
    var conflict = RuleSubmissionGuard(); conflict.failed(DashboardError.revisionConflict, creating: false)
    #expect(conflict.conflict && !conflict.uncertainCreation)
    conflict.reviewedReload(); #expect(!conflict.blocksSubmission)
}

private actor RuleWire: DashboardHTTPTransport {
    var requests: [DashboardHTTPRequest] = []
    func send(_ request: DashboardHTTPRequest) async throws -> Data {
        requests.append(request)
        if request.method == "DELETE" { return Data() }
        if request.path == "/api/v1/rules" && request.method == "GET" { return try JSONEncoder().encode(DashboardAPI.RulePage(items: [ruleFixture()])) }
        return try JSONEncoder().encode(ruleFixture())
    }
}
@Test func generatedRuleTransportUsesDedicatedCASOperationsAndNoFakeIdempotency() async throws {
    let wire = RuleWire(), input = try RuleDraft(rule: ruleFixture()).input()
    let client = DashboardClient(transport: wire)
    _ = try await client.rules(query: .init(cursor: "opaque", limit: 20))
    _ = try await client.createRule(body: input)
    _ = try await client.updateRule(ruleId: ruleID, headers: .init(ifMatch: "9007199254740993"), body: input)
    _ = try await client.setRuleEnabled(ruleId: ruleID, headers: .init(ifMatch: "2"), body: .init(enabled: false))
    _ = try await client.revalidateRule(ruleId: ruleID, headers: .init(ifMatch: "3"))
    try await client.deleteRule(ruleId: ruleID, headers: .init(ifMatch: "4"))
    let requests = await wire.requests
    #expect(requests[0].query == ["limit":"20", "cursor":"opaque"])
    #expect(requests[1].headers["Idempotency-Key"] == nil)
    #expect(requests[2].headers["If-Match"] == "9007199254740993")
    let enabled = try JSONSerialization.jsonObject(with: requests[3].body!) as! [String: Any]
    #expect(Set(enabled.keys) == ["enabled"] && enabled["enabled"] as? Bool == false)
    #expect(requests[3].path.hasSuffix("/enabled") && requests[3].method == "PUT")
    #expect(requests[4].path.hasSuffix("/revalidate") && requests[4].body == nil && requests[4].method == "POST")
    #expect(requests[5].method == "DELETE" && requests[5].headers["If-Match"] == "4")
}
