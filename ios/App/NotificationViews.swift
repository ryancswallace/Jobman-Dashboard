import DashboardCore
import SwiftUI
import UserNotifications

struct InboxView: View {
    var body: some View {
        List {
            Section { Text("History remains available when push delivery is disabled or delayed. Items are retained for 30 days.").font(.footnote).foregroundStyle(.secondary) }
            PagedRows<InboxItem, InboxRow>(path: "/api/v1/inbox", scoped: false) { InboxRow(item: $0) }
        }.navigationTitle("Inbox")
    }
}

private struct InboxRow: View {
    let item: InboxItem
    var body: some View {
        NavigationLink { InboxDetailView(item: item) } label: {
            VStack(alignment: .leading, spacing: 5) {
                Label(item.outcome.replacingOccurrences(of: "_", with: " ").capitalized, systemImage: item.read ? "envelope.open" : "envelope.badge")
                Text("\(item.job.deploymentId) / \(item.job.namespaceId)").font(.caption)
                TimestampRow(label: "Event", value: item.eventAt)
            }
        }
    }
}

struct InboxDestinationView: View {
    @Environment(DashboardStore.self) private var store
    let id: String
    @State private var item: InboxItem?
    @State private var error: String?
    var body: some View {
        Group {
            if let item { InboxDetailView(item: item) }
            else if let error { ContentUnavailableView("Update unavailable", systemImage: "network.slash", description: Text(error)) }
            else { ProgressView("Opening authorized update…") }
        }.task {
            do { item = try await store.request(path: "/api/v1/inbox/\(APIPath.component(id))") }
            catch { self.error = error.localizedDescription }
        }
    }
}

private struct InboxDetailView: View {
    @Environment(DashboardStore.self) private var store
    let item: InboxItem
    @State private var read: Bool?
    @State private var error: String?
    var body: some View {
        List {
            Section {
                LabeledContent("Outcome", value: item.outcome)
                Text("\(item.job.deploymentId) / \(item.job.namespaceId)")
                TimestampRow(label: "Event", value: item.eventAt)
                ForEach(item.matchedRules, id: \.self) { Text("Matched rule: \($0)") }
                if let status = item.deliveryStatus { LabeledContent("Delivery", value: status) }
                NavigationLink { JobDetailView(ref: item.job) } label: { Label("Open current job", systemImage: "arrow.up.right.square") }
                Button((read ?? item.read) ? "Mark unread" : "Mark read") { Task { await markRead() } }
                if let error { ErrorMessage(error: error) }
            }
        }.navigationTitle("Update")
    }
    private func markRead() async {
        do {
            let next = !(read ?? item.read)
            struct Change: Encodable { let read: Bool }
            let _: InboxItem = try await store.request(path: "/api/v1/inbox/\(APIPath.component(item.id))", method: "PATCH", body: JSONEncoder().encode(Change(read: next)))
            read = next; error = nil
        } catch { self.error = error.localizedDescription }
    }
}

struct AlertRulesView: View {
    @State private var newRule = false
    @State private var refresh = UUID()
    var body: some View {
        List {
            Section { Text("Rules are opt-in. A new namespace or deployment is never added to a rule automatically.").font(.footnote) }
            PagedRows<AlertRule, AlertRuleRow>(path: "/api/v1/rules", scoped: false) { AlertRuleRow(rule: $0) }.id(refresh)
        }.navigationTitle("Alert rules").toolbar { Button { newRule = true } label: { Label("New rule", systemImage: "plus") } }
            .sheet(isPresented: $newRule, onDismiss: { refresh = UUID() }) { AlertEditor(initial: AlertRule()) }
            .refreshable { refresh = UUID() }
    }
}

private struct AlertRuleRow: View {
    let rule: AlertRule
    @State private var editing = false
    var body: some View {
        Button { editing = true } label: {
            VStack(alignment: .leading) {
                Text(rule.name).font(.headline)
                Text("\(rule.enabled ? "Enabled" : "Disabled") • \(rule.scope.replacingOccurrences(of: "_", with: " "))").font(.caption)
                Text(rule.outcomeMode == "all_terminal" ? "All terminal outcomes" : rule.outcomes.joined(separator: ", ")).font(.caption)
                ForEach(Array((rule.activation ?? []).enumerated()), id: \.offset) { _, activation in Text("\(activation.deploymentId): \(activation.status)").font(.caption) }
            }
        }.sheet(isPresented: $editing) { AlertEditor(initial: rule) }
    }
}

