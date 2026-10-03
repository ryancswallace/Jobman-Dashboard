import DashboardCore
import Foundation
import Observation

@MainActor @Observable
final class DashboardStore {
    var address: String = UserDefaults.standard.string(forKey: "dashboardAddress") ?? ""
    private(set) var bootstrap: Bootstrap?
    private(set) var overview: Overview?
    private(set) var jobs: Page<Job>?
    private(set) var jobRows: [Job] = []
    private(set) var nextJobsCursor: String?
    private(set) var loadingMore = false
    private(set) var error: String?
    private(set) var busy = false
    var scope: DashboardScope = .all
    var phase = ""
    var outcome = ""
    var ownerOnly = false
    var attentionOnly = false
    var completedFrom: Date?
    var completedTo: Date?
    var path: [DashboardRoute] = []
    var inboxPath: [DashboardRoute] = []
    var pendingRoute: DashboardRoute?
    private(set) var selectedTab = 0
    private(set) var active = true
    private let authentication = NativeAuthentication()
    private let boundary = SessionBoundary()
    private var client: DashboardTransport?
    private var polling: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    private var freshnessTask: Task<Void, Never>?
    private(set) var contentGeneration = UUID()
    private var sessionGeneration = UUID()
    private var refreshGeneration = UUID()
    private var deviceBinding: DeviceBinding?
    private var listGeneration = UUID()
    private(set) var evictedJobRows = 0
    private var consecutiveRefreshFailures = 0
    private(set) var previewMode = false

