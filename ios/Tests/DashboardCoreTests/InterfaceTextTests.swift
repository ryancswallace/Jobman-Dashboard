import Testing
@testable import DashboardCore

@Test func notificationCopyDistinguishesPermissionFromRegistrationAndDelivery() {
    #expect(InterfaceText.notificationPermission("denied") == "Not allowed — inbox still available")
    #expect(InterfaceText.notificationPermission("provisional") == "Quiet notifications allowed")
    #expect(InterfaceText.notificationPermission("ephemeral") == "Temporarily allowed")
    #expect(InterfaceText.notificationRegistration("current") == "Registered with Apple")
    #expect(InterfaceText.notificationRegistration("invalid") == "Apple registration needs updating")
    #expect(InterfaceText.notificationRegistration("absent") == "Not registered with Apple")
    #expect(InterfaceText.notificationRegistration("future").contains("future"))
    #expect(!InterfaceText.notificationRegistration("current").contains("delivered"))
}

@Test func reportAndMissingCommandCopyKeepSourceLimitationsExplicit() {
    #expect(InterfaceText.reportProfile("metadata") == "Job details only")
    #expect(InterfaceText.reportProfile("include_log_tail") == "Job details and recent logs")
    #expect(InterfaceText.reportProfile("future").contains("future"))
    #expect(InterfaceText.submittedCommandUnavailable("unsupported").contains("does not support"))
    #expect(InterfaceText.submittedCommandUnavailable("missing").contains("not recorded"))
    #expect(InterfaceText.submittedCommandUnavailable("too_large").contains("exceeds the size"))
    #expect(InterfaceText.submittedCommandUnavailable(nil).contains("did not provide"))
    #expect(InterfaceText.submittedCommandUnavailable("future\u{202e}reason").contains("future\\u{202E}reason"))
}

@Test func dependencyErrorsExplainNextStepWithoutClaimingPermissionOrEmptyResults() {
    #expect(DashboardError.authorizationUnavailable.errorDescription!.contains("cannot confirm"))
    #expect(DashboardError.storageUnavailable.errorDescription!.contains("does not mean the log is empty"))
    #expect(DashboardError.logGap.errorDescription!.contains("missing"))
    #expect(DashboardError.redactionUnavailable.errorDescription!.contains("administrator"))
    #expect(DashboardError.forbidden != DashboardError.authorizationUnavailable)
    #expect(DashboardError.server("private diagnostic must stay hidden").errorDescription?.contains("private diagnostic") == false)
}

@Test func jobStatusCopyKeepsFinishedSeparateFromSuccessAndPreservesUnknownValues() {
    #expect(InterfaceText.jobStatus("terminal") == "Finished")
    #expect(InterfaceText.jobStatus("accepted") == "Submitted")
    #expect(InterfaceText.jobStatus("assigning") == "Assigning execution")
    #expect(InterfaceText.jobStatus("accepted_execution") == "Execution accepted")
    #expect(InterfaceText.statusConfidence("stale") == "Outdated")
    #expect(InterfaceText.statusConfidence("lost") == "Reported lost")
    #expect(InterfaceText.statusConfidence("future_confidence") == "future_confidence")
    #expect(InterfaceText.finalResult(InterfaceText.jobStatus("terminal")) == "Finished")
    #expect(InterfaceText.finalResult(InterfaceText.jobStatus("success")) == "Success")
    #expect(InterfaceText.finalResult("failure") == "Failure")
    #expect(InterfaceText.finalResult("timed_out") == "Timed out")
    #expect(InterfaceText.jobStatus("future_source_status") == "future_source_status")
    #expect(InterfaceText.finalResult("future_result") == "future_result")
    #expect(DashboardError.sourceUnavailable.errorDescription!.contains("service"))
    #expect(!DashboardError.sourceUnavailable.errorDescription!.contains("deployment"))
}
