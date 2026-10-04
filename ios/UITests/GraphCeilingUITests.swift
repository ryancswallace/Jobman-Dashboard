import XCTest

@MainActor
final class GraphCeilingUITests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }
    private func node(_ index: Int) -> String { String(format: "76000000-0000-4000-8000-%012d", index + 1) }
    private func app() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--dashboard-ui-fixtures", "--dashboard-graph-ceiling-fixtures", "--dashboard-manual-refresh-fixtures"]
        app.launch()
        XCTAssertTrue(app.staticTexts["fixtureBanner"].waitForExistence(timeout: 10))
        app.tabBars.buttons["Workloads"].tap(); app.buttons["Graphs"].tap()
        let graph = app.staticTexts["Synthetic ceiling graph"]
        XCTAssertTrue(graph.waitForExistence(timeout: 10)); graph.tap()
        XCTAssertTrue(app.staticTexts["Complete source summary"].waitForExistence(timeout: 10))
        return app
    }
    func testLargeGraphChildPagesReplaceAndKeepExactTotal() {
        let app = app(), total = app.staticTexts["childPageTotal"]
        reveal(total, app: app)
        XCTAssertEqual(total.label, "50 children on this page; 10000 at source")
        reveal(app.buttons["node-00000"], app: app)
        capture("Native ceiling first child page", app)
        let next = app.buttons["Next child page"]
        reveal(next, app: app, up: false); next.tap()
        XCTAssertTrue(app.buttons["node-00050"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["node-00000"].exists)
        XCTAssertEqual(total.label, "50 children on this page; 10000 at source")
        app.buttons["Previous child page"].tap()
        XCTAssertTrue(app.buttons["node-00000"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["node-00050"].exists)
    }
    func testLargeGraphMaximumDiagramAndAccessibleRecenter() {
        let app = app()
        openNeighborhood(app)
        let bounds = app.staticTexts["graphDiagramBounds"]
        reveal(bounds, app: app)
        XCTAssertTrue(bounds.label.contains("200 nodes and 500 edges"))
        XCTAssertEqual(app.staticTexts["graphOmissions"].label, "Not shown in this neighborhood: 9800 nodes, 99500 edges")
        capture("Native 200 node 500 edge diagram", app)
        let selected = app.buttons["diagram-node-" + node(0)]
        XCTAssertTrue(selected.exists && selected.isSelected)
        let diagram = app.switches["Show diagram"]
        reveal(diagram, app: app, up: false); hideDiagram(diagram)
        let listSelected = app.buttons["center-" + node(0)]
        reveal(listSelected, app: app)
        XCTAssertTrue(listSelected.isSelected); XCTAssertFalse(listSelected.isEnabled)
        let recenter = app.buttons["center-" + node(1)]
        reveal(recenter, app: app); recenter.tap()
        XCTAssertEqual(app.staticTexts["graphCenter"].label, node(1))
        let total = app.staticTexts["graphNeighborhoodTotal"]
        reveal(total, app: app)
        XCTAssertEqual(total.label, "Neighborhood totals: 12 nodes, 66 edges")
        XCTAssertEqual(app.staticTexts["graphOmissions"].label, "Not shown in this neighborhood: 0 nodes, 0 edges")
        capture("Native selected node and exact source counts", app)
    }
    func testLargeGraphDependencyPageDirectionAndPredicate() {
        let app = app(); openNeighborhood(app)
        app.buttons["Browse dependencies"].tap()
        let total = app.staticTexts["edgePageTotal"]
        XCTAssertTrue(total.waitForExistence(timeout: 10)); XCTAssertTrue(total.label.contains("0 matching edges"))
        app.buttons["Outgoing"].tap()
        XCTAssertTrue(app.buttons["node-00000 → node-00001"].waitForExistence(timeout: 10))
        XCTAssertEqual(total.label, "100 edges on this page; 9999 matching edges at source")
        app.buttons["Next dependency page"].tap()
        XCTAssertTrue(app.buttons["node-00000 → node-00101"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["node-00000 → node-00001"].exists)
        app.buttons["Previous dependency page"].tap()
        let edge = app.buttons["node-00000 → node-00001"]
        XCTAssertTrue(edge.waitForExistence(timeout: 10)); edge.tap()
        XCTAssertTrue(app.staticTexts["Predicate, success"].exists)
        XCTAssertTrue(app.staticTexts["Dependency state, waiting"].exists)
        capture("Native ceiling exact dependency predicate", app)
        app.buttons["Incoming"].tap()
        XCTAssertTrue(total.label.contains("0 matching edges")); XCTAssertFalse(app.buttons["Next dependency page"].exists)
        app.buttons["Both"].tap()
        XCTAssertTrue(app.buttons["node-00000 → node-00001"].waitForExistence(timeout: 10))
        XCTAssertEqual(total.label, "100 edges on this page; 9999 matching edges at source")
    }
    func testLargeGraphAccessibleTextAndMotionSettings() throws {
        // The opt-in runner captures/restores real simulator settings, even on failure.
        guard ProcessInfo.processInfo.environment["JOBMAN_GRAPH_ACCESSIBILITY"] == "1" else {
            throw XCTSkip("Requires the captured/restored per-simulator accessibility profile")
        }
        let app = app(); openNeighborhood(app)
        XCTAssertEqual(app.staticTexts["graphDisplayEnvironment"].label, "Synthetic display: Dynamic Type accessibility5; Reduce Motion enabled")
        let diagram = app.switches["Show diagram"]
        reveal(diagram, app: app); hideDiagram(diagram)
        let selected = app.buttons["center-" + node(0)]
        reveal(selected, app: app)
        XCTAssertTrue(selected.isSelected)
        capture("Native large graph accessible text list", app)
        let next = app.buttons["center-" + node(1)]
        reveal(next, app: app); next.tap()
        XCTAssertEqual(app.staticTexts["graphCenter"].label, node(1))
        reveal(app.buttons["Browse dependencies"], app: app)
        app.buttons["Browse dependencies"].tap()
        reveal(app.staticTexts["edgePageTotal"], app: app)
        XCTAssertTrue(app.staticTexts["edgePageTotal"].exists)
        capture("Native large graph accessible dependency controls", app)
    }
    private func hideDiagram(_ toggle: XCUIElement) {
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        XCTAssertTrue(NSPredicate(format: "value == %@", "0").evaluate(with: toggle), "Diagram toggle must actually turn off before list scrolling")
    }
    private func openNeighborhood(_ app: XCUIApplication) {
        let explore = app.buttons["Explore graph neighborhood"]
        reveal(explore, app: app); explore.tap()
        XCTAssertTrue(app.staticTexts["graphCenter"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["graphCenter"].label, node(0))
    }
    private func reveal(_ element: XCUIElement, app: XCUIApplication, up: Bool = true) {
        for _ in 0..<24 {
            if element.exists && element.isHittable { return }
            if up { app.swipeUp() } else { app.swipeDown() }
        }
        capture("Unreachable synthetic graph control", app)
        XCTAssertTrue(element.exists && element.isHittable)
    }
    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
    }
}
