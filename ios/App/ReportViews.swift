import DashboardCore
import SwiftUI

struct ReportsView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    @State private var includeLogs = false
    @State private var runID = ""
    @State private var requestKey = UUID()
    @State private var requesting = false
    @State private var requested: DiagnosisReport?
    @State private var requestError: String?
    @State private var page: DashboardAPI.ReportPage?
    @State private var cursor: String?
    @State private var history: [String?] = []
    @State private var loading = false
    @State private var loadGeneration = UUID()
    @State private var error: String?

    var body: some View {
        List {
            Section("Generate deterministic diagnosis") {
                Text("Use recorded metadata, scheduler, and lifecycle facts. Commands, environment values, source files, and AI-provider calls are excluded from collection.").font(.footnote)
                Toggle("Include bounded redacted log tails", isOn: $includeLogs).disabled(requesting)
                if includeLogs { Text("Requires an operator-configured value-aware redaction policy. Up to 64 KiB per stream is selected; source offsets and omissions are recorded.").font(.footnote) }
                TextField("Optional run UUID (blank = selected history)", text: $runID).textInputAutocapitalization(.never).autocorrectionDisabled().disabled(requesting)
                Button(requesting ? "Requesting…" : "Generate report") { Task { await generate() } }.disabled(requesting)
                if let requested { NavigationLink("Open requested report · \(requested.state)") { ReportDetailView(ref: ref, taskID: requested.taskId) } }
                if let requestError { ErrorMessage(error: requestError) }
            }
            Section("Report history") {
                if let page {
                    if page.items.isEmpty { Text("No report yet. Generate a report to analyze this job’s factual evidence.") }
                    ForEach(page.items) { report in
                        NavigationLink { ReportDetailView(ref: ref, taskID: report.taskId) } label: {
                            VStack(alignment: .leading) {
                                Text("Diagnosis · \(report.state)").font(.headline)
                                if report.outdated { Text("Recorded snapshot is outdated").foregroundStyle(.orange) }
                                Text("Revision \(report.sourceRevision) · \(report.profile)").font(.caption)
                                Text(report.createdAt).font(.caption)
                            }
                        }.accessibilityIdentifier("report-\(report.taskId)")
                    }
                    if !history.isEmpty { Button("Previous report page") { Task { let previous = history.last!; if await load(previous) { history.removeLast() } } }.disabled(loading) }
                    if let next = page.nextCursor { Button("Next report page") { Task { let previous = cursor; if await load(next) { history.append(previous) } } }.disabled(loading) }
                }
                if let error { ErrorMessage(error: error) }
                if loading { ProgressView("Loading report history…") }
                Button("Refresh report history") { Task { if await load() { history = [] } } }.disabled(loading)
            }
        }.navigationTitle("Diagnosis").task(id: store.active) {
            guard store.active else { loadGeneration = UUID(); loading = false; page = nil; requested = nil; return }
            if await load() { history = [] }
        }
            .refreshable { if await load() { history = [] } }
            .onChange(of: includeLogs) { _, _ in changedRequest() }
            .onChange(of: runID) { _, _ in changedRequest() }
    }
    private func changedRequest() { requestKey = UUID(); requested = nil; requestError = nil }
    private func generate() async {
        guard store.active, !requesting else { return }
        let run = runID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard run.isEmpty || UUID(uuidString: run) != nil else { requestError = "Enter the actual run UUID or leave it blank."; return }
        requesting = true; defer { requesting = false }
        let body = DashboardAPI.ReportRequest(profile: includeLogs ? "include_log_tail" : "metadata", runId: run.isEmpty ? nil : run.lowercased())
        do {
            let result: DiagnosisReport = try await store.request(path: APIPath.job(ref) + "/reports", method: "POST", body: JSONEncoder().encode(body), idempotencyKey: requestKey)
            try result.validate(job: ref)
            guard store.active else { throw CancellationError() }
            guard result.profile == body.profile, result.runId == body.runId, result.detail == nil else { throw DashboardError.invalidEvidence }
            requested = result; requestError = nil; requestKey = UUID()
            if await load() { history = [] }
        } catch is CancellationError {} catch {
            loadGeneration = UUID(); loading = false; requested = nil; page = nil; cursor = nil; history = []; requestError = error.localizedDescription
            if let failure = error as? DashboardError, !failure.retainsReportRequestKey { requestKey = UUID() }
        }
    }
    @discardableResult private func load(_ next: String? = nil) async -> Bool {
        guard store.active else { return false }
        let generation = UUID(); loadGeneration = generation; loading = true
        defer { if loadGeneration == generation { loading = false } }
        do {
            var query = [URLQueryItem(name: "limit", value: "20")]
            if let next { query.append(.init(name: "cursor", value: next)) }
            let result: DashboardAPI.ReportPage = try await store.request(path: APIPath.job(ref) + "/reports", query: query)
            try result.validate(job: ref)
            guard store.active, loadGeneration == generation else { throw CancellationError() }
            page = result; cursor = next; error = nil; return true
        } catch is CancellationError { return false } catch {
            guard loadGeneration == generation else { return false }
            page = nil; requested = nil; self.error = error.localizedDescription; return false
        }
    }
}

