import DashboardCore
import Foundation
import Observation
import UIKit
import UserNotifications

struct NativeDeviceContext: Equatable, Sendable {
    let accountID: String
    let session: UUID
    let origin: URL
}

@MainActor @Observable final class NativeDeviceController {
    private(set) var items: [NotificationDevice] = []
    private(set) var ownership: InstallationOwnership?
    private(set) var installationID: String?
    private(set) var permission = "not_checked"
    private(set) var busy = false
    private(set) var error: String?
    private(set) var mustReload = false
    private(set) var tokenAvailable = false
    private(set) var offlineProtected = false
    private var generation = UUID()
    private var context: NativeDeviceContext?
    private var configuration: DeviceConfiguration?
    private var installation: DeviceInstallation?
    private var lastRefresh: String?

    var current: NotificationDevice? { items.first { $0.id == installationID } }
    func clear() {
        if context != nil {
            do { try DeviceRevocations.cancelPending(); Task { await DeviceRevocations.flush() } }
            catch { /* Sign-out separately reports any inability to persist unbinding. */ }
        }
        generation = UUID(); context = nil; configuration = nil; installation = nil
        installationID = nil; items = []; ownership = nil; error = nil; mustReload = false; busy = false; tokenAvailable = false; permission = "not_checked"; lastRefresh = nil; offlineProtected = false
    }
    private func prepare(store: DashboardStore) throws -> (NativeDeviceContext, DeviceConfiguration, DeviceInstallation) {
        guard let target = store.deviceContext else { throw CancellationError() }
        if context != target { clear(); context = target }
        let config = try DeviceConfiguration(origin: target.origin, topic: Bundle.main.bundleIdentifier ?? "", entitlementEnvironment: Bundle.main.object(forInfoDictionaryKey: "APNSEnvironment") as? String ?? "")
        let proof: DeviceInstallation
        #if DEBUG
        if store.previewMode { proof = try DeviceInstallation(id: "90000000-0000-4000-8000-000000000001", randomBytes: Data(repeating: 7, count: 32)) }
        else { proof = try DeviceCredentials.installation(for: config) }
        #else
        proof = try DeviceCredentials.installation(for: config)
        #endif
        configuration = config; installation = proof; installationID = proof.id
        return (target, config, proof)
    }
    private func valid(_ request: UUID, target: NativeDeviceContext, store: DashboardStore) -> Bool {
        generation == request && context == target && store.deviceContext == target && !Task.isCancelled
    }
    private func systemPermission(preview: Bool) async -> String {
        #if DEBUG
        if preview { return NativeDeviceFixtures.enabled ? "authorized" : "not_determined" }
        #endif
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        switch settings.authorizationStatus {
        case .notDetermined: return "not_determined"
        case .denied: return "denied"
        case .authorized: return "authorized"
        case .provisional: return "provisional"
        case .ephemeral: return "ephemeral"
        @unknown default: return "unknown"
        }
    }
    func load(store: DashboardStore, refreshToken: Bool = false) async {
        guard !busy, let requestedContext = store.deviceContext else { return }
        var requestID: UUID?
        do {
            let (target, config, proof) = try prepare(store: store)
            let request = generation; requestID = request
            busy = true; error = nil; mustReload = false; items = []; ownership = nil; offlineProtected = false
            defer { if generation == request { busy = false } }
            let osPermission = await systemPermission(preview: store.previewMode)
            guard valid(request, target: target, store: store) else { return }
            #if DEBUG
            if NativeDeviceFixtures.enabled { PushRegistration.token = "aabb1122" }
            #endif
            permission = osPermission; tokenAvailable = PushRegistration.token.map(DeviceInstallation.validToken) ?? false
            let state: DashboardAPI.InstallationState?
            do { state = try await store.generatedAccountRequest { try await $0.inspectInstallation(installationId: proof.id, body: proof.proof) } }
            catch DashboardError.notFound { state = nil }
            var currentOwnership = try InstallationOwnership(state: state, installationID: proof.id)
            var page = try await store.generatedAccountRequest { try await $0.devices() }
            try page.validate()
            guard valid(request, target: target, store: store) else { return }
            if currentOwnership.refreshRevision != nil {
                guard let device = page.items.first(where: { $0.id == proof.id }) else { throw DeviceInputError.recheckRequired }
                try device.validate(expectedID: proof.id, configuration: config)
                var currentRevision = device.revision
                if let saved = try DeviceRevocations.read().active(configuration: config, accountID: target.accountID, installationID: proof.id) {
                    do {
                        let receipt = try await store.generatedAccountRequest { try await $0.reserveDeviceRevocation(installationId: proof.id, headers: .init(ifMatch: device.revision), body: proof.revocationInput(saved.capability)) }
                        try receipt.validate(installationID: proof.id, capability: saved.capability)
                        guard valid(request, target: target, store: store) else { return }
                        currentRevision = receipt.revision; offlineProtected = true
                        page.items = page.items.map { var item = $0; if item.id == proof.id { item.revision = receipt.revision }; return item }
                    } catch DashboardError.revisionConflict {
                        try DeviceRevocations.queue(saved.capability); Task { await DeviceRevocations.flush() }
                    }
                }
                currentOwnership = .current(revision: currentRevision)
                if refreshToken, let token = PushRegistration.token, DeviceInstallation.permissions.contains(osPermission), lastRefresh != token + ":" + osPermission {
                    let input = try proof.tokenInput(token: token, permission: osPermission)
                    let refreshRevision = currentRevision
                    let value = try await store.generatedAccountRequest { try await $0.refreshDeviceToken(installationId: proof.id, headers: .init(ifMatch: refreshRevision), body: input) }
                    try value.validate(expectedID: proof.id, configuration: config)
                    guard valid(request, target: target, store: store) else { return }
                    page.items = page.items.map { $0.id == proof.id ? value : $0 }
                    currentOwnership = .current(revision: value.revision); lastRefresh = token + ":" + osPermission
                }
            } else if page.items.contains(where: { $0.id == proof.id }) { throw DeviceInputError.recheckRequired }
            guard valid(request, target: target, store: store) else { return }
            if case .unbound = currentOwnership { try DeviceRevocations.confirmedRemoval(configuration: config, installationID: proof.id) }
            ownership = currentOwnership; items = page.items
        } catch is CancellationError { }
        catch {
            guard store.deviceContext == requestedContext, requestID == nil || requestID == generation else { return }
            self.error = error.localizedDescription; ownership = nil; items = []; mustReload = true
        }
    }
    func requestPermission(store: DashboardStore) async {
        guard !busy, let target = store.deviceContext else { return }
        guard !store.previewMode else { error = "Synthetic previews never request system notification permission or register with APNs."; return }
        let request = generation; busy = true; error = nil
        defer { if generation == request { busy = false } }
        do {
            _ = try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound])
            await PushRegistration.refreshIfAllowed(preview: store.previewMode)
            let updated = await systemPermission(preview: false)
            guard valid(request, target: target, store: store) else { return }
            permission = updated
        } catch { if valid(request, target: target, store: store) { self.error = error.localizedDescription } }
    }
    func attach(store: DashboardStore, draft: DeviceSettingsDraft, intent: String) async -> Bool {
        guard !busy, !mustReload, let target = context, target == store.deviceContext,
              let config = configuration, let proof = installation, let ownership,
              ownership.intent == intent, intent == "bind" || intent == "switch" else { return false }
        return await changeBinding(store: store, target: target, config: config, proof: proof, ownership: ownership, draft: draft)
    }
    func secureExisting(store: DashboardStore) async {
        guard !busy, !mustReload, let target = context, target == store.deviceContext,
              let config = configuration, let proof = installation, let ownership, ownership.intent == "existing" else { return }
        _ = await changeBinding(store: store, target: target, config: config, proof: proof, ownership: ownership, draft: .init())
    }
    private func changeBinding(store: DashboardStore, target: NativeDeviceContext, config: DeviceConfiguration,
                               proof: DeviceInstallation, ownership: InstallationOwnership, draft: DeviceSettingsDraft) async -> Bool {
        let request = generation; busy = true; error = nil
        var capability: DeviceRevocationCapability?
        var succeeded = false
        defer {
            if !succeeded, let capability {
                do { try DeviceRevocations.queue(capability); Task { await DeviceRevocations.flush() } }
                catch { if generation == request { self.error = "Secure unbinding could not be queued. Sign out locally, reconnect, and remove this installation before relying on notification privacy." } }
            }
            if generation == request { busy = false }
        }
        do {
            let token = PushRegistration.token
            if ownership.intent != "existing" { guard let token, DeviceInstallation.validToken(token) else { throw DeviceInputError.unavailableToken }; _ = try draft.input() }
            let cap = try DeviceRevocations.begin(configuration: config, accountID: target.accountID, installationID: proof.id, existing: ownership.intent == "existing")
            capability = cap
            let receipt = try await store.generatedAccountRequest {
                try await $0.reserveDeviceRevocation(installationId: proof.id, headers: .init(ifMatch: ownership.revision, ifNoneMatch: ownership.revision == nil ? "*" : nil), body: proof.revocationInput(cap))
            }
            try receipt.validate(installationID: proof.id, capability: cap, intent: ownership.intent)
            guard valid(request, target: target, store: store) else { return false }
            try DeviceRevocations.acknowledge(cap, receipt: receipt)
            guard valid(request, target: target, store: store) else { return false }
            if ownership.intent == "existing" {
                let activated = try await store.generatedAccountRequest { try await $0.activateDeviceRevocation(installationId: proof.id, headers: .init(ifMatch: receipt.revision), body: proof.revocationInput(cap)) }
                try activated.validate(installationID: proof.id, capability: cap, intent: "existing")
                guard valid(request, target: target, store: store) else { return false }
                try DeviceRevocations.activate(cap)
                succeeded = true // The acknowledged capability survives an uncertain subsequent read.
                let page = try await store.generatedAccountRequest { try await $0.devices() }
                try page.validate()
                guard let device = page.items.first(where: { $0.id == proof.id }) else { throw DeviceInputError.recheckRequired }
                try device.validate(expectedID: proof.id, configuration: config)
                guard valid(request, target: target, store: store) else { return false }
                self.ownership = .current(revision: device.revision); items = page.items
            } else {
                let permission = await systemPermission(preview: store.previewMode)
                guard valid(request, target: target, store: store) else { return false }
                let device: NotificationDevice
                if ownership.intent == "bind" {
                    let input = try proof.bindInput(configuration: config, capability: cap, token: token!, permission: permission, label: draft.label, enabled: draft.enabled, muted: draft.muted)
                    device = try await store.generatedAccountRequest { try await $0.bindDevice(installationId: proof.id, headers: .init(ifMatch: receipt.revision), body: input) }
                } else {
                    let input = try proof.switchInput(configuration: config, capability: cap, token: token!, permission: permission, label: draft.label, enabled: draft.enabled, muted: draft.muted)
                    device = try await store.generatedAccountRequest { try await $0.switchDeviceBinding(installationId: proof.id, headers: .init(ifMatch: receipt.revision), body: input) }
                }
                try device.validate(expectedID: proof.id, configuration: config)
                guard device.revocationReady else { throw DashboardError.invalidResponse }
                guard valid(request, target: target, store: store) else { return false }
                try DeviceRevocations.activate(cap)
                items.removeAll { $0.id == proof.id }; items.insert(device, at: 0)
                self.ownership = .current(revision: device.revision)
            }
            offlineProtected = true; succeeded = true
            return true
        } catch is CancellationError { return false }
        catch {
            if valid(request, target: target, store: store) { self.error = error.localizedDescription; mustReload = true }
            return false
        }
    }
    func registrationFailed(_ message: String) { tokenAvailable = false; error = message }
    func settings(store: DashboardStore, device: NotificationDevice, draft: DeviceSettingsDraft) async -> Bool {
        guard !busy, !mustReload, let target = context, target == store.deviceContext else { return false }
        let request = generation; busy = true; error = nil
        defer { if generation == request { busy = false } }
        do {
            let input = try draft.input()
            let value = try await store.generatedAccountRequest { try await $0.updateDeviceSettings(installationId: device.id, headers: .init(ifMatch: device.revision), body: input) }
            try value.validate(expectedID: device.id)
            guard valid(request, target: target, store: store) else { return false }
            items = items.map { $0.id == value.id ? value : $0 }
            if value.id == installationID { ownership = .current(revision: value.revision) }
            return true
        } catch is CancellationError { return false }
        catch { if valid(request, target: target, store: store) { self.error = error.localizedDescription; mustReload = true }; return false }
    }
    func remove(store: DashboardStore, device: NotificationDevice) async -> Bool {
        guard !busy, !mustReload, let target = context, target == store.deviceContext else { return false }
        let request = generation; busy = true; error = nil
        defer { if generation == request { busy = false } }
        do {
            try await store.generatedAccountRequest { try await $0.removeDevice(installationId: device.id, headers: .init(ifMatch: device.revision)) }
            guard valid(request, target: target, store: store) else { return false }
            items.removeAll { $0.id == device.id }
            if device.id == installationID {
                ownership = nil; mustReload = true; offlineProtected = false
                if let configuration { try DeviceRevocations.confirmedRemoval(configuration: configuration, installationID: device.id) }
            }
            return true
        } catch is CancellationError { return false }
        catch { if valid(request, target: target, store: store) { self.error = error.localizedDescription; mustReload = true }; return false }
    }
}
