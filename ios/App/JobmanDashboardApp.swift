import DashboardCore
import SwiftUI

@main
struct JobmanDashboardApp: App {
    @UIApplicationDelegateAdaptor(NotificationDelegate.self) private var notifications
    @State private var store = DashboardStore()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            VStack(spacing: 0) {
                if store.previewMode {
                    Text("SYNTHETIC PREVIEW • No live sign-in or job data").font(.caption.bold()).frame(maxWidth: .infinity).padding(8).background(.yellow.opacity(0.25)).accessibilityIdentifier("fixtureBanner")
                }
                Group {
                    if store.signedIn { DashboardTabs() } else { ConnectionView() }
                }
            }
            .environment(store)
            .tint(Color(red: 0.12, green: 0.43, blue: 0.42))
            .preferredColorScheme(store.bootstrap?.preferences.appearance == "dark" ? .dark : store.bootstrap?.preferences.appearance == "light" ? .light : nil)
            .overlay {
                if scenePhase != .active {
                    Color(uiColor: .systemBackground).ignoresSafeArea()
                        .overlay(Label("Jobman Dashboard", systemImage: "square.grid.2x2.fill").font(.title2))
                }
            }
            .onOpenURL { if let route = DashboardRoute(url: $0) { store.open(route) } }
            .onReceive(NotificationCenter.default.publisher(for: .dashboardInboxOpened)) { message in
                if let id = notifications.consumePendingInbox() ?? (message.userInfo?["inboxId"] as? String) { store.open(.inbox(id)) }
            }
            .task {
                if let id = notifications.consumePendingInbox() { store.open(.inbox(id)) }
                #if DEBUG
                if store.previewMode, NativeInboxFixtures.enabled, ProcessInfo.processInfo.arguments.contains("--dashboard-inbox-open-id"),
                   let route = InboxPushRoute.route(schemaVersion: 1, inboxID: "92000000-0000-4000-8000-000000000001") { store.open(route) }
                #endif
            }
            .onReceive(NotificationCenter.default.publisher(for: .dashboardPushRegistered)) { _ in
                Task { await store.refreshDeviceRegistration() }
            }
            .onReceive(NotificationCenter.default.publisher(for: .dashboardPushRegistrationFailed)) { _ in
                store.devices.registrationFailed("This iPhone could not register for Apple notifications. Check your connection and try again. If it continues, contact your administrator.")
            }
            .onChange(of: scenePhase) { _, phase in store.setActive(phase == .active) }
        }
    }
}

private struct ConnectionView: View {
    @Environment(DashboardStore.self) private var store
    var body: some View {
        @Bindable var store = store
        NavigationStack {
            Form {
                Section {
                    DashboardBrand().font(.title.bold())
                    Text("View job progress, read logs and investigate results on your organization's private network.").foregroundStyle(.secondary)
                }
                Section("Connect to your organization") {
                    TextField("https://dashboard.example.internal", text: $store.address)
                        .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL)
                        .accessibilityLabel("Dashboard HTTPS address")
                    Text("Connect to your private network or VPN first. Use the address supplied by your administrator.").font(.footnote)
                    Button { Task { await store.signIn() } } label: {
                        if store.busy { ProgressView("Opening organization sign-in…") }
                        else { Label("Sign in with your organization", systemImage: "person.crop.circle.badge.checkmark") }
                    }.disabled(store.busy || store.address.isEmpty)
                }
                if let error = store.error { Section { ErrorMessage(error: error) } }
                if store.pendingRoute != nil { Section { Text("An update is waiting. Sign in and connect to your private network to view it.") } }
            }.navigationTitle("Connect")
        }
    }
}

private struct DashboardTabs: View {
    @Environment(DashboardStore.self) private var store
    var body: some View {
        TabView(selection: Binding(get: { store.selectedTab }, set: { store.setTab($0) })) {
            NavigationStack { OverviewView() }.tabItem { Label("Overview", systemImage: "square.grid.2x2") }.tag(0)
            NavigationStack(path: Binding(get: { store.path }, set: { store.path = $0 })) {
                JobsView().navigationDestination(for: DashboardRoute.self) { route in
                    switch route {
                    case .job(let ref): JobDetailView(ref: ref)
                    case .inbox(let id): InboxDestinationView(id: id)
                    }
                }
            }.tabItem { Label("Jobs", systemImage: "list.bullet.rectangle") }.tag(1)
            NavigationStack { WorkloadsView() }.tabItem { Label("Job groups", systemImage: "point.3.connected.trianglepath.dotted") }.tag(2)
            NavigationStack(path: Binding(get: { store.inboxPath }, set: { store.inboxPath = $0 })) {
                InboxView().navigationDestination(for: DashboardRoute.self) { route in
                    switch route {
                    case .job(let ref): JobDetailView(ref: ref)
                    case .inbox(let id): InboxDestinationView(id: id)
                    }
                }
            }.tabItem { Label("Inbox", systemImage: "tray") }.tag(3)
            NavigationStack { MoreView() }.tabItem { Label("More", systemImage: "ellipsis.circle") }.tag(4)
        }
        .id(store.contentGeneration)
    }
}