struct ReportDetailView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    let taskID: String
    @State private var report: DiagnosisReport?
    @State private var expandedFindings: Set<String> = []
    @State private var loading = false
    @State private var loadGeneration = UUID()
    @State private var error: String?
    var body: some View {
        List {
            Section {
                Text("Advice is informational. Dashboard does not execute job actions.").font(.footnote)
                Text("\(ref.deploymentId) / \(ref.namespaceId) / \(ref.jobId)").font(.caption)
                Button("Refresh report") { Task { await load() } }.disabled(loading)
                if loading { ProgressView("Verifying report access…") }
                if let error { ErrorMessage(error: error) }
            }
            if let report {
                Section("Task and snapshot") {
                    LabeledContent("State", value: report.state).accessibilityIdentifier("reportState")
                    Text("Task: \(report.taskId)").font(.caption.monospaced()).textSelection(.enabled)
                    LabeledContent("Source revision", value: report.sourceRevision)
                    LabeledContent("Disclosure profile", value: report.profile)
                    if let run = report.runId { Text("Selected run: \(run)").font(.caption) }
                    TimestampRow(label: "Requested", value: report.createdAt)
                    TimestampRow(label: "Cache expires", value: report.expiresAt)
                    if report.outdated { Text("This report describes its recorded snapshot. The source or collection policy has changed.").foregroundStyle(.orange).accessibilityIdentifier("reportOutdated") }
                    if let failure = report.failureCode { Text("Analysis failed: \(failure). Refresh or request a new report after resolving the cause.") }
                }
                if let detail = report.detail {
                    ReportContents(ref: ref, report: report, detail: detail, expandedFindings: $expandedFindings) { failure in self.report = nil; self.error = failure }
                } else if report.pending { Section { Text("Analysis is pending. This view checks for completion every five seconds.") } }
                else if report.state == "ready" { Section { Text("Verified report detail is unavailable. Refresh to retrieve it.") } }
            }
        }.navigationTitle("Diagnosis report")
            .task(id: store.active) {
                guard store.active else { loadGeneration = UUID(); loading = false; report = nil; return }
                repeat {
                    await load()
                    guard store.active, report?.pending == true, !Task.isCancelled else { return }
                    do { try await Task.sleep(for: .seconds(5)) } catch { return }
                } while !Task.isCancelled
            }
            .refreshable { await load() }
    }
    private func load() async {
        guard store.active else { return }
        let generation = UUID(); loadGeneration = generation; loading = true
        defer { if loadGeneration == generation { loading = false } }
        do {
            let result: DiagnosisReport = try await store.request(path: APIPath.job(ref) + "/reports/\(APIPath.component(taskID))")
            try result.validate(job: ref, task: taskID)
            guard store.active, loadGeneration == generation else { throw CancellationError() }
            report = result; error = nil
        } catch is CancellationError {} catch {
            guard loadGeneration == generation else { return }
            report = nil; self.error = error.localizedDescription
        }
    }
}

