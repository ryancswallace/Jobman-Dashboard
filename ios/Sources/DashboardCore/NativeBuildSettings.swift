import Foundation

/// Registrations are compiled into the signed application, never learned from
/// the server being contacted. Exact callback and content-scheme checks remain.
public enum NativeBuildSettings {
    public static var authenticationScheme: String { Bundle.main.object(forInfoDictionaryKey: "JobmanAuthenticationScheme") as? String ?? "jobman-dashboard-auth" }
    public static var contentScheme: String { Bundle.main.object(forInfoDictionaryKey: "JobmanContentScheme") as? String ?? "jobman-dashboard" }
    public static var redirectURI: String { authenticationScheme + "://callback" }
    public static var productName: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String ?? "Jobman Dashboard" }
    public static var authLabel: String { Bundle.main.object(forInfoDictionaryKey: "JobmanAuthLabel") as? String ?? "Sign in with your organization" }
    public static func validScheme(_ value: String) -> Bool {
        value.range(of: "^[a-z][a-z0-9.-]{2,79}$", options: .regularExpression) != nil &&
        !value.contains("..") && !value.hasSuffix(".") &&
        (value == "jobman-dashboard" || value == "jobman-dashboard-auth" || value.contains("."))
    }
}
