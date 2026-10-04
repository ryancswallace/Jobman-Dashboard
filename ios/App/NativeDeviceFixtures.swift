#if DEBUG
import DashboardCore
import Foundation

/// Explicit DEBUG fixtures never contact APNs, the Keychain, or a real account.
enum NativeDeviceFixtures {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--dashboard-device-fixtures")
    static let installationID = "90000000-0000-4000-8000-000000000001"
    static let remoteID = "90000000-0000-4000-8000-000000000002"
    static let server = DeviceFixtureServer()
}
final class DeviceFixtureServer: @unchecked Sendable {
    private let lock = NSLock()
    private var revision = 0
    private var owner = "none"
    private var revoked: Set<String> = []
    private var reserved: [String: (secret: String, intent: String)] = [:]
    private var rows: [String: NotificationDevice] = [:]
    init() {
        if ProcessInfo.processInfo.arguments.contains("--dashboard-device-other-account") { owner = "other"; revision = 4 }
        if ProcessInfo.processInfo.arguments.contains("--dashboard-device-existing") {
            owner = "current"; revision = 4
            rows[NativeDeviceFixtures.installationID] = Self.device(id: NativeDeviceFixtures.installationID, revision: "4", label: "Existing iPhone")
        }
        rows[NativeDeviceFixtures.remoteID] = Self.device(id: NativeDeviceFixtures.remoteID, revision: "7", label: "Synthetic remote iPhone")
    }
    private static func device(id: String, revision: String, label: String) -> NotificationDevice {
        let now = ISO8601DateFormatter().string(from: Date())
        return .init(installationId: id, revision: revision, label: label, topic: Bundle.main.bundleIdentifier!, environment: "sandbox", state: "bound", enabled: false, muted: true, permission: "authorized", tokenStatus: "current", createdAt: now, updatedAt: now, lastSeenAt: now, revocationReady: false)
    }
    func response(_ request: URLRequest) throws -> (Int, Data)? {
        guard let path = request.url?.path, path.hasPrefix("/api/v1/devices") || path == "/auth/native/device-revocations" else { return nil }
        return try lock.withLock {
            func json<T: Encodable>(_ value: T) throws -> (Int, Data) { (200, try JSONEncoder().encode(value)) }
            func failure(_ code: String, _ status: Int) throws -> (Int, Data) { (status, try JSONSerialization.data(withJSONObject: ["code":code])) }
            guard NativeDeviceFixtures.enabled else { return path == "/api/v1/devices" ? try json(DashboardAPI.DevicePage(items: [])) : try failure("not_found_or_inaccessible", 404) }
            let body = try Self.body(request)
            if path == "/auth/native/device-revocations" {
                if ProcessInfo.processInfo.arguments.contains("--dashboard-device-offline-revoke") { return try failure("source_unavailable", 503) }
                guard request.value(forHTTPHeaderField: "Authorization") == nil else { return try failure("invalid_request", 400) }
                let input = try JSONDecoder().decode(DashboardAPI.NativeDeviceRevocationInput.self, from: body)
                if let entry = reserved[input.revocationId], entry.secret == input.revocationCredential {
                    revoked.insert(input.revocationId)
                    if owner == "current" { rows.removeValue(forKey: NativeDeviceFixtures.installationID); owner = "none"; revision += 1 }
                }
                return (204, Data())
            }
            if path == "/api/v1/devices" { return try json(DashboardAPI.DevicePage(items: rows.values.sorted { $0.id < $1.id })) }
            let parts = path.split(separator: "/"), id = String(parts[3]), method = request.httpMethod ?? "GET"
            if path.hasSuffix("/inspect") {
                guard revision > 0 else { return try failure("not_found_or_inaccessible", 404) }
                return try json(DashboardAPI.InstallationState(installationId: id, revision: String(revision), hasBinding: owner != "none", boundToCurrentAccount: owner == "current"))
            }
            if path.hasSuffix("/revocation-reservations") {
                let input = try JSONDecoder().decode(DashboardAPI.DeviceRevocationInput.self, from: body)
                guard !revoked.contains(input.revocationId) else { return try failure("revision_conflict", 409) }
                let intent: String
                if let prior = reserved[input.revocationId] { guard prior.secret == input.revocationCredential else { return try failure("revision_conflict", 409) }; intent = prior.intent }
                else {
                    guard revision == 0 ? request.value(forHTTPHeaderField: "If-None-Match") == "*" : request.value(forHTTPHeaderField: "If-Match") == String(revision) else { return try failure("revision_conflict", 409) }
                    intent = owner == "none" ? "bind" : owner == "other" ? "switch" : "existing"
                    reserved[input.revocationId] = (input.revocationCredential, intent); revision += 1
                }
                let receipt = DashboardAPI.DeviceRevocationReceipt(installationId: id, revision: String(revision), revocationId: input.revocationId, intent: intent)
                if ProcessInfo.processInfo.arguments.contains("--dashboard-device-delayed-reservation") {
                    var object = try JSONSerialization.jsonObject(with: JSONEncoder().encode(receipt)) as! [String: Any]
                    object["fixtureDelay"] = true
                    return (200, try JSONSerialization.data(withJSONObject: object))
                }
                return try json(receipt)
            }
            if id == NativeDeviceFixtures.installationID {
                guard request.value(forHTTPHeaderField: "If-Match") == String(revision) else { return try failure("revision_conflict", 409) }
            } else { guard request.value(forHTTPHeaderField: "If-Match") == rows[id]?.revision else { return try failure("revision_conflict", 409) } }
            if method == "DELETE" {
                rows.removeValue(forKey: id)
                if id == NativeDeviceFixtures.installationID { owner = "none"; revision += 1 }
                return (204, Data())
            }
            if path.hasSuffix("/revocation-credentials") {
                let input = try JSONDecoder().decode(DashboardAPI.DeviceRevocationInput.self, from: body)
                guard let reservation = reserved[input.revocationId], reservation.secret == input.revocationCredential, !revoked.contains(input.revocationId), owner == "current" else { return try failure("revision_conflict", 409) }
                revision += 1; rows[id]?.revocationReady = true; rows[id]?.revision = String(revision)
                return try json(DashboardAPI.DeviceRevocationReceipt(installationId: id, revision: String(revision), revocationId: input.revocationId, intent: "existing"))
            }
            if path.hasSuffix("/bind") || path.hasSuffix("/switch") {
                let object = try JSONSerialization.jsonObject(with: body) as! [String: Any]
                guard let capID = object["revocationId"] as? String, let secret = object["revocationCredential"] as? String,
                      let reservation = reserved[capID], reservation.secret == secret, !revoked.contains(capID),
                      reservation.intent == (path.hasSuffix("/bind") ? "bind" : "switch") else { return try failure("revision_conflict", 409) }
                revision += 1; owner = "current"
                var device = Self.device(id: id, revision: String(revision), label: object["label"] as! String)
                device.enabled = object["enabled"] as! Bool; device.muted = object["muted"] as! Bool; device.revocationReady = true
                rows[id] = device
                if device.label == "Uncertain attachment" { return try failure("source_unavailable", 503) }
                return try json(device)
            }
            guard var device = rows[id] else { return try failure("not_found_or_inaccessible", 404) }
            if path.hasSuffix("/token") {
                let object = try JSONSerialization.jsonObject(with: body) as! [String: Any]
                guard Set(object.keys) == ["installationSecret", "token", "permission"] else { return try failure("invalid_request", 400) }
                let input = try JSONDecoder().decode(DashboardAPI.DeviceTokenInput.self, from: body)
                device.permission = input.permission
            } else if path.hasSuffix("/settings") {
                let input = try JSONDecoder().decode(DashboardAPI.DeviceSettingsInput.self, from: body)
                device.label = input.label; device.enabled = input.enabled; device.muted = input.muted
            } else { return try failure("invalid_request", 400) }
            if id == NativeDeviceFixtures.installationID { revision += 1; device.revision = String(revision) }
            else { device.revision = String(Int(device.revision)! + 1) }
            rows[id] = device
            return try json(device)
        }
    }
    private static func body(_ request: URLRequest) throws -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open(); defer { stream.close() }
        var data = Data(), bytes = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable { let count = stream.read(&bytes, maxLength: bytes.count); guard count >= 0, data.count + max(count, 0) <= 16_384 else { throw DashboardError.invalidResponse }; if count == 0 { break }; data.append(contentsOf: bytes.prefix(count)) }
        return data
    }
}
#endif