private struct ReportContents: View {
    let ref: JobRef
    let report: DiagnosisReport
    let detail: DashboardAPI.ReportDetail
    @Binding var expandedFindings: Set<String>
    let onCitationFailure: (String) -> Void
    var body: some View {
        Section("Findings") {
            Text("Confidence scores are analyzer output, not calibrated probabilities.").font(.footnote)
            if detail.mode != "deterministic" { Label("This report contains generated hypotheses (\(detail.mode)).", systemImage: "exclamationmark.bubble") }
            ForEach(detail.findings) { finding in
                DisclosureGroup(finding.title, isExpanded: Binding(get: { expandedFindings.contains(finding.id) }, set: { expanded in
                    if expanded { expandedFindings.insert(finding.id) } else { expandedFindings.remove(finding.id) }
                })) {
                    LabeledContent("Severity / category", value: "\(finding.severity) / \(finding.category)")
                    if finding.id == detail.primaryFindingId { Text("Primary finding").font(.caption.bold()) }
                    Text(finding.explanation)
                    confidence(finding.confidence)
                    evidence("Supporting evidence", finding.supportingEvidence)
                    evidence("Contradicting evidence", finding.contradictingEvidence)
                    ForEach(finding.contradictingFindings, id: \.self) { id in Text("Contradicting finding: \(detail.findings.first { $0.id == id }?.title ?? id)") }
                    Text("Analyzer: \(finding.analyzer) · \(finding.code)").font(.caption)
                }
            }
        }
        Section("Suggested actions (text only)") {
            ForEach(detail.actions) { action in
                DisclosureGroup(action.summary) {
                    Text(action.description); Text("Kind: \(action.kind)").font(.caption)
                    if action.requiresConfirmation { Text("This advice requires the appropriate operator’s confirmation.").font(.caption) }
                    evidence("Supporting evidence", action.supportingEvidence)
                }
            }
        }
        Section("Retry advice") {
            LabeledContent("Verdict", value: detail.retry.verdict)
            LabeledContent("Existing policy", value: detail.retry.existingPolicy)
            Text(detail.retry.rationale); confidence(detail.retry.confidence)
            ForEach(detail.retry.reasons, id: \.self) { Text($0) }
            if let earliest = detail.retry.earliestAt { TimestampRow(label: "Earliest", value: earliest) }
            evidence("Supporting evidence", detail.retry.supportingEvidence)
        }
        Section("Missing evidence and limitations") {
            if detail.missingEvidence.isEmpty { Text("No additional missing evidence identified by this report.") }
            ForEach(Array(detail.missingEvidence.enumerated()), id: \.offset) { _, value in Text("\(value.description) (\(value.code))") }
            ForEach(Array(detail.warnings.enumerated()), id: \.offset) { _, value in Text("\(value.message) (\(value.code))").foregroundStyle(.orange) }
            DisclosureGroup("Collection omissions (\(detail.omissions.count))") { ForEach(Array(detail.omissions.enumerated()), id: \.offset) { _, value in Text("\(value.code): \(value.affects.joined(separator: ", "))").font(.caption) } }
            DisclosureGroup("Redaction notices (\(detail.redactionNotices.count))") { ForEach(Array(detail.redactionNotices.enumerated()), id: \.offset) { _, value in Text("\(value.code) · \(value.count): \(value.affects.joined(separator: ", "))").font(.caption) } }
        }
        Section("Immutable provenance") {
            Text("Report: \(report.reportId ?? "Unavailable")").textSelection(.enabled).font(.caption.monospaced())
            Text("Core evidence: \(report.evidenceId ?? "Unavailable")").textSelection(.enabled).font(.caption.monospaced())
            Text("Analysis evidence: \(report.analysisEvidenceId ?? "Unavailable")").textSelection(.enabled).font(.caption.monospaced())
            TimestampRow(label: "Captured", value: detail.capturedAt); TimestampRow(label: "Generated", value: detail.generatedAt)
            LabeledContent("Phase / outcome", value: "\(detail.phase) / \(detail.outcome ?? "Unavailable")")
            LabeledContent("Control instance", value: detail.controlInstanceId)
            LabeledContent("Control / contract", value: "\(detail.controlVersion) / \(detail.contractVersion)")
            LabeledContent("Collection platform", value: detail.platform)
            ForEach(detail.runs, id: \.id) { run in Text("Run \(run.number) · \(run.id)\nExecution: \(run.executionId ?? "Unavailable")").font(.caption) }
            DisclosureGroup("Component versions") {
                Text("Core \(detail.versions.jobman) · collector \(detail.versions.collector)")
                Text("Companion \(detail.versions.companion) · engine \(detail.versions.engine)")
                Text("Evidence schema \(detail.versions.evidenceSchema) · report schema \(detail.versions.reportSchema)")
                Text("Generation schema \(detail.versions.generationSchema) · proposal schema \(detail.versions.proposalSchema)")
                ForEach(Array(detail.analyzers.enumerated()), id: \.offset) { _, analyzer in Text("\(analyzer.name) · \(analyzer.version)") }
            }
            DisclosureGroup("Provider disclosure") {
                Text("Invoked: \(detail.disclosure.providerInvoked ? "Yes" : "No"); generated content used: \(detail.disclosure.generatedContentUsed ? "Yes" : "No")")
                Text("Locality: \(detail.disclosure.locality); profile: \(detail.disclosure.profile ?? "None")")
                if let provider = detail.disclosure.provider { Text("\(provider) / \(detail.disclosure.model ?? "Unknown model")") }
                if let requestID = detail.disclosure.requestId { Text("Request ID: \(requestID)") }
                Text("Classes: \(detail.disclosure.classes.joined(separator: ", "))")
                Text("Items: \(detail.disclosure.itemCount); artifacts: \(detail.disclosure.artifactCount); enrichment: \(detail.disclosure.enrichmentCount)")
                Text("Artifact bytes: \(detail.disclosure.artifactBytes); enrichment bytes: \(detail.disclosure.enrichmentBytes); request bytes: \(detail.disclosure.requestBytes); redaction notices: \(detail.disclosure.redactionNoticeCount)")
                ForEach(detail.disclosure.itemIds + detail.disclosure.artifactIds + detail.disclosure.enrichmentIds, id: \.self) { Text($0).font(.caption) }
                ForEach(Array(detail.generators.enumerated()), id: \.offset) { _, generator in Text("\(generator.provider) / \(generator.model) / \(generator.profile) / \(generator.locality)") }
            }
        }
    }
    @ViewBuilder private func confidence(_ value: DashboardAPI.ReportConfidence) -> some View {
        Text("Confidence: \(value.score)/100 (\(value.band))").font(.subheadline)
        Text("Basis: \(value.basis)")
    }
    @ViewBuilder private func evidence(_ label: String, _ ids: [String]) -> some View {
        if !ids.isEmpty {
            Text(label).font(.subheadline.bold())
            ForEach(ids, id: \.self) { id in
                if let citation = detail.citations.first(where: { $0.id == id }) {
                    NavigationLink(citation.label) { ReportCitationView(ref: ref, report: report, reference: citation, onFailure: onCitationFailure) }.buttonStyle(.borderless).accessibilityIdentifier("citation-\(id)")
                }
            }
        }
    }
}

