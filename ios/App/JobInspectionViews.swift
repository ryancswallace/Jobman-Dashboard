import DashboardCore
import SwiftUI
import UIKit

/// Literal text, never Markdown or a link. Values wrap and remain selectable.
struct InspectionText: View {
    let label: String
    let value: String
    var empty = "Empty value"
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
                Text("The executable is the program to run. Arguments are the values passed to it, in order; a shell script can be one argument.").font(.footnote)
                Text("All text is included. Invisible control characters are shown as Unicode codes. Copy saves the original program and arguments as a JSON list, not a ready-to-run terminal command.").font(.footnote).foregroundStyle(.secondary)
                Button("Copy command and arguments (JSON)") {
                    do {
                        let value = try JobInspectionText.argumentVectorJSON(execution.command)
                        UIPasteboard.general.setItems([["public.utf8-plain-text": value]], options: [.localOnly: true, .expirationDate: Date().addingTimeInterval(60)])
                        copyStatus = "Copied to this device for 60 seconds."
                    } catch { copyStatus = "The command could not be copied. Try again." }
                }.accessibilityIdentifier("copyArgumentVector")
                if let copyStatus { Text(copyStatus).font(.caption).accessibilityIdentifier("argumentCopyStatus") }
            }
            Section("As submitted") {
                InspectionText(label: "Submitted working directory", value: execution.workingDirectory, empty: "No directory specified")
                Text("This is the submitted directory reference. The actual directory used during execution may differ.").font(.caption).foregroundStyle(.secondary)
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
        Section("Selected run details") {
            Text(currentRunID == run.id ? "The selected run matches the current job's run reference." : "These details describe the run you selected. The current job details above are unchanged.")
                .font(.footnote).foregroundStyle(.secondary)
            Text("Loaded when you selected this run. Select it again to refresh these details.").font(.caption).foregroundStyle(.secondary)
            LabeledContent("Selected run number", value: run.number)
            InspectionText(label: "Selected run ID", value: run.id)
            LabeledContent("Run status", value: InterfaceText.jobStatus(run.phase))
            LabeledContent("Run requested state", value: run.desiredState)
            LabeledContent("Run final result", value: run.outcome.map(InterfaceText.finalResult) ?? "Not available")
            TimestampRow(label: "Run record created", value: run.createdAt)
            TimestampRow(label: "Run record updated", value: run.updatedAt)
            Text("These are record dates, not execution start or completion times.").font(.caption).foregroundStyle(.secondary)
            InspectionText(label: "Selected execution ID", value: run.executionId ?? "No execution recorded")
            LabeledContent("Execution status", value: run.executionPhase ?? "Not available")
            InspectionText(label: "Run target ID", value: run.targetId ?? "Not available")
            InspectionText(label: "Run target configuration version ID", value: run.targetGenerationId ?? "Not available")
            LabeledContent("Run execution system", value: run.backend ?? "Not available")
            LabeledContent("Run status confidence", value: run.confidence.map(InterfaceText.statusConfidence) ?? "Not available")
        }
    }
}
