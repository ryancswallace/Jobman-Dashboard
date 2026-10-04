import Foundation
import Testing
@testable import DashboardCore

private let ledgerInstallation = "90000000-0000-4000-8000-000000000001"
private func ledgerConfig() throws -> DeviceConfiguration { try .init(origin: URL(string: "https://dashboard.example.test")!, topic: "org.jobman.dashboard", entitlementEnvironment: "development") }
private func capability(_ n: Int) throws -> DeviceRevocationCapability { try .init(id: String(format: "91000000-0000-4000-8000-%012d", n), randomBytes: Data(repeating: UInt8(n), count: 32)) }

@Test func pendingAdmissionCannotBecomeActiveAfterSignOutOrBeforeAcknowledgment() throws {
    var ledger = DeviceRevocationLedger(); let cap = try capability(1)
    try ledger.begin(configuration: ledgerConfig(), accountID: "account-a", installationID: ledgerInstallation, capability: cap, existing: false)
    #expect(throws: CancellationError.self) { try ledger.activate(cap.id) }
    let queued = ledger.queueAll(); #expect(queued)
    #expect(throws: CancellationError.self) { try ledger.acknowledge(cap.id, revision: "1", intent: "bind") }
    #expect(throws: CancellationError.self) { try ledger.activate(cap.id) }
    ledger.complete(cap.id) // Unknown204 cannot race an admission that requires acknowledgment.
    #expect(ledger.entries.isEmpty)
}
@Test func acknowledgedBindingQueuesWithoutTokensAndCannotAffectNewGeneration() throws {
    var ledger = DeviceRevocationLedger(); let old = try capability(2), new = try capability(3)
    try ledger.begin(configuration: ledgerConfig(), accountID: "account-a", installationID: ledgerInstallation, capability: old, existing: false)
    try ledger.acknowledge(old.id, revision: "1", intent: "bind"); try ledger.activate(old.id); ledger.queue(old.id)
    try ledger.begin(configuration: ledgerConfig(), accountID: "account-b", installationID: ledgerInstallation, capability: new, existing: false)
    try ledger.acknowledge(new.id, revision: "1", intent: "bind"); try ledger.activate(new.id)
    ledger.complete(old.id)
    #expect(ledger.entries.count == 1 && ledger.entries[0].capability == new && !ledger.entries[0].queued)
    #expect(try ledger.active(configuration: ledgerConfig(), accountID: "account-a", installationID: ledgerInstallation)?.capability == nil)
    let object = try JSONSerialization.jsonObject(with: JSONEncoder().encode(old.request)) as! [String: Any]
    #expect(Set(object.keys) == ["revocationId", "revocationCredential"])
    #expect(!String(describing: old).contains(old.request.revocationCredential))
}
@Test func legacyUnknownRevocationRemainsExplicitlyUnconfirmedAndLedgerIsBounded() throws {
    var ledger = DeviceRevocationLedger()
    for n in 1...32 {
        try ledger.begin(configuration: ledgerConfig(), accountID: "account-a", installationID: ledgerInstallation, capability: capability(n), existing: n == 1)
    }
    #expect(throws: DashboardError.deviceCapacity) { try ledger.begin(configuration: ledgerConfig(), accountID: "account-a", installationID: ledgerInstallation, capability: capability(33), existing: false) }
    let queued = ledger.queueAll(); #expect(queued) // Capacity is reserved before admission, so sign-out cannot overflow it.
    for n in 1...32 { ledger.complete(try capability(n).id) }
    #expect(ledger.entries.count == 1 && ledger.hasUnconfirmedExisting && ledger.entries[0].accountID == nil)
    let restored = try JSONDecoder().decode(DeviceRevocationLedger.self, from: JSONEncoder().encode(ledger))
    #expect(restored.hasUnconfirmedExisting)
}
@Test func sceneCancellationKeepsActiveCapabilityButCancelsUnfinishedReservation() throws {
    var ledger = DeviceRevocationLedger(); let active = try capability(1), pending = try capability(2)
    for cap in [active, pending] { try ledger.begin(configuration: ledgerConfig(), accountID: "account-a", installationID: ledgerInstallation, capability: cap, existing: true); try ledger.acknowledge(cap.id, revision: "1", intent: "bind") }
    try ledger.activate(active.id)
    let queued = ledger.queuePending(); #expect(queued)
    #expect(!ledger.entries[0].queued && ledger.entries[1].queued)
    let receipt = DashboardAPI.DeviceRevocationReceipt(installationId: ledgerInstallation, revision: "9007199254740993", revocationId: pending.id, intent: "existing")
    try receipt.validate(installationID: ledgerInstallation, capability: pending, intent: "existing")
    #expect(throws: DashboardError.invalidResponse) { try receipt.validate(installationID: ledgerInstallation, capability: active) }
    #expect(throws: DashboardError.invalidResponse) { try receipt.validate(installationID: ledgerInstallation, capability: pending, intent: "bind") }
}

@Test func confirmedRemovalClearsOnlyTheVerifiedInstallationAndOrigin() throws {
    let config = try ledgerConfig()
    let other = try DeviceConfiguration(origin: URL(string: "https://other.example.test")!, topic: config.topic, entitlementEnvironment: "development")
    var ledger = DeviceRevocationLedger()
    try ledger.begin(configuration: config, accountID: "a", installationID: ledgerInstallation, capability: capability(1), existing: true)
    try ledger.begin(configuration: other, accountID: "a", installationID: ledgerInstallation, capability: capability(2), existing: true)
    ledger.queueAll(); ledger.complete(try capability(1).id)
    #expect(ledger.entries.count == 2) // An unknown204 is not a confirmed legacy removal.
    ledger.confirmedRemoval(configuration: config, installationID: ledgerInstallation)
    #expect(ledger.entries.count == 1 && ledger.entries[0].origin == other.origin)
}