private struct ReportCitationView: View {
    @Environment(DashboardStore.self) private var store
    let ref: JobRef
    let report: DiagnosisReport
    let reference: DashboardAPI.CitationRef
    let onFailure: (String) -> Void
    @State private var citation: DashboardAPI.Citation?
    @State private var bytes: Data?
    @State private var error: String?
    var body: some View {
        List {
            Section("Exact sealed evidence") {
                Text(reference.label).font(.headline)
                Text("Report: \(report.reportId ?? "Unavailable")").font(.caption).textSelection(.enabled)
                Text("Citation: \(reference.id)").font(.caption).textSelection(.enabled)
                if let citation {
                    Text("\(citation.kind) · \(citation.quality) · \(citation.disclosure)").font(.caption)
                    if let value = citation.valueJSON { Text(value).font(.system(.body, design: .monospaced)).textSelection(.enabled).accessibilityIdentifier("citationValue") }
                    if let bytes {
                        Text("Sanitized artifact offsets \(citation.startOffset ?? "?")–\(citation.endOffset ?? "?")").font(.caption)
                        if citation.originalOffsetsExact { Text("Original selected offsets \(citation.originalStartOffset ?? "?")–\(citation.originalEndOffset ?? "?")").font(.caption) }
                        else { Text("Redaction changed byte lengths. Original source offsets cannot be mapped exactly.").font(.caption) }
                        Text(String(decoding: bytes, as: UTF8.self)).font(.system(.body, design: .monospaced)).textSelection(.enabled).accessibilityIdentifier("citationBytes")
                        if String(data: bytes, encoding: .utf8) == nil { Text("Invalid UTF-8 is displayed with replacement characters. Exact bytes remain available below.").font(.caption) }
                        DisclosureGroup("Exact base64 bytes") { Text(citation.bytesBase64 ?? "").font(.caption.monospaced()).textSelection(.enabled) }
                        Text("Run \(citation.runNumber ?? "?") · \(citation.runId ?? "?")\nExecution \(citation.executionId ?? "?") · \(citation.stream ?? "?")").font(.caption)
                    }
                    if let entity = citation.sourceEntityId { Text("Source entity: \(entity)").font(.caption) }
                    if let revision = citation.sourceRevision { Text("Source revision: \(revision)").font(.caption) }
                    TimestampRow(label: "Captured", value: citation.capturedAt); TimestampRow(label: "Observed", value: citation.observedAt)
                } else if let error { ErrorMessage(error: error) }
                else { ProgressView("Reauthorizing and resolving sealed citation…") }
                Text("This evidence is read from the report’s immutable stored pair. Current log contents are not consulted.").font(.footnote)
            }
        }.navigationTitle("Citation").task(id: store.active) {
            guard store.active else { citation = nil; bytes = nil; return }
            do {
                let result: DashboardAPI.Citation = try await store.request(path: APIPath.job(ref) + "/reports/\(APIPath.component(report.taskId))/citations/\(APIPath.component(reference.id))")
                let data = try result.validatedBytes(report: report, reference: reference)
                guard store.active else { throw CancellationError() }
                citation = result; bytes = data; error = nil
            } catch is CancellationError {} catch { citation = nil; bytes = nil; self.error = error.localizedDescription; onFailure(error.localizedDescription) }
        }
    }
}
