import DashboardCore
import SwiftUI

struct AlertRulesView: View {
    @Environment(DashboardStore.self) private var store
    @State private var page: DashboardAPI.RulePage?
    @State private var cursor: String?
    @State private var history: [String?] = []
    @State private var generation = UUID()
    @State private var loading = false
    @State private var error: String?
    @State private var creating = false
    var body: some View {
        List {
            Section {
                Text("Rules are opt-in. Choose explicit namespaces and outcomes. Signing in or gaining a new namespace never creates a rule.").font(.footnote)
            }
            if let error { Section { ErrorMessage(error: error); Button("Refresh rules") { Task { await load(reset: true) } } } }
            if loading { ProgressView("Checking current rules…") }
            if let page {
                if page.items.isEmpty { ContentUnavailableView("No alert rules", systemImage: "bell", description: Text("Create a rule to choose which future terminal events to follow.")) }
                ForEach(page.items) { rule in
                    NavigationLink { AlertRuleDetailView(id: rule.id) } label: { RuleSummary(rule: rule) }
                        .accessibilityIdentifier("rule-\(rule.id)")
                }
                Section {
                    if !history.isEmpty { Button("Previous rule page") { cursor = history.removeLast(); Task { await load() } }.disabled(loading) }
                    if let next = page.nextCursor { Button("Next rule page") { history.append(cursor); cursor = next; Task { await load() } }.disabled(loading) }
                    Text("Up to 20 rules per page. All rules belong to your current account.").font(.caption).foregroundStyle(.secondary)
                }
            }
        }.navigationTitle("Alert rules")
            .toolbar { Button { creating = true } label: { Label("New rule", systemImage: "plus") }.disabled(!store.active).accessibilityIdentifier("newRule") }
            .sheet(isPresented: $creating, onDismiss: { Task { await load(reset: true) } }) { AlertEditor() }
            .task(id: store.active) { if store.active { await load() } else { clear() } }
            .refreshable { await load(reset: true) }
            .onDisappear { clear() }
    }
    private func clear() { generation = UUID(); page = nil; loading = false }
    private func load(reset: Bool = false) async {
        guard store.active else { clear(); return }
        if reset { cursor = nil; history = [] }
        let request = UUID(), requestedCursor = cursor
        generation = request; loading = true; page = nil; error = nil
        defer { if generation == request { loading = false } }
        do {
            let value = try await store.generatedRuleRequest { try await $0.rules(query: .init(cursor: requestedCursor, limit: 20)) }
            try value.validate()
            guard generation == request, store.active, !Task.isCancelled else { return }
            page = value
        } catch is CancellationError { }
        catch { if generation == request { page = nil; self.error = error.localizedDescription } }
    }
}

private struct RuleSummary: View {
    let rule: AlertRule
    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(rule.name).font(.headline)
            Text("\(rule.enabled ? "Enabled" : "Stopped") · \(ruleScopeLabel(rule.scope))").font(.subheadline)
            Text(rule.outcomeMode == "all_terminal" ? "Every terminal outcome, including future outcomes" : rule.outcomes.map(ruleOutcomeLabel).joined(separator: ", ")).font(.caption)
            if rule.hiddenScopeCount > 0 { Text("\(rule.hiddenScopeCount) scope references hidden").font(.caption).foregroundStyle(.secondary) }
        }.padding(.vertical, 4)
    }
}

