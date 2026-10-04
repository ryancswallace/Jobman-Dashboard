import DashboardCore
import Foundation
import Security

/// One device-only Keychain value commits state transitions atomically. No bearer,
/// APNs token, namespace, job, or report content is retained in this queue.
@MainActor enum DeviceRevocations {
    private static var flushing = false
    private static var recovered = false
    #if DEBUG
    private static var fixtureLedger = DeviceRevocationLedger()
    private static var fixtureMode: Bool { ProcessInfo.processInfo.arguments.contains("--dashboard-ui-fixtures") }
    #endif
    static func read() throws -> DeviceRevocationLedger {
        #if DEBUG
        if fixtureMode { return fixtureLedger }
        #endif
        guard let data = try DeviceCredentials.read("revocations-v2") else { return .init() }
        return try JSONDecoder().decode(DeviceRevocationLedger.self, from: data)
    }
    private static func write(_ ledger: DeviceRevocationLedger) throws {
        #if DEBUG
        if fixtureMode { fixtureLedger = ledger; return }
        #endif
        try DeviceCredentials.write(JSONEncoder().encode(ledger), key: "revocations-v2")
    }
    static func begin(configuration: DeviceConfiguration, accountID: String, installationID: String, existing: Bool) throws -> DeviceRevocationCapability {
        try recoverInterrupted()
        var bytes = Data(count: 32)
        guard bytes.withUnsafeMutableBytes({ SecRandomCopyBytes(kSecRandomDefault, 32, $0.baseAddress!) }) == errSecSuccess else { throw DeviceInputError.secureStorage }
        let capability = try DeviceRevocationCapability(id: UUID().uuidString.lowercased(), randomBytes: bytes)
        var ledger = try read()
        try ledger.begin(configuration: configuration, accountID: accountID, installationID: installationID, capability: capability, existing: existing)
        try write(ledger)
        return capability
    }
    static func acknowledge(_ capability: DeviceRevocationCapability, receipt: DashboardAPI.DeviceRevocationReceipt) throws {
        var ledger = try read(); try ledger.acknowledge(capability.id, revision: receipt.revision, intent: receipt.intent); try write(ledger)
    }
    static func activate(_ capability: DeviceRevocationCapability) throws {
        var ledger = try read(); try ledger.activate(capability.id); try write(ledger)
    }
    static func queue(_ capability: DeviceRevocationCapability) throws {
        var ledger = try read(); ledger.queue(capability.id); try write(ledger)
    }
    static func confirmedRemoval(configuration: DeviceConfiguration, installationID: String) throws {
        var ledger = try read(); ledger.confirmedRemoval(configuration: configuration, installationID: installationID); try write(ledger)
    }
    static func cancelPending() throws {
        var ledger = try read()
        if ledger.queuePending() { try write(ledger) }
    }
    static func enqueueActive() throws -> Bool {
        var ledger = try read(); let queued = ledger.queueAll()
        if queued { try write(ledger) }
        return queued
    }
    static func flush() async {
        guard !flushing else { return }
        flushing = true
        defer { flushing = false }
        do { try recoverInterrupted() } catch { return }
        guard let snapshot = try? read() else { return }
        for item in snapshot.entries where item.queued {
            do {
                let client: DashboardTransport
                #if DEBUG
                if fixtureMode { client = NativeFixtures.client() }
                else { client = DashboardTransport(connection: try .init(address: item.origin.absoluteString)) }
                #else
                client = DashboardTransport(connection: try .init(address: item.origin.absoluteString))
                #endif
                let _: EmptyResponse = try await client.request(path: "/auth/native/device-revocations", token: nil, method: "POST", body: JSONEncoder().encode(item.capability.request))
                // Re-read after await: another session may have queued a different generation.
                var current = try read(); current.complete(item.capability.id); try write(current)
            } catch { /* Keep the narrow request for the next foreground connection. */ }
        }
    }
    /// A process may have died after admission but before saving its response.
    /// Cancel those acknowledged pending capabilities; never infer successful bind.
    private static func recoverInterrupted() throws {
        guard !recovered else { return }
        try cancelPending()
        recovered = true
    }
}
