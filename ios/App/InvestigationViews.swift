import DashboardCore
import SwiftUI

struct LogView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    @State private var stream = "stdout"
    @State private var read = LogReadState()
    @State private var generation = UUID()
    @State private var error: String?
    @State private var following = false
    @State private var loading = false
    @State private var search = ""
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Picker("Stream", selection: $stream) { Text("stdout").tag("stdout"); Text("stderr").tag("stderr") }.pickerStyle(.segmented).disabled(loading)
            HStack {
                Toggle("Follow", isOn: $following).toggleStyle(.switch).disabled(read.requiresRefresh || (read.cursor == nil && read.state == "complete"))
                Button { Task { await load() } } label: { Label("Read next", systemImage: "arrow.clockwise") }.disabled(loading || read.requiresRefresh || (read.cursor == nil && read.state == "complete"))
            }
            Button("Refresh log") { Task { await refresh() } }.disabled(loading).accessibilityIdentifier("refreshLog")
            if read.requiresRefresh { Text("Refresh starts a new verified stream and clears the previously loaded bytes.").font(.footnote) }
            TextField("Search loaded text", text: $search).textFieldStyle(.roundedBorder).autocorrectionDisabled()
            if !search.isEmpty { Text("\(read.buffer.text.components(separatedBy: search).count - 1) matches in loaded text only").font(.caption) }
            Text("Bytes \(read.buffer.startOffset)–\(read.buffer.nextOffset) • \(read.state.isEmpty ? "not yet loaded" : read.state)").font(.caption).accessibilityLabel("Loaded byte range \(read.buffer.startOffset) through \(read.buffer.nextOffset), \(read.state)")
            if read.buffer.evicted { Text("Earlier loaded output was removed to keep the 2 MiB display limit.").font(.caption) }
            if read.truncated { Text("Source output is truncated.").font(.caption) }
            if let error { ErrorMessage(error: error) }
            if loading && read.buffer.byteCount == 0 { ProgressView("Reading verified log bytes…") }
            ScrollView([.vertical, .horizontal]) {
                Text(read.buffer.text.isEmpty ? (read.state == "complete" ? "Empty complete stream" : "No captured output in this range") : read.buffer.text)
                    .font(.system(.body, design: .monospaced)).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                    .accessibilityLabel(read.buffer.text.isEmpty ? "No output in loaded range" : read.buffer.text)
            }
            .simultaneousGesture(DragGesture().onChanged { _ in following = false })
        }.padding().navigationTitle("Logs").navigationBarTitleDisplayMode(.inline)
            .task(id: stream) { await refresh() }
            .task(id: following) {
                while following && !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    if store.active && !Task.isCancelled { await load() }
                }
            }
            .onDisappear { following = false; generation = UUID(); read.reset() }
    }
    private func refresh() async {
        generation = UUID(); read.reset(); error = nil; following = false
        await load()
    }
    private func load() async {
        guard !loading, !read.requiresRefresh else { return }
        loading = true; defer { loading = false }
        let ticket = generation, requestedStream = stream
        do {
            var query: [URLQueryItem] = [.init(name: "stream", value: requestedStream), .init(name: "limitBytes", value: "262144")]
            if let cursor = read.cursor { query.append(.init(name: "cursor", value: cursor)) }
            let chunk: LogChunk = try await store.request(path: APIPath.job(ref) + "/logs", query: query)
            guard generation == ticket, requestedStream == stream else { return }
            try read.accept(chunk, job: ref, stream: requestedStream)
            error = nil
            if read.cursor == nil { following = false }
        } catch is CancellationError {} catch {
            guard generation == ticket, requestedStream == stream else { return }
            read.failed(error); self.error = error.localizedDescription; following = false
        }
    }
}

struct ArtifactsView: View {
    let ref: JobRef
    var body: some View {
        List {
            Section { Text("Published metadata only. File downloads are outside this release.").font(.footnote).foregroundStyle(.secondary) }
            PagedRows<Artifact, ArtifactRow>(path: APIPath.job(ref) + "/artifacts", scoped: false) { ArtifactRow(artifact: $0) }
        }.navigationTitle("Artifacts")
    }
}
private struct ArtifactRow: View {
    let artifact: Artifact
    var body: some View {
        DisclosureGroup(artifact.name) {
            LabeledContent("Recorded bytes", value: artifact.sizeBytes)
            LabeledContent("Run number", value: artifact.runNumber).accessibilityIdentifier("artifactRunNumber")
            LabeledContent("Run ID", value: artifact.runId)
            LabeledContent("Execution ID", value: artifact.executionId).accessibilityIdentifier("artifactExecutionID")
            LabeledContent("Target generation", value: artifact.targetGenerationId)
            LabeledContent("Availability", value: artifact.availability).accessibilityIdentifier("artifactAvailability")
            Text("File bytes have not been verified. Size and checksum are published metadata.").font(.footnote)
            Text("Recorded checksum: \(artifact.checksum)").font(.caption.monospaced()).textSelection(.enabled)
            TimestampRow(label: "Published", value: artifact.publishedAt)
        }
    }
}
