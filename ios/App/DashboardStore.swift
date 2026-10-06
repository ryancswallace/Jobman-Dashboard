import DashboardCore
import Foundation
import Observation

@MainActor @Observable
final class DashboardStore {
    var address: String = UserDefaults.standard.string(forKey: "dashboardAddress") ?? ""
    private(set) var bootstrap: Bootstrap?
    private(set) var overview: Overview?
    private(set) var jobPage = MonitoredPage<Job, JobRef>()
    var jobs: Page<Job>? { jobPage.page }
    var jobRows: [Job] { jobPage.items }
    var nextJobsCursor: String? { jobPage.nextCursor }
    var canLoadPreviousJobs: Bool { jobPage.canGoBack }
    var jobsRequireRestart: Bool { jobPage.requiresRestart }
    var jobsAreFirstPage: Bool { jobPage.isFirstPage }
    var evictedJobRows: Int { jobPage.history.discardedPages }
    var overviewWindow: OverviewWindow = .day
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
    private var refreshAfterPage = false
    private var freshnessTask: Task<Void, Never>?
    private(set) var contentGeneration = UUID()
    private(set) var foregroundGeneration = UUID()
    private var sceneGeneration = UUID()
    private var resumeReadPending = false
    private var sessionGeneration = UUID()
    private var refreshGeneration = UUID()
    let devices = NativeDeviceController()
    private var listGeneration = UUID()
    private var consecutiveRefreshFailures = 0
    private var lastDataRefresh = Date.distantPast
    private(set) var previewMode = false
    private var ruleNamespaceOptionsInvalidated = false

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
        case .all: "All namespaces you can access"
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
            await refreshDeviceRegistration()
            if let pendingRoute { open(pendingRoute) }
        } catch {
            guard generation == sessionGeneration else { return }
            authentication.signOut(); self.error = error.localizedDescription
        }
    }

    private func activate(_ value: Bootstrap) throws {
        guard value.apiVersion == "jobman.dashboard/v1" else { throw DashboardError.unsupportedContract }
        try validateAuthorization(value)
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
        guard active, signedIn, refreshTask == nil else { return }
        if loadingMore { refreshAfterPage = true; return }
        refreshAfterPage = false
        lastDataRefresh = Date()
        let refreshID = UUID(), session = sessionGeneration
        refreshGeneration = refreshID
        refreshTask = Task { @MainActor in
            defer { if refreshGeneration == refreshID { refreshTask = nil } }
            do {
                let boot = try await fetchBootstrap()
                guard boot.account.id == bootstrap?.account.id else { signOut(); return }
                try validateAuthorization(boot)
                let revoked = boundary.refreshAuthorization(namespaces: Set(boot.namespaces), checkedAt: boot.authorizationCheckedAt ?? Date(),
                                                            maximumAge: (boot.authorizationDeadline ?? Date()).timeIntervalSince(boot.authorizationCheckedAt ?? Date()))
                let grantsChanged = authorizationVersions(boot) != bootstrap.map(authorizationVersions)
                bootstrap = boot
                ruleNamespaceOptionsInvalidated = false
                if revoked || grantsChanged { try activate(boot); purgeContent(); path = []; inboxPath = [] }
                scheduleExpiry()
                if resumeReadPending { resumeReadPending = false; foregroundGeneration = UUID() }
                if boot.namespaces.isEmpty { error = "Your account does not currently have access to any namespaces."; return }
                guard let ticket = boundary.ticket() else { return }
                let query = try scope.queryItems(authorized: boot.namespaces)
                async let summary: Overview = request(path: "/api/v1/overview", query: query + [overviewWindow.query])
                var jobQuery = query + [.init(name: "limit", value: "50")] + jobFilters()
                if let cursor = try jobPage.requestCursor(for: .refresh) { jobQuery.append(.init(name: "cursor", value: cursor)) }
                async let list: Page<Job> = request(path: "/api/v1/jobs", query: jobQuery)
                let result = try await (summary, list)
                guard !Task.isCancelled, boundary.canPublish(ticket) else { return }
                overview = result.0
                try jobPage.accept(result.1, for: .refresh, identity: \.ref)
                error = nil
                consecutiveRefreshFailures = 0
                lastDataRefresh = Date()
            } catch is CancellationError { }
            catch {
                guard session == sessionGeneration, refreshGeneration == refreshID else { return }
                jobPage.failed(error); handle(error)
            }
        }
    }

    func request<T: Decodable & Sendable>(path: String, query: [URLQueryItem] = [], method: String = "GET", body: Data? = nil,
                                         revision: String? = nil, idempotencyKey: UUID? = nil, bypassFreshness: Bool = false) async throws -> T {
        guard active else { throw CancellationError() }
        guard let client else { throw DashboardError.authenticationRequired }
        let generation = sessionGeneration, scene = sceneGeneration
        let ticket = boundary.ticket()
        let token = previewMode ? "synthetic-preview" : try await authentication.token()
        guard active, generation == sessionGeneration, scene == sceneGeneration, !Task.isCancelled else { throw CancellationError() }
        let result: T
        do {
            result = try await client.request(path: path, query: query, token: token, method: method, body: body,
                                              revision: revision, idempotencyKey: idempotencyKey ?? (method == "POST" ? UUID() : nil))
        } catch {
            if active, generation == sessionGeneration, scene == sceneGeneration, let value = error as? DashboardError,
               [.authenticationRequired, .forbidden, .authorizationUnavailable].contains(value) { handle(value) }
            throw error
        }
        try Task.checkCancellation()
        guard active, generation == sessionGeneration, scene == sceneGeneration else { throw CancellationError() }
        if !bypassFreshness {
            guard let ticket, boundary.canPublish(ticket) else { throw DashboardError.forbidden }
        }
        return result
    }

    func query() throws -> [URLQueryItem] { try scope.queryItems(authorized: bootstrap?.namespaces ?? []) }

    /// Account-owned controls return their own current authorization projection.
    /// Stop/delete remain usable when namespace freshness is unavailable; all
    /// namespace data paths retain the separate SessionBoundary freshness gate.
    func generatedAccountRequest<T: Sendable>(clearNamespaceContentOnFailure: Bool = true, _ operation: @Sendable (DashboardClient) async throws -> T) async throws -> T {
        guard active, signedIn, let client else { throw DashboardError.authenticationRequired }
        let generation = sessionGeneration, content = contentGeneration, account = bootstrap?.account.id
        do {
            let token = previewMode ? "synthetic-preview" : try await authentication.token()
            try Task.checkCancellation()
            guard active, generation == sessionGeneration, content == contentGeneration, account == bootstrap?.account.id else { throw CancellationError() }
            let result = try await operation(DashboardClient(transport: AuthenticatedTransport(transport: client, token: token)))
            try Task.checkCancellation()
            guard active, generation == sessionGeneration, content == contentGeneration, account == bootstrap?.account.id else { throw CancellationError() }
            return result
        } catch {
            if active, generation == sessionGeneration, content == contentGeneration, account == bootstrap?.account.id, let value = error as? DashboardError,
               value == .authenticationRequired || (clearNamespaceContentOnFailure && [.forbidden, .authorizationUnavailable].contains(value)) { handle(value) }
            throw error
        }
    }

    func generatedRuleRequest<T: Sendable>(_ operation: @Sendable (DashboardClient) async throws -> T) async throws -> T { try await generatedAccountRequest(operation) }

    var deviceContext: NativeDeviceContext? {
        guard active, let account = bootstrap?.account.id, let origin = client?.origin else { return nil }
        return NativeDeviceContext(accountID: account, session: sessionGeneration, origin: origin)
    }

    var hasFreshNamespaceOptions: Bool { !ruleNamespaceOptionsInvalidated && (boundary.authorizationValidUntil.map { $0 > Date() } ?? false) }

    private func fetchBootstrap() async throws -> Bootstrap {
        guard let client else { throw DashboardError.authenticationRequired }
        let generation = sessionGeneration, scene = sceneGeneration
        guard active else { throw CancellationError() }
        let token = previewMode ? "synthetic-preview" : try await authentication.token()
        try Task.checkCancellation()
        guard active, scene == sceneGeneration, generation == sessionGeneration else { throw CancellationError() }
        let result = try await client.bootstrap(token: token)
        try Task.checkCancellation()
        guard active, scene == sceneGeneration, generation == sessionGeneration else { throw CancellationError() }
        return result
    }

    func resetJobs() {
        listGeneration = UUID(); loadingMore = false
        cancelRefresh()
        jobPage.reset()
        refresh()
    }

    func loadMoreJobs() async {
        guard let cursor = nextJobsCursor else { return }
        await loadJobsPage(.next(cursor))
    }
    func loadPreviousJobs() async { await loadJobsPage(.previous) }
    private func loadJobsPage(_ load: MonitoredPage<Job, JobRef>.Load) async {
        guard !loadingMore else { return }
        cancelRefresh()
        let generation = listGeneration
        loadingMore = true
        defer {
            if generation == listGeneration {
                loadingMore = false
                if refreshAfterPage { refresh() }
            }
        }
        do {
            var parameters = try query() + [.init(name: "limit", value: "50")] + jobFilters()
            if let cursor = try jobPage.requestCursor(for: load) { parameters.append(.init(name: "cursor", value: cursor)) }
            let page: Page<Job> = try await request(path: "/api/v1/jobs", query: parameters)
            guard generation == listGeneration else { return }
            try jobPage.accept(page, for: load, identity: \.ref)
            error = nil
        } catch is CancellationError {} catch { if generation == listGeneration { jobPage.failed(error); handle(error) } }
    }
    func selectOverviewWindow(_ value: OverviewWindow) {
        overviewWindow = value; overview = nil; cancelRefresh(); refresh()
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
        if active != value {
            sceneGeneration = UUID()
            if value { resumeReadPending = true }
        }
        active = value
        if value {
            Task { await DeviceRevocations.flush() }
            Task {
                await PushRegistration.refreshIfAllowed(preview: previewMode)
                if active, signedIn { await refreshDeviceRegistration() }
            }
        }
        else { devices.clear(); listGeneration = UUID(); loadingMore = false; refreshAfterPage = false }
        polling?.cancel(); polling = nil
        guard value, signedIn else { cancelRefresh(); return }
        refresh()
        polling = Task { @MainActor in
            while !Task.isCancelled {
                let seconds = bootstrap?.preferences.refreshSeconds ?? 5
                // Authorization still refreshes every five seconds when data polling is manual.
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { break }
                let base = max(5, seconds)
                let dataDelay = consecutiveRefreshFailures == 0 ? Double(base) : min(60, Double(base) * pow(2, Double(min(consecutiveRefreshFailures, 4)))) * Double.random(in: 0.85...1.0)
                if seconds > 0, Date().timeIntervalSince(lastDataRefresh) >= dataDelay { refresh() }
                else { await refreshAccessOnly() }
            }
        }
    }

    private func refreshAccessOnly() async {
        let session = sessionGeneration
        do {
            let boot = try await fetchBootstrap()
            guard boot.account.id == bootstrap?.account.id else { signOut(); return }
            try validateAuthorization(boot)
            let removed = boundary.refreshAuthorization(namespaces: Set(boot.namespaces), checkedAt: boot.authorizationCheckedAt ?? Date(),
                                                         maximumAge: (boot.authorizationDeadline ?? Date()).timeIntervalSince(boot.authorizationCheckedAt ?? Date()))
            let grantsChanged = authorizationVersions(boot) != bootstrap.map(authorizationVersions)
            bootstrap = boot
            ruleNamespaceOptionsInvalidated = false
            if removed || grantsChanged { try activate(boot); purgeContent(); path = []; inboxPath = [] }
            scheduleExpiry()
            if resumeReadPending { refresh() }
        } catch is CancellationError { }
        catch { if session == sessionGeneration, active { handle(error) } }
    }

    private func scheduleExpiry() {
        freshnessTask?.cancel()
        guard !boundary.authorizedNamespaces.isEmpty, let expiry = boundary.authorizationValidUntil else { return }
        freshnessTask = Task { @MainActor in
            try? await Task.sleep(for: .seconds(max(0, expiry.timeIntervalSinceNow)))
            guard !Task.isCancelled else { return }
            purgeContent(); path = []
            error = DashboardError.authorizationUnavailable.localizedDescription
        }
    }

    var connectedOrigin: String? { client?.origin.absoluteString }
    @discardableResult func openCanonicalLink(_ text: String) -> Bool {
        guard let origin = client?.origin,
              let connection = try? DashboardConnection(address: origin.absoluteString),
              let url = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              let route = DashboardRoute(canonicalURL: url, connection: connection) else { return false }
        open(route); return true
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

    func refreshDeviceRegistration() async { await devices.load(store: self, refreshToken: true) }

    func signOut() {
        var queuedUnbind = false
        var unbindQueueFailed = false
        var unconfirmedBinding = devices.ownership?.refreshRevision != nil && !devices.offlineProtected
        do {
            queuedUnbind = try DeviceRevocations.enqueueActive()
            let unresolved = try DeviceRevocations.read().hasUnconfirmedExisting
            unconfirmedBinding = unconfirmedBinding || unresolved
        } catch { unbindQueueFailed = true }
        sessionGeneration = UUID()
        polling?.cancel(); refreshTask?.cancel(); freshnessTask?.cancel()
        polling = nil; refreshTask = nil; freshnessTask = nil
        authentication.signOut(); boundary.clear(); client = nil
        bootstrap = nil; path = []; inboxPath = []; pendingRoute = nil; error = nil
        purgeContent()
        if queuedUnbind {
            error = "Signed out on this phone. The saved request to stop its notifications will finish when Dashboard is reachable."
            Task { await DeviceRevocations.flush() }
        }
        if unconfirmedBinding { error = "Signed out on this phone. We could not confirm that notifications have stopped for its older connection. Reconnect, sign in and remove this device. Your sign-in credentials were cleared." }
        if unbindQueueFailed { error = "Signed out on this phone. The request to stop notifications could not be saved. Reconnect, sign in and remove this device to finish." }
        devices.clear()
    }

    private func purgeContent() { loadingMore = false; refreshAfterPage = false; devices.clear(); overview = nil; jobPage.reset(); listGeneration = UUID(); contentGeneration = UUID() }
    private func authorizationVersions(_ bootstrap: Bootstrap) -> [NamespaceRef: String] {
        Dictionary(uniqueKeysWithValues: bootstrap.deployments.flatMap { deployment in
            deployment.namespaces.map { (NamespaceRef(deploymentId: deployment.id, namespaceId: $0.id), $0.authorizationVersion) }
        })
    }
    private func validateAuthorization(_ bootstrap: Bootstrap) throws {
        let namespaces = bootstrap.deployments.flatMap(\.namespaces)
        guard Set(bootstrap.namespaces).count == bootstrap.namespaces.count,
              namespaces.allSatisfy({ namespace in
                  guard let checked = WireDate.parse(namespace.authorizationCheckedAt), let expiry = WireDate.parse(namespace.authorizationExpiresAt) else { return false }
                  return checked <= Date().addingTimeInterval(30) && expiry > checked
              }) else { throw DashboardError.authorizationUnavailable }
    }
    private func handle(_ error: Error) {
        consecutiveRefreshFailures = min(consecutiveRefreshFailures + 1, 4)
        if error as? DashboardError == .authenticationRequired { signOut() }
        if let value = error as? DashboardError, [.forbidden, .authorizationUnavailable].contains(value) { ruleNamespaceOptionsInvalidated = true; purgeContent(); path = [] }
        self.error = error.localizedDescription
    }
}

private extension DashboardRoute {
    var isInbox: Bool { if case .inbox = self { true } else { false } }
}
