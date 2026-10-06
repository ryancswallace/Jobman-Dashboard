import DashboardCore
import SwiftUI

struct GraphExplorer: View {
    let workload: Workload
    @State private var center: String
    init(workload: Workload, initialCenter: String) {
        self.workload = workload
        _center = State(initialValue: initialCenter)
    }
    var body: some View {
        GraphSnapshotView(workload: workload, center: center) { center = $0 }
            .id(center).navigationTitle("Connected jobs")
    }
}

private struct GraphSnapshotView: View {
    @Environment(DashboardStore.self) private var store
    let workload: Workload
    let center: String
    let select: (String) -> Void
    @State private var neighborhood: GraphNeighborhood?
    @State private var showDiagram = true
    @State private var error: String?
    @State private var requests = ReadRequestGate()
    private var loading: Bool { requests.busy }
    var body: some View {
        List {
            Section("Selected job") {
                #if DEBUG
                if NativeGraphFixtures.enabled { NativeGraphDisplayFacts() }
                #endif
                Text(center).font(.headline).accessibilityIdentifier("graphCenter")
                Text("\(workload.deploymentId) / \(workload.namespaceId)").font(.caption)
                NavigationLink("Browse dependencies") { GraphDependenciesView(workload: workload, node: center) }
                if let node = neighborhood?.nodes.first(where: { $0.id == center }) {
                    WorkloadChildFacts(child: node)
                    NavigationLink("Open selected job") { JobDetailView(ref: node.job.ref) }
                }
            }
            if let error { Section { ErrorMessage(error: error); Button("Retry connected jobs") { Task { await load() } } } }
            if loading && neighborhood == nil { ProgressView("Loading connected jobs…") }
            if let neighborhood {
                Section("Connected jobs diagram") {
                    Text("Connected area: \(neighborhood.totalNodes) jobs, \(neighborhood.totalEdges) dependencies").font(.caption).accessibilityIdentifier("graphNeighborhoodTotal")
                    Text("Not shown here: \(neighborhood.omittedNodes) jobs, \(neighborhood.omittedEdges) dependencies").font(.caption).accessibilityIdentifier("graphOmissions")
                    Toggle("Show diagram", isOn: $showDiagram)
                    if showDiagram { DependencyDiagram(neighborhood: neighborhood, select: select) }
                    Text("Each node represents a job; arrows show dependencies. Select a node to center the diagram on it, or use the list below with VoiceOver. Check the reported readiness to know whether a job can proceed.").font(.footnote)
                }
                Section("Connected jobs list") {
                    ForEach(neighborhood.nodes) { child in
                        VStack(alignment: .leading, spacing: 8) {
                            Button("Center on \(child.name ?? child.id)") { select(child.id) }
                                .buttonStyle(.borderless).disabled(child.id == center)
                                .accessibilityAddTraits(child.id == center ? .isSelected : [])
                                .accessibilityIdentifier("center-\(child.id)")
                            Text("Job \(child.id)").font(.caption)
                            WorkloadChildFacts(child: child)
                            NavigationLink("Open job \(child.job.id)") { JobDetailView(ref: child.job.ref) }.buttonStyle(.borderless)
                        }
                    }
                }
                Section { SourceSummary(completeness: neighborhood.completeness, sources: neighborhood.sources, fetchedAt: neighborhood.fetchedAt) }
            }
        }.task(id: store.foregroundGeneration) { await load(replacing: true) }.refreshable { await load() }
    }
    private func load(replacing: Bool = false) async {
        guard let token = requests.begin(replacing: replacing) else { return }; defer { requests.finish(token) }
        do {
            let result: GraphNeighborhood = try await store.request(path: workload.path + "/neighborhood", query: [
                .init(name: "nodeId", value: center), .init(name: "maxNodes", value: "200"), .init(name: "maxEdges", value: "500")])
            guard requests.accepts(token) else { return }
            try result.validate(workload: workload, center: center)
            neighborhood = result; error = nil
        } catch is CancellationError {} catch { if requests.accepts(token) { self.error = error.localizedDescription } }
    }
}

