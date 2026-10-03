import DashboardCore
import SwiftUI

struct LogView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    @State private var stream = "stdout"
    @State private var buffer = LogBuffer()
    @State private var cursor: String?
    @State private var state = ""
    @State private var error: String?
    @State private var following = false
    @State private var loading = false
    @State private var search = ""
    @State private var sourceTruncated = false
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Picker("Stream", selection: $stream) { Text("stdout").tag("stdout"); Text("stderr").tag("stderr") }.pickerStyle(.segmented)
            HStack {
                Toggle("Follow", isOn: $following).toggleStyle(.switch)
                Button { Task { await load() } } label: { Label("Read next", systemImage: "arrow.clockwise") }.disabled(loading)
            }
            TextField("Search loaded text", text: $search).textFieldStyle(.roundedBorder).autocorrectionDisabled()
            if !search.isEmpty { Text("\(buffer.text.components(separatedBy: search).count - 1) matches in loaded text only").font(.caption) }
            Text("Bytes \(buffer.startOffset)–\(buffer.nextOffset) • \(state.isEmpty ? "not yet loaded" : state)").font(.caption).accessibilityLabel("Loaded byte range \(buffer.startOffset) through \(buffer.nextOffset), \(state)")
            if buffer.evicted { Text("Earlier loaded output was removed to keep the 2 MiB display limit.").font(.caption) }
            if sourceTruncated { Text("Source output is truncated.").font(.caption) }
            if let error { ErrorMessage(error: error) }
            if loading && buffer.byteCount == 0 { ProgressView("Reading verified log bytes…") }
            ScrollView([.vertical, .horizontal]) {
                Text(buffer.text.isEmpty ? (state == "complete" ? "Empty complete stream" : "No captured output in this range") : buffer.text)
                    .font(.system(.body, design: .monospaced)).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                    .accessibilityLabel(buffer.text.isEmpty ? "No output in loaded range" : buffer.text)
            }
            .simultaneousGesture(DragGesture().onChanged { _ in following = false })
        }.padding().navigationTitle("Logs").navigationBarTitleDisplayMode(.inline)
            .task(id: stream) { buffer.clear(); cursor = nil; error = nil; await load() }
            .task(id: following) {
                while following && !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    if store.active && !Task.isCancelled { await load() }
                }
            }
            .onDisappear { following = false; buffer.clear() }
    }
    private func load() async {
        guard !loading else { return }; loading = true; defer { loading = false }
        do {
            var query: [URLQueryItem] = [.init(name: "stream", value: stream), .init(name: "limitBytes", value: "262144")]
            if let cursor { query.append(.init(name: "cursor", value: cursor)) }
            let chunk: LogChunk = try await store.request(path: APIPath.job(ref) + "/logs", query: query)
            guard let bytes = chunk.bytesBase64, let offset = UInt64(chunk.startOffset), let end = UInt64(chunk.endOffset),
                  let data = Data(base64Encoded: bytes), end >= offset, end - offset == UInt64(data.count),
                  chunk.stream == stream else { throw DashboardError.invalidResponse }
            let identity = [ref.deploymentId, ref.namespaceId, ref.jobId, chunk.runId ?? "unavailable", chunk.executionId ?? "unavailable", stream].joined(separator: "/")
            try buffer.append(base64: bytes, streamIdentity: identity, offset: offset)
            cursor = chunk.nextCursor; state = chunk.state; sourceTruncated = chunk.truncated ?? false; error = nil
            if cursor == nil { following = false }
        } catch is CancellationError {} catch { self.error = error.localizedDescription; following = false }
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
            LabeledContent("Bytes", value: artifact.sizeBytes)
            LabeledContent("Availability", value: artifact.availability)
            Text(artifact.checksum ?? "Checksum unavailable").font(.caption.monospaced()).textSelection(.enabled)
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
