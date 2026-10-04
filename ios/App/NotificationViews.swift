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
                Picker("Appearance", selection: $appearance) { Text("System").tag("system"); Text("Light").tag("light"); Text("Dark").tag("dark") }
                TextField("IANA timezone", text: $timezone).autocorrectionDisabled().textInputAutocapitalization(.never)
                Picker("Data refresh", selection: $refresh) { Text("Manual").tag(0); Text("5 seconds").tag(5); Text("10 seconds").tag(10); Text("30 seconds").tag(30) }
                Button("Save preferences") { Task { await save() } }
                if let error { ErrorMessage(error: error) }
            }
            Section("Notifications") {
                NavigationLink("Manage notification devices") { DevicesView() }
                Text("Notifications contain a generic update. Job details require your private network and current access.").font(.footnote)
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
        guard TimeZone(identifier: timezone) != nil else { error = "Enter a valid timezone, such as America/New_York or UTC."; return }
        value.appearance = appearance; value.timezone = timezone; value.refreshSeconds = refresh
        do {
            let _: Preferences = try await store.request(path: "/api/v1/preferences", method: "PUT", body: JSONEncoder().encode(value), revision: value.revision)
            store.refresh(); error = nil
        } catch { self.error = error.localizedDescription }
    }
}
