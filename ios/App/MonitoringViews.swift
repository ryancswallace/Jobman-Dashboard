import DashboardCore
import SwiftUI

struct OverviewView: View {
    @Environment(DashboardStore.self) private var store
    var body: some View {
        List {
            Section { ScopeMenu() }
            if let error = store.error { Section { ErrorMessage(error: error) } }
            if let summary = store.overview {
                Section("Recorded job state") {
                    count("Active jobs", summary.active, phase: "active")
                    count("Awaiting reported execution", summary.awaitingExecution, phase: "awaiting")
                    count("Running", summary.running, phase: "running")
                    Button { store.drilldown(phase: "active", attention: true) } label: { LabeledContent("Evidence needs attention", value: summary.evidenceAttention.map(String.init) ?? "Unavailable") }.buttonStyle(.plain)
                    Text("Evidence attention can overlap active jobs. It does not mean the jobs have failed.").font(.footnote).foregroundStyle(.secondary)
                }
                Section("Terminal outcomes") {
                    ForEach(summary.terminal.keys.sorted(), id: \.self) { outcome in
                        count(outcome.replacingOccurrences(of: "_", with: " ").capitalized, summary.terminal[outcome] ?? nil, phase: "terminal", outcome: outcome, window: summary.window)
                    }
                    LabeledContent("Missing completion time", value: summary.missingCompletionTime.map(String.init) ?? "Unavailable")
                    TimestampRow(label: "Window from", value: summary.window.from)
                    TimestampRow(label: "Window until (exclusive)", value: summary.window.to)
                }
                Section { SourceSummary(completeness: summary.completeness, sources: summary.sources, fetchedAt: summary.fetchedAt) }
            } else if store.error == nil { ProgressView("Loading authorized activity…") }
        }.navigationTitle("Overview").refreshable { store.refresh() }
            .toolbar { Button { store.refresh() } label: { Label("Refresh", systemImage: "arrow.clockwise") } }
    }

    private func count(_ title: String, _ value: Int?, phase: String, outcome: String = "", window: Overview.Window? = nil) -> some View {
        Button {
            store.drilldown(phase: phase, outcome: outcome, window: window)
        } label: { LabeledContent(title, value: value.map(String.init) ?? "Unavailable") }.buttonStyle(.plain)
    }
}

struct JobsView: View {
    @Environment(DashboardStore.self) private var store
    @State private var exactID = ""
    var body: some View {
        @Bindable var store = store
        List {
            Section { ScopeMenu() }
            Section("Filters") {
                Picker("Phase", selection: $store.phase) {
                    Text("All phases").tag("")
                    ForEach(["active", "awaiting", "accepted", "assigning", "accepted_execution", "running", "terminal"], id: \.self) { Text($0.replacingOccurrences(of: "_", with: " ")).tag($0) }
                }.onChange(of: store.phase) { _, _ in store.resetJobs() }
                Picker("Outcome", selection: $store.outcome) {
                    Text("All outcomes").tag("")
                    ForEach(["success", "failure", "timed_out", "aborted", "lost", "cancelled"], id: \.self) { Text($0.replacingOccurrences(of: "_", with: " ")).tag($0) }
                }.onChange(of: store.outcome) { _, _ in store.resetJobs() }
                Toggle("Submitted by me", isOn: $store.ownerOnly).onChange(of: store.ownerOnly) { _, _ in store.resetJobs() }
                Toggle("Evidence needs attention", isOn: $store.attentionOnly).onChange(of: store.attentionOnly) { _, _ in store.resetJobs() }
                Toggle("Filter completion window", isOn: Binding(get: { store.completedFrom != nil }, set: { enabled in
                    store.completedFrom = enabled ? Date().addingTimeInterval(-86400) : nil
                    store.completedTo = enabled ? Date() : nil
                    store.resetJobs()
                }))
                if store.completedFrom != nil {
                    DatePicker("From (inclusive)", selection: Binding(get: { store.completedFrom ?? Date() }, set: { store.completedFrom = $0; store.resetJobs() }))
                    DatePicker("Until (exclusive)", selection: Binding(get: { store.completedTo ?? Date() }, set: { store.completedTo = $0; store.resetJobs() }))
                }
                if case .namespace(let ref) = store.scope {
                    HStack {
                        TextField("Full job ID", text: $exactID).textInputAutocapitalization(.never).autocorrectionDisabled()
                        Button("Open") { store.open(.job(.init(deploymentId: ref.deploymentId, namespaceId: ref.namespaceId, jobId: exactID))) }.disabled(exactID.isEmpty)
                    }
                } else { Text("Select a namespace to open an exact job ID.").font(.footnote).foregroundStyle(.secondary) }
            }
            if let error = store.error { ErrorMessage(error: error) }
            if let page = store.jobs {
                Section {
                    if store.evictedJobRows > 0 { Text("\(store.evictedJobRows) earlier rows were removed from memory. Pull to refresh to return to the first page.").font(.caption) }
                    if store.jobRows.isEmpty { ContentUnavailableView("No matching jobs", systemImage: "line.3.horizontal.decrease.circle", description: Text("Change the filters or refresh this scope.")) }
                    ForEach(store.jobRows, id: \.ref) { job in NavigationLink(value: DashboardRoute.job(job.ref)) { JobRow(job: job) } }
                    if store.nextJobsCursor != nil { Button { Task { await store.loadMoreJobs() } } label: { if store.loadingMore { ProgressView() } else { Text("Load next page") } }.disabled(store.loadingMore) }
                }
                Section { SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt) }
            } else if store.error == nil { ProgressView("Loading jobs…") }
        }.navigationTitle("Jobs").refreshable { store.resetJobs() }
    }
}

