import DashboardCore
import Foundation
import Security

struct DeviceBinding: Codable, Sendable {
    let address: String
    let deviceId: String
    let credential: String
}

/// The only durable queue contains narrow, irreversible unbind credentials. No access or job data.
@MainActor
enum DeviceRevocations {
    private static var flushing = false
    static func saveActive(_ binding: DeviceBinding) throws {
        if let old: DeviceBinding = try read(account: "active"),
           old.address != binding.address || old.deviceId != binding.deviceId || old.credential != binding.credential {
            _ = try enqueueActive()
        }
        try write(binding, account: "active")
    }
    static func enqueueActive() throws -> Bool {
        guard let active: DeviceBinding = try read(account: "active") else { return false }
        var queue: [DeviceBinding] = try read(account: "pending") ?? []
        if !queue.contains(where: { $0.address == active.address && $0.deviceId == active.deviceId }) { queue.append(active) }
        // A bound prevents indefinite growth if repeated offline account switching is attempted.
        guard queue.count <= 32 else { throw DashboardError.rateLimited }
        try write(queue, account: "pending")
        delete(account: "active")
        return true
    }

    static func flush() async {
        guard !flushing else { return }
        flushing = true
        defer { flushing = false }
        guard let snapshot: [DeviceBinding] = try? read(account: "pending") else { return }
        for item in snapshot {
            do {
                let connection = try DashboardConnection(address: item.address)
                struct Revocation: Encodable { let deviceId: String; let revocationCredential: String }
                let client = DashboardTransport(connection: connection)
                let _: EmptyResponse = try await client.request(path: "/auth/native/device-revocations", token: nil, method: "POST",
                                                                 body: JSONEncoder().encode(Revocation(deviceId: item.deviceId, revocationCredential: item.credential)))
                // Re-read after await so newly queued revocations are not overwritten by an old snapshot.
                var remaining: [DeviceBinding] = try read(account: "pending") ?? []
                remaining.removeAll { $0.address == item.address && $0.deviceId == item.deviceId && $0.credential == item.credential }
                try write(remaining, account: "pending")
            } catch { /* Retain only the narrow unbind request for the next foreground connection. */ }
        }
    }

    private static func query(account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: "JobmanDashboard.DeviceUnbind", kSecAttrAccount as String: account]
    }
    private static func read<T: Decodable>(account: String) throws -> T? {
        var query = query(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let result = SecItemCopyMatching(query as CFDictionary, &item)
        if result == errSecItemNotFound { return nil }
        guard result == errSecSuccess, let data = item as? Data else { throw DashboardError.authenticationRequired }
        return try JSONDecoder().decode(T.self, from: data)
    }
    private static func write<T: Encodable>(_ value: T, account: String) throws {
        let data = try JSONEncoder().encode(value)
        let attributes: [String: Any] = [kSecValueData as String: data, kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        let existing = SecItemUpdate(query(account: account) as CFDictionary, attributes as CFDictionary)
        if existing == errSecSuccess { return }
        guard existing == errSecItemNotFound else { throw DashboardError.authenticationRequired }
        var item = query(account: account)
        item.merge(attributes) { _, new in new }
        guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else { throw DashboardError.authenticationRequired }
    }
    private static func delete(account: String) { SecItemDelete(query(account: account) as CFDictionary) }
}
