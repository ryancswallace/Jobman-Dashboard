import Foundation

extension DashboardAPI.PresentationTheme {
    public func validate() throws {
        for (pair, surfaces) in [(light, ["#ffffff", "#f7f8fc", "#fafafe", "#efebfb"]),
                                 (dark, ["#000000", "#14141d", "#1c1c28", "#222230", "#312645"])] {
            guard pair.accent.range(of: "^#[0-9a-fA-F]{6}$", options: .regularExpression) != nil,
                  pair.foreground.range(of: "^#[0-9a-fA-F]{6}$", options: .regularExpression) != nil,
                  Self.contrast(pair.accent, pair.foreground) >= 4.5,
                  surfaces.allSatisfy({ Self.contrast(pair.accent, $0) >= 4.5 }) else { throw DashboardError.unsupportedContract }
        }
    }
    private static func luminance(_ value: String) -> Double {
        let number = UInt32(value.dropFirst(), radix: 16) ?? 0
        func channel(_ shift: UInt32) -> Double {
            let value = Double((number >> shift) & 255) / 255
            return value <= 0.04045 ? value / 12.92 : pow((value + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * channel(16) + 0.7152 * channel(8) + 0.0722 * channel(0)
    }
    private static func contrast(_ first: String, _ second: String) -> Double {
        let x = luminance(first), y = luminance(second)
        return (max(x, y) + 0.05) / (min(x, y) + 0.05)
    }
}