struct JobRow: View {
    let job: Job
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(job.title).font(.headline)
            Text("\(job.deploymentId) / \(job.namespaceId)").font(.caption).foregroundStyle(.secondary)
            ViewThatFits(in: .horizontal) {
                HStack { StatusBadge(title: "Phase", value: job.phase); StatusBadge(title: "Outcome", value: job.outcome) }
                VStack(alignment: .leading) { StatusBadge(title: "Phase", value: job.phase); StatusBadge(title: "Outcome", value: job.outcome) }
            }
            if let confidence = job.confidence { Text("Evidence: \(confidence)").font(.caption).foregroundStyle(.secondary) }
            Text("Target: \(job.targetId)").font(.caption)
        }.padding(.vertical, 4).accessibilityElement(children: .combine)
    }
}

struct JobDetailView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    @State private var detail: JobDetail?
    @State private var error: String?
    @State private var watching = false
    var body: some View {
        List {
            Section { Text("\(ref.deploymentId) / \(ref.namespaceId)").font(.caption); Text(ref.jobId).textSelection(.enabled) }
            if let error { ErrorMessage(error: error) }
            if let detail {
                let job = detail.job
                Section("Lifecycle") {
                    JobRow(job: job)
                    LabeledContent("Desired state", value: job.desiredState)
                    LabeledContent("Graph disposition", value: job.disposition ?? "Unavailable")
                    Text("A cancellation request is intent. Only the reported terminal outcome confirms cancellation.").font(.footnote).foregroundStyle(.secondary)
                    TimestampRow(label: "Created", value: job.createdAt)
                    TimestampRow(label: "Execution started", value: job.startedAt)
                    TimestampRow(label: "Start recorded by Control", value: job.lifecycle?.startedRecordedAt)
                    LabeledContent("Start evidence", value: job.lifecycle?.startedProvenance ?? "Unavailable")
                    TimestampRow(label: "Completed", value: job.completedAt)
                    TimestampRow(label: "Completion recorded by Control", value: job.lifecycle?.completedRecordedAt)
                    LabeledContent("Completion evidence", value: job.lifecycle?.completedProvenance ?? "Unavailable")
                    TimestampRow(label: "Metadata updated", value: job.updatedAt)
                    TimestampRow(label: "Last fetched", value: detail.fetchedAt)
                }
                Section("Placement and ownership") {
                    LabeledContent("Submitted by", value: job.owner?.displayName ?? job.owner?.id ?? "Unavailable")
                    LabeledContent("Target", value: job.targetId)
                    LabeledContent("Target generation ID", value: job.targetGenerationId ?? "Unavailable")
                    LabeledContent("Backend", value: job.backend ?? "Unavailable")
                    LabeledContent("Revision", value: job.revision)
                    LabeledContent("Scheduler state", value: job.scheduler?.state ?? "Unavailable")
                    LabeledContent("Scheduler job ID", value: job.scheduler?.jobId ?? "Unavailable")
                    LabeledContent("Scheduler reason", value: job.scheduler?.reason ?? "Unavailable")
                    LabeledContent("Cluster", value: job.scheduler?.cluster ?? "Unavailable")
                    TimestampRow(label: "Scheduler observation", value: job.scheduler?.observedAt)
                    LabeledContent("Imported history", value: job.imported.map { $0 ? "Yes" : "No" } ?? "Unavailable")
                    ForEach(job.labels.keys.sorted(), id: \.self) { key in LabeledContent(key, value: job.labels[key] ?? "") }
                }
                Section("Run and workload references") {
                    LabeledContent("Current run", value: job.currentRun?.number ?? "Unavailable")
                    LabeledContent("Run ID", value: job.currentRun?.id ?? "Unavailable")
                    LabeledContent("Execution ID", value: job.currentRun?.executionId ?? "Unavailable")
                    if let id = job.group?.collectionId {
                        NavigationLink("Collection \(id)") { WorkloadReferenceView(namespace: ref.namespace, kind: "collection", id: id) }
                        if let index = job.group?.collectionIndex { LabeledContent("Collection index", value: String(index)) }
                    }
                    if let id = job.group?.graphId {
                        NavigationLink("Graph \(id)") { WorkloadReferenceView(namespace: ref.namespace, kind: "graph", id: id) }
                        if let index = job.group?.graphIndex { LabeledContent("Graph index", value: String(index)) }
                    }
                }
                Section("Investigate") {
                    NavigationLink { LogView(ref: ref) } label: { Label("Logs", systemImage: "text.alignleft") }
                    NavigationLink { ArtifactsView(ref: ref) } label: { Label("Artifact metadata", systemImage: "doc") }
                    NavigationLink { ReportsView(ref: ref) } label: { Label("Diagnosis", systemImage: "stethoscope") }
                    Button { watching = true } label: { Label("Watch this job…", systemImage: "bell.badge") }
                }
            } else if error == nil { ProgressView("Loading job…") }
        }.navigationTitle(detail?.job.name ?? "Job").navigationBarTitleDisplayMode(.inline)
            .task(id: ref) {
                await load()
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(max(5, store.bootstrap?.preferences.refreshSeconds ?? 5)))
                    if !Task.isCancelled, store.active, store.bootstrap?.preferences.refreshSeconds != 0 { await load() }
                }
            }.refreshable { await load() }
            .sheet(isPresented: $watching) { AlertEditor(initial: AlertRule(name: "Watched job", scope: "watched_jobs", namespaces: [ref.namespace], jobs: [ref])) }
    }
    private func load() async {
        do {
            let result: JobDetail = try await store.request(path: APIPath.job(ref))
            guard result.job.ref == ref else { throw DashboardError.invalidResponse }
            detail = result; error = nil
        }
        catch is CancellationError {} catch { self.error = error.localizedDescription }
    }
}

