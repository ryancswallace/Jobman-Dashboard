import XCTest

@MainActor
final class DashboardUITests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }

    func testConnectionScreenDoesNotOfferFakeAuthentication() {
        let app = XCUIApplication()
        app.launch()
        XCTAssertTrue(app.staticTexts["Connect to your organization"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["fixtureBanner"].exists)
        XCTAssertTrue(app.buttons["Sign in with AD FS"].exists)
    }

    func testNativeMonitoringTabsUseExplicitSyntheticFixtureMode() {
        let app = XCUIApplication()
        app.launchArguments = ["--dashboard-ui-fixtures"]
        app.launch()
        XCTAssertTrue(app.staticTexts["fixtureBanner"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Recorded job state"].waitForExistence(timeout: 10))
        app.tabBars.buttons["Workloads"].tap()
        XCTAssertTrue(app.buttons["Graphs"].waitForExistence(timeout: 5))
        app.buttons["Graphs"].tap()
        let graph = app.staticTexts["Synthetic graph"]
        XCTAssertTrue(graph.waitForExistence(timeout: 10))
        graph.tap()
        XCTAssertTrue(app.staticTexts["Complete source summary"].waitForExistence(timeout: 10))
        app.swipeUp()
        XCTAssertTrue(app.switches["Show diagram"].waitForExistence(timeout: 10))
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].firstMatch.waitForExistence(timeout: 5))
        app.tabBars.buttons["More"].tap()
        app.buttons["Settings"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic researcher"].waitForExistence(timeout: 5))
    }
}
