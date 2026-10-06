import Foundation

/// User-facing descriptions only; wire values and authorization decisions stay unchanged.
public enum InterfaceText {
    /// Context-specific labels: "accepted" means something different for notification delivery.
    public static func jobStatus(_ value: String) -> String {
        switch value {
        case "active": "Active"
        case "awaiting": "Waiting for execution"
        case "accepted": "Submitted"
        case "assigning": "Assigning execution"
        case "accepted_execution": "Execution accepted"
        case "running": "Running"
        case "terminal": "Finished"
        default: value
        }
    }

    public static func finalResult(_ value: String) -> String {
        switch value {
        case "success": "Success"
        case "failure": "Failure"
        case "timed_out": "Timed out"
        case "aborted": "Aborted"
        case "lost": "Lost"
        case "cancelled": "Cancelled"
        default: value
        }
    }

    public static func statusConfidence(_ value: String) -> String {
        switch value {
        case "current": "Current"
        case "stale": "Outdated"
        case "uncertain": "Uncertain"
        case "lost": "Reported lost"
        default: value
        }
    }

    public static func notificationPermission(_ value: String) -> String {
        switch value {
        case "not_checked": "Not checked yet"
        case "not_determined": "Not requested yet"
        case "denied": "Not allowed — inbox still available"
        case "authorized": "Allowed"
        case "provisional": "Quiet notifications allowed"
        case "ephemeral": "Temporarily allowed"
        default: "Unknown permission (\(value))"
        }
    }

    public static func notificationRegistration(_ value: String) -> String {
        switch value {
        case "current": "Registered with Apple"
        case "invalid": "Apple registration needs updating"
        case "absent": "Not registered with Apple"
        default: "Unknown registration status (\(value))"
        }
    }

    public static func reportProfile(_ value: String) -> String {
        switch value {
        case "metadata": "Job details only"
        case "include_log_tail": "Job details and recent logs"
        default: "Other report profile (\(value))"
        }
    }

    public static func submittedCommandUnavailable(_ reason: String?) -> String {
        switch reason {
        case "missing": "The submitted command was not recorded or is no longer available."
        case "unsupported", "unsupported_contract": "This deployment does not support showing the submitted command."
        case "too_large": "The submitted command exceeds the size this service can safely return."
        case .none: "This deployment did not provide the submitted command."
        case .some(let reason): "The submitted command is not available. Reason: \(JobInspectionText.literal(reason))"
        }
    }
}
