import CryptoKit
import Foundation

public struct DeviceConfiguration: Equatable, Sendable {
    public let origin: URL
    public let topic: String
    public let environment: String
    public init(origin: URL, topic: String, entitlementEnvironment: String) throws {
        let connection = try DashboardConnection(address: origin.absoluteString)
        guard var parts = URLComponents(url: connection.baseURL, resolvingAgainstBaseURL: false),
              Self.validTopic(topic), ["development", "production"].contains(entitlementEnvironment)
        else { throw DashboardError.invalidResponse }
        parts.scheme = "https"; parts.host = parts.host?.lowercased(); parts.path = ""
        if parts.port == 443 { parts.port = nil }
        guard let origin = parts.url else { throw DashboardError.invalidAddress }
        self.origin = origin; self.topic = topic
        environment = entitlementEnvironment == "development" ? "sandbox" : "production"
    }
    public var storageKey: String {
        let components = ["jobman-dashboard/installation/v1", origin.absoluteString, topic, environment]
        let data = Data(components.joined(separator: "\u{0}").utf8)
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }
    public static func validTopic(_ value: String) -> Bool {
        value.utf8.count <= 255 && value.range(of: #"^[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$"#, options: .regularExpression) != nil
    }
}

/// Durable only in device-only Keychain. Its proof is write-only on the wire.
public struct DeviceInstallation: Codable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public let id: String
    private let secret: String
    public var description: String { "DeviceInstallation(private proof)" }
    public var debugDescription: String { description }
    public init(id: String, randomBytes: Data) throws {
        guard deviceUUID(id), randomBytes.count == 32 else { throw DashboardError.invalidResponse }
        self.id = id; secret = Self.encodeSecret(randomBytes)
    }
    private enum CodingKeys: String, CodingKey { case id, secret }
    public init(from decoder: any Decoder) throws {
        let fields = try decoder.container(keyedBy: CodingKeys.self)
        let id = try fields.decode(String.self, forKey: .id), secret = try fields.decode(String.self, forKey: .secret)
        guard deviceUUID(id), Self.validSecret(secret) else { throw DashboardError.invalidResponse }
        self.id = id; self.secret = secret
    }
    public var proof: DashboardAPI.InstallationProofInput { .init(installationSecret: secret) }
    public func tokenInput(token: String, permission: String) throws -> DashboardAPI.DeviceTokenInput {
        guard Self.validToken(token), Self.permissions.contains(permission) else { throw DashboardError.invalidResponse }
        return .init(installationSecret: secret, token: token, permission: permission)
    }
    public func bindInput(configuration: DeviceConfiguration, capability: DeviceRevocationCapability, token: String, permission: String, label: String, enabled: Bool, muted: Bool) throws -> DashboardAPI.DeviceBindInput {
        _ = try tokenInput(token: token, permission: permission)
        let label = try DeviceSettingsDraft.validatedLabel(label)
        return .init(installationSecret: secret, label: label, topic: configuration.topic, environment: configuration.environment, token: token, permission: permission, enabled: enabled, muted: muted, confirmBind: true, revocationId: capability.id, revocationCredential: capability.request.revocationCredential)
    }
    public func switchInput(configuration: DeviceConfiguration, capability: DeviceRevocationCapability, token: String, permission: String, label: String, enabled: Bool, muted: Bool) throws -> DashboardAPI.DeviceSwitchInput {
        let input = try bindInput(configuration: configuration, capability: capability, token: token, permission: permission, label: label, enabled: enabled, muted: muted)
        return .init(installationSecret: secret, label: input.label, topic: input.topic, environment: input.environment, token: token, permission: permission, enabled: enabled, muted: muted, confirmSwitch: true, revocationId: capability.id, revocationCredential: capability.request.revocationCredential)
    }
    public func revocationInput(_ capability: DeviceRevocationCapability) -> DashboardAPI.DeviceRevocationInput {
        .init(installationSecret: secret, revocationId: capability.id, revocationCredential: capability.request.revocationCredential)
    }
    public static let permissions = ["not_determined", "denied", "authorized", "provisional", "ephemeral"]
    public static func validToken(_ token: String) -> Bool {
        token.utf8.count >= 2 && token.utf8.count <= 1024 && token.utf8.count % 2 == 0
        && token.utf8.allSatisfy { (48...57).contains($0) || (97...102).contains($0) }
    }
    public static func validSecret(_ value: String) -> Bool {
        guard value.utf8.count == 43, let bytes = Data(base64Encoded: value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + "="), bytes.count == 32 else { return false }
        return encodeSecret(bytes) == value
    }
    private static func encodeSecret(_ bytes: Data) -> String { bytes.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") }
}

public typealias NotificationDevice = DashboardAPI.Device
extension DashboardAPI.Device: Identifiable {
    public var id: String { installationId }
    public func validate(expectedID: String? = nil, configuration: DeviceConfiguration? = nil) throws {
        guard deviceUUID(installationId), expectedID == nil || expectedID == installationId, deviceRevision(revision),
              (try? DeviceSettingsDraft.validatedLabel(label)) == label, DeviceConfiguration.validTopic(topic),
              ["sandbox", "production"].contains(environment), state == "bound",
              ["current", "invalid", "absent"].contains(tokenStatus), DeviceInstallation.permissions.contains(permission),
              let created = WireDate.parse(createdAt), let updated = WireDate.parse(updatedAt), let seen = WireDate.parse(lastSeenAt),
              created <= seen, seen <= updated else { throw DashboardError.invalidResponse }
        if let configuration, topic != configuration.topic || environment != configuration.environment { throw DashboardError.invalidResponse }
    }
}
extension DashboardAPI.DevicePage {
    public func validate() throws {
        guard items.count <= 50, Set(items.map(\.id)).count == items.count else { throw DashboardError.invalidResponse }
        for item in items { try item.validate() }
    }
}

public enum InstallationOwnership: Equatable, Sendable {
    case absent, unbound(revision: String), current(revision: String), otherAccount(revision: String)
    public init(state: DashboardAPI.InstallationState?, installationID: String) throws {
        guard deviceUUID(installationID) else { throw DashboardError.invalidResponse }
        guard let state else { self = .absent; return }
        guard state.installationId == installationID, deviceRevision(state.revision), !state.boundToCurrentAccount || state.hasBinding else { throw DashboardError.invalidResponse }
        if !state.hasBinding { self = .unbound(revision: state.revision) }
        else if state.boundToCurrentAccount { self = .current(revision: state.revision) }
        else { self = .otherAccount(revision: state.revision) }
    }
    public var revision: String? { switch self { case .absent: nil; case .unbound(let r), .current(let r), .otherAccount(let r): r } }
    public var intent: String { switch self { case .absent, .unbound: "bind"; case .current: "existing"; case .otherAccount: "switch" } }
    /// Automatic work may refresh a current binding; it can never create one.
    public var refreshRevision: String? { if case .current(let revision) = self { revision } else { nil } }
}

public struct DeviceSettingsDraft: Sendable {
    public var label: String
    public var enabled: Bool
    public var muted: Bool
    public init(label: String = "iPhone", enabled: Bool = false, muted: Bool = false) { self.label = label; self.enabled = enabled; self.muted = muted }
    public init(device: NotificationDevice) { label = device.label; enabled = device.enabled; muted = device.muted }
    public func input() throws -> DashboardAPI.DeviceSettingsInput { .init(label: try Self.validatedLabel(label), enabled: enabled, muted: muted) }
    public static func validatedLabel(_ value: String) throws -> String {
        let value = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty, value.utf8.count <= 120, !value.unicodeScalars.contains(where: CharacterSet.controlCharacters.contains) else { throw DeviceInputError.label }
        return value
    }
}
public enum DeviceInputError: Error, LocalizedError {
    case label, secureStorage, unavailableToken, recheckRequired
    public var errorDescription: String? {
        switch self {
        case .label: "Enter a device name up to 120 bytes, without control characters. Some characters use more than one byte."
        case .secureStorage: "Secure storage is not available. Unlock this phone and try again. A new account connection has not been confirmed."
        case .unavailableToken: "This iPhone is not currently registered for Apple notifications. Check notification permission and your connection, then try again."
        case .recheckRequired: "Device state changed or could not be confirmed. Refresh and review it before another change."
        }
    }
}

public enum NotificationRegistrationPolicy {
    /// Registration only captures an application token. User/account attachment
    /// is a separate explicit operation, even after an OS permission prompt.
    public static func shouldRegister(permission: String, appActive: Bool, fixtureMode: Bool) -> Bool {
        appActive && !fixtureMode && ["authorized", "provisional", "ephemeral"].contains(permission)
    }
}

private func deviceUUID(_ value: String) -> Bool { UUID(uuidString: value)?.uuidString.lowercased() == value && value != "00000000-0000-0000-0000-000000000000" }
private func deviceRevision(_ value: String) -> Bool { if let number = Int64(value), number > 0 { String(number) == value } else { false } }

extension DashboardAPI.DeviceRevocationReceipt {
    public func validate(installationID: String, capability: DeviceRevocationCapability, intent: String? = nil) throws {
        guard installationId == installationID, revocationId == capability.id, deviceRevision(revision),
              ["bind", "switch", "existing"].contains(self.intent), intent == nil || self.intent == intent else { throw DashboardError.invalidResponse }
    }
}
