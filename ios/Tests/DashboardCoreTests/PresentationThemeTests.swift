import Testing
@testable import DashboardCore

@Test func brandThemeRequiresReadablePairsAndFixedSurfaceContrast() throws {
    let theme = DashboardAPI.PresentationTheme(light: .init(accent: "#00675f", foreground: "#ffffff"), dark: .init(accent: "#78d9cb", foreground: "#14141d"))
    try theme.validate()
    for pair in [DashboardAPI.PresentationAccentPair(accent: "red", foreground: "#ffffff"),
                 .init(accent: "#ffffff", foreground: "#000000"),
                 .init(accent: "#00675f;url(x)", foreground: "#ffffff"),
                 .init(accent: "#00675f", foreground: "#00675f")] {
        let bad = DashboardAPI.PresentationTheme(light: pair, dark: theme.dark)
        #expect(throws: DashboardError.self) { try bad.validate() }
    }
    let dark = DashboardAPI.PresentationTheme(light: theme.light, dark: .init(accent: "#14141d", foreground: "#ffffff"))
    #expect(throws: DashboardError.self) { try dark.validate() }
}