struct ScopeMenu: View {
    @Environment(DashboardStore.self) private var store
    @State private var choosing = false
    var body: some View {
        Menu {
            Button("All namespaces you can access") { store.selectScope(.all) }
            ForEach(store.bootstrap?.deployments ?? []) { deployment in
                Section(deployment.name) {
                    Button("All in \(deployment.name)") { store.selectScope(.deployments([deployment.id])) }
                    ForEach(deployment.namespaces) { namespace in
                        Button(namespace.name) { store.selectScope(.namespace(.init(deploymentId: deployment.id, namespaceId: namespace.id))) }
                    }
                }
            }
            Button("Choose deployments…") { choosing = true }
        } label: { Label(store.scopeLabel, systemImage: "line.3.horizontal.decrease.circle").lineLimit(2) }
        .accessibilityHint("Choose a namespace, or combine jobs from several deployments. A namespace groups jobs with their own access permissions.")
        .sheet(isPresented: $choosing) { DeploymentPicker() }
    }
}

private struct DeploymentPicker: View {
    @Environment(DashboardStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var selected: Set<String> = []
    var body: some View {
        NavigationStack {
            List(store.bootstrap?.deployments ?? []) { deployment in
                Toggle(deployment.name, isOn: Binding(get: { selected.contains(deployment.id) }, set: { value in
                    if value { selected.insert(deployment.id) } else { selected.remove(deployment.id) }
                })).toggleStyle(.switch)
            }.navigationTitle("Deployments")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                    ToolbarItem(placement: .confirmationAction) { Button("Apply") { store.selectScope(.deployments(selected)); dismiss() }.disabled(selected.isEmpty) }
                }
        }
    }
}

struct ErrorMessage: View {
    let error: String
    var body: some View { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.secondary).accessibilityAddTraits(.isStaticText) }
}

struct SourceSummary: View {
    let completeness: String
    let sources: [SourceStatus]
    let fetchedAt: String
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if completeness != "complete" { Label("Partial results — some deployments or namespaces could not be included", systemImage: "exclamationmark.triangle").font(.callout.bold()) }
            ForEach(sources.filter { $0.status != "available" }) { source in
                Text("\(source.deploymentId) / \(source.namespaceId): \(source.status.replacingOccurrences(of: "_", with: " "))").font(.caption)
            }
            TimestampRow(label: "Last refreshed", value: fetchedAt)
        }.foregroundStyle(.secondary)
    }
}

struct TimestampRow: View {
    @Environment(DashboardStore.self) private var store
    let label: String
    let value: String?
    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.caption).foregroundStyle(.secondary)
            if let value, let date = WireDate.parse(value) {
                Text(format(date))
                    .accessibilityLabel("\(label), \(value)")
                Text("UTC: \(value)").font(.caption2).foregroundStyle(.secondary).textSelection(.enabled)
            } else { Text("Not available").foregroundStyle(.secondary) }
        }
    }
    private func format(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.timeZone = TimeZone(identifier: store.bootstrap?.preferences.timezone ?? "UTC") ?? .gmt
        formatter.dateStyle = .medium
        formatter.timeStyle = .long
        return formatter.string(from: date)
    }
}

struct StatusBadge: View {
    let title: String
    let value: String?
    var body: some View {
        Text("\(title): \(value?.replacingOccurrences(of: "_", with: " ") ?? "Not available")")
            .font(.caption.weight(.medium)).padding(.horizontal, 8).padding(.vertical, 5)
            .background(.quaternary, in: RoundedRectangle(cornerRadius: 6))
    }
}

// Uses the supplied transparent artwork without applying the app's tint.
struct DashboardBrand: View {
    @Environment(\.colorScheme) private var colorScheme
    var width: CGFloat = 220

    var body: some View {
        if colorScheme == .dark {
            Image("DashboardLogoDark")
                .renderingMode(.original)
                .resizable()
                .scaledToFit()
                .frame(width: width, height: width * 131.6 / 420.1)
                .accessibilityLabel("Jobman Dashboard")
        } else {
            Label("Jobman Dashboard", systemImage: "square.grid.2x2.fill")
        }
    }
}