    init() {
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--dashboard-ui-fixtures") {
            previewMode = true
            address = "https://dashboard-fixtures.example.test"
            client = NativeFixtures.client()
            if let boot = try? JSONDecoder().decode(Bootstrap.self, from: NativeFixtures.data(path: "/api/v1/bootstrap")) {
                try? activate(boot)
                refresh()
            }
        }
        #endif
    }

    var signedIn: Bool { bootstrap != nil }
    var scopeLabel: String {
        switch scope {
        case .all: "All authorized namespaces"
        case .deployments(let ids): bootstrap?.deployments.filter { ids.contains($0.id) }.map(\.name).joined(separator: ", ") ?? "Selected deployments"
        case .namespace(let ref): bootstrap?.deployments.first { $0.id == ref.deploymentId }?.namespaces.first { $0.id == ref.namespaceId }?.name ?? "Namespace"
        }
    }

    func signIn() async {
        guard !previewMode else { error = "Restart without the fixture launch argument to use real organization sign-in."; return }
        guard !busy else { return }
        busy = true; error = nil
        let generation = sessionGeneration
        defer { busy = false }
        do {
            let connection = try DashboardConnection(address: address.trimmingCharacters(in: .whitespacesAndNewlines))
            client = DashboardTransport(connection: connection)
            let token = try await authentication.signIn(connection: connection)
            let result = try await client!.bootstrap(token: token)
            guard generation == sessionGeneration, !Task.isCancelled else { return }
            try activate(result)
            UserDefaults.standard.set(connection.baseURL.absoluteString, forKey: "dashboardAddress")
            refresh(); setActive(true)
            if let pendingRoute { open(pendingRoute) }
        } catch {
            guard generation == sessionGeneration else { return }
            authentication.signOut(); self.error = error.localizedDescription
        }
    }

    private func activate(_ value: Bootstrap) throws {
        guard value.apiVersion == "jobman.dashboard/v1" else { throw DashboardError.unsupportedContract }
        bootstrap = value
        let now = Date()
        let checked = value.authorizationCheckedAt ?? now
        let maxAge = max(0, (value.authorizationDeadline ?? now.addingTimeInterval(120)).timeIntervalSince(checked))
        boundary.activate(accountId: value.account.id, scope: scope, namespaces: Set(value.namespaces), checkedAt: checked, maximumAge: maxAge)
        scheduleExpiry()
    }

    func selectScope(_ value: DashboardScope) {
        scope = value
        cancelRefresh()
        purgeContent()
        path = []
        if let bootstrap { try? activate(bootstrap) }
        refresh()
    }

    func setTab(_ value: Int) { selectedTab = value }

    func refresh() {
        guard signedIn, refreshTask == nil else { return }
        let refreshID = UUID()
        refreshGeneration = refreshID
        refreshTask = Task { @MainActor in
            defer { if refreshGeneration == refreshID { refreshTask = nil } }
            do {
                let boot = try await fetchBootstrap()
                guard boot.account.id == bootstrap?.account.id else { signOut(); return }
                let revoked = boundary.refreshAuthorization(namespaces: Set(boot.namespaces), checkedAt: boot.authorizationCheckedAt ?? Date(),
                                                            maximumAge: (boot.authorizationDeadline ?? Date()).timeIntervalSince(boot.authorizationCheckedAt ?? Date()))
                let grantsChanged = authorizationVersions(boot) != bootstrap.map(authorizationVersions)
                bootstrap = boot
                if revoked || grantsChanged { try activate(boot); purgeContent(); path = []; inboxPath = [] }
                scheduleExpiry()
                guard let ticket = boundary.ticket() else { return }
                let query = try scope.queryItems(authorized: boot.namespaces)
                async let summary: Overview = request(path: "/api/v1/overview", query: query)
                let jobQuery = query + [.init(name: "limit", value: "50")] + jobFilters()
                async let list: Page<Job> = request(path: "/api/v1/jobs", query: jobQuery)
                let result = try await (summary, list)
                guard !Task.isCancelled, boundary.canPublish(ticket) else { return }
                overview = result.0
                // Refresh preserves row identity in SwiftUI; pagination reload is explicit.
                if nextJobsCursor == jobs?.nextCursor {
                    jobRows = result.1.items
                    nextJobsCursor = result.1.nextCursor
                } else {
                    let updates = Dictionary(uniqueKeysWithValues: result.1.items.map { ($0.ref, $0) })
                    jobRows = jobRows.map { updates[$0.ref] ?? $0 }
                }
                jobs = result.1
                error = nil
                consecutiveRefreshFailures = 0
            } catch is CancellationError { }
            catch {
                handle(error)
            }
        }
    }

    func request<T: Decodable & Sendable>(path: String, query: [URLQueryItem] = [], method: String = "GET", body: Data? = nil,
                                         revision: String? = nil, idempotencyKey: UUID? = nil, bypassFreshness: Bool = false) async throws -> T {
        guard let client else { throw DashboardError.authenticationRequired }
        let generation = sessionGeneration
        let ticket = boundary.ticket()
        let token = previewMode ? "synthetic-preview" : try await authentication.token()
        let result: T = try await client.request(path: path, query: query, token: token, method: method, body: body,
                                                 revision: revision, idempotencyKey: idempotencyKey ?? (method == "POST" ? UUID() : nil))
        try Task.checkCancellation()
        guard generation == sessionGeneration else { throw CancellationError() }
        if !bypassFreshness {
            guard let ticket, boundary.canPublish(ticket) else { throw DashboardError.forbidden }
        }
        return result
    }

    func query() throws -> [URLQueryItem] { try scope.queryItems(authorized: bootstrap?.namespaces ?? []) }

    private func fetchBootstrap() async throws -> Bootstrap {
        guard let client else { throw DashboardError.authenticationRequired }
        let generation = sessionGeneration
        let token = previewMode ? "synthetic-preview" : try await authentication.token()
        let result = try await client.bootstrap(token: token)
        try Task.checkCancellation()
        guard generation == sessionGeneration else { throw CancellationError() }
        return result
    }

    func resetJobs() {
        listGeneration = UUID()
        cancelRefresh()
        jobs = nil; jobRows = []; nextJobsCursor = nil; evictedJobRows = 0
        refresh()
    }

    func loadMoreJobs() async {
        guard let cursor = nextJobsCursor, !loadingMore else { return }
        let generation = listGeneration
        loadingMore = true
        defer { loadingMore = false }
        do {
            var parameters = try query() + [.init(name: "limit", value: "50"), .init(name: "cursor", value: cursor)]
            parameters += jobFilters()
            let page: Page<Job> = try await request(path: "/api/v1/jobs", query: parameters)
            guard generation == listGeneration else { return }
            let existing = Set(jobRows.map(\.ref))
            jobRows += page.items.filter { !existing.contains($0.ref) }
            if jobRows.count > 1_000 { let removed = jobRows.count - 1_000; jobRows.removeFirst(removed); evictedJobRows += removed }
            nextJobsCursor = page.nextCursor
        } catch { handle(error) }
    }

    private func jobFilters() -> [URLQueryItem] {
        var values: [URLQueryItem] = []
        if !phase.isEmpty { values.append(.init(name: "phase", value: phase)) }
        if !outcome.isEmpty { values.append(.init(name: "outcome", value: outcome)) }
        if ownerOnly { values.append(.init(name: "owner", value: "me")) }
        if attentionOnly { values.append(.init(name: "attention", value: "true")) }
        if let completedFrom { values.append(.init(name: "from", value: ISO8601DateFormatter().string(from: completedFrom))) }
        if let completedTo { values.append(.init(name: "to", value: ISO8601DateFormatter().string(from: completedTo))) }
        return values
    }

    func drilldown(phase: String, outcome: String = "", attention: Bool = false, window: Overview.Window? = nil) {
        self.phase = phase; self.outcome = outcome; attentionOnly = attention; ownerOnly = false
        completedFrom = window.flatMap { WireDate.parse($0.from) }
        completedTo = window.flatMap { WireDate.parse($0.to) }
        resetJobs(); setTab(1)
    }

    private func cancelRefresh() { refreshGeneration = UUID(); refreshTask?.cancel(); refreshTask = nil }

    func setActive(_ value: Bool) {
        active = value
        if value { Task { await DeviceRevocations.flush() } }
        polling?.cancel(); polling = nil
        guard value, signedIn else { refreshTask?.cancel(); return }
        refresh()
        polling = Task { @MainActor in
            while !Task.isCancelled {
                let seconds = bootstrap?.preferences.refreshSeconds ?? 5
                // Authorization still refreshes every five seconds when data polling is manual.
                let base = max(5, seconds)
                let delay = consecutiveRefreshFailures == 0 ? Double(base) : min(60, Double(base) * pow(2, Double(min(consecutiveRefreshFailures, 4)))) * Double.random(in: 0.85...1.0)
                try? await Task.sleep(for: .seconds(delay))
                guard !Task.isCancelled else { break }
                if seconds > 0 { refresh() }
                else { await refreshAccessOnly() }
            }
        }
    }

    private func refreshAccessOnly() async {
        do {
            let boot = try await fetchBootstrap()
            guard boot.account.id == bootstrap?.account.id else { signOut(); return }
            let removed = boundary.refreshAuthorization(namespaces: Set(boot.namespaces), checkedAt: boot.authorizationCheckedAt ?? Date(),
                                                         maximumAge: (boot.authorizationDeadline ?? Date()).timeIntervalSince(boot.authorizationCheckedAt ?? Date()))
            bootstrap = boot
            if removed { purgeContent(); path = [] }
            scheduleExpiry()
        } catch { handle(error) }
    }

    private func scheduleExpiry() {
        freshnessTask?.cancel()
        guard let expiry = boundary.authorizationValidUntil else { return }
        freshnessTask = Task { @MainActor in
            try? await Task.sleep(for: .seconds(max(0, expiry.timeIntervalSinceNow)))
            guard !Task.isCancelled else { return }
            purgeContent(); path = []
            error = DashboardError.authorizationUnavailable.localizedDescription
        }
    }

    func open(_ route: DashboardRoute) {
        guard signedIn else { pendingRoute = route; return }
        if case .job(let ref) = route, !Set(bootstrap?.namespaces ?? []).contains(ref.namespace) {
            error = DashboardError.notFound.localizedDescription; return
        }
        if case .job(let ref) = route, !scope.contains(ref.namespace) { selectScope(.namespace(ref.namespace)) }
        pendingRoute = nil
        selectedTab = route.isInbox ? 3 : 1
        if route.isInbox { inboxPath = [route] } else { path = [route] }
    }

    func registerPush(token: String) async {
        guard signedIn else { return }
        do {
            struct Registration: Encodable {
                let installationId: String; let token: String; let topic: String; let environment: String
                let name: String; let enabled: Bool; let permission: String
            }
            let key = "installationId"
            let installation = UserDefaults.standard.string(forKey: key) ?? UUID().uuidString
            UserDefaults.standard.set(installation, forKey: key)
            let body = Registration(installationId: installation, token: token, topic: Bundle.main.bundleIdentifier ?? "", environment: Bundle.main.object(forInfoDictionaryKey: "APNSEnvironment") as? String ?? "development", name: "iPhone", enabled: true, permission: "authorized")
            struct Registered: Decodable, Sendable { let id: String; let revocationCredential: String }
            let result: Registered = try await request(path: "/api/v1/devices", method: "POST", body: JSONEncoder().encode(body))
            let binding = DeviceBinding(address: address, deviceId: result.id, credential: result.revocationCredential)
            try DeviceRevocations.saveActive(binding)
            deviceBinding = binding
        } catch { self.error = error.localizedDescription }
    }

    func signOut() {
        let queuedUnbind = (try? DeviceRevocations.enqueueActive()) ?? false
        sessionGeneration = UUID()
        polling?.cancel(); refreshTask?.cancel(); freshnessTask?.cancel()
        polling = nil; refreshTask = nil; freshnessTask = nil
        authentication.signOut(); boundary.clear(); client = nil
        bootstrap = nil; path = []; inboxPath = []; pendingRoute = nil; error = nil
        purgeContent()
        if queuedUnbind {
            error = "Signed out on this phone. Device alert unbinding will finish when the private service is reachable."
            Task { await DeviceRevocations.flush() }
        }
        deviceBinding = nil
    }

    private func purgeContent() { overview = nil; jobs = nil; jobRows = []; nextJobsCursor = nil; listGeneration = UUID(); evictedJobRows = 0; contentGeneration = UUID() }
    private func authorizationVersions(_ bootstrap: Bootstrap) -> [NamespaceRef: String] {
        Dictionary(uniqueKeysWithValues: bootstrap.deployments.flatMap { deployment in
            deployment.namespaces.map { (NamespaceRef(deploymentId: deployment.id, namespaceId: $0.id), $0.authorizationVersion) }
        })
    }
    private func handle(_ error: Error) {
        consecutiveRefreshFailures = min(consecutiveRefreshFailures + 1, 4)
        if error as? DashboardError == .authenticationRequired { signOut() }
        if let value = error as? DashboardError, [.forbidden, .authorizationUnavailable].contains(value) { purgeContent(); path = [] }
        self.error = error.localizedDescription
    }
}

private extension DashboardRoute {
    var isInbox: Bool { if case .inbox = self { true } else { false } }
}