struct WorkloadsView: View {
    @Environment(DashboardStore.self) private var store
    @State private var kind = "collection"
    var body: some View {
        List {
            Section { ScopeMenu(); Picker("Kind", selection: $kind) { Text("Collections").tag("collection"); Text("Arrays").tag("array"); Text("Graphs").tag("graph") }.pickerStyle(.segmented) }
            PagedRows<Workload, WorkloadRow>(path: "/api/v1/workloads/\(kind)") { workload in WorkloadRow(workload: workload) }.id(kind)
        }.navigationTitle("Workloads")
    }
}

struct WorkloadRow: View {
    let workload: Workload
    var body: some View {
        NavigationLink { WorkloadDetailView(workload: workload) } label: {
            VStack(alignment: .leading) {
                Text(workload.name ?? workload.resourceId).font(.headline)
                Text("\(workload.deploymentId) / \(workload.namespaceId)").font(.caption).foregroundStyle(.secondary)
                Text("\(workload.totalChildren) children • \(workload.kind)").font(.callout)
            }
        }
    }
}

struct WorkloadDetailView: View {
    @Environment(DashboardStore.self) private var store
    let workload: Workload
    @State private var detail: WorkloadDetail?
    @State private var children: [WorkloadChild] = []
    @State private var page: Page<WorkloadChild>?
    @State private var cursor: String?
    @State private var error: String?
    @State private var loading = false
    @State private var currentCursor: String?
    @State private var pageHistory: [String?] = []
    private var summary: Workload { detail?.workload ?? workload }
    var body: some View {
        List {
            Section("Complete source summary") {
                Text("\(workload.deploymentId) / \(workload.namespaceId)")
                LabeledContent("Children", value: summary.totalChildren)
                LabeledContent("Revision", value: summary.revision)
                TimestampRow(label: "Source observation", value: summary.asOf)
                if let phase = summary.phase { LabeledContent("Phase", value: phase) }
                if let outcome = summary.outcome { LabeledContent("Outcome", value: outcome) }
                ForEach(summary.counts.keys.sorted(), id: \.self) { LabeledContent($0, value: summary.counts[$0] ?? "Unavailable") }
                LabeledContent("Concurrency", value: summary.concurrency ?? "Unavailable")
                LabeledContent("Failure policy", value: summary.failurePolicy ?? "Unavailable")
                if let policy = summary.unsatisfiedPolicy { LabeledContent("Unsatisfied dependency policy", value: policy) }
                if workload.kind == "array" {
                    LabeledContent("Array policy", value: summary.arrayPolicy ?? "Unavailable").accessibilityIdentifier("arrayPolicy")
                    LabeledContent("Array mode", value: summary.arrayMode ?? "Unavailable").accessibilityIdentifier("arrayMode")
                    LabeledContent("Slurm array", value: summary.arrayId ?? "Unavailable")
                    Text("Task indices are the exact Slurm indices; child position is shown separately.").font(.footnote)
                }
            }
            if let error { Section { ErrorMessage(error: error); Button("Refresh workload") { Task { await restart() } } } }
            if workload.kind == "graph", let first = children.first {
                Section("Graph exploration") {
                    NavigationLink("Explore graph neighborhood") { GraphExplorer(workload: workload, initialCenter: first.id) }
                    Text("Choose a node below to explore its bounded diagram, complete dependency counts, and paged incoming or outgoing edges.").font(.footnote)
                }
            }
            Section(workload.kind == "graph" ? "Nodes" : "Children") {
                ForEach(children) { child in
                    DisclosureGroup {
                        WorkloadChildFacts(child: child)
                        NavigationLink { JobDetailView(ref: child.job.ref) } label: { JobRow(job: child.job) }
                        if workload.kind == "graph" {
                            NavigationLink("Explore node \(child.id)") { GraphExplorer(workload: workload, initialCenter: child.id) }
                        }
                    } label: { Text(child.taskIndex.map { "Task \($0) · \(child.name ?? child.id)" } ?? child.name ?? child.id) }
                }
                if !pageHistory.isEmpty { Button("Previous child page") { Task { await previous() } }.disabled(loading) }
                if let cursor { Button("Next child page") { Task { await next(cursor) } }.disabled(loading) }
                if children.isEmpty && detail == nil && error == nil { ProgressView() }
                if let total = page?.total ?? detail?.total { Text("\(children.count) children on this page; \(total) at source").font(.caption) }
            }
            if let page { Section { SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt) } }
            else if let detail { Section { SourceSummary(completeness: detail.completeness, sources: detail.sources, fetchedAt: detail.fetchedAt) } }
        }.navigationTitle(workload.name ?? workload.kind.capitalized).task {
            await load()
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(max(5, store.bootstrap?.preferences.refreshSeconds ?? 5)))
                if !Task.isCancelled, store.active, store.bootstrap?.preferences.refreshSeconds != 0 { await load(cursor: currentCursor) }
            }
        }.refreshable { await restart() }
    }
    private func restart() async { if await load() { pageHistory = [] } }
    private func next(_ cursor: String) async { let previous = currentCursor; if await load(cursor: cursor) { pageHistory.append(previous) } }
    private func previous() async { guard let previous = pageHistory.last else { return }; if await load(cursor: previous) { pageHistory.removeLast() } }
    @discardableResult private func load(cursor: String? = nil) async -> Bool {
        guard !loading else { return false }; loading = true; defer { loading = false }
        do {
            if let cursor {
                let result: Page<WorkloadChild> = try await store.request(path: workload.path + "/children", query: [.init(name: "cursor", value: cursor), .init(name: "limit", value: "50")])
                try workload.validate(children: result.items, sources: result.sources)
                guard result.total != nil else { throw DashboardError.invalidResponse }
                children = result.items; self.cursor = result.nextCursor; page = result
            } else {
                let result: WorkloadDetail = try await store.request(path: workload.path, query: [.init(name: "limit", value: "50")])
                guard result.workload.path == workload.path else { throw DashboardError.invalidResponse }
                try workload.validate(children: result.children, sources: result.sources)
                detail = result; children = result.children; self.cursor = result.nextCursor; page = nil
            }
            currentCursor = cursor; error = nil; return true
        } catch is CancellationError { return false } catch { self.error = error.localizedDescription; return false }
    }
}

