import Foundation

/// A capability can only revoke one reserved binding generation. It cannot sign in,
/// read data, register a token, or authorize a subsequent binding.
public struct DeviceRevocationCapability: Codable, Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public let id: String
    private let credential: String
    public init(id: String, randomBytes: Data) throws {
        guard UUID(uuidString: id)?.uuidString.lowercased() == id,
              id != "00000000-0000-0000-0000-000000000000", randomBytes.count == 32 else { throw DashboardError.invalidResponse }
        self.id = id
        credential = randomBytes.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
    private enum CodingKeys: String, CodingKey { case id, credential }
    public init(from decoder: any Decoder) throws {
        let fields = try decoder.container(keyedBy: CodingKeys.self)
        let id = try fields.decode(String.self, forKey: .id), value = try fields.decode(String.self, forKey: .credential)
        guard UUID(uuidString: id)?.uuidString.lowercased() == id, id != "00000000-0000-0000-0000-000000000000",
              DeviceInstallation.validSecret(value) else { throw DashboardError.invalidResponse }
        self.id = id; credential = value
    }
    public var description: String { "DeviceRevocationCapability(private credential)" }
    public var debugDescription: String { description }
    public var request: DashboardAPI.NativeDeviceRevocationInput { .init(revocationId: id, revocationCredential: credential) }
}

/// Persisted as one atomic Keychain value. Counting active and pending capabilities
/// together reserves space for sign-out before any server mutation can start.
public struct DeviceRevocationLedger: Codable, Sendable {
    public struct Entry: Codable, Sendable {
        public let storageKey: String
        public let origin: URL
        public fileprivate(set) var accountID: String?
        public let installationID: String
        public let capability: DeviceRevocationCapability
        public let wasExistingBinding: Bool
        public fileprivate(set) var acknowledged: Bool
        public fileprivate(set) var reservationRevision: String?
        public fileprivate(set) var reservationIntent: String?
        public fileprivate(set) var active: Bool
        public fileprivate(set) var queued: Bool
    }
    public private(set) var entries: [Entry] = []
    public init() {}
    private enum CodingKeys: String, CodingKey { case entries }
    public init(from decoder: any Decoder) throws {
        let fields = try decoder.container(keyedBy: CodingKeys.self)
        let entries = try fields.decode([Entry].self, forKey: .entries)
        guard entries.count <= 32, Set(entries.map { $0.capability.id }).count == entries.count else { throw DeviceInputError.secureStorage }
        for item in entries {
            let origin = try DashboardConnection(address: item.origin.absoluteString).baseURL
            guard origin.host != nil, item.queued ? item.accountID == nil : (!(item.accountID ?? "").isEmpty && (item.accountID ?? "").utf8.count <= 256),
                  item.storageKey.count == 64, item.storageKey.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
                  UUID(uuidString: item.installationID)?.uuidString.lowercased() == item.installationID,
                  !item.active || item.acknowledged,
                  !item.acknowledged || (Int64(item.reservationRevision ?? "0") ?? 0) > 0,
                  !item.acknowledged || ["bind", "switch", "existing"].contains(item.reservationIntent ?? "") else { throw DeviceInputError.secureStorage }
        }
        self.entries = entries
    }
    public mutating func begin(configuration: DeviceConfiguration, accountID: String, installationID: String,
                               capability: DeviceRevocationCapability, existing: Bool) throws {
        guard entries.count < 32 else { throw DashboardError.deviceCapacity }
        guard !entries.contains(where: { $0.capability.id == capability.id }), !accountID.isEmpty, accountID.utf8.count <= 256,
              UUID(uuidString: installationID)?.uuidString.lowercased() == installationID else { throw DashboardError.invalidResponse }
        entries.append(.init(storageKey: configuration.storageKey, origin: configuration.origin, accountID: accountID,
                             installationID: installationID, capability: capability, wasExistingBinding: existing,
                             acknowledged: false, reservationRevision: nil, reservationIntent: nil, active: false, queued: false))
    }
    public mutating func acknowledge(_ id: String, revision: String, intent: String) throws {
        guard let number = Int64(revision), number > 0, String(number) == revision, ["bind", "switch", "existing"].contains(intent) else { throw DashboardError.invalidResponse }
        guard let index = entries.firstIndex(where: { $0.capability.id == id }), !entries[index].queued else { throw CancellationError() }
        entries[index].acknowledged = true; entries[index].reservationRevision = revision; entries[index].reservationIntent = intent
    }
    public mutating func activate(_ id: String) throws {
        guard let index = entries.firstIndex(where: { $0.capability.id == id }), entries[index].acknowledged, !entries[index].queued else { throw CancellationError() }
        entries[index].active = true
    }
    public func active(configuration: DeviceConfiguration, accountID: String, installationID: String) -> Entry? {
        entries.last { $0.storageKey == configuration.storageKey && $0.accountID == accountID && $0.installationID == installationID && $0.active && !$0.queued }
    }
    @discardableResult public mutating func queueAll() -> Bool {
        let any = !entries.isEmpty
        for index in entries.indices { entries[index].queued = true; entries[index].accountID = nil }
        return any
    }
    @discardableResult public mutating func queuePending() -> Bool {
        var changed = false
        for index in entries.indices where !entries[index].active && !entries[index].queued { entries[index].queued = true; entries[index].accountID = nil; changed = true }
        return changed
    }
    public mutating func queue(_ id: String) { if let index = entries.firstIndex(where: { $0.capability.id == id }) { entries[index].queued = true; entries[index].accountID = nil } }
    /// Unknown204 is sufficient for an unacknowledged future binding: admission
    /// was never allowed. A legacy existing binding stays explicitly unresolved.
    public mutating func complete(_ id: String) {
        entries.removeAll { $0.capability.id == id && $0.queued && ($0.acknowledged || !$0.wasExistingBinding) }
    }
    /// Only after authenticated proof inspection reports unbound, or a current
    /// revision-checked removal has completed. Never use a generic404 as proof.
    public mutating func confirmedRemoval(configuration: DeviceConfiguration, installationID: String) {
        entries.removeAll { $0.storageKey == configuration.storageKey && $0.installationID == installationID }
    }
    public var hasUnconfirmedExisting: Bool { entries.contains { $0.queued && $0.wasExistingBinding && !$0.acknowledged } }
}
