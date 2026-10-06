import DashboardCore
import SwiftUI

struct OverviewView: View {
    @Environment(\.colorScheme) private var colorScheme
    @Environment(DashboardStore.self) private var store
    var body: some View {
        List {
            Section {
                ScopeMenu()
                Picker("Final results from", selection: Binding(get: { store.overviewWindow }, set: { store.selectOverviewWindow($0) })) {
                    ForEach(OverviewWindow.allCases, id: \.self) { Text($0.title).tag($0) }
                }.accessibilityIdentifier("overviewWindow")
            }
            if let error = store.error { Section { ErrorMessage(error: error) } }
            if let summary = store.overview {
                Section("Recorded job status") {
                    count("Active jobs", summary.active, phase: "active")
                    count("Waiting for execution", summary.awaitingExecution, phase: "awaiting")
                    count("Running", summary.running, phase: "running")
                    Button { store.drilldown(phase: "active", attention: true) } label: { LabeledContent("Status needs attention", value: summary.evidenceAttention.map(String.init) ?? "Not available") }.buttonStyle(.plain)
                    Text("Jobs needing attention may still be active. Their reported status needs checking; this does not mean they failed.").font(.footnote).foregroundStyle(.secondary)
                }
                Section("Final results") {
                    ForEach(summary.terminal.keys.sorted(), id: \.self) { outcome in
                        count(InterfaceText.finalResult(outcome), summary.terminal[outcome] ?? nil, phase: "terminal", outcome: outcome, window: summary.window)
                    }
                    LabeledContent("Completion time not recorded", value: summary.missingCompletionTime.map(String.init) ?? "Not available")
                    TimestampRow(label: "From", value: summary.window.from)
                    TimestampRow(label: "Before", value: summary.window.to)
                }
                Section { SourceSummary(completeness: summary.completeness, sources: summary.sources, fetchedAt: summary.fetchedAt) }
            } else if store.error == nil { ProgressView("Loading activity…") }
        }.navigationTitle("Overview").refreshable { store.refresh() }
            .toolbar {
                if colorScheme == .dark {
                    ToolbarItem(placement: .topBarLeading) { DashboardBrand(width: 140) }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { store.refresh() } label: { Label("Refresh", systemImage: "arrow.clockwise") }
                }
            }
    }

