import Foundation

public typealias AlertRule = DashboardAPI.Rule
extension DashboardAPI.Rule: Identifiable {
    public var hiddenScopeCount: Int64 { inaccessibleScopes + unavailableScopes }
    public var canEditFullRule: Bool {
        hiddenScopeCount == 0 && RuleDraft.scopes.contains(scope)
        && ["selected", "all_terminal"].contains(outcomeMode)
        && outcomes.allSatisfy(RuleDraft.knownOutcomes.contains)
    }
    public func validate(expectedID: String? = nil) throws {
        guard ruleUUID(id), expectedID == nil || expectedID == id,
              let number = Int64(revision), number > 0, String(number) == revision,
              !name.isEmpty, name.utf8.count <= 120, inaccessibleScopes >= 0, unavailableScopes >= 0,
              inaccessibleScopes <= 320, unavailableScopes <= 320,
              scopes.count + Int(hiddenScopeCount) <= 320, scopes.count + Int(hiddenScopeCount) > 0,
              jobs.count <= 100, outcomes.count <= 6, WireDate.parse(createdAt) != nil, WireDate.parse(updatedAt) != nil
        else { throw DashboardError.invalidResponse }
        let refs = scopes.map { NamespaceRef(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId) }
        let jobRefs = jobs.map { JobRef(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, jobId: $0.jobId) }
        guard Set(refs).count == refs.count, Set(jobRefs).count == jobRefs.count,
              refs.allSatisfy({ ruleUUID($0.deploymentId) && ruleUUID($0.namespaceId) }),
              jobRefs.allSatisfy({ ruleUUID($0.jobId) && refs.contains($0.namespace) }),
              scopes.allSatisfy({ $0.activatedAt == nil || WireDate.parse($0.activatedAt!) != nil })
        else { throw DashboardError.invalidResponse }
    }
}
extension DashboardAPI.RulePage {
    public func validate() throws {
        guard items.count <= 20, Set(items.map(\.id)).count == items.count,
              nextCursor == nil || (!nextCursor!.isEmpty && nextCursor!.utf8.count <= 4096)
        else { throw DashboardError.invalidResponse }
        for item in items { try item.validate() }
    }
}

public enum RuleDraftError: Error, LocalizedError, Equatable {
    case hiddenReferences, unsupported, name, namespaces, watchedJobs, outcomes
    public var errorDescription: String? {
        switch self {
        case .hiddenReferences: "Some selected jobs or namespaces are hidden because access cannot be confirmed. You can stop or delete this rule. Refresh access before editing all its settings."
        case .unsupported: "This rule uses an option this app cannot edit. You can still stop or delete it."
        case .name: "Enter a rule name up to 120 bytes, without control characters. Some characters use more than one byte."
        case .namespaces: "Choose 1–320 namespaces from no more than 32 deployments."
        case .watchedJobs: "Add 1–100 full job IDs (UUIDs) from the selected namespaces."
        case .outcomes: "Choose at least one final result, or choose every final result."
        }
    }
}

