import DashboardCore
import SwiftUI
import UIKit

/// Literal text, never Markdown or a link. Values wrap and remain selectable.
struct InspectionText: View {
    let label: String
    let value: String
    var empty = "Empty string"
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(verbatim: label).font(.caption).foregroundStyle(.secondary)
            Text(verbatim: value.isEmpty ? empty : JobInspectionText.literal(value))
                .font(.system(.body, design: .monospaced)).textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct JobCommandView: View {
    let execution: DashboardAPI.JobExecution
    @State private var copyStatus: String?
    var body: some View {
        List {
            Section {
                Text("The executable and each ordered argument are separate literal values. This is not a shell-quoted command. Scripts passed to a shell remain literal arguments.").font(.footnote)
                Text("No text is truncated. Nonprinting and direction controls are displayed as Unicode escapes; JSON copy preserves the original values.").font(.footnote).foregroundStyle(.secondary)
                Button("Copy argument vector as JSON") {
                    do {
                        let value = try JobInspectionText.argumentVectorJSON(execution.command)
                        UIPasteboard.general.setItems([["public.utf8-plain-text": value]], options: [.localOnly: true, .expirationDate: Date().addingTimeInterval(60)])
                        copyStatus = "Copied to this device for 60 seconds."
                    } catch { copyStatus = "Could not encode the argument vector." }
                }.accessibilityIdentifier("copyArgumentVector")
                if let copyStatus { Text(copyStatus).font(.caption).accessibilityIdentifier("argumentCopyStatus") }
            }
            Section("Submitted specification") {
                InspectionText(label: "Submitted working directory", value: execution.workingDirectory, empty: "Empty (no directory specified)")
                Text("The submitted directory is a portable logical path; no runtime path expansion is inferred.").font(.caption).foregroundStyle(.secondary)
                InspectionText(label: "Executable · argv[0]", value: execution.command.executable).accessibilityIdentifier("submittedExecutable")
            }
            Section("\(execution.command.args.count) ordered arguments") {
                if execution.command.args.isEmpty { Text("No arguments") }
                ForEach(execution.command.args.indices, id: \.self) { index in
                    let argument = execution.command.args[index]
                    InspectionText(label: "Argument \(index + 1) · argv[\(index + 1)] · \(argument.utf8.count) UTF-8 bytes", value: argument, empty: "Empty argument (0 bytes)")
                        .accessibilityIdentifier("submittedArgument-\(index + 1)")
                }
            }
        }.navigationTitle("Submitted command").navigationBarTitleDisplayMode(.inline)
    }
}

struct SelectedRunFacts: View {
    let run: DashboardAPI.JobRun
    let currentRunID: String?
    var body: some View {
        Section("Selected run facts") {
            Text(currentRunID == run.id ? "The selected run matches the current job's run reference." : "These are the selected recorded run's facts. Current job facts above remain separate.")
                .font(.footnote).foregroundStyle(.secondary)
            Text("Recorded when the run was selected. Choose the run again to refresh these facts.").font(.caption).foregroundStyle(.secondary)
            LabeledContent("Selected run number", value: run.number)
            InspectionText(label: "Selected run ID", value: run.id)
            LabeledContent("Run phase", value: run.phase)
            LabeledContent("Run desired state", value: run.desiredState)
            LabeledContent("Run outcome", value: run.outcome ?? "Unavailable")
            TimestampRow(label: "Run metadata created", value: run.createdAt)
            TimestampRow(label: "Run metadata updated", value: run.updatedAt)
            Text("Run metadata dates are not execution start or completion times.").font(.caption).foregroundStyle(.secondary)
            InspectionText(label: "Selected execution ID", value: run.executionId ?? "No execution recorded")
            LabeledContent("Execution phase", value: run.executionPhase ?? "Unavailable")
            InspectionText(label: "Run target ID", value: run.targetId ?? "Unavailable")
            InspectionText(label: "Run target generation ID", value: run.targetGenerationId ?? "Unavailable")
            LabeledContent("Run backend", value: run.backend ?? "Unavailable")
            LabeledContent("Run confidence", value: run.confidence ?? "Unavailable")
        }
    }
}
