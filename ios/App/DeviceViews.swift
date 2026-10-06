import DashboardCore
import SwiftUI

struct DevicesView: View {
    @Environment(DashboardStore.self) private var store
    @State private var editor: DeviceEditorSelection?
    private var controller: NativeDeviceController { store.devices }
    var body: some View {
        List {
            Section("This iPhone") {
                LabeledContent("System permission", value: InterfaceText.notificationPermission(controller.permission))
                LabeledContent("Apple registration", value: controller.tokenAvailable ? "Registered with Apple" : "Not registered yet")
                Button("Request notification permission") { Task { await controller.requestPermission(store: store) } }
                    .disabled(controller.busy)
                Text("Allowing notifications in iOS does not connect this phone to your Dashboard account. Connect it below to receive updates. Job details still require current access.").font(.footnote).foregroundStyle(.secondary)
                if let ownership = controller.ownership {
                    switch ownership {
                    case .absent, .unbound:
                        Text("This phone is not connected to an account for notifications.")
                        Button("Connect this iPhone") { editor = .init(device: nil, mode: "bind") }
                            .accessibilityIdentifier("device-bind").disabled(!controller.tokenAvailable || controller.busy || controller.mustReload)
                    case .otherAccount:
                        Text("This phone receives notifications for another account. Switching stops those updates on this phone.")
                        Button("Switch to this account") { editor = .init(device: nil, mode: "switch") }
                            .accessibilityIdentifier("device-switch").disabled(!controller.tokenAvailable || controller.busy || controller.mustReload)
                    case .current:
                        Text("Connected to this account for notifications.")
                        if !controller.offlineProtected {
                            Text("Setup to stop notifications after an offline sign-out is incomplete. Finish setup before using background notifications.").font(.footnote)
                            Button("Complete secure setup") { Task { await controller.secureExisting(store: store) } }
                                .disabled(controller.busy || controller.mustReload)
                        } else { Label("Ready to stop notifications after offline sign-out", systemImage: "lock.shield").font(.footnote) }
                    }
                }
                if controller.busy { ProgressView("Checking device state…") }
                if let error = controller.error { ErrorMessage(error: error) }
                Button("Refresh devices") { Task { await controller.load(store: store) } }.disabled(controller.busy).accessibilityIdentifier("devices-reload")
            }
            Section("Your registered devices") {
                if controller.items.isEmpty && !controller.busy { Text("No registered devices are available.").foregroundStyle(.secondary) }
                ForEach(controller.items) { device in
                    Button { editor = .init(device: device, mode: "settings") } label: {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(device.label).font(.headline)
                            Text(device.id == controller.installationID ? "This iPhone" : "Another device").font(.caption)
                            Text("\(device.enabled ? "Enabled" : "Disabled") · \(device.muted ? "Muted" : "Not muted") · \(InterfaceText.notificationRegistration(device.tokenStatus))").font(.subheadline)
                            if !device.revocationReady { Text("Setup incomplete — notifications cannot be sent").font(.caption) }
                            TimestampRow(label: "Last seen", value: device.lastSeenAt)
                        }.foregroundStyle(.primary)
                    }.disabled(controller.busy || controller.mustReload).accessibilityIdentifier("device-row-\(device.id)")
                }
            }
            Section {
                Text("Refreshing keeps each device’s enabled and muted settings. A removed device must be connected again before it can receive notifications.").font(.footnote).foregroundStyle(.secondary)
            }
        }
        .navigationTitle("Notification devices")
        .task { await controller.load(store: store, refreshToken: true) }
        .sheet(item: $editor) { selection in DeviceEditorView(selection: selection) }
        .onChange(of: store.contentGeneration) { _, _ in editor = nil }
        .onChange(of: store.active) { _, active in if !active { editor = nil } }
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
                    TextField("Device name", text: $draft.label).focused($labelFocused).accessibilityIdentifier("device-label")
                    Toggle("Enable background alerts", isOn: $draft.enabled)
                    Toggle("Mute this device", isOn: $draft.muted)
                }
                Section {
                    if selection.mode == "switch" { Text("Notifications on this phone will switch to your signed-in account. Other devices are unchanged.") }
                    else if selection.mode == "bind" { Text("Connect this iPhone to your signed-in account. To receive notifications, you also need alert rules and iOS notification permission.") }
                    else if let device = selection.device {
                        LabeledContent("System permission", value: InterfaceText.notificationPermission(device.permission))
                        LabeledContent("Apple registration", value: InterfaceText.notificationRegistration(device.tokenStatus))
                        LabeledContent("Record version", value: device.revision)
                        Button("Remove device", role: .destructive) { confirmRemove = true }.accessibilityIdentifier("device-remove")
                    }
                    if saving { ProgressView("Saving device…").accessibilityIdentifier("device-saving") }
                    if let error = store.devices.error { ErrorMessage(error: error) }
                    if store.devices.mustReload { Text("Close this form, refresh devices, and review their current settings before another change.").font(.footnote) }
                }
            }.disabled(saving)
                .navigationTitle(selection.mode == "settings" ? "Device settings" : selection.mode == "switch" ? "Switch account" : "Connect iPhone")
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
                } message: { Text("Notifications to this device will stop. You must connect it again to resume them.") }
                .confirmationDialog(selection.mode == "switch" ? "Switch this iPhone to the signed-in account?" : "Connect this iPhone?", isPresented: $confirmAttach, titleVisibility: .visible) {
                    Button(selection.mode == "switch" ? "Confirm switch" : "Confirm connection") { Task { await save() } }
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
