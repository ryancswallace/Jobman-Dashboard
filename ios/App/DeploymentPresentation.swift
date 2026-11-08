import DashboardCore
import Foundation
import SwiftUI
import UIKit

extension DashboardAPI.Presentation {
    static var appDefault: Self {
        .init(schemaVersion: 1, productName: NativeBuildSettings.productName, organizationName: "", authLabel: NativeBuildSettings.authLabel,
              headerText: "", footerText: "Private workspace", logoPath: "/logo.svg", logoDarkPath: "/logo-transparent-dashboard.svg",
              contentLinkScheme: NativeBuildSettings.contentScheme, helpLinks: [], notices: [])
    }
    func validateForApp() throws {
        try theme?.validate()
        func text(_ value: String, _ limit: Int) -> Bool { value.utf8.count <= limit && value.rangeOfCharacter(from: .controlCharacters) == nil }
        guard schemaVersion == 1, !productName.isEmpty, text(productName, 120), text(organizationName, 200),
              !authLabel.isEmpty, text(authLabel, 120), text(headerText, 1000), text(footerText, 1000),
              contentLinkScheme == NativeBuildSettings.contentScheme, helpLinks.count <= 8, notices.count <= 8,
              Set(notices.map(\.id)).count == notices.count else { throw DashboardError.unsupportedContract }
        for notice in notices {
            guard !notice.id.isEmpty, text(notice.id, 64), !notice.text.isEmpty, text(notice.text, 4000), ["info", "warning"].contains(notice.severity) else { throw DashboardError.unsupportedContract }
        }
        for link in helpLinks {
            guard !link.label.isEmpty, text(link.label, 120), text(link.url, 2048), let url = URL(string: link.url),
                  url.scheme == "https", url.host != nil, url.user == nil, url.password == nil else { throw DashboardError.unsupportedContract }
        }
    }
    var brandTint: Color {
        guard let theme else { return Color(red: 0.12, green: 0.43, blue: 0.42) }
        return Color(uiColor: UIColor { traits in
            let pair = traits.userInterfaceStyle == .dark ? theme.dark : theme.light
            let value = UInt32(pair.accent.dropFirst(), radix: 16) ?? 0
            return UIColor(red: CGFloat((value >> 16) & 255) / 255, green: CGFloat((value >> 8) & 255) / 255,
                           blue: CGFloat(value & 255) / 255, alpha: 1)
        })
    }
}

struct DeploymentNotices: View {
    @Environment(DashboardStore.self) private var store
    var body: some View {
        if !store.presentation.headerText.isEmpty { Text(store.presentation.headerText).font(.callout).padding(8) }
        ForEach(store.presentation.notices, id: \.id) { notice in
            Text(notice.text).font(.footnote).frame(maxWidth: .infinity, alignment: .leading).padding(8)
                .background(notice.severity == "warning" ? Color.yellow.opacity(0.15) : Color.secondary.opacity(0.08))
        }
    }
}
