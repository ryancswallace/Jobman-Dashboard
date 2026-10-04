import DashboardCore
import UIKit
import UserNotifications

extension Notification.Name {
    static let dashboardInboxOpened = Notification.Name("JobmanDashboard.inboxOpened")
    static let dashboardPushRegistrationFailed = Notification.Name("JobmanDashboard.pushRegistrationFailed")
    static let dashboardPushRegistered = Notification.Name("JobmanDashboard.pushRegistered")
}

final class NotificationDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    private var pendingInboxID: String?
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--dashboard-ui-fixtures") { return true }
        #endif
        Task { @MainActor in
            await PushRegistration.refreshIfAllowed(preview: false)
        }
        return true
    }
    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        let token = deviceToken.map { String(format: "%02x", $0) }.joined()
        guard DeviceInstallation.validToken(token) else { return }
        PushRegistration.token = token
        NotificationCenter.default.post(name: .dashboardPushRegistered, object: nil, userInfo: ["token": token])
    }
    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        PushRegistration.token = nil
        NotificationCenter.default.post(name: .dashboardPushRegistrationFailed, object: nil)
    }
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        let payload = response.notification.request.content.userInfo
        // Only an opaque inbox ID crosses this boundary; no job metadata is trusted from APNs.
        if case .inbox(let id) = InboxPushRoute.route(schemaVersion: payload["schemaVersion"] as? Int, inboxID: payload["inboxId"] as? String) {
            DispatchQueue.main.async {
                self.pendingInboxID = id
                NotificationCenter.default.post(name: .dashboardInboxOpened, object: nil, userInfo: ["inboxId": id])
            }
        }
        completionHandler()
    }
    func consumePendingInbox() -> String? {
        defer { pendingInboxID = nil }
        return pendingInboxID
    }
}

@MainActor
enum PushRegistration {
    static var token: String?
    static func refreshIfAllowed(preview: Bool) async {
        guard !preview else { return }
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        let permission: String
        switch settings.authorizationStatus {
        case .authorized: permission = "authorized"
        case .provisional: permission = "provisional"
        case .ephemeral: permission = "ephemeral"
        case .denied: permission = "denied"
        case .notDetermined: permission = "not_determined"
        @unknown default: permission = "unknown"
        }
        guard NotificationRegistrationPolicy.shouldRegister(permission: permission, appActive: UIApplication.shared.applicationState == .active, fixtureMode: preview) else { return }
        UIApplication.shared.registerForRemoteNotifications()
    }
}