    private func count(_ title: String, _ value: Int?, phase: String, outcome: String = "", window: Overview.Window? = nil) -> some View {
        Button {
            store.drilldown(phase: phase, outcome: outcome, window: window)
        } label: { LabeledContent(title, value: value.map(String.init) ?? "Not available") }.buttonStyle(.plain)
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
                Picker("Job status", selection: $store.phase) {
                    Text("All statuses").tag("")
                    ForEach(["active", "awaiting", "accepted", "assigning", "accepted_execution", "running", "terminal"], id: \.self) { Text(InterfaceText.jobStatus($0)).tag($0) }
                }.onChange(of: store.phase) { _, _ in store.resetJobs() }
                Picker("Final result", selection: $store.outcome) {
                    Text("All final results").tag("")
                    ForEach(["success", "failure", "timed_out", "aborted", "lost", "cancelled"], id: \.self) { Text(InterfaceText.finalResult($0)).tag($0) }
                }.onChange(of: store.outcome) { _, _ in store.resetJobs() }
                Toggle("Submitted by me", isOn: $store.ownerOnly).onChange(of: store.ownerOnly) { _, _ in store.resetJobs() }
                Toggle("Status needs attention", isOn: $store.attentionOnly).onChange(of: store.attentionOnly) { _, _ in store.resetJobs() }
                Toggle("Filter by completion time", isOn: Binding(get: { store.completedFrom != nil }, set: { enabled in
                    store.completedFrom = enabled ? Date().addingTimeInterval(-86400) : nil
                    store.completedTo = enabled ? Date() : nil
                    store.resetJobs()
                }))
                if store.completedFrom != nil {
                    DatePicker("Completed on or after", selection: Binding(get: { store.completedFrom ?? Date() }, set: { store.completedFrom = $0; store.resetJobs() }))
                    DatePicker("Completed before", selection: Binding(get: { store.completedTo ?? Date() }, set: { store.completedTo = $0; store.resetJobs() }))
                }
                if case .namespace(let ref) = store.scope {
                    HStack {
                        TextField("Full job ID", text: $exactID).textInputAutocapitalization(.never).autocorrectionDisabled()
                        Button("Open") { store.open(.job(.init(deploymentId: ref.deploymentId, namespaceId: ref.namespaceId, jobId: exactID))) }.disabled(exactID.isEmpty)
                    }
                } else { Text("Choose a namespace to open a job by its full ID.").font(.footnote).foregroundStyle(.secondary) }
            }
            if let error = store.error { ErrorMessage(error: error) }
            Section {
                Button("Restart from first page") { store.resetJobs() }.accessibilityIdentifier("restartJobs")
                if !store.jobsAreFirstPage { Text("You are viewing a later page. Return to the first page to see newly added jobs.").font(.caption) }
                if store.canLoadPreviousJobs { Button("Previous page") { Task { await store.loadPreviousJobs() } }.disabled(store.loadingMore) }
            }
            if let page = store.jobs {
                Section {
                    if store.evictedJobRows > 0 { Text("\(store.evictedJobRows) older pages are no longer kept in memory. Restart to return to the first page.").font(.caption) }
                    if store.jobRows.isEmpty { ContentUnavailableView("No matching jobs", systemImage: "line.3.horizontal.decrease.circle", description: Text("Change the filters or refresh this selection.")) }
                    ForEach(store.jobRows, id: \.ref) { job in NavigationLink(value: DashboardRoute.job(job.ref)) { JobRow(job: job) } }
                    if store.nextJobsCursor != nil { Button { Task { await store.loadMoreJobs() } } label: { if store.loadingMore { ProgressView() } else { Text("Next page") } }.disabled(store.loadingMore) }
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
                HStack { StatusBadge(title: "Job status", value: InterfaceText.jobStatus(job.phase)); StatusBadge(title: "Final result", value: job.outcome.map(InterfaceText.finalResult)) }
                VStack(alignment: .leading) { StatusBadge(title: "Job status", value: InterfaceText.jobStatus(job.phase)); StatusBadge(title: "Final result", value: job.outcome.map(InterfaceText.finalResult)) }
            }
            if let confidence = job.confidence { Text("Status confidence: \(InterfaceText.statusConfidence(confidence))").font(.caption).foregroundStyle(.secondary) }
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
    @State private var selectedRun: DashboardAPI.JobRun?
    var body: some View {
        List {
            Section { Text("\(ref.deploymentId) / \(ref.namespaceId)").font(.caption); Text(ref.jobId).textSelection(.enabled) }
            if let error { ErrorMessage(error: error) }
            if let detail {
                let job = detail.job
                Section("Current job status and times") {
                    JobRow(job: job)
                    Text("These are the latest loaded job details. Choosing a recorded run below does not change them.").font(.footnote).foregroundStyle(.secondary)
                    TimestampRow(label: "Status confidence updated", value: job.confidenceUpdatedAt)
                    Text("Status confidence describes how current or certain the reported status is. It does not predict success. Outdated means Control has not heard recently from the execution agent; it does not prove the job has stopped.").font(.footnote).foregroundStyle(.secondary)
                    LabeledContent("Requested state", value: job.desiredState)
                    LabeledContent("Dependency progress", value: job.disposition ?? "Not available")
                    Text("Requested cancellation is not confirmed cancellation. Check the final result to see how the job ended.").font(.footnote).foregroundStyle(.secondary)
                    TimestampRow(label: "Created", value: job.createdAt)
                    TimestampRow(label: "Execution started", value: job.startedAt)
                    TimestampRow(label: "Start recorded by deployment", value: job.lifecycle?.startedRecordedAt)
                    LabeledContent("Start time source", value: job.lifecycle?.startedProvenance ?? "Not available")
                    TimestampRow(label: "Completed", value: job.completedAt)
                    TimestampRow(label: "Completion recorded by deployment", value: job.lifecycle?.completedRecordedAt)
                    LabeledContent("Completion time source", value: job.lifecycle?.completedProvenance ?? "Not available")
                    TimestampRow(label: "Job record updated", value: job.updatedAt)
                    TimestampRow(label: "Last refreshed", value: detail.fetchedAt)
                }
                Section("Where this job runs and who submitted it") {
                    LabeledContent("Submitted by", value: job.owner?.displayName ?? job.owner?.id ?? "Not available")
                    InspectionText(label: "Owner ID", value: job.owner?.id ?? "Not available")
                    LabeledContent("Target name", value: job.targetName ?? "Not available")
                    LabeledContent("Target ID", value: job.targetId)
                    LabeledContent("Partition", value: job.partition ?? "Not available")
                    InspectionText(label: "Submitted workload fingerprint", value: job.workloadDigest ?? "Not available")
                    LabeledContent("Target configuration version ID", value: job.targetGenerationId ?? "Not available")
                    LabeledContent("Execution system", value: job.backend ?? "Not available")
                    LabeledContent("Record version", value: job.revision)
                    LabeledContent("Scheduler system", value: job.scheduler?.backend ?? "Not available")
                    LabeledContent("Scheduler status", value: job.scheduler?.state ?? "Not available")
                    LabeledContent("Scheduler job ID", value: job.scheduler?.jobId ?? "Not available")
                    LabeledContent("Scheduler reason", value: job.scheduler?.reason ?? "Not available")
                    LabeledContent("Cluster", value: job.scheduler?.cluster ?? "Not available")
                    TimestampRow(label: "Scheduler observation", value: job.scheduler?.observedAt)
                    LabeledContent("Imported job record", value: job.imported.map { $0 ? "Yes" : "No" } ?? "Not available")
                    ForEach(job.labels.keys.sorted(), id: \.self) { key in LabeledContent(key, value: job.labels[key] ?? "") }
                }
                Section("Current run and job groups") {
                    LabeledContent("Current run", value: job.currentRun?.number ?? "Not available")
                    LabeledContent("Run ID", value: job.currentRun?.id ?? "Not available")
                    LabeledContent("Execution ID", value: job.currentRun?.executionId ?? "Not available")
                    if let id = job.group?.collectionId {
                        NavigationLink("Collection \(id)") { WorkloadReferenceView(namespace: ref.namespace, kind: "collection", id: id) }
                        if let index = job.group?.collectionIndex { LabeledContent("Position in collection", value: String(index)) }
                    }
                    if let id = job.group?.graphId {
                        NavigationLink("Graph \(id)") { WorkloadReferenceView(namespace: ref.namespace, kind: "graph", id: id) }
                        if let index = job.group?.graphIndex { LabeledContent("Position in graph", value: String(index)) }
                    }
                }
                Section("Submitted command") {
                    if let execution = detail.execution {
                        Text("The command as submitted. It may not have run; values changed by the program or shell during execution are not shown.").font(.footnote).foregroundStyle(.secondary)
                        InspectionText(label: "Submitted working directory", value: execution.workingDirectory, empty: "No directory specified")
                        NavigationLink { JobCommandView(execution: execution) } label: {
                            Label("View full command and arguments", systemImage: "text.alignleft")
                        }.accessibilityIdentifier("viewSubmittedCommand")
                        Text("\(execution.command.args.count) ordered arguments; environment values are not included.").font(.caption)
                    } else {
                        Text("Submitted command unavailable")
                        Text(verbatim: InterfaceText.submittedCommandUnavailable(detail.executionUnavailableReason))
                            .font(.footnote).textSelection(.enabled).accessibilityIdentifier("executionUnavailableReason")
                    }
                }
                Section("Investigate") {
                    NavigationLink(selectedRun.map { "Selected run \($0.number)" } ?? "Choose a recorded run") { RunPickerView(ref: ref, selection: $selectedRun) }
                    if let run = selectedRun { Text("Run \(run.number) · \(run.id)").font(.caption).accessibilityIdentifier("selectedRun") }
                    NavigationLink { LogView(ref: ref, run: selectedRun).id(selectedRun?.id ?? "current") } label: { Label("Logs", systemImage: "text.alignleft") }
                    NavigationLink { ArtifactsView(ref: ref, run: selectedRun).id(selectedRun?.id ?? "all") } label: { Label("Output files", systemImage: "doc") }
                    NavigationLink { ReportsView(ref: ref, selectedRunID: selectedRun?.id).id(selectedRun?.id ?? "current") } label: { Label("Diagnosis", systemImage: "stethoscope") }
                    Button { watching = true } label: { Label("Watch this job…", systemImage: "bell.badge") }
                }
                if let run = selectedRun {
                    SelectedRunFacts(run: run, currentRunID: job.currentRun?.id)
                }
            } else if error == nil { ProgressView("Loading job…") }
        }.navigationTitle(detail?.job.name ?? "Job").navigationBarTitleDisplayMode(.inline)
            .task(id: store.foregroundGeneration) {
                await load()
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(max(5, store.bootstrap?.preferences.refreshSeconds ?? 5)))
                    if !Task.isCancelled, store.active, store.bootstrap?.preferences.refreshSeconds != 0 { await load() }
                }
            }.refreshable { await load() }
            .sheet(isPresented: $watching) { AlertEditor(watching: ref) }
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
            Section { ScopeMenu(); Text("Collections group jobs together. Arrays run related Slurm tasks. Graphs show jobs and the dependencies between them.").font(.footnote).foregroundStyle(.secondary); Picker("Group type", selection: $kind) { Text("Collections").tag("collection"); Text("Arrays").tag("array"); Text("Graphs").tag("graph") }.pickerStyle(.segmented) }
            PagedRows<Workload, WorkloadRow>(path: "/api/v1/workloads/\(kind)") { workload in WorkloadRow(workload: workload) }.id(kind)
        }.navigationTitle("Job groups")
    }
}

