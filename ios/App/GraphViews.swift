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
            .id(center).navigationTitle("Graph neighborhood")
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
            Section("Selected node") {
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
            if let error { Section { ErrorMessage(error: error); Button("Retry neighborhood") { Task { await load() } } } }
            if loading && neighborhood == nil { ProgressView("Loading bounded neighborhood…") }
            if let neighborhood {
                Section("Bounded graph diagram") {
                    Text("Neighborhood totals: \(neighborhood.totalNodes) nodes, \(neighborhood.totalEdges) edges").font(.caption).accessibilityIdentifier("graphNeighborhoodTotal")
                    Text("Not shown in this neighborhood: \(neighborhood.omittedNodes) nodes, \(neighborhood.omittedEdges) edges").font(.caption).accessibilityIdentifier("graphOmissions")
                    Toggle("Show diagram", isOn: $showDiagram)
                    if showDiagram { DependencyDiagram(neighborhood: neighborhood, select: select) }
                    Text("Select a node to recenter. The list below offers the same selection and job access with VoiceOver. Diagram edges do not determine readiness.").font(.footnote)
                }
                Section("Accessible node list") {
                    ForEach(neighborhood.nodes) { child in
                        VStack(alignment: .leading, spacing: 8) {
                            Button("Center on \(child.name ?? child.id)") { select(child.id) }
                                .buttonStyle(.borderless).disabled(child.id == center)
                                .accessibilityAddTraits(child.id == center ? .isSelected : [])
                                .accessibilityIdentifier("center-\(child.id)")
                            Text("Node \(child.id)").font(.caption)
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
                Text("Node \(node)").font(.headline)
                Text("\(workload.deploymentId) / \(workload.namespaceId)").font(.caption)
                Picker("Direction", selection: $direction) {
                    Text("Incoming").tag("incoming"); Text("Outgoing").tag("outgoing"); Text("Both").tag("")
                }.pickerStyle(.segmented)
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
                Text("\(page.items.count) edges on this page; \(page.total ?? "unavailable") matching edges at source").font(.caption).accessibilityIdentifier("edgePageTotal")
                if history.canGoBack { Button("Previous dependency page") { Task { await previous() } }.disabled(loading) }
                if let cursor = page.nextCursor { Button("Next dependency page") { Task { await next(cursor) } }.disabled(loading) }
                if history.discardedPages > 0 {
                    Text("Only the most recent \(InboxPageHistory.capacity) previous pages are retained. Refresh dependencies to return to the first page.")
                        .font(.footnote).accessibilityIdentifier("graphHistoryWindow")
                }
                ForEach(page.items) { edge in
                    DisclosureGroup("\(edge.from) → \(edge.to)") {
                        LabeledContent("Predicate", value: edge.predicate)
                        LabeledContent("Accepted outcomes", value: edge.outcomes.isEmpty ? "None specified" : edge.outcomes.joined(separator: ", "))
                        LabeledContent("Dependency state", value: edge.state)
                        LabeledContent("Upstream phase", value: edge.upstreamPhase)
                        LabeledContent("Upstream outcome", value: edge.upstreamOutcome ?? "Unavailable")
                        NavigationLink("Open upstream job") { JobDetailView(ref: reference(edge.fromJobId)) }
                        NavigationLink("Open downstream job") { JobDetailView(ref: reference(edge.toJobId)) }
                        NavigationLink("Explore upstream node") { GraphExplorer(workload: workload, initialCenter: edge.fromJobId) }
                        NavigationLink("Explore downstream node") { GraphExplorer(workload: workload, initialCenter: edge.toJobId) }
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