struct AlertEditor: View {
    @Environment(DashboardStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var rule: AlertRule
    @State private var saving = false
    @State private var error: String?
    @State private var watchNamespace: NamespaceRef?
    @State private var watchJobId = ""
    @State private var confirmingDelete = false
    init(initial: AlertRule) { _rule = State(initialValue: initial) }
    var body: some View {
        NavigationStack {
            Form {
                Section("Rule") {
                    TextField("Name", text: $rule.name)
                    Toggle("Enabled", isOn: $rule.enabled)
                    Picker("Scope", selection: $rule.scope) {
                        Text("Watched jobs").tag("watched_jobs")
                        Text("All my jobs").tag("my_jobs")
                        Text("Namespace jobs").tag("namespace_jobs")
                    }
                }
                Section("Selected namespaces") {
                    ForEach(store.bootstrap?.deployments ?? []) { deployment in
                        ForEach(deployment.namespaces) { namespace in
                            let ref = NamespaceRef(deploymentId: deployment.id, namespaceId: namespace.id)
                            Toggle("\(deployment.name) / \(namespace.name)", isOn: Binding(get: { rule.namespaces.contains(ref) }, set: { selected in
                                if selected { if !rule.namespaces.contains(ref) { rule.namespaces.append(ref) } }
                                else { rule.namespaces.removeAll { $0 == ref }; rule.jobs.removeAll { $0.namespace == ref } }
                            }))
                        }
                    }
                }
                if rule.scope == "watched_jobs" {
                    Section("Watched jobs") {
                        ForEach(rule.jobs) { job in Text("\(job.deploymentId) / \(job.namespaceId) / \(job.jobId)") }.onDelete { rule.jobs.remove(atOffsets: $0) }
                        Picker("Namespace for job", selection: $watchNamespace) {
                            Text("Choose namespace").tag(Optional<NamespaceRef>.none)
                            ForEach(rule.namespaces) { ref in Text("\(ref.deploymentId) / \(ref.namespaceId)").tag(Optional(ref)) }
                        }
                        TextField("Full job ID", text: $watchJobId).textInputAutocapitalization(.never).autocorrectionDisabled()
                        Button("Add job") {
                            if let ref = watchNamespace {
                                let job = JobRef(deploymentId: ref.deploymentId, namespaceId: ref.namespaceId, jobId: watchJobId)
                                if !rule.jobs.contains(job) { rule.jobs.append(job) }; watchJobId = ""
                            }
                        }.disabled(watchNamespace == nil || watchJobId.isEmpty)
                    }
                }
                Section("Terminal events") {
                    Toggle("Every terminal outcome", isOn: Binding(get: { rule.outcomeMode == "all_terminal" }, set: { rule.outcomeMode = $0 ? "all_terminal" : "selected" }))
                    if rule.outcomeMode == "selected" {
                        Button("Select unsuccessful outcomes") { rule.outcomes = ["failure", "timed_out", "aborted", "lost"] }
                        ForEach(["failure", "timed_out", "aborted", "lost", "success", "cancelled"], id: \.self) { outcome in
                            Toggle(outcome.replacingOccurrences(of: "_", with: " ").capitalized, isOn: Binding(get: { rule.outcomes.contains(outcome) }, set: { selected in
                                if selected { if !rule.outcomes.contains(outcome) { rule.outcomes.append(outcome) } } else { rule.outcomes.removeAll { $0 == outcome } }
                            }))
                        }
                    }
                    Text("Rules begin from an established source checkpoint and apply to future eligible events. Source outages may leave activation pending.").font(.footnote)
                }
                if let error { ErrorMessage(error: error) }
                if !rule.id.isEmpty { Section { Button("Delete alert rule", role: .destructive) { confirmingDelete = true } } }
            }.navigationTitle(rule.id.isEmpty ? "New alert rule" : "Edit alert rule")
                .confirmationDialog("Delete this alert rule? Other matching rules will continue to apply.", isPresented: $confirmingDelete) {
                    Button("Delete rule", role: .destructive) { Task { await delete() } }
                }
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                    ToolbarItem(placement: .confirmationAction) { Button("Save") { Task { await save() } }.disabled(saving || rule.name.isEmpty || rule.namespaces.isEmpty || (rule.outcomeMode == "selected" && rule.outcomes.isEmpty) || (rule.scope == "watched_jobs" && rule.jobs.isEmpty)) }
                }
        }
    }
    private func save() async {
        saving = true; defer { saving = false }
        do {
            let _: AlertRule = try await store.request(path: "/api/v1/rules" + (rule.id.isEmpty ? "" : "/\(APIPath.component(rule.id))"), method: rule.id.isEmpty ? "POST" : "PUT", body: JSONEncoder().encode(rule.input), revision: rule.id.isEmpty ? nil : rule.revision)
            dismiss()
        } catch { self.error = error.localizedDescription }
    }
    private func delete() async {
        saving = true; defer { saving = false }
        do {
            let _: EmptyResponse = try await store.request(path: "/api/v1/rules/\(APIPath.component(rule.id))", method: "DELETE", revision: rule.revision)
            dismiss()
        } catch { self.error = error.localizedDescription }
    }
}

struct MoreView: View {
    var body: some View {
        List {
            NavigationLink { TargetsView() } label: { Label("Targets", systemImage: "server.rack") }
            NavigationLink { AlertRulesView() } label: { Label("Alert rules", systemImage: "bell.badge") }
            NavigationLink { SettingsView() } label: { Label("Settings", systemImage: "gearshape") }
        }.navigationTitle("More")
    }
}