struct AlertRuleDetailView: View {
    @Environment(DashboardStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let id: String
    @State private var rule: AlertRule?
    @State private var generation = UUID()
    @State private var action: Task<Void, Never>?
    @State private var busy = false
    @State private var error: String?
    @State private var guardState = RuleSubmissionGuard()
    @State private var editing = false
    @State private var deleting = false
    var body: some View {
        List {
            if let error { Section { ErrorMessage(error: error) } }
            if guardState.conflict { Section { Text("This rule changed elsewhere. Reload and review its current settings before another change."); Button("Reload current rule") { Task { await load() } } } }
            if busy && rule == nil { ProgressView("Checking current rule…") }
            if let rule {
                Section("Personal monitoring") {
                    RuleSummary(rule: rule)
                    LabeledContent("Revision", value: rule.revision)
                    Button(rule.enabled ? "Stop monitoring" : "Enable monitoring") { mutate(.enabled(!rule.enabled)) }
                        .disabled(busy || guardState.conflict).accessibilityIdentifier("ruleEnabled")
                    Button("Revalidate selected scopes") { mutate(.revalidate) }.disabled(busy || guardState.conflict || !rule.enabled)
                    Text("Revalidation checks current access and establishes fresh monitoring where needed. It does not backfill historical terminal jobs.").font(.footnote)
                }
                Section("Selected scope status") {
                    ForEach(rule.scopes, id: \.namespaceRef) { scope in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(ruleNamespaceLabel(scope.namespaceRef, store: store))
                            Text("Status: \(scope.status.replacingOccurrences(of: "_", with: " "))").font(.subheadline)
                            if let activated = scope.activatedAt { TimestampRow(label: "Monitoring since", value: activated) }
                        }
                    }
                    if rule.inaccessibleScopes > 0 { Text("\(rule.inaccessibleScopes) scope references hidden because access is unavailable or no longer permitted.") }
                    if rule.unavailableScopes > 0 { Text("\(rule.unavailableScopes) scope references hidden while their source or authorization service is unavailable.") }
                }
                if !rule.jobs.isEmpty {
                    Section("Watched jobs") {
                        ForEach(rule.jobs, id: \.nativeRef) { job in
                            NavigationLink { JobDetailView(ref: job.nativeRef) } label: { Text(job.jobId).font(.caption); Text(ruleNamespaceLabel(job.nativeRef.namespace, store: store)).font(.caption) }
                        }
                    }
                }
                Section {
                    Button("Edit rule") { editing = true }.disabled(busy || guardState.conflict || !rule.canEditFullRule).accessibilityIdentifier("editRule")
                    if !rule.canEditFullRule { Text(rule.hiddenScopeCount > 0 ? RuleDraftError.hiddenReferences.localizedDescription : RuleDraftError.unsupported.localizedDescription).font(.footnote).accessibilityIdentifier("ruleEditRestriction") }
                    Button("Delete alert rule", role: .destructive) { deleting = true }.disabled(busy || guardState.conflict)
                    TimestampRow(label: "Last updated", value: rule.updatedAt)
                }
            } else if !busy { Button("Reload current rule") { Task { await load() } } }
        }.navigationTitle("Alert rule").navigationBarTitleDisplayMode(.inline)
            .sheet(isPresented: $editing, onDismiss: { Task { await load() } }) { AlertEditor(ruleID: id) }
            .confirmationDialog("Delete this alert rule? Other matching rules will continue to apply.", isPresented: $deleting) { Button("Delete rule", role: .destructive) { mutate(.delete) } }
            .task(id: store.active) { if store.active { await load() } else { clear() } }
            .refreshable { await load() }
            .onDisappear { action?.cancel(); clear() }
    }
    private enum Mutation { case enabled(Bool), revalidate, delete }
    private func clear() { generation = UUID(); action?.cancel(); rule = nil; busy = false }
    private func load() async {
        guard store.active else { clear(); return }
        let request = UUID(); generation = request; rule = nil; busy = true; error = nil
        defer { if generation == request { busy = false } }
        do {
            let value = try await store.generatedRuleRequest { try await $0.rule(ruleId: id) }
            try value.validate(expectedID: id)
            guard generation == request, store.active, !Task.isCancelled else { return }
            rule = value; guardState.reviewedReload()
        } catch is CancellationError { }
        catch { if generation == request { rule = nil; self.error = error.localizedDescription } }
    }
    private func mutate(_ mutation: Mutation) {
        guard let rule, !busy, !guardState.blocksSubmission, store.active else { return }
        let request = generation
        busy = true; error = nil
        action = Task { @MainActor in
            defer { if generation == request { busy = false } }
            do {
                let result: AlertRule?
                switch mutation {
                case .enabled(let enabled): result = try await store.generatedRuleRequest { try await $0.setRuleEnabled(ruleId: id, headers: .init(ifMatch: rule.revision), body: .init(enabled: enabled)) }
                case .revalidate: result = try await store.generatedRuleRequest { try await $0.revalidateRule(ruleId: id, headers: .init(ifMatch: rule.revision)) }
                case .delete:
                    try await store.generatedRuleRequest { try await $0.deleteRule(ruleId: id, headers: .init(ifMatch: rule.revision)) }
                    result = nil
                }
                if let result { try result.validate(expectedID: id) }
                guard generation == request, store.active, !Task.isCancelled else { return }
                if let result { self.rule = result } else { self.rule = nil; dismiss() }
            } catch is CancellationError { }
            catch {
                guard generation == request else { return }
                guardState.failed(error, creating: false); self.error = error.localizedDescription
                if ruleAuthorityFailure(error) { self.rule = nil }
            }
        }
    }
}

