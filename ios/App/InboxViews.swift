import DashboardCore
import SwiftUI

struct InboxView: View {
    @Environment(DashboardStore.self) private var store
    @State private var page: DashboardAPI.InboxItemPage?
    @State private var cursor: String?
    @State private var history = InboxPageHistory()
    @State private var unreadOnly = false
    @State private var generation = UUID()
    @State private var loading = false
    @State private var error: String?
    var body: some View {
        List {
            Section {
                InboxScopeMenu()
                Toggle("Unread only", isOn: $unreadOnly).accessibilityIdentifier("inbox-unread-only")
                Text("History remains available when push is disabled or delayed. Original matches remain after a rule is stopped or deleted; retained items expire after 30 days.").font(.footnote).foregroundStyle(.secondary)
            }
            if loading { ProgressView("Checking authorized history…") }
            if let error { Section { ErrorMessage(error: error); Button("Refresh inbox") { Task { await load(reset: true) } } } }
            if let page {
                Section {
                    LabeledContent(page.completeness == "partial" ? "Unread in available scopes" : "Unread in selected scopes", value: page.unreadCount).accessibilityIdentifier("inbox-unread-count")
                    if page.completeness == "partial" {
                        Text(partialDescription(page)).font(.footnote).accessibilityIdentifier("inbox-partial")
                    }
                    TimestampRow(label: "Checked", value: page.fetchedAt)
                    if page.items.isEmpty { Text("No updates in this selection.").foregroundStyle(.secondary) }
                }
                ForEach(page.items) { item in
                    NavigationLink(value: DashboardRoute.inbox(item.id)) { InboxSummary(item: item) }.accessibilityIdentifier("inbox-row-\(item.id)")
                }
                Section {
                    if history.canGoBack { Button("Previous inbox page") { cursor = history.previous(); Task { await load() } }.disabled(loading) }
                    if let next = page.nextCursor { Button("Next inbox page") { history.record(cursor); cursor = next; Task { await load() } }.disabled(loading) }
                    if history.discardedPages > 0 { Text("Earlier back pages were discarded to limit memory. Refresh to return to the newest history.").font(.footnote) }
                    Text("20 updates per page. Refresh starts a new authorized history snapshot.").font(.caption).foregroundStyle(.secondary)
                }
            }
        }.navigationTitle("Inbox")
            .task(id: page?.items.map(\.expiresAt).min()) {
                guard let expires = page?.items.compactMap({ WireDate.parse($0.expiresAt) }).min() else { return }
                do { try await Task.sleep(for: .seconds(max(0, expires.timeIntervalSinceNow))); try Task.checkCancellation(); clear(); error = "This history page contains an expired update. Refresh to check current history." } catch { }
            }
            .toolbar { Button { Task { await load(reset: true) } } label: { Label("Refresh inbox", systemImage: "arrow.clockwise") }.disabled(loading).accessibilityIdentifier("inbox-refresh") }
            .task(id: store.active) { if store.active { await load() } else { clear() } }
            .onChange(of: unreadOnly) { _, _ in Task { await load(reset: true) } }
            .refreshable { await load(reset: true) }
            .onDisappear { clear() }
    }
    private func partialDescription(_ page: DashboardAPI.InboxItemPage) -> String {
        var parts = ["Partial history."]
        if page.unavailableSources > 0 { parts.append("\(page.unavailableSources) sources unavailable.") }
        if page.inaccessibleScopes > 0 { parts.append("\(page.inaccessibleScopes) scopes inaccessible.") }
        if page.items.contains(where: { $0.jobAvailability == "unavailable" }) { parts.append("Some current job details are unavailable.") }
        parts.append("Counts include only currently verified access.")
        return parts.joined(separator: " ")
    }
    private func clear() { generation = UUID(); page = nil; loading = false; error = nil }
    private func load(reset: Bool = false) async {
        guard store.active else { clear(); return }
        if reset { cursor = nil; history.reset() }
        let request = UUID(), selected = store.scope, requestedCursor = cursor, unread = unreadOnly
        generation = request; page = nil; error = nil; loading = true
        defer { if generation == request { loading = false } }
        do {
            let scope = try selected.queryItems(authorized: store.bootstrap?.namespaces ?? []).first(where: { $0.name == "scope" })?.value
            let result = try await store.generatedAccountRequest(clearNamespaceContentOnFailure: false) { try await $0.inbox(query: .init(scope: scope, cursor: requestedCursor, limit: 20, unread: unread)) }
            try result.validate(scope: selected, unreadOnly: unread)
            guard generation == request, store.active, selected == store.scope, !Task.isCancelled else { return }
            page = result
        } catch is CancellationError { }
        catch { if generation == request { page = nil; self.error = error.localizedDescription } }
    }
}

private struct InboxScopeMenu: View {
    @Environment(DashboardStore.self) private var store
    var body: some View {
        Menu {
            Button("All currently authorized namespaces") { store.selectScope(.all) }
            Button("No namespaces") { store.selectScope(.deployments([])) }
            if store.hasFreshNamespaceOptions {
                ForEach(store.bootstrap?.deployments ?? []) { source in
                    Section(source.name) {
                        Button("All in \(source.name)") { store.selectScope(.deployments([source.id])) }
                        ForEach(source.namespaces) { namespace in Button(namespace.name) { store.selectScope(.namespace(.init(deploymentId: source.id, namespaceId: namespace.id))) } }
                    }
                }
            } else { Text("Namespace choices need refreshed authorization") }
        } label: { Label(store.scope == .deployments([]) ? "No namespaces" : store.hasFreshNamespaceOptions ? store.scopeLabel : "Inbox source selection", systemImage: "line.3.horizontal.decrease.circle").frame(maxWidth: .infinity, alignment: .leading).contentShape(Rectangle()) }
            .accessibilityIdentifier("inbox-scope")
    }
}