struct WorkloadRow: View {
    let workload: Workload
    var body: some View {
        NavigationLink { WorkloadDetailView(workload: workload) } label: {
            VStack(alignment: .leading) {
                Text(workload.name ?? workload.resourceId).font(.headline)
                Text("\(workload.deploymentId) / \(workload.namespaceId)").font(.caption).foregroundStyle(.secondary)
                Text("\(workload.totalChildren) jobs • \(workload.kind)").font(.callout)
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
    @State private var requests = ReadRequestGate()
    private var loading: Bool { requests.busy }
    @State private var currentCursor: String?
    @State private var pageHistory: [String?] = []
    private var summary: Workload { detail?.workload ?? workload }
    var body: some View {
        List {
            Section("Whole group summary") {
                Text("\(workload.deploymentId) / \(workload.namespaceId)")
                LabeledContent("Jobs in group", value: summary.totalChildren)
                LabeledContent("Record version", value: summary.revision)
                TimestampRow(label: "Source observation", value: summary.asOf)
                if let phase = summary.phase { LabeledContent("Job status", value: InterfaceText.jobStatus(phase)) }
                if let outcome = summary.outcome { LabeledContent("Final result", value: InterfaceText.finalResult(outcome)) }
                ForEach(summary.counts.keys.sorted(), id: \.self) { LabeledContent(InterfaceText.finalResult(InterfaceText.jobStatus($0)), value: summary.counts[$0] ?? "Not available") }
                LabeledContent("Maximum jobs running at once", value: summary.concurrency ?? "Not available")
                LabeledContent("What happens after a failure", value: summary.failurePolicy ?? "Not available")
                if let policy = summary.unsatisfiedPolicy { LabeledContent("When a dependency is not met", value: policy) }
                if workload.kind == "array" {
                    LabeledContent("Array policy", value: summary.arrayPolicy ?? "Not available").accessibilityIdentifier("arrayPolicy")
                    LabeledContent("Array mode", value: summary.arrayMode ?? "Not available").accessibilityIdentifier("arrayMode")
                    LabeledContent("Slurm array", value: summary.arrayId ?? "Not available")
                    Text("Slurm task numbers are shown exactly as recorded. A job’s position in this group is shown separately.").font(.footnote)
                }
            }
            if let error { Section { ErrorMessage(error: error); Button("Refresh job group") { Task { await restart() } } } }
            if workload.kind == "graph", let first = children.first {
                Section("Graph exploration") {
                    NavigationLink("Explore connected jobs") { GraphExplorer(workload: workload, initialCenter: first.id) }
                    Text("Choose a job below to see its connected jobs and dependencies. Large diagrams show a limited area; use the dependency pages for the full list.").font(.footnote)
                }
            }
            Section("Jobs in group") {
                if !pageHistory.isEmpty { Button("Previous jobs page") { Task { await previous() } }.disabled(loading) }
                if let cursor { Button("Next jobs page") { Task { await next(cursor) } }.disabled(loading) }
                if let total = page?.total ?? detail?.total { Text("\(children.count) jobs on this page; \(total) in the group").font(.caption).accessibilityIdentifier("childPageTotal") }
                ForEach(children) { child in
                    DisclosureGroup {
                        WorkloadChildFacts(child: child)
                        NavigationLink { JobDetailView(ref: child.job.ref) } label: { JobRow(job: child.job) }
                        if workload.kind == "graph" {
                            NavigationLink("Explore node \(child.id)") { GraphExplorer(workload: workload, initialCenter: child.id) }
                        }
                    } label: { Text(child.taskIndex.map { "Task \($0) · \(child.name ?? child.id)" } ?? child.name ?? child.id) }
                }
                if children.isEmpty && detail == nil && error == nil { ProgressView() }
            }
            if let page { Section { SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt) } }
            else if let detail { Section { SourceSummary(completeness: detail.completeness, sources: detail.sources, fetchedAt: detail.fetchedAt) } }
        }.navigationTitle(workload.name ?? workload.kind.capitalized).task(id: store.foregroundGeneration) {
            await load(cursor: currentCursor, replacing: true)
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(max(5, store.bootstrap?.preferences.refreshSeconds ?? 5)))
                if !Task.isCancelled, store.active, store.bootstrap?.preferences.refreshSeconds != 0 { await load(cursor: currentCursor) }
            }
        }.refreshable { await restart() }
    }
    private func restart() async { if await load() { pageHistory = [] } }
    private func next(_ cursor: String) async { let previous = currentCursor; if await load(cursor: cursor) { pageHistory.append(previous) } }
    private func previous() async { guard let previous = pageHistory.last else { return }; if await load(cursor: previous) { pageHistory.removeLast() } }
    @discardableResult private func load(cursor: String? = nil, replacing: Bool = false) async -> Bool {
        guard let token = requests.begin(replacing: replacing) else { return false }; defer { requests.finish(token) }
        do {
            if let cursor {
                let result: Page<WorkloadChild> = try await store.request(path: workload.path + "/children", query: [.init(name: "cursor", value: cursor), .init(name: "limit", value: "50")])
                guard requests.accepts(token) else { return false }
                try workload.validate(children: result.items, sources: result.sources)
                guard result.total != nil else { throw DashboardError.invalidResponse }
                children = result.items; self.cursor = result.nextCursor; page = result
            } else {
                let result: WorkloadDetail = try await store.request(path: workload.path, query: [.init(name: "limit", value: "50")])
                guard requests.accepts(token) else { return false }
                guard result.workload.path == workload.path else { throw DashboardError.invalidResponse }
                try workload.validate(children: result.children, sources: result.sources)
                detail = result; children = result.children; self.cursor = result.nextCursor; page = nil
            }
            currentCursor = cursor; error = nil; return true
        } catch is CancellationError { return false } catch { if requests.accepts(token) { self.error = error.localizedDescription }; return false }
    }
}

