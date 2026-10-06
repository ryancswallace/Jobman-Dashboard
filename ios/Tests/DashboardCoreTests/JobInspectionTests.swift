import Foundation
import Testing
@testable import DashboardCore

private func inspectionFixture() -> [String: Any] {
    ["job": ["deploymentId": "east", "namespaceId": "research", "id": "job-042", "targetId": "target-1", "revision": "7", "createdAt": "2026-10-06T11:00:00Z", "updatedAt": "2026-10-06T11:01:00Z", "desiredState": "run", "phase": "running", "labels": [:]], "fetchedAt": "2026-10-06T11:02:00Z"]
}

@Test func jobInspectionOptionalFieldsKeepOlderResponsesReadable() throws {
    let detail = try JSONDecoder().decode(JobDetail.self, from: JSONSerialization.data(withJSONObject: inspectionFixture()))
    #expect(detail.job.targetName == nil)
    #expect(detail.job.partition == nil)
    #expect(detail.job.workloadDigest == nil)
    #expect(detail.job.confidenceUpdatedAt == nil)
    #expect(detail.execution == nil)
    #expect(detail.executionUnavailableReason == nil)
}

@Test func jobInspectionDecodesFullSubmittedSpecificationWithoutLosingArguments() throws {
    var wire = inspectionFixture()
    var job = wire["job"] as! [String: Any]
    job["targetName"] = "Synthetic cluster"; job["partition"] = "batch"
    job["workloadDigest"] = "sha256:synthetic"; job["confidenceUpdatedAt"] = "2026-10-06T11:01:30Z"
    job["scheduler"] = ["backend": "slurm", "state": "FAILED", "jobId": "123_4", "reason": "synthetic reason", "cluster": "synthetic cluster", "observedAt": "2026-10-06T11:01:31Z"]
    wire["job"] = job
    let args = ["-c", "printf '%s\\n' \"$1\"\n# literal script\nexit 7", "", "  spaced  ", "quote'\"\\value", "日本語", "\u{1b}[31m\u{202e}hidden", String(repeating: "z", count: 65536)]
    wire["execution"] = ["command": ["executable": "/bin/sh", "args": args], "workingDirectory": "workspace:/synthetic path"]
    let detail = try JSONDecoder().decode(JobDetail.self, from: JSONSerialization.data(withJSONObject: wire))
    #expect(detail.job.targetName == "Synthetic cluster")
    #expect(detail.job.partition == "batch")
    #expect(detail.job.scheduler?.backend == "slurm")
    #expect(detail.job.scheduler?.jobId == "123_4")
    #expect(detail.job.workloadDigest == "sha256:synthetic")
    #expect(detail.job.confidenceUpdatedAt == "2026-10-06T11:01:30Z")
    let execution = try #require(detail.execution)
    #expect(execution.workingDirectory == "workspace:/synthetic path")
    #expect(execution.command.args == args)
    let copy = try JobInspectionText.argumentVectorJSON(execution.command)
    #expect(try JSONDecoder().decode([String].self, from: Data(copy.utf8)) == ["/bin/sh"] + args)
    #expect(JobInspectionText.literal(args.last!) == args.last!)
    #expect(JobInspectionText.literal(args[1]) == args[1])
    #expect(JobInspectionText.literal(args[2]) == "")
    #expect(JobInspectionText.literal(args[3]) == "  spaced  ")
    #expect(JobInspectionText.literal(args[6]) == "\\u{1B}[31m\\u{202E}hidden")
}

@Test func jobInspectionPreservesMissingReasonAndRejectsIncompleteSpecification() throws {
    var wire = inspectionFixture()
    wire["executionUnavailableReason"] = "unsupported_contract"
    let detail = try JSONDecoder().decode(JobDetail.self, from: JSONSerialization.data(withJSONObject: wire))
    #expect(detail.execution == nil)
    #expect(detail.executionUnavailableReason == "unsupported_contract")
    wire["execution"] = ["command": ["executable": "/bin/sh"], "workingDirectory": ""]
    #expect(throws: DecodingError.self) { try JSONDecoder().decode(JobDetail.self, from: JSONSerialization.data(withJSONObject: wire)) }
    wire["execution"] = ["command": ["executable": "/bin/sh", "args": []]]
    #expect(throws: DecodingError.self) { try JSONDecoder().decode(JobDetail.self, from: JSONSerialization.data(withJSONObject: wire)) }
}

@Test func jobInspectionEmptyVectorAndControlRenderingAreExplicitAndLossless() throws {
    let command = DashboardAPI.JobCommand(executable: "tool", args: [])
    #expect(try JSONDecoder().decode([String].self, from: Data(JobInspectionText.argumentVectorJSON(command).utf8)) == ["tool"])
    #expect(JobInspectionText.literal("a\r\n\tb\u{0}\u{2028}\u{2066}") == "a\\u{D}\n\tb\\u{0}\\u{2028}\\u{2066}")
}