struct WorkloadChildFacts: View {
    let child: WorkloadChild
    var body: some View {
        LabeledContent("Child position", value: child.index)
        if let index = child.taskIndex { LabeledContent("Slurm task index", value: index).accessibilityIdentifier("slurmTaskIndex") }
        if let readiness = child.readiness { LabeledContent("Source readiness", value: readiness) }
        if let disposition = child.disposition { LabeledContent("Disposition", value: disposition) }
        if let counts = child.dependencyCounts {
            Text("Complete incoming dependency counts").font(.caption.bold())
            ForEach(counts.keys.sorted(), id: \.self) { LabeledContent($0, value: counts[$0] ?? "Unavailable") }
        }
    }
}

struct WorkloadReferenceView: View {
    @Environment(DashboardStore.self) private var store
    let namespace: NamespaceRef
    let kind: String
    let id: String
    @State private var workload: Workload?
    @State private var error: String?
    private var path: String {
        "/api/v1/deployments/\(APIPath.component(namespace.deploymentId))/namespaces/\(APIPath.component(namespace.namespaceId))/workloads/\(APIPath.component(kind))/\(APIPath.component(id))"
    }
    var body: some View {
        Group {
            if let workload { WorkloadDetailView(workload: workload) }
            else if let error { List { ErrorMessage(error: error); Button("Retry") { Task { await load() } } } }
            else { ProgressView("Loading authorized workload…") }
        }.task { await load() }
    }
    private func load() async {
        do {
            let result: WorkloadDetail = try await store.request(path: path)
            guard result.workload.path == path else { throw DashboardError.invalidResponse }
            workload = result.workload; error = nil
        } catch is CancellationError {} catch { self.error = error.localizedDescription }
    }
}

