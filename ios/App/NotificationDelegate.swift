import UIKit
import UserNotifications

extension Notification.Name {
    static let dashboardInboxOpened = Notification.Name("JobmanDashboard.inboxOpened")
    static let dashboardPushRegistered = Notification.Name("JobmanDashboard.pushRegistered")
}

final class NotificationDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }
    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        let token = deviceToken.map { String(format: "%02x", $0) }.joined()
        NotificationCenter.default.post(name: .dashboardPushRegistered, object: nil, userInfo: ["token": token])
    }
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        let payload = response.notification.request.content.userInfo
        // Only an opaque inbox ID crosses this boundary; no job metadata is trusted from APNs.
        if (payload["schemaVersion"] as? Int) == 1, let id = payload["inboxId"] as? String,
           !id.isEmpty, id.utf8.count <= 256, id.rangeOfCharacter(from: .controlCharacters) == nil {
            DispatchQueue.main.async { NotificationCenter.default.post(name: .dashboardInboxOpened, object: nil, userInfo: ["inboxId": id]) }
        }
        completionHandler()
    }
}
