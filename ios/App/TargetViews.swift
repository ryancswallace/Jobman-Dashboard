import DashboardCore
import SwiftUI

struct TargetsView: View {
    var body: some View {
        List {
            Section { ScopeMenu(); Text("Configured state describes policy, not agent connectivity or health. Capacity monitoring comes later.").font(.footnote)
                Text("Grouped by source; newest targets first within each source.").font(.caption)
            }
            TargetCatalogRows()
        }.navigationTitle("Targets")
    }
}
private struct TargetCatalogRows: View {
    @Environment(DashboardStore.self) private var store
    @State private var page: Page<Target>?
    @State private var cursor: String?
    @State private var history: [String?] = []
    @State private var error: String?
    @State private var requests = ReadRequestGate()
    private var loading: Bool { requests.busy }
    var body: some View {
        Section {
            if let page {
                Text("\(page.total ?? "Unavailable") targets\(page.completeness == "partial" ? " in available sources (subtotal)" : "")").font(.caption)
                ForEach(page.items) { target in
                    NavigationLink { TargetDetailView(selection: target) } label: {
                        VStack(alignment: .leading) { Text(target.name).font(.headline); Text("\(target.deploymentId) / \(target.namespaceId)").font(.caption)
                            Text("\(target.state) · \(target.generation.executionBackend) · generation \(target.generation.number)").font(.caption)
                        }
                    }
                }
                if !history.isEmpty { Button("Previous target page") { Task { let prior = history.last!; if await load(prior) { history.removeLast() } } }.disabled(loading) }
                if let next = page.nextCursor { Button("Next target page") { Task { let prior = cursor; if await load(next) { history.append(prior) } } }.disabled(loading) }
                DisclosureGroup("Source totals and observations") { ForEach(Array((page.totals ?? []).enumerated()), id: \.offset) { _, total in Text("\(total.deploymentId) / \(total.namespaceId): \(total.total) · \(total.asOf)").font(.caption) } }
                SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt)
            }
            if let error { ErrorMessage(error: error) }
            if loading { ProgressView("Loading targets…") }
            Button("Refresh targets") { Task { if await load() { history = [] } } }.disabled(loading)
        }.task(id: store.foregroundGeneration) { await load(cursor, replacing: true) }
    }
    @discardableResult private func load(_ next: String? = nil, replacing: Bool = false) async -> Bool {
        guard let token = requests.begin(replacing: replacing) else { return false }; defer { requests.finish(token) }
        do {
            var query = try store.query(); query.append(.init(name: "limit", value: "50")); if let next { query.append(.init(name: "cursor", value: next)) }
            let result: Page<Target> = try await store.request(path: "/api/v1/targets", query: query)
            guard requests.accepts(token) else { return false }
            guard result.total != nil, result.totals != nil, result.items.count <= 200, Set(result.items.map(\.id)).count == result.items.count else { throw DashboardError.invalidResponse }
            try result.items.forEach { try $0.validate() }
            page = result; cursor = next; error = nil; return true
        } catch is CancellationError { return false } catch { if requests.accepts(token) { self.error = error.localizedDescription }; return false }
    }
}
struct TargetDetailView: View {
    @Environment(DashboardStore.self) private var store
    let selection: Target
    @State private var detail: TargetDetail?
    @State private var error: String?
    @State private var requests = ReadRequestGate()
    private var loading: Bool { requests.busy }
    @State private var refreshID = UUID()
    var body: some View {
        List {
            Section { Text("\(selection.deploymentId) / \(selection.namespaceId)"); Text("Configuration describes policy, not agent health or capacity.").font(.footnote)
                Button("Refresh target") { Task { await load() } }.disabled(loading).accessibilityIdentifier("refreshTarget")
            }
            if let error { ErrorMessage(error: error) }
            if loading { ProgressView("Loading target…") }
            if let detail {
                let target = detail.target
                Section("Configuration") {
                    LabeledContent("Target ID", value: target.targetId)
                    LabeledContent("Kind / state", value: "\(target.kind) / \(target.state)")
                    LabeledContent("Revision", value: target.revision)
                    LabeledContent("Generation", value: target.generation.number).accessibilityIdentifier("targetGeneration")
                    LabeledContent("Generation ID", value: target.generation.id)
                    LabeledContent("Backend", value: target.generation.executionBackend)
                    LabeledContent("Transport", value: target.generation.transport)
                    LabeledContent("Provider", value: target.generation.provider.kind)
                    if let region = target.generation.provider.region {
                        LabeledContent("Region", value: String(region.prefix(512)))
                        if region.count > 512 { Text("Display truncated after 512 characters; the source value is preserved.").font(.caption) }
                    }
                    if let cluster = target.generation.provider.clusterName { LabeledContent("Cluster", value: cluster) }
                    targetSet("Runtimes", target.generation.runtimes)
                    targetSet("Operating systems", target.generation.operatingSystems)
                    targetSet("Architectures", target.generation.architectures)
                    targetSet("Capabilities", target.generation.capabilities)
                    LabeledContent("Log store", value: target.generation.logStore.map { "\($0.name) · version \($0.version)" } ?? "None configured")
                    DisclosureGroup("Artifact stores (\(target.generation.artifactStores.count))") { ForEach(target.generation.artifactStores, id: \.name) { store in Text("\(store.name) · version \(store.version)") } }
                    LabeledContent("Created", value: target.createdAt); LabeledContent("Updated", value: target.updatedAt)
                    LabeledContent("Source observed", value: target.asOf)
                }
                Section("Partitions") {
                    Text("\(target.generation.partitions.count) of \(target.generation.partitionCount) partitions in preview\(target.generation.partitionsTruncated ? "; preview truncated" : "")").accessibilityIdentifier("targetPartitionPreview")
                    DisclosureGroup("Partition preview") { ForEach(target.generation.partitions, id: \.name) { partition in Text(partition.name + (partition.isDefault ? " (default)" : "")) } }
                    NavigationLink("Browse all partitions") { TargetPartitionsView(target: target) }.id(refreshID)
                }
                SourceSummary(completeness: detail.completeness, sources: detail.sources, fetchedAt: detail.fetchedAt)
            }
        }.navigationTitle(detail?.target.name ?? selection.name).task(id: store.foregroundGeneration) { await load(replacing: true) }.refreshable { await load() }
    }
    private func targetSet(_ title: String, _ values: [String]) -> some View { DisclosureGroup("\(title) (\(values.count))") { ForEach(values, id: \.self) { Text($0) }; if values.isEmpty { Text("None advertised") } } }
    private func load(replacing: Bool = false) async {
        guard let token = requests.begin(replacing: replacing) else { return }; defer { requests.finish(token) }
        do { let result: TargetDetail = try await store.request(path: selection.path); guard requests.accepts(token) else { return }; try result.validate(expected: selection); detail = result; refreshID = UUID(); error = nil }
        catch is CancellationError {} catch { if requests.accepts(token) { self.error = error.localizedDescription } }
    }
}
private struct TargetPartitionsView: View {
    @Environment(DashboardStore.self) private var store
    @State var target: Target
    @State private var page: TargetPartitionPage?
    @State private var cursor: String?
    @State private var history: [String?] = []
    @State private var requiresRefresh = false
    @State private var error: String?
    @State private var requests = ReadRequestGate()
    private var loading: Bool { requests.busy }
    var body: some View {
        List {
            Section { Text("\(target.deploymentId) / \(target.namespaceId)"); Text("Generation \(target.generation.number)").font(.caption); Text("Each page verifies current access and target generation.").font(.footnote) }
            if let page, !requiresRefresh {
                Section("\(page.items.count) on this page · \(page.total) partitions") {
                    ForEach(page.items, id: \.name) { partition in Text(partition.name + (partition.isDefault ? " (default)" : "")) }
                    if !history.isEmpty { Button("Previous partition page") { Task { let prior = history.last!; if await load(prior) { history.removeLast() } } }.disabled(loading) }
                    if let next = page.nextCursor { Button("Next partition page") { Task { let prior = cursor; if await load(next) { history.append(prior) } } }.disabled(loading) }
                }
                SourceSummary(completeness: page.completeness, sources: page.sources, fetchedAt: page.fetchedAt)
            }
            if let error { ErrorMessage(error: error) }
            if loading { ProgressView("Loading partitions…") }
            Button("Refresh target and restart partitions") { Task { await restart() } }.disabled(loading).accessibilityIdentifier("restartTargetPartitions")
        }.navigationTitle("Partitions").task(id: store.foregroundGeneration) { await load(cursor, replacing: true) }
    }
    private func restart() async {
        guard let token = requests.begin() else { return }; defer { requests.finish(token) }
        do { let result: TargetDetail = try await store.request(path: target.path); guard requests.accepts(token) else { return }; try result.validate(expected: target); target = result.target; page = nil; cursor = nil; history = []; requiresRefresh = false; error = nil; requests.finish(token); await load() }
        catch is CancellationError {} catch { if requests.accepts(token) { self.error = error.localizedDescription } }
    }
    @discardableResult private func load(_ next: String? = nil, replacing: Bool = false) async -> Bool {
        guard !requiresRefresh, let token = requests.begin(replacing: replacing) else { return false }; defer { requests.finish(token) }
        do { var query: [URLQueryItem] = [.init(name: "generationId", value: target.generation.id), .init(name: "limit", value: "50")]; if let next { query.append(.init(name: "cursor", value: next)) }
            let result: TargetPartitionPage = try await store.request(path: target.path + "/partitions", query: query); guard requests.accepts(token) else { return false }; try result.validate(target: target); page = result; cursor = next; error = nil; return true
        } catch is CancellationError { return false } catch { guard requests.accepts(token) else { return false }; if let e = error as? DashboardError, [.targetChanged, .cursorExpired].contains(e) { requiresRefresh = true; page = nil }; self.error = error.localizedDescription; return false }
    }
}