/// An explicit editable intent, never reconstructed from a redacted response.
public struct RuleDraft: Sendable, Equatable {
    public static let scopes = ["my_jobs", "watched_jobs", "namespace_jobs"]
    public static let knownOutcomes = ["failure", "timed_out", "aborted", "lost", "success", "cancelled"]
    public static let unsuccessful = ["failure", "timed_out", "aborted", "lost"]
    public var name = ""
    public var enabled = true
    public var scope = "my_jobs"
    public var namespaces: [NamespaceRef] = []
    public var jobs: [JobRef] = []
    public var outcomeMode = "selected"
    public var outcomes = unsuccessful
    public init(watching job: JobRef? = nil) {
        if let job { name = "Watched job"; scope = "watched_jobs"; namespaces = [job.namespace]; jobs = [job] }
    }
    public init(rule: AlertRule) throws {
        try rule.validate()
        guard rule.hiddenScopeCount == 0 else { throw RuleDraftError.hiddenReferences }
        guard rule.canEditFullRule else { throw RuleDraftError.unsupported }
        name = rule.name; enabled = rule.enabled; scope = rule.scope
        namespaces = rule.scopes.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId) }
        jobs = rule.jobs.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, jobId: $0.jobId) }
        outcomeMode = rule.outcomeMode; outcomes = rule.outcomes
    }
    public mutating func selectScope(_ value: String) {
        scope = value
        if value != "watched_jobs" { jobs = [] }
    }
    public mutating func selectNamespace(_ ref: NamespaceRef, selected: Bool) {
        if selected { if !namespaces.contains(ref) { namespaces.append(ref) } }
        else { namespaces.removeAll { $0 == ref }; jobs.removeAll { $0.namespace == ref } }
    }
    public mutating func selectAllTerminal(_ value: Bool) {
        outcomeMode = value ? "all_terminal" : "selected"
        outcomes = value ? [] : Self.unsuccessful
    }
    public mutating func addJob(id: String, namespace: NamespaceRef) throws {
        let id = id.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let ref = JobRef(deploymentId: namespace.deploymentId, namespaceId: namespace.namespaceId, jobId: id)
        guard ruleUUID(id), namespaces.contains(namespace) else { throw RuleDraftError.watchedJobs }
        if jobs.contains(ref) { return }
        guard jobs.count < 100 else { throw RuleDraftError.watchedJobs }
        jobs.append(ref)
    }
    public func input() throws -> DashboardAPI.RuleInput {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.utf8.count <= 120, !trimmed.unicodeScalars.contains(where: CharacterSet.controlCharacters.contains) else { throw RuleDraftError.name }
        guard Self.scopes.contains(scope), ["selected", "all_terminal"].contains(outcomeMode) else { throw RuleDraftError.unsupported }
        let refs = Array(Set(namespaces)).sorted { ($0.deploymentId, $0.namespaceId) < ($1.deploymentId, $1.namespaceId) }
        guard !refs.isEmpty, refs.count <= 320, Set(refs.map(\.deploymentId)).count <= 32,
              refs.allSatisfy({ ruleUUID($0.deploymentId) && ruleUUID($0.namespaceId) }) else { throw RuleDraftError.namespaces }
        let selectedJobs = scope == "watched_jobs" ? Array(Set(jobs)).sorted { ($0.deploymentId, $0.namespaceId, $0.jobId) < ($1.deploymentId, $1.namespaceId, $1.jobId) } : []
        if scope == "watched_jobs" {
            guard !selectedJobs.isEmpty, selectedJobs.count <= 100, selectedJobs.allSatisfy({ ruleUUID($0.jobId) && refs.contains($0.namespace) }) else { throw RuleDraftError.watchedJobs }
        }
        let selectedOutcomes = outcomeMode == "selected" ? Array(Set(outcomes)).sorted() : []
        if outcomeMode == "selected", selectedOutcomes.isEmpty || !selectedOutcomes.allSatisfy(Self.knownOutcomes.contains) { throw RuleDraftError.outcomes }
        return .init(name: trimmed, enabled: enabled, scope: scope,
                     namespaces: refs.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId) },
                     jobs: selectedJobs.map { .init(deploymentId: $0.deploymentId, namespaceId: $0.namespaceId, jobId: $0.jobId) },
                     outcomeMode: outcomeMode, outcomes: selectedOutcomes)
    }
}

/// Creates have no idempotency guarantee. An uncertain response requires checking
/// the catalog before another create; a conflicting edit requires explicit reload.
public struct RuleSubmissionGuard: Sendable, Equatable {
    public private(set) var conflict = false
    public private(set) var uncertainCreation = false
    public var blocksSubmission: Bool { conflict || uncertainCreation }
    public init() {}
    public mutating func failed(_ error: Error, creating: Bool) {
        let error = error as? DashboardError
        if error == .revisionConflict { conflict = true }
        if creating {
            switch error {
            case .server("invalid_request"), .server("invalid_rule"), .rateLimited, .ruleCapacity, .unsupportedOutcome, .forbidden, .authenticationRequired, .notFound: break
            default: uncertainCreation = true
            }
        }
    }
    public mutating func reviewedReload() { conflict = false }
}

private func ruleUUID(_ value: String) -> Bool {
    guard let id = UUID(uuidString: value), value != "00000000-0000-0000-0000-000000000000" else { return false }
    return id.uuidString.lowercased() == value
}
