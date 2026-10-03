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

struct ReportsView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    @State private var includeLogs = false
    @State private var requested = false
    @State private var requestError: String?
    @State private var reload = UUID()
    @State private var requestKey = UUID()
    var body: some View {
        List {
            Section("Generate deterministic diagnosis") {
                Text("Collect recorded metadata, scheduler, and lifecycle facts. Commands, environment, source files, and AI providers are excluded.").font(.footnote)
                Toggle("Include bounded log tails", isOn: $includeLogs)
                Button("Generate report") { Task { await generate() } }.disabled(requested)
                if requested { Text("Request accepted. Refresh the report list to follow analysis.").font(.footnote) }
                if let requestError { ErrorMessage(error: requestError) }
            }
            PagedRows<DiagnosisReport, ReportRow>(path: APIPath.job(ref) + "/reports", scoped: false) { ReportRow(ref: ref, report: $0) }.id(reload)
        }.navigationTitle("Diagnosis").refreshable { reload = UUID(); requested = false }
    }
    private func generate() async {
        do {
            struct Request: Encodable { let includeLogTail: Bool }
            struct Accepted: Decodable, Sendable { let id: String?; let taskId: String?; let state: String? }
            let _: Accepted = try await store.request(path: APIPath.job(ref) + "/reports", method: "POST", body: JSONEncoder().encode(Request(includeLogTail: includeLogs)), idempotencyKey: requestKey)
            requested = true; requestError = nil; reload = UUID()
        } catch { requestError = error.localizedDescription }
    }
}

private struct ReportRow: View {
    let ref: JobRef
    let report: DiagnosisReport
    var body: some View {
        NavigationLink { ReportDetailView(ref: ref, report: report) } label: {
            VStack(alignment: .leading) { Text("Diagnosis \(report.state)").font(.headline); Text(report.id).font(.caption); if let message = report.message { Text(message).font(.caption) } }
        }
    }
}

private struct ReportDetailView: View {
    let ref: JobRef
    let report: DiagnosisReport
    var body: some View {
        List {
            Section("Snapshot provenance") {
                LabeledContent("State", value: report.state)
                LabeledContent("Source revision", value: report.sourceRevision ?? "Unavailable")
                LabeledContent("Engine", value: report.engineVersion ?? "Unavailable")
                Text("Evidence: \(report.evidenceId ?? "Unavailable")").textSelection(.enabled)
                Text("Disclosure: \(report.disclosure ?? "Unavailable")")
                TimestampRow(label: "Generated", value: report.createdAt)
            }
            ForEach(report.findings) { finding in
                Section(finding.title) {
                    LabeledContent("Severity", value: finding.severity)
                    if finding.generated == true { Label("Generated hypothesis", systemImage: "exclamationmark.bubble") }
                    Text(finding.explanation)
                    if let confidence = finding.confidence { Text("Analyzer confidence: \(confidence). This is not a calibrated probability.").font(.footnote) }
                    if let basis = finding.confidenceBasis { Text(basis) }
                    ForEach(finding.citations) { citation in
                        NavigationLink(citation.label) { CitationView(ref: ref, reportID: report.id, citationID: citation.id) }
                    }
                    ForEach(finding.suggestions ?? [], id: \.self) { Text($0) }
                }
            }
            if !report.missingEvidence.isEmpty { Section("Missing evidence") { ForEach(report.missingEvidence, id: \.self) { Text($0) } } }
            if let warnings = report.warnings { Section("Warnings") { ForEach(warnings, id: \.self) { Text($0) } } }
            if let retry = report.retryAdvice { Section("Retry advice") { Text(retry) } }
            Section { Text("Advice is informational. Dashboard does not execute job actions.").font(.footnote) }
        }.navigationTitle("Report")
    }
}

private struct CitationView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    let reportID: String
    let citationID: String
    @State private var citation: DiagnosisReport.Citation?
    @State private var error: String?
    var body: some View {
        List {
            Section("Exact report evidence") {
                Text("Report: \(reportID)").textSelection(.enabled)
                Text("\(ref.deploymentId) / \(ref.namespaceId) / \(ref.jobId)").font(.caption)
                if let citation {
                    Text(citation.label).font(.headline)
                    if let start = citation.startOffset, let end = citation.endOffset { Text("Original bytes \(start)–\(end)").font(.caption) }
                    Text(citation.text ?? "No text in this evidence item").font(.system(.body, design: .monospaced)).textSelection(.enabled)
                } else if let error { ErrorMessage(error: error) }
                else { ProgressView("Validating access to cited evidence…") }
            }
        }.navigationTitle("Citation").task {
            do {
                let result: DiagnosisReport.Citation = try await store.request(path: APIPath.job(ref) + "/reports/\(APIPath.component(reportID))/citations/\(APIPath.component(citationID))")
                guard result.id == citationID else { throw DashboardError.invalidEvidence }
                citation = result
            } catch { self.error = error.localizedDescription }
        }
    }
}
