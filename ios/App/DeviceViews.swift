import DashboardCore
import SwiftUI

struct DevicesView: View {
    @Environment(DashboardStore.self) private var store
    @State private var editor: DeviceEditorSelection?
    private var controller: NativeDeviceController { store.devices }
    var body: some View {
        List {
            Section("This iPhone") {
                LabeledContent("System permission", value: permissionLabel(controller.permission))
                LabeledContent("Apple registration", value: controller.tokenAvailable ? "Token available" : "No current token")
                Button("Request notification permission") { Task { await controller.requestPermission(store: store) } }
                    .disabled(controller.busy)
                Text("System permission alone does not attach this phone to an account. Notifications show a generic update; current access is checked when you open it.").font(.footnote).foregroundStyle(.secondary)
                if let ownership = controller.ownership {
                    switch ownership {
                    case .absent, .unbound:
                        Text("This phone is not attached to an account.")
                        Button("Attach this iPhone") { editor = .init(device: nil, mode: "bind") }
                            .accessibilityIdentifier("device-bind").disabled(!controller.tokenAvailable || controller.busy || controller.mustReload)
                    case .otherAccount:
                        Text("This phone is attached to another account. Switching stops updates for that account on this installation.")
                        Button("Switch to this account") { editor = .init(device: nil, mode: "switch") }
                            .accessibilityIdentifier("device-switch").disabled(!controller.tokenAvailable || controller.busy || controller.mustReload)
                    case .current:
                        Text("Attached to the signed-in account.")
                        if !controller.offlineProtected {
                            Text("Offline sign-out protection is not confirmed. Complete secure setup before relying on background notifications.").font(.footnote)
                            Button("Complete secure setup") { Task { await controller.secureExisting(store: store) } }
                                .disabled(controller.busy || controller.mustReload)
                        } else { Label("Offline sign-out protection confirmed", systemImage: "lock.shield").font(.footnote) }
                    }
                }
                if controller.busy { ProgressView("Checking device state…") }
                if let error = controller.error { ErrorMessage(error: error) }
                Button("Reload devices") { Task { await controller.load(store: store) } }.disabled(controller.busy).accessibilityIdentifier("devices-reload")
            }
            Section("Your registered devices") {
                if controller.items.isEmpty && !controller.busy { Text("No registered devices are available.").foregroundStyle(.secondary) }
                ForEach(controller.items) { device in
                    Button { editor = .init(device: device, mode: "settings") } label: {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(device.label).font(.headline)
                            Text(device.id == controller.installationID ? "This iPhone" : "Another installation").font(.caption)
                            Text("\(device.enabled ? "Enabled" : "Disabled") · \(device.muted ? "Muted" : "Not muted") · \(device.tokenStatus)").font(.subheadline)
                            if !device.revocationReady { Text("Secure setup incomplete — delivery unavailable").font(.caption) }
                            TimestampRow(label: "Last seen", value: device.lastSeenAt)
                        }.foregroundStyle(.primary)
                    }.disabled(controller.busy || controller.mustReload).accessibilityIdentifier("device-row-\(device.id)")
                }
            }
            Section {
                Text("Refresh preserves each device’s enabled and muted settings. Removing a device requires an explicit attachment before it can receive updates again.").font(.footnote).foregroundStyle(.secondary)
            }
        }
        .navigationTitle("Notification devices")
        .task { await controller.load(store: store, refreshToken: true) }
        .sheet(item: $editor) { selection in DeviceEditorView(selection: selection) }
        .onChange(of: store.contentGeneration) { _, _ in editor = nil }
        .onChange(of: store.active) { _, active in if !active { editor = nil } }
    }
    private func permissionLabel(_ value: String) -> String {
        switch value { case "not_checked": "Not checked"; case "not_determined": "Not requested"; case "denied": "Denied — inbox remains available"; default: value.capitalized }
    }
}

private struct DeviceEditorSelection: Identifiable {
    let id = UUID()
    let device: NotificationDevice?
    let mode: String
}
private struct DeviceEditorView: View {
    @Environment(DashboardStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    let selection: DeviceEditorSelection
    @State private var draft = DeviceSettingsDraft()
    @State private var saving = false
    @State private var confirmRemove = false
    @State private var confirmAttach = false
    @FocusState private var labelFocused: Bool
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Device label", text: $draft.label).focused($labelFocused).accessibilityIdentifier("device-label")
                    Toggle("Enable background alerts", isOn: $draft.enabled)
                    Toggle("Mute this device", isOn: $draft.muted)
                }
                Section {
                    if selection.mode == "switch" { Text("This replaces the installation’s previous account binding. Other devices are unchanged.") }
                    else if selection.mode == "bind" { Text("Attach this iPhone to your signed-in account. Alerts remain opt-in; rules and system permission are also required.") }
                    else if let device = selection.device {
                        LabeledContent("System permission", value: device.permission)
                        LabeledContent("Apple token", value: device.tokenStatus)
                        LabeledContent("Revision", value: device.revision)
                        Button("Remove device", role: .destructive) { confirmRemove = true }.accessibilityIdentifier("device-remove")
                    }
                    if saving { ProgressView("Saving device…").accessibilityIdentifier("device-saving") }
                    if let error = store.devices.error { ErrorMessage(error: error) }
                    if store.devices.mustReload { Text("Close this form, reload devices, and review the current state before another change.").font(.footnote) }
                }
            }.disabled(saving)
                .navigationTitle(selection.mode == "settings" ? "Device settings" : selection.mode == "switch" ? "Switch account" : "Attach iPhone")
                .toolbar {
                    ToolbarItemGroup(placement: .keyboard) { Spacer(); Button("Done") { labelFocused = false }.accessibilityIdentifier("device-keyboard-done") }
                    ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() }.disabled(saving).accessibilityIdentifier("device-editor-close") }
                    ToolbarItem(placement: .confirmationAction) {
                        Button(selection.mode == "settings" ? "Save" : "Continue") {
                            if selection.mode == "settings" { Task { await save() } } else { confirmAttach = true }
                        }.disabled(saving || store.devices.busy || store.devices.mustReload || (try? draft.input()) == nil)
                    }
                }
                .confirmationDialog("Remove this device?", isPresented: $confirmRemove, titleVisibility: .visible) {
                    Button("Remove device", role: .destructive) { Task { await remove() } }.accessibilityIdentifier("device-confirm-remove")
                } message: { Text("Background delivery stops. Automatic token refresh cannot attach it again.") }
                .confirmationDialog(selection.mode == "switch" ? "Switch this iPhone to the signed-in account?" : "Attach this iPhone?", isPresented: $confirmAttach, titleVisibility: .visible) {
                    Button(selection.mode == "switch" ? "Confirm switch" : "Confirm attachment") { Task { await save() } }
                }
        }.interactiveDismissDisabled(saving)
            .onAppear { if let device = selection.device { draft = .init(device: device) } }
    }
    private func save() async {
        saving = true
        defer { saving = false }
        let success: Bool
        if let device = selection.device { success = await store.devices.settings(store: store, device: device, draft: draft) }
        else { success = await store.devices.attach(store: store, draft: draft, intent: selection.mode) }
        if success { dismiss() }
    }
    private func remove() async {
        guard let device = selection.device else { return }
        saving = true
        defer { saving = false }
        if await store.devices.remove(store: store, device: device) { dismiss() }
    }
}
