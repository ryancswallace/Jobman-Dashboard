import DashboardCore
import SwiftUI
import UserNotifications

struct MoreView: View {
    var body: some View {
        List {
            NavigationLink { ContentLinkView() } label: { Label("Open a Dashboard link", systemImage: "link") }
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
    var body: some View {
        Form {
            Section("Display") {
                Picker("Appearance", selection: $appearance) { Text("Use device setting").tag("system"); Text("Light").tag("light"); Text("Dark").tag("dark") }
                TextField("Time zone, e.g. America/New_York", text: $timezone).autocorrectionDisabled().textInputAutocapitalization(.never)
                Text("Use a time zone name such as America/New_York, Europe/London or UTC.").font(.footnote).foregroundStyle(.secondary)
                Picker("Automatic refresh", selection: $refresh) { Text("Manual only").tag(0); Text("5 seconds").tag(5); Text("10 seconds").tag(10); Text("30 seconds").tag(30) }
                Text("Manual only stops routine data refreshes. Access checks continue, and active log following or a report being prepared may still refresh.").font(.footnote).foregroundStyle(.secondary)
                Button("Save settings") { Task { await save() } }
                if let error { ErrorMessage(error: error) }
            }
            Section("Notifications") {
                NavigationLink("Manage notification devices") { DevicesView() }
                Text("Notifications show a generic message. To view job details, connect to your private network and sign in with permission to view the job.").font(.footnote)
            }
            Section("Account and access") {
                Text("A deployment is a connected Jobman Control service. A namespace groups jobs within a deployment and has its own access permissions.").font(.footnote).foregroundStyle(.secondary)
                Text(store.bootstrap?.account.displayName ?? "Not available")
                ForEach(store.bootstrap?.deployments ?? []) { deployment in
                    ForEach(deployment.namespaces) { namespace in
                        DisclosureGroup("\(deployment.name) / \(namespace.name)") {
                            Text("Your roles: \(namespace.roles.joined(separator: ", "))")
                            Text("Permissions from these roles: \(namespace.capabilities.joined(separator: ", "))")
                        }
                    }
                }
                Button(role: .destructive) { store.signOut() } label: {
                    Text("Sign out").frame(maxWidth: .infinity, alignment: .leading).contentShape(Rectangle())
                }.buttonStyle(.borderless)
            }
        }.navigationTitle("Settings").task {
            if let preferences = store.bootstrap?.preferences { appearance = preferences.appearance; timezone = preferences.timezone; refresh = preferences.refreshSeconds }
        }
    }
    private func save() async {
        guard var value = store.bootstrap?.preferences else { return }
        guard TimeZone(identifier: timezone) != nil else { error = "Enter a time zone such as America/New_York or UTC."; return }
        value.appearance = appearance; value.timezone = timezone; value.refreshSeconds = refresh
        do {
            let _: Preferences = try await store.request(path: "/api/v1/preferences", method: "PUT", body: JSONEncoder().encode(value), revision: value.revision)
            store.refresh(); error = nil
        } catch { self.error = error.localizedDescription }
    }
}
