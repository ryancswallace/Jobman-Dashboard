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

    init(ref: JobRef, selectedRunID: String? = nil) { self.ref = ref; _runID = State(initialValue: selectedRunID ?? "") }

    var body: some View {
        List {
            Section("Create a diagnosis report") {
                Text("Analyze recorded job details, scheduler observations and execution times using fixed rules. No AI service is called. Commands, environment values and source files are not collected.").font(.footnote)
                Toggle("Include recent logs with redaction rules applied", isOn: $includeLogs).disabled(requesting)
                if includeLogs { Text("An administrator must configure rules for removing sensitive text. Includes up to 64 KiB from the end of each log, with the original positions and any omissions recorded.").font(.footnote) }
                Text("Leave the run ID blank to let the source select the job’s recorded run. Report history below includes all runs.").font(.footnote).foregroundStyle(.secondary)
                TextField("Run ID (optional)", text: $runID).textInputAutocapitalization(.never).autocorrectionDisabled().disabled(requesting)
                Button(requesting ? "Requesting…" : "Generate report") { Task { await generate() } }.disabled(requesting)
                if let requested { NavigationLink("Open requested report · \(requested.state)") { ReportDetailView(ref: ref, taskID: requested.taskId) } }
                if let requestError { ErrorMessage(error: requestError) }
            }
            Section("Report history") {
                if let page {
                    if page.items.isEmpty { Text("No reports yet. Generate one to analyze the recorded information about this job.") }
                    ForEach(page.items) { report in
                        NavigationLink { ReportDetailView(ref: ref, taskID: report.taskId) } label: {
                            VStack(alignment: .leading) {
                                Text("Diagnosis · \(report.state)").font(.headline)
                                if report.outdated { Text("Job details or analysis settings have changed").foregroundStyle(.orange) }
                                Text("Job version \(report.sourceRevision) · \(InterfaceText.reportProfile(report.profile))").font(.caption)
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
        guard run.isEmpty || UUID(uuidString: run) != nil else { requestError = "Enter a full run ID or leave it blank."; return }
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
                Text("These are suggestions for you to review. Dashboard will not run commands or change the job.").font(.footnote)
                Text("\(ref.deploymentId) / \(ref.namespaceId) / \(ref.jobId)").font(.caption)
                Button("Refresh report") { Task { await load() } }.disabled(loading)
                if loading { ProgressView("Verifying report access…") }
                if let error { ErrorMessage(error: error) }
            }
            if let report {
                Section("Report request and source details") {
                    LabeledContent("Status", value: report.state).accessibilityIdentifier("reportState")
                    Text("Request ID: \(report.taskId)").font(.caption.monospaced()).textSelection(.enabled)
                    LabeledContent("Source record version", value: report.sourceRevision)
                    LabeledContent("Information included", value: InterfaceText.reportProfile(report.profile))
                    if let run = report.runId { Text("Selected run: \(run)").font(.caption) }
                    TimestampRow(label: "Requested", value: report.createdAt)
                    TimestampRow(label: "Saved report expires", value: report.expiresAt)
                    if report.outdated { Text("This report uses earlier recorded information. The job source or analysis settings have changed since then.").foregroundStyle(.orange).accessibilityIdentifier("reportOutdated") }
                    if let failure = report.failureCode { Text("The report could not be created (\(failure)). Try again, or contact your administrator with this code.") }
                }
                if let detail = report.detail {
                    ReportContents(ref: ref, report: report, detail: detail, expandedFindings: $expandedFindings) { failure in self.report = nil; self.error = failure }
                } else if report.pending { Section { Text("The report is being prepared. This screen checks for completion every five seconds.") } }
                else if report.state == "ready" { Section { Text("The report is ready, but its details have not loaded. Refresh to try again.") } }
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
            Text("Confidence scores describe how strongly the analysis supports a finding. They are not the probability that it is correct.").font(.footnote)
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
        Section("Suggested next steps") {
            ForEach(detail.actions) { action in
                DisclosureGroup(action.summary) {
                    Text(action.description); Text("Kind: \(action.kind)").font(.caption)
                    if action.requiresConfirmation { Text("Check with the responsible administrator before taking this action.").font(.caption) }
                    evidence("Supporting evidence", action.supportingEvidence)
                }
            }
        }
        Section("Retry advice") {
            LabeledContent("Recommendation", value: detail.retry.verdict)
            LabeledContent("Current retry policy", value: detail.retry.existingPolicy)
            Text(detail.retry.rationale); confidence(detail.retry.confidence)
            ForEach(detail.retry.reasons, id: \.self) { Text($0) }
            if let earliest = detail.retry.earliestAt { TimestampRow(label: "Earliest retry time", value: earliest) }
            evidence("Supporting evidence", detail.retry.supportingEvidence)
        }
        Section("Missing evidence and limitations") {
            if detail.missingEvidence.isEmpty { Text("The report did not identify any other missing information.") }
            ForEach(Array(detail.missingEvidence.enumerated()), id: \.offset) { _, value in Text("\(value.description) (\(value.code))") }
            ForEach(Array(detail.warnings.enumerated()), id: \.offset) { _, value in Text("\(value.message) (\(value.code))").foregroundStyle(.orange) }
            DisclosureGroup("Information left out (\(detail.omissions.count))") { ForEach(Array(detail.omissions.enumerated()), id: \.offset) { _, value in Text("\(value.code): \(value.affects.joined(separator: ", "))").font(.caption) } }
            DisclosureGroup("Redaction notices (\(detail.redactionNotices.count))") { ForEach(Array(detail.redactionNotices.enumerated()), id: \.offset) { _, value in Text("\(value.code) · \(value.count): \(value.affects.joined(separator: ", "))").font(.caption) } }
        }
        Section("Saved evidence and versions") {
            Text("Report: \(report.reportId ?? "Not available")").textSelection(.enabled).font(.caption.monospaced())
            Text("Core evidence: \(report.evidenceId ?? "Not available")").textSelection(.enabled).font(.caption.monospaced())
            Text("Analysis evidence: \(report.analysisEvidenceId ?? "Not available")").textSelection(.enabled).font(.caption.monospaced())
            TimestampRow(label: "Captured", value: detail.capturedAt); TimestampRow(label: "Generated", value: detail.generatedAt)
            LabeledContent("Job status / final result", value: "\(InterfaceText.jobStatus(detail.phase)) / \(detail.outcome.map(InterfaceText.finalResult) ?? "Not available")")
            LabeledContent("Control service ID", value: detail.controlInstanceId)
            LabeledContent("Control / API version", value: "\(detail.controlVersion) / \(detail.contractVersion)")
            LabeledContent("Evidence collection platform", value: detail.platform)
            ForEach(detail.runs, id: \.id) { run in Text("Run \(run.number) · \(run.id)\nExecution: \(run.executionId ?? "Not available")").font(.caption) }
            DisclosureGroup("Component versions") {
                Text("Core \(detail.versions.jobman) · collector \(detail.versions.collector)")
                Text("Companion \(detail.versions.companion) · engine \(detail.versions.engine)")
                Text("Evidence schema \(detail.versions.evidenceSchema) · report schema \(detail.versions.reportSchema)")
                Text("Generation schema \(detail.versions.generationSchema) · proposal schema \(detail.versions.proposalSchema)")
                ForEach(Array(detail.analyzers.enumerated()), id: \.offset) { _, analyzer in Text("\(analyzer.name) · \(analyzer.version)") }
            }
            DisclosureGroup("AI service use") {
                Text("AI service called: \(detail.disclosure.providerInvoked ? "Yes" : "No"); AI-generated content used: \(detail.disclosure.generatedContentUsed ? "Yes" : "No")")
                Text("Processing location: \(detail.disclosure.locality); profile: \(detail.disclosure.profile ?? "None")")
                if let provider = detail.disclosure.provider { Text("\(provider) / \(detail.disclosure.model ?? "Unknown model")") }
                if let requestID = detail.disclosure.requestId { Text("Request ID: \(requestID)") }
                Text("Types of information: \(detail.disclosure.classes.joined(separator: ", "))")
                Text("Items: \(detail.disclosure.itemCount); artifacts: \(detail.disclosure.artifactCount); enrichment: \(detail.disclosure.enrichmentCount)")
                Text("Artifact bytes: \(detail.disclosure.artifactBytes); enrichment bytes: \(detail.disclosure.enrichmentBytes); request bytes: \(detail.disclosure.requestBytes); redaction notices: \(detail.disclosure.redactionNoticeCount)")
                ForEach(detail.disclosure.itemIds + detail.disclosure.artifactIds + detail.disclosure.enrichmentIds, id: \.self) { Text($0).font(.caption) }
                ForEach(Array(detail.generators.enumerated()), id: \.offset) { _, generator in Text("\(generator.provider) / \(generator.model) / \(generator.profile) / \(generator.locality)") }
            }
        }
    }
    @ViewBuilder private func confidence(_ value: DashboardAPI.ReportConfidence) -> some View {
        Text("Confidence: \(value.score)/100 (\(value.band))").font(.subheadline)
        Text("Why this confidence level: \(value.basis)")
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
            Section("Saved evidence for this report") {
                Text(reference.label).font(.headline)
                Text("Report: \(report.reportId ?? "Not available")").font(.caption).textSelection(.enabled)
                Text("Citation: \(reference.id)").font(.caption).textSelection(.enabled)
                if let citation {
                    Text("\(citation.kind) · \(citation.quality) · \(citation.disclosure)").font(.caption)
                    if let value = citation.valueJSON { Text(value).font(.system(.body, design: .monospaced)).textSelection(.enabled).accessibilityIdentifier("citationValue") }
                    if let bytes {
                        Text("Byte positions after redaction: \(citation.startOffset ?? "?")–\(citation.endOffset ?? "?")").font(.caption)
                        if citation.originalOffsetsExact { Text("Original log byte positions: \(citation.originalStartOffset ?? "?")–\(citation.originalEndOffset ?? "?")").font(.caption) }
                        else { Text("Removing sensitive data changed the length. Exact positions in the original log are not available.").font(.caption) }
                        Text(String(decoding: bytes, as: UTF8.self)).font(.system(.body, design: .monospaced)).textSelection(.enabled).accessibilityIdentifier("citationBytes")
                        if String(data: bytes, encoding: .utf8) == nil { Text("Some bytes could not be shown as text and use replacement characters. The original bytes are available below as Base64.").font(.caption) }
                        DisclosureGroup("Original bytes (Base64 encoding)") { Text(citation.bytesBase64 ?? "").font(.caption.monospaced()).textSelection(.enabled) }
                        Text("Run \(citation.runNumber ?? "?") · \(citation.runId ?? "?")\nExecution \(citation.executionId ?? "?") · \(citation.stream ?? "?")").font(.caption)
                    }
                    if let entity = citation.sourceEntityId { Text("Source entity: \(entity)").font(.caption) }
                    if let revision = citation.sourceRevision { Text("Source revision: \(revision)").font(.caption) }
                    TimestampRow(label: "Captured", value: citation.capturedAt); TimestampRow(label: "Observed", value: citation.observedAt)
                } else if let error { ErrorMessage(error: error) }
                else { ProgressView("Checking access and loading saved evidence…") }
                Text("This is the evidence saved with the report. It is not a fresh reading of the current logs.").font(.footnote)
            }
        }.navigationTitle("Evidence reference").task(id: store.active) {
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