struct PagedRows<Item: Decodable & Sendable & Identifiable, Row: View>: View {
    @Environment(DashboardStore.self) private var store
    let path: String
    var extraQuery: [URLQueryItem] = []
    var scoped = true
    let row: (Item) -> Row
    @State private var items: [Item] = []
    @State private var cursor: String?
    @State private var loaded = false
    @State private var busy = false
    @State private var error: String?
    @State private var lastPage: Page<Item>?
    @State private var evictedRows = 0
    var body: some View {
        Section {
            if !loaded && error == nil { ProgressView("Loading authorized items…") }
            ForEach(items, content: row)
            if evictedRows > 0 { Text("\(evictedRows) earlier rows removed from memory; reopen this view to restart.").font(.caption) }
            if let page = lastPage {
                if let total = page.total { Text("Source total: \(total)").font(.caption) }
                if let totals = page.totals {
                    ForEach(Array(totals.enumerated()), id: \.offset) { _, total in
                        VStack(alignment: .leading) {
                            Text("\(total.deploymentId) / \(total.namespaceId): \(total.total)").font(.caption)
                            TimestampRow(label: "Source observation", value: total.asOf)
                        }
                    }
                }
                SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt)
            }
            if let error { ErrorMessage(error: error); Button("Retry") { Task { await load(cursor: cursor) } } }
            if busy { ProgressView() }
            else if let cursor { Button("Load next page") { Task { await load(cursor: cursor) } } }
            else if loaded && items.isEmpty && error == nil { Text("No items in this view").foregroundStyle(.secondary) }
        }.task(id: store.contentGeneration) {
            await load()
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(max(5, store.bootstrap?.preferences.refreshSeconds ?? 5)))
                if !Task.isCancelled, store.active, store.bootstrap?.preferences.refreshSeconds != 0 { await load(refresh: true) }
            }
        }
    }
    private func load(cursor: String? = nil, refresh: Bool = false) async {
        guard !busy else { return }; busy = true; defer { busy = false }
        do {
            var query = try (scoped ? store.query() : []) + extraQuery + [.init(name: "limit", value: "50")]
            if let cursor { query.append(.init(name: "cursor", value: cursor)) }
            let page: Page<Item> = try await store.request(path: path, query: query)
            lastPage = page
            if refresh {
                let updates = Dictionary(page.items.map { ($0.id, $0) }, uniquingKeysWith: { _, last in last })
                items = items.map { updates[$0.id] ?? $0 }
                if items.isEmpty { items = page.items; self.cursor = page.nextCursor }
            } else if cursor == nil { items = page.items; self.cursor = page.nextCursor }
            else {
                let existing = Set(items.map(\.id))
                items += page.items.filter { !existing.contains($0.id) }
                self.cursor = page.nextCursor
            }
            if items.count > 1_000 { let count = items.count - 1_000; items.removeFirst(count); evictedRows += count }
            loaded = true; error = nil
        } catch is CancellationError {} catch { self.error = error.localizedDescription }
    }
}