struct WorkloadChildFacts: View {
    let child: WorkloadChild
    var body: some View {
        LabeledContent("Position in group", value: child.index)
        if let index = child.taskIndex { LabeledContent("Slurm task index", value: index).accessibilityIdentifier("slurmTaskIndex") }
        if let readiness = child.readiness { LabeledContent("Reported readiness", value: readiness) }
        if let disposition = child.disposition { LabeledContent("Dependency progress", value: disposition) }
        if let counts = child.dependencyCounts {
            Text("All dependencies this job requires").font(.caption.bold())
            ForEach(counts.keys.sorted(), id: \.self) { LabeledContent($0, value: counts[$0] ?? "Not available") }
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
            else { ProgressView("Loading job group…") }
        }.task(id: store.foregroundGeneration) { await load() }
    }
    private func load() async {
        do {
            let result: WorkloadDetail = try await store.request(path: path)
            guard result.workload.path == path else { throw DashboardError.invalidResponse }
            workload = result.workload; error = nil
        } catch is CancellationError {} catch { self.error = error.localizedDescription }
    }
}

struct PagedRows<Item: Decodable & Sendable & Identifiable, Row: View>: View where Item.ID: Sendable {
    @Environment(DashboardStore.self) private var store
    let path: String
    var extraQuery: [URLQueryItem] = []
    var scoped = true
    var validate: (Page<Item>) throws -> Void = { _ in }
    let row: (Item) -> Row
    @State private var state = MonitoredPage<Item, Item.ID>()
    @State private var requests = ReadRequestGate()
    private var busy: Bool { requests.busy }
    @State private var error: String?
    var body: some View {
        Section {
            if state.page == nil && error == nil { ProgressView("Loading items…") }
            ForEach(state.items, content: row)
            if !state.isFirstPage { Text("You are viewing a later page. Return to the first page to see newly added items.").font(.caption) }
            if state.history.discardedPages > 0 { Text("Older pages are no longer kept in memory. Restart to return to the first page.").font(.caption) }
            if let page = state.page {
                if let total = page.total { Text("Total reported: \(total)").font(.caption) }
                if let totals = page.totals {
                    ForEach(Array(totals.enumerated()), id: \.offset) { _, total in
                        VStack(alignment: .leading) {
                            Text("\(total.deploymentId) / \(total.namespaceId): \(total.total)").font(.caption)
                            TimestampRow(label: "Source observation", value: total.asOf)
                        }
                    }
                }
                SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt)
                if page.items.isEmpty { Text("No items on this page").foregroundStyle(.secondary) }
            }
            if let error { ErrorMessage(error: error) }
            if busy { ProgressView() }
            Button("Refresh current page") { Task { await load(.refresh) } }.disabled(busy || state.requiresRestart)
            Button("Restart from first page") { Task { await load(.restart) } }.disabled(busy).accessibilityIdentifier("restartPagedRows")
            if state.canGoBack { Button("Previous page") { Task { await load(.previous) } }.disabled(busy) }
            if let cursor = state.nextCursor { Button("Next page") { Task { await load(.next(cursor)) } }.disabled(busy) }
        }.task(id: store.foregroundGeneration) {
            await load(.refresh, replacing: true)
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(max(5, store.bootstrap?.preferences.refreshSeconds ?? 5)))
                if !Task.isCancelled, store.active, store.bootstrap?.preferences.refreshSeconds != 0 { await load(.refresh) }
            }
        }
    }
    private func load(_ action: MonitoredPage<Item, Item.ID>.Load, replacing: Bool = false) async {
        guard let token = requests.begin(replacing: replacing) else { return }; defer { requests.finish(token) }
        do {
            var query = try (scoped ? store.query() : []) + extraQuery + [.init(name: "limit", value: "50")]
            if let cursor = try state.requestCursor(for: action) { query.append(.init(name: "cursor", value: cursor)) }
            let page: Page<Item> = try await store.request(path: path, query: query)
            guard requests.accepts(token) else { return }
            try validate(page)
            try state.accept(page, for: action, identity: \.id)
            error = nil
        } catch is CancellationError {} catch { if requests.accepts(token) { state.failed(error); self.error = error.localizedDescription } }
    }
}