private struct InboxSummary: View {
    let item: NotificationInboxItem
    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Label(item.outcome.replacingOccurrences(of: "_", with: " ").capitalized, systemImage: item.read ? "envelope.open" : "envelope.badge").font(.headline)
            Text(item.displayName).font(.subheadline)
            Text("\(item.job.deploymentId) / \(item.job.namespaceId)").font(.caption).foregroundStyle(.secondary)
            TimestampRow(label: "Recorded terminal event", value: item.eventAt)
            if item.jobAvailability != "available" { Text(item.jobAvailability == "missing" ? "Current job no longer available; retained event history" : "Current job unavailable; no cached name shown").font(.caption) }
        }.padding(.vertical, 4)
    }
}

struct InboxDestinationView: View {
    @Environment(DashboardStore.self) private var store
    let id: String
    @State private var item: NotificationInboxItem?
    @State private var generation = UUID()
    @State private var action: Task<Void, Never>?
    @State private var loading = false
    @State private var error: String?
    var body: some View {
        List {
            if loading && item == nil { ProgressView("Opening authorized update…") }
            if let error { ErrorMessage(error: error) }
            if let item {
                Section("Recorded update") {
                    InboxSummary(item: item)
                    if let completed = item.observedCompletedAt { TimestampRow(label: "Observed completion", value: completed) }
                    TimestampRow(label: "Added to inbox", value: item.createdAt)
                    TimestampRow(label: "Expires", value: item.expiresAt)
                    if let readAt = item.readAt { TimestampRow(label: "Marked read", value: readAt) }
                    Button(item.read ? "Mark unread" : "Mark read") { markRead(!item.read) }.disabled(loading).accessibilityIdentifier("inbox-mark-read")
                    if item.jobAvailability == "available" { NavigationLink { JobDetailView(ref: item.jobReference) } label: { Label("Open current job", systemImage: "arrow.up.right.square") } }
                }
                Section("Original rule matches") {
                    Text("These versions matched when this update was created. Later rule edits or deletion do not rewrite this history.").font(.footnote)
                    ForEach(item.matchedRules, id: \.ruleId) { match in
                        DisclosureGroup(match.name) {
                            LabeledContent("Rule version", value: match.revision).accessibilityIdentifier("inbox-rule-version-\(match.ruleId)")
                            LabeledContent("Rule ID", value: match.ruleId).font(.caption)
                            LabeledContent("Scope", value: match.scope.replacingOccurrences(of: "_", with: " "))
                            Text(match.outcomeMode == "all_terminal" ? "Every terminal outcome, including future outcomes" : "Outcomes: \(match.outcomes.joined(separator: ", "))")
                        }
                    }
                }
                Section("Delivery records") {
                    LabeledContent("Device delivery records", value: item.delivery.total)
                    ForEach(item.delivery.byState.keys.sorted(), id: \.self) { state in LabeledContent(state.replacingOccurrences(of: "_", with: " "), value: item.delivery.byState[state] ?? "0") }
                    Text("Provider acceptance records a handoff. It does not prove the phone displayed or read the notification. Inbox history is independent of delivery.").font(.footnote).foregroundStyle(.secondary).accessibilityIdentifier("inbox-delivery-disclosure")
                }
                Section("Event identity") {
                    LabeledContent("Inbox ID", value: item.id).font(.caption)
                    LabeledContent("Event ID", value: item.eventId).font(.caption)
                    LabeledContent("Control instance", value: item.controlInstanceId).font(.caption)
                }
            }
            Button("Reload authorized update") { Task { await load() } }.disabled(loading)
        }.navigationTitle("Update").navigationBarTitleDisplayMode(.inline)
            .task(id: item?.expiresAt) {
                guard let expires = item.flatMap({ WireDate.parse($0.expiresAt) }) else { return }
                do { try await Task.sleep(for: .seconds(max(0, expires.timeIntervalSinceNow))); try Task.checkCancellation(); clear(); error = "This update has expired. Reload to check current access." } catch { }
            }
            .task(id: store.active) { if store.active { await load() } else { clear() } }
            .refreshable { await load() }
            .onDisappear { clear() }
    }
    private func clear() { generation = UUID(); action?.cancel(); action = nil; item = nil; loading = false; error = nil }
    private func load() async {
        guard store.active else { clear(); return }
        let request = UUID(); generation = request; item = nil; loading = true; error = nil
        defer { if generation == request { loading = false } }
        do {
            let value = try await store.generatedAccountRequest(clearNamespaceContentOnFailure: false) { try await $0.inboxItem(inboxId: id) }
            try value.validate(expectedID: id)
            guard generation == request, store.active, !Task.isCancelled else { return }
            item = value
        } catch is CancellationError { }
        catch { if generation == request { item = nil; self.error = error.localizedDescription } }
    }
    private func markRead(_ read: Bool) {
        guard !loading, item != nil else { return }
        let request = UUID(); generation = request; loading = true; error = nil
        action?.cancel()
        action = Task { @MainActor in
            defer { if generation == request { loading = false } }
            do {
                let value = try await store.generatedAccountRequest(clearNamespaceContentOnFailure: false) { try await $0.updateInbox(inboxId: id, body: .init(read: read)) }
                try value.validate(expectedID: id)
                guard generation == request, store.active, !Task.isCancelled else { return }
                item = value
            } catch is CancellationError { }
            catch { if generation == request { item = nil; self.error = error.localizedDescription } }
        }
    }
}
