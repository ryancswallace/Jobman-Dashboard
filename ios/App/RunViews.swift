import DashboardCore
import SwiftUI

struct RunPickerView: View {
    @Environment(DashboardStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let ref: JobRef
    @Binding var selection: DashboardAPI.JobRun?
    @State private var page: DashboardAPI.RunPage?
    @State private var cursor: String?
    @State private var history = InboxPageHistory()
    @State private var requests = ReadRequestGate()
    @State private var error: String?
    var body: some View {
        List {
            Section {
                Button("Use current run / all artifact metadata") { selection = nil; dismiss() }
                Text("A selected run pins logs, artifact metadata and new diagnosis requests to its actual source run. Created and updated dates describe run metadata, not execution start or completion.").font(.footnote)
            }
            if let page {
                Section("\(page.total) recorded runs") {
                    ForEach(page.items, id: \.id) { run in
                        Button {
                            selection = run; dismiss()
                        } label: {
                            VStack(alignment: .leading, spacing: 5) {
                                Text("Run \(run.number)").font(.headline)
                                Text("\(run.phase) · \(run.outcome ?? "No outcome")")
                                Text(run.id).font(.caption)
                                if let execution = run.executionId { Text("Execution \(execution)").font(.caption) }
                                Text("Metadata updated \(run.updatedAt)").font(.caption)
                            }
                        }.accessibilityIdentifier("selectRun-\(run.number)")
                    }
                    if page.items.isEmpty { Text("No recorded runs") }
                    Text("Fetched \(page.fetchedAt)").font(.caption)
                    if page.completeness != "complete" { Text("Run results are partial.").foregroundStyle(.orange) }
                }
                Section {
                    if history.canGoBack { Button("Previous run page") { Task { var previous = history; let prior = previous.previous(); if await load(prior) { history = previous } } }.disabled(requests.busy) }
                    if let next = page.nextCursor { Button("Next run page") { Task { let prior = cursor; if await load(next) { history.record(prior) } } }.disabled(requests.busy) }
                    if history.discardedPages > 0 { Text("Earlier back-page cursors were discarded; restart remains available.").font(.caption) }
                }
            }
            if let error { ErrorMessage(error: error) }
            if requests.busy { ProgressView("Loading recorded runs…") }
            Button("Restart run history") { Task { if await load() { history.reset() } } }.disabled(requests.busy)
        }.navigationTitle("Choose run").task(id: store.foregroundGeneration) { await load(cursor, replacing: true) }
    }
    @discardableResult private func load(_ next: String? = nil, replacing: Bool = false) async -> Bool {
        guard let token = requests.begin(replacing: replacing) else { return false }; defer { requests.finish(token) }
        do {
            var query = [URLQueryItem(name: "limit", value: "50")]
            if let next { query.append(.init(name: "cursor", value: next)) }
            let value: DashboardAPI.RunPage = try await store.request(path: APIPath.job(ref) + "/runs", query: query)
            guard requests.accepts(token) else { return false }
            try value.validate(job: ref)
            page = value; cursor = next; error = nil; return true
        } catch is CancellationError { return false } catch {
            if requests.accepts(token) { page = nil; self.error = error.localizedDescription }
            return false
        }
    }
}
