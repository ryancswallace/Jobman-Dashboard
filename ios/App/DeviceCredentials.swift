import DashboardCore
import Foundation
import Security

/// Installation proof survives account switches, but never crosses the configured
/// Dashboard origin, topic or APNs environment. No shared/synchronizable Keychain.
@MainActor enum DeviceCredentials {
    static func installation(for configuration: DeviceConfiguration) throws -> DeviceInstallation {
        let key = "installations", identity = configuration.storageKey
        var installations: [String: DeviceInstallation] = [:]
        if let data = try read(key) { installations = try JSONDecoder().decode([String: DeviceInstallation].self, from: data) }
        guard installations.count <= 32 else { throw DeviceInputError.secureStorage }
        if let value = installations[identity] { return value }
        guard installations.count < 32 else { throw DashboardError.deviceCapacity }
        var bytes = Data(count: 32)
        let status = bytes.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, 32, $0.baseAddress!) }
        guard status == errSecSuccess else { throw DeviceInputError.secureStorage }
        let installation = try DeviceInstallation(id: UUID().uuidString.lowercased(), randomBytes: bytes)
        installations[identity] = installation
        try write(JSONEncoder().encode(installations), key: key)
        return installation
    }
    static func read(_ key: String) throws -> Data? {
        var query = query(key)
        query[kSecReturnData as String] = true; query[kSecMatchLimit as String] = kSecMatchLimitOne
        var value: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &value)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = value as? Data, data.count <= 128 * 1024 else { throw DeviceInputError.secureStorage }
        return data
    }
    static func write(_ data: Data, key: String) throws {
        guard data.count <= 128 * 1024 else { throw DeviceInputError.secureStorage }
        let attributes: [String: Any] = [kSecValueData as String: data, kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        let status = SecItemUpdate(query(key) as CFDictionary, attributes as CFDictionary)
        if status == errSecSuccess { return }
        guard status == errSecItemNotFound else { throw DeviceInputError.secureStorage }
        var item = query(key); item.merge(attributes) { _, value in value }
        guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else { throw DeviceInputError.secureStorage }
    }
    private static func query(_ key: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: "JobmanDashboard.Installation.v1", kSecAttrAccount as String: key,
         kSecAttrSynchronizable as String: false]
    }
}
