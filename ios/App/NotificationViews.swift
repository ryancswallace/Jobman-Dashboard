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