struct GraphDependenciesView: View {
    let workload: Workload
    let node: String
    @State private var direction = "incoming"
    var body: some View {
        List {
            Section {
                Text("Job \(node)").font(.headline)
                Text("\(workload.deploymentId) / \(workload.namespaceId)").font(.caption)
                Picker("Direction", selection: $direction) {
                    Text("Incoming").tag("incoming"); Text("Outgoing").tag("outgoing"); Text("Both").tag("")
                }.pickerStyle(.segmented)
                Text("Incoming dependencies are jobs this job depends on. Outgoing dependencies lead to jobs that depend on this one.").font(.footnote).foregroundStyle(.secondary)
            }
            GraphEdgeRows(workload: workload, node: node, direction: direction).id(direction)
        }.navigationTitle("Dependencies")
    }
}

private struct GraphEdgeRows: View {
    @Environment(DashboardStore.self) private var store
    let workload: Workload
    let node: String
    let direction: String
    @State private var page: Page<GraphEdge>?
    @State private var currentCursor: String?
    @State private var history = InboxPageHistory()
    @State private var error: String?
    @State private var requests = ReadRequestGate()
    private var loading: Bool { requests.busy }
    var body: some View {
        Section("\(direction.isEmpty ? "All connected" : direction.capitalized) dependencies") {
            if let page {
                Text("\(page.items.count) dependencies on this page; \(page.total ?? "not available") matching dependencies in total").font(.caption).accessibilityIdentifier("edgePageTotal")
                if history.canGoBack { Button("Previous dependency page") { Task { await previous() } }.disabled(loading) }
                if let cursor = page.nextCursor { Button("Next dependency page") { Task { await next(cursor) } }.disabled(loading) }
                if history.discardedPages > 0 {
                    Text("Only the most recent \(InboxPageHistory.capacity) previous pages are retained. Refresh dependencies to return to the first page.")
                        .font(.footnote).accessibilityIdentifier("graphHistoryWindow")
                }
                ForEach(page.items) { edge in
                    DisclosureGroup("\(edge.from) → \(edge.to)") {
                        LabeledContent("Dependency condition", value: edge.predicate)
                        LabeledContent("Required results", value: edge.outcomes.isEmpty ? "None specified" : edge.outcomes.joined(separator: ", "))
                        LabeledContent("Dependency state", value: edge.state)
                        LabeledContent("Earlier job status", value: InterfaceText.jobStatus(edge.upstreamPhase))
                        LabeledContent("Earlier job final result", value: edge.upstreamOutcome.map(InterfaceText.finalResult) ?? "Not available")
                        NavigationLink("Open earlier job") { JobDetailView(ref: reference(edge.fromJobId)) }
                        NavigationLink("Open dependent job") { JobDetailView(ref: reference(edge.toJobId)) }
                        NavigationLink("Explore earlier job") { GraphExplorer(workload: workload, initialCenter: edge.fromJobId) }
                        NavigationLink("Explore dependent job") { GraphExplorer(workload: workload, initialCenter: edge.toJobId) }
                    }
                }
                if page.items.isEmpty { Text("No matching dependencies") }
                SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt)
            }
            if loading { ProgressView("Loading dependencies…") }
            if let error { ErrorMessage(error: error) }
            Button("Refresh dependencies") { Task { if await load() { history.reset() } } }.disabled(loading)
        }.task(id: store.foregroundGeneration) { await load(currentCursor, replacing: true) }
    }
    private func reference(_ id: String) -> JobRef { .init(deploymentId: workload.deploymentId, namespaceId: workload.namespaceId, jobId: id) }
    private func next(_ cursor: String) async { let previous = currentCursor; if await load(cursor) { history.record(previous) } }
    private func previous() async {
        guard history.canGoBack else { return }
        var retained = history
        let previous = retained.previous()
        if await load(previous) { history = retained }
    }
    @discardableResult private func load(_ cursor: String? = nil, replacing: Bool = false) async -> Bool {
        guard let token = requests.begin(replacing: replacing) else { return false }; defer { requests.finish(token) }
        do {
            var query: [URLQueryItem] = [.init(name: "nodeId", value: node), .init(name: "limit", value: "100")]
            if !direction.isEmpty { query.append(.init(name: "direction", value: direction)) }
            if let cursor { query.append(.init(name: "cursor", value: cursor)) }
            let result: Page<GraphEdge> = try await store.request(path: workload.path + "/dependencies", query: query)
            guard requests.accepts(token) else { return false }
            try workload.validate(edges: result.items, node: node, direction: direction, sources: result.sources)
            guard result.total != nil else { throw DashboardError.invalidResponse }
            page = result; currentCursor = cursor; error = nil; return true
        } catch is CancellationError { return false } catch { if requests.accepts(token) { self.error = error.localizedDescription }; return false }
    }
}
