import Foundation
import Testing
@testable import DashboardCore

private let installationID = "90000000-0000-4000-8000-000000000001"
private func deviceConfiguration(_ origin: String = "https://dashboard.example.test", environment: String = "development") throws -> DeviceConfiguration {
    try .init(origin: URL(string: origin)!, topic: "org.jobman.dashboard", entitlementEnvironment: environment)
}
private func deviceFixture() -> NotificationDevice {
    .init(installationId: installationID, revision: "9007199254740993", label: "Research iPhone", topic: "org.jobman.dashboard", environment: "sandbox", state: "bound", enabled: false, muted: true, permission: "authorized", tokenStatus: "current", createdAt: "2026-10-04T01:00:00Z", updatedAt: "2026-10-04T01:30:00Z", lastSeenAt: "2026-10-04T01:20:00Z", revocationReady: true)
}
@Test func installationProofIsCanonicalPrivateAndOriginScoped() throws {
    let a = try deviceConfiguration("https://DASHBOARD.example.test:443/"), b = try deviceConfiguration()
    #expect(a == b && a.storageKey == b.storageKey && a.environment == "sandbox")
    #expect(try deviceConfiguration(environment: "production").storageKey != a.storageKey)
    #expect(try deviceConfiguration("https://another.example.test").storageKey != a.storageKey)
    #expect(throws: DashboardError.invalidAddress) { try deviceConfiguration("https://dashboard.example.test/path") }
    #expect(throws: DashboardError.invalidResponse) { try deviceConfiguration(environment: "sandbox") }
    let proof = try DeviceInstallation(id: installationID, randomBytes: Data(repeating: 255, count: 32))
    let wire = proof.proof.installationSecret
    #expect(wire.count == 43 && DeviceInstallation.validSecret(wire))
    #expect(!DeviceInstallation.validSecret(wire + "="))
    #expect(!String(describing: proof).contains(wire) && !String(reflecting: proof).contains(wire))
    let restored = try JSONDecoder().decode(DeviceInstallation.self, from: JSONEncoder().encode(proof))
    #expect(restored.id == installationID && restored.proof.installationSecret == wire)
    #expect(throws: DashboardError.invalidResponse) { try DeviceInstallation(id: installationID, randomBytes: Data(repeating: 1, count: 31)) }
}
@Test func automaticRegistrationNeverCreatesOrSwitchesBindings() throws {
    #expect(try InstallationOwnership(state: nil, installationID: installationID).refreshRevision == nil)
    for state in [DashboardAPI.InstallationState(installationId: installationID, revision: "1", hasBinding: false, boundToCurrentAccount: false),
                  .init(installationId: installationID, revision: "2", hasBinding: true, boundToCurrentAccount: false)] {
        #expect(try InstallationOwnership(state: state, installationID: installationID).refreshRevision == nil)
    }
    let current = DashboardAPI.InstallationState(installationId: installationID, revision: "9007199254740993", hasBinding: true, boundToCurrentAccount: true)
    #expect(try InstallationOwnership(state: current, installationID: installationID).refreshRevision == "9007199254740993")
    #expect(throws: DashboardError.invalidResponse) { try InstallationOwnership(state: current, installationID: "90000000-0000-4000-8000-000000000002") }
    #expect(throws: DashboardError.invalidResponse) { try InstallationOwnership(state: .init(installationId: installationID, revision: "2", hasBinding: false, boundToCurrentAccount: true), installationID: installationID) }
}
@Test func deviceRefreshContainsNoDeliveryPreferenceOrBindingIntent() throws {
    let proof = try DeviceInstallation(id: installationID, randomBytes: Data(repeating: 2, count: 32))
    let refresh = try proof.tokenInput(token: "ab12", permission: "denied")
    let object = try JSONSerialization.jsonObject(with: JSONEncoder().encode(refresh)) as! [String: Any]
    #expect(Set(object.keys) == ["installationSecret", "token", "permission"])
    #expect(!DeviceInstallation.validToken("ABC123") && !DeviceInstallation.validToken("abc") && !DeviceInstallation.validToken(String(repeating: "ab", count: 513)))
    #expect(throws: DashboardError.invalidResponse) { try proof.tokenInput(token: "ab12", permission: "unknown") }
    let capability = try DeviceRevocationCapability(id: "90000000-0000-4000-8000-000000000009", randomBytes: Data(repeating: 3, count: 32))
    let bind = try proof.bindInput(configuration: deviceConfiguration(), capability: capability, token: "ab12", permission: "authorized", label: " New iPhone ", enabled: true, muted: false)
    #expect(bind.confirmBind && bind.label == "New iPhone" && bind.environment == "sandbox")
    let change = try proof.switchInput(configuration: deviceConfiguration(), capability: capability, token: "ab12", permission: "authorized", label: "iPhone", enabled: false, muted: true)
    #expect(change.confirmSwitch && !change.enabled && change.muted)
}
@Test func deviceProjectionAndSettingsCheckIdentityLimitsAndWideRevisions() throws {
    let value = deviceFixture()
    try value.validate(expectedID: installationID, configuration: deviceConfiguration())
    #expect(try DeviceSettingsDraft(device: value).input().muted)
    var invalid = value; invalid.environment = "production"
    #expect(throws: DashboardError.invalidResponse) { try invalid.validate(configuration: deviceConfiguration()) }
    invalid = value; invalid.lastSeenAt = "2026-10-04T02:00:00Z"
    #expect(throws: DashboardError.invalidResponse) { try invalid.validate() }
    #expect(throws: DashboardError.invalidResponse) { try DashboardAPI.DevicePage(items: [value, value]).validate() }
    #expect(try DeviceSettingsDraft.validatedLabel(String(repeating: "é", count: 60)).utf8.count == 120)
    #expect(throws: DeviceInputError.self) { try DeviceSettingsDraft.validatedLabel(String(repeating: "é", count: 61)) }
    #expect(DashboardError.from(code: "device_capacity", status: 429) == .deviceCapacity)
}

@Test func foregroundRegistrationSurvivesAccountInvalidationWithoutImplicitBinding() {
    for permission in DeviceInstallation.permissions + ["unknown"] {
        let allowed = ["authorized", "provisional", "ephemeral"].contains(permission)
        #expect(NotificationRegistrationPolicy.shouldRegister(permission: permission, appActive: true, fixtureMode: false) == allowed)
        #expect(!NotificationRegistrationPolicy.shouldRegister(permission: permission, appActive: false, fixtureMode: false))
        #expect(!NotificationRegistrationPolicy.shouldRegister(permission: permission, appActive: true, fixtureMode: true))
    }
    // No account or binding state is an input: the permission sheet can invalidate
    // its presenting account view while foreground registration still captures a token.
}