struct AlertEditor: View {
    @Environment(DashboardStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let ruleID: String?
    @State private var draft: RuleDraft
    @State private var revision: String?
    @State private var ready: Bool
    @State private var saving = false
    @State private var generation = UUID()
    @State private var action: Task<Void, Never>?
    @State private var error: String?
    @State private var guardState = RuleSubmissionGuard()
    @State private var watchNamespace: NamespaceRef?
    @State private var watchJobID = ""
    @FocusState private var focusedField: Field?
    private enum Field { case name, job }
    init(ruleID: String? = nil, watching job: JobRef? = nil) {
        self.ruleID = ruleID
        _draft = State(initialValue: RuleDraft(watching: job)); _ready = State(initialValue: ruleID == nil)
        _watchNamespace = State(initialValue: job?.namespace)
    }
    var body: some View {
        NavigationStack {
            Form {
                if let error { Section { ErrorMessage(error: error) } }
                if guardState.conflict { Section { Text("This rule changed elsewhere. Your edits have not overwritten it."); Button("Discard edits and reload") { Task { await reload() } } } }
                if guardState.uncertainCreation { Section { Text("The create response was uncertain. Close this editor and check the refreshed rule list before creating another rule.").accessibilityIdentifier("uncertainRuleCreation") } }
                if ready && saving { Section { ProgressView("Saving rule…").accessibilityIdentifier("savingRule") } }
                if ready {
                    Section("Rule") {
                        TextField("Name", text: $draft.name).focused($focusedField, equals: .name).submitLabel(.done).onSubmit { focusedField = nil }.accessibilityIdentifier("ruleName")
                        Text("\(draft.name.utf8.count) / 120 UTF-8 bytes").font(.caption).foregroundStyle(.secondary)
                        Toggle("Enabled", isOn: $draft.enabled)
                        Picker("Scope", selection: Binding(get: { draft.scope }, set: { draft.selectScope($0) })) {
                            Text("All my jobs").tag("my_jobs"); Text("Watched jobs").tag("watched_jobs"); Text("Namespace jobs").tag("namespace_jobs")
                        }.accessibilityIdentifier("ruleScope")
                        Text(draft.scope == "my_jobs" ? "Follow current and future jobs submitted by you in the selected namespaces." : draft.scope == "namespace_jobs" ? "Follow every member’s eligible jobs in the selected namespaces." : "Follow only the explicit jobs listed below.").font(.footnote)
                    }
                    namespaceSection
                    if draft.scope == "watched_jobs" { jobsSection }
                    outcomeSection
                    Section { Text("Monitoring starts after an established source checkpoint. An outage can leave individual namespaces pending. Changing scope or outcomes starts new monitoring intervals.").font(.footnote) }
                } else if saving { ProgressView("Checking full rule access…") }
            }.scrollDismissesKeyboard(.interactively).disabled(saving).navigationTitle(ruleID == nil ? "New alert rule" : "Edit alert rule").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItemGroup(placement: .keyboard) { Spacer(); Button("Done") { focusedField = nil }.accessibilityIdentifier("ruleKeyboardDone") }
                    ToolbarItem(placement: .cancellationAction) { Button(guardState.uncertainCreation ? "Close" : "Cancel") { dismiss() }.accessibilityLabel(guardState.uncertainCreation ? "Close and check rules" : "Cancel").disabled(saving) }
                    ToolbarItem(placement: .confirmationAction) { Button("Save") { save() }.disabled(!ready || saving || guardState.blocksSubmission || (try? draft.input()) == nil).accessibilityIdentifier("saveRule") }
                }
        }.interactiveDismissDisabled(saving)
            .task { if ruleID != nil { await reload() } }
            .onChange(of: store.active) { _, active in if !active { clear(); dismiss() } }
            .onDisappear { clear() }
    }
    private var namespaceSection: some View {
        Section("Selected namespaces") {
            if !store.hasFreshNamespaceOptions {
                Text("Namespace choices are unavailable until access can be refreshed. Existing rule controls remain available from the current rule page.").font(.footnote)
                ForEach(draft.namespaces) { ref in Text(ruleNamespaceLabel(ref, store: store)).font(.caption) }
            }
            ForEach(store.hasFreshNamespaceOptions ? (store.bootstrap?.deployments ?? []) : []) { deployment in
                ForEach(deployment.namespaces) { namespace in
                    let ref = NamespaceRef(deploymentId: deployment.id, namespaceId: namespace.id)
                    Toggle("\(deployment.name) / \(namespace.name)", isOn: Binding(get: { draft.namespaces.contains(ref) }, set: { selected in
                        draft.selectNamespace(ref, selected: selected)
                        if !selected && watchNamespace == ref { watchNamespace = nil }
                    })).accessibilityIdentifier("ruleScope-\(deployment.id)-\(namespace.id)")
                }
            }
            Text("Removing a namespace also removes its watched jobs from this draft.").font(.footnote).foregroundStyle(.secondary)
        }
    }
    private var jobsSection: some View {
        Section("Watched jobs · \(draft.jobs.count) / 100") {
            ForEach(draft.jobs) { job in
                VStack(alignment: .leading) { Text(job.jobId).font(.caption); Text(ruleNamespaceLabel(job.namespace, store: store)).font(.caption).foregroundStyle(.secondary) }
            }.onDelete { draft.jobs.remove(atOffsets: $0) }
            Picker("Namespace for job", selection: $watchNamespace) {
                Text("Choose namespace").tag(Optional<NamespaceRef>.none)
                ForEach(draft.namespaces) { ref in Text(ruleNamespaceLabel(ref, store: store)).tag(Optional(ref)) }
            }
            TextField("Full job UUID", text: $watchJobID).focused($focusedField, equals: .job).submitLabel(.done).onSubmit { focusedField = nil }.textInputAutocapitalization(.never).autocorrectionDisabled().accessibilityIdentifier("watchJobID")
            Button("Add job") {
                do { if let watchNamespace { try draft.addJob(id: watchJobID, namespace: watchNamespace); watchJobID = ""; error = nil } }
                catch { self.error = error.localizedDescription }
            }.disabled(watchNamespace == nil || watchJobID.isEmpty || draft.jobs.count >= 100)
        }
    }
    private var outcomeSection: some View {
        Section("Terminal events") {
            Toggle("Every terminal outcome", isOn: Binding(get: { draft.outcomeMode == "all_terminal" }, set: { draft.selectAllTerminal($0) }))
            if draft.outcomeMode == "all_terminal" { Text("Includes success, unsuccessful outcomes, cancellation, and future unknown terminal outcomes.").font(.footnote) }
            else {
                Button("Select unsuccessful outcomes") { draft.outcomes = RuleDraft.unsuccessful }
                Button("Select success only") { draft.outcomes = ["success"] }
                Button("Select cancellation only") { draft.outcomes = ["cancelled"] }
                ForEach(RuleDraft.knownOutcomes, id: \.self) { outcome in
                    Toggle(ruleOutcomeLabel(outcome), isOn: Binding(get: { draft.outcomes.contains(outcome) }, set: { selected in
                        if selected { if !draft.outcomes.contains(outcome) { draft.outcomes.append(outcome) } }
                        else { draft.outcomes.removeAll { $0 == outcome } }
                    }))
                }
            }
        }
    }
    private func clear() { generation = UUID(); action?.cancel(); draft = RuleDraft(); revision = nil; ready = false; watchJobID = ""; watchNamespace = nil; saving = false }
    private func reload() async {
        guard let ruleID, store.active else { return }
        clear(); let request = generation; saving = true; error = nil
        defer { if generation == request { saving = false } }
        do {
            let value = try await store.generatedRuleRequest { try await $0.rule(ruleId: ruleID) }
            try value.validate(expectedID: ruleID)
            let valueDraft = try RuleDraft(rule: value)
            guard generation == request, store.active, !Task.isCancelled else { return }
            draft = valueDraft; revision = value.revision; ready = true; guardState.reviewedReload()
        } catch is CancellationError { }
        catch { if generation == request { self.error = error.localizedDescription } }
    }
    private func save() {
        guard store.active, ready, !saving, !guardState.blocksSubmission, let body = try? draft.input() else { return }
        let request = generation, currentRevision = revision, editingID = ruleID
        saving = true; error = nil
        action = Task { @MainActor in
            defer { if generation == request { saving = false } }
            do {
                let value: AlertRule
                if let editingID, let currentRevision { value = try await store.generatedRuleRequest { try await $0.updateRule(ruleId: editingID, headers: .init(ifMatch: currentRevision), body: body) } }
                else { value = try await store.generatedRuleRequest { try await $0.createRule(body: body) } }
                try value.validate(expectedID: editingID)
                guard generation == request, store.active, !Task.isCancelled else { return }
                dismiss()
            } catch is CancellationError { }
            catch {
                guard generation == request else { return }
                guardState.failed(error, creating: editingID == nil); self.error = error.localizedDescription
                if ruleAuthorityFailure(error) { draft = RuleDraft(); revision = nil; ready = false; watchJobID = ""; watchNamespace = nil }
            }
        }
    }
}