struct SettingsView: View {
    @Environment(DashboardStore.self) private var store
    @State private var appearance = "system"
    @State private var timezone = "UTC"
    @State private var refresh = 5
    @State private var error: String?
    @State private var notificationState = "Not checked"
    var body: some View {
        Form {
            Section("Display") {
                Picker("Appearance", selection: $appearance) { Text("System").tag("system"); Text("Light").tag("light"); Text("Dark").tag("dark") }
                TextField("IANA timezone", text: $timezone).autocorrectionDisabled().textInputAutocapitalization(.never)
                Picker("Data refresh", selection: $refresh) { Text("Manual").tag(0); Text("5 seconds").tag(5); Text("10 seconds").tag(10); Text("30 seconds").tag(30) }
                Button("Save preferences") { Task { await save() } }
                if let error { ErrorMessage(error: error) }
            }
            Section("Notifications") {
                Text("OS permission: \(notificationState)")
                Button("Enable notifications on this phone") { Task { await enableNotifications() } }
                Text("Notifications contain a generic update. Job details require your private network and current access.").font(.footnote)
                NavigationLink("Manage devices") { List { PagedRows<Device, DeviceRow>(path: "/api/v1/devices", scoped: false) { DeviceRow(device: $0) } }.navigationTitle("Devices") }
            }
            Section("Account and access") {
                Text(store.bootstrap?.account.displayName ?? "Unavailable")
                ForEach(store.bootstrap?.deployments ?? []) { deployment in
                    ForEach(deployment.namespaces) { namespace in
                        DisclosureGroup("\(deployment.name) / \(namespace.name)") {
                            Text("Contributing roles: \(namespace.roles.joined(separator: ", "))")
                            Text("Effective capabilities: \(namespace.capabilities.joined(separator: ", "))")
                        }
                    }
                }
                Button("Sign out", role: .destructive) { store.signOut() }
            }
        }.navigationTitle("Settings").task {
            if let preferences = store.bootstrap?.preferences { appearance = preferences.appearance; timezone = preferences.timezone; refresh = preferences.refreshSeconds }
            await checkNotifications()
        }
    }
    private func save() async {
        guard var value = store.bootstrap?.preferences else { return }
        guard TimeZone(identifier: timezone) != nil else { error = "Enter a valid timezone, such as America/New_York or UTC."; return }
        value.appearance = appearance; value.timezone = timezone; value.refreshSeconds = refresh
        do {
            let _: Preferences = try await store.request(path: "/api/v1/preferences", method: "PUT", body: JSONEncoder().encode(value), revision: value.revision)
            store.refresh(); error = nil
        } catch { self.error = error.localizedDescription }
    }
    private func enableNotifications() async {
        guard !store.previewMode else { error = "Synthetic previews do not request notification permission or register with APNs."; return }
        do {
            let granted = try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound])
            if granted {
                if let token = PushRegistration.token { await store.registerPush(token: token, enabled: true) }
                else { PushRegistration.enableRequested = true }
                UIApplication.shared.registerForRemoteNotifications()
            }
            await checkNotifications()
        } catch { self.error = error.localizedDescription }
    }
    private func checkNotifications() async {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        switch settings.authorizationStatus {
        case .authorized: notificationState = "Allowed"
        case .denied: notificationState = "Denied — inbox remains available"
        case .provisional: notificationState = "Provisional"
        case .notDetermined: notificationState = "Not requested"
        default: notificationState = "Other system setting"
        }
    }
}

private struct DeviceRow: View {
    @Environment(DashboardStore.self) private var store
    let device: Device
    @State private var enabled: Bool?
    @State private var error: String?
    @State private var removed = false
    var body: some View {
        VStack(alignment: .leading) {
            if removed { Text("\(device.name) removed") }
            else {
                Toggle(device.name, isOn: Binding(get: { enabled ?? device.enabled }, set: { value in Task { await update(value) } }))
                Button("Remove device", role: .destructive) { Task { await remove() } }
            }
            Text("Permission: \(device.permission ?? "Unavailable")").font(.caption)
            if let error { ErrorMessage(error: error) }
        }
    }
    private func update(_ value: Bool) async {
        do {
            struct Change: Encodable { let enabled: Bool }
            let _: Device = try await store.request(path: "/api/v1/devices/\(APIPath.component(device.id))", method: "PATCH", body: JSONEncoder().encode(Change(enabled: value)))
            enabled = value; error = nil
        } catch { self.error = error.localizedDescription }
    }
    private func remove() async {
        do {
            let _: EmptyResponse = try await store.request(path: "/api/v1/devices/\(APIPath.component(device.id))", method: "DELETE")
            removed = true; error = nil
        } catch { self.error = error.localizedDescription }
    }
}