private func ruleScopeLabel(_ scope: String) -> String {
    switch scope { case "my_jobs": "All my jobs"; case "watched_jobs": "Watched jobs"; case "namespace_jobs": "Namespace jobs"; default: scope }
}
private func ruleOutcomeLabel(_ value: String) -> String { value.replacingOccurrences(of: "_", with: " ").capitalized }
@MainActor private func ruleNamespaceLabel(_ ref: NamespaceRef, store: DashboardStore) -> String {
    guard store.hasFreshNamespaceOptions, let deployment = store.bootstrap?.deployments.first(where: { $0.id == ref.deploymentId }),
          let namespace = deployment.namespaces.first(where: { $0.id == ref.namespaceId }) else { return "\(ref.deploymentId) / \(ref.namespaceId)" }
    return "\(deployment.name) / \(namespace.name)"
}
private func ruleAuthorityFailure(_ error: Error) -> Bool {
    guard let error = error as? DashboardError else { return false }
    return [.authenticationRequired, .authorizationUnavailable, .forbidden, .notFound].contains(error)
}
private extension DashboardAPI.RuleScope { var namespaceRef: NamespaceRef { .init(deploymentId: deploymentId, namespaceId: namespaceId) } }
private extension DashboardAPI.JobRef { var nativeRef: JobRef { .init(deploymentId: deploymentId, namespaceId: namespaceId, jobId: jobId) } }
