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
        capture("Connection", app: app)
    }

    func testNativeMonitoringTabsUseExplicitSyntheticFixtureMode() {
        let app = fixtureApp()
        XCTAssertTrue(app.staticTexts["Recorded job state"].waitForExistence(timeout: 10))
        capture("Synthetic overview", app: app)
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].firstMatch.waitForExistence(timeout: 5))
        app.tabBars.buttons["More"].tap()
        app.buttons["Settings"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic researcher"].waitForExistence(timeout: 5))
        capture("Synthetic settings", app: app)
    }

    func testGraphNeighborhoodSelectionAndDirectionalDependencyPages() {
        let app = fixtureApp()
        openWorkload("Graphs", name: "Synthetic graph", app: app)
        let explore = app.buttons["Explore graph neighborhood"]
        reveal(explore, app: app); explore.tap()
        XCTAssertTrue(app.staticTexts["graphCenter"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["graphCenter"].label, "node-0")
        let diagram = app.switches["Show diagram"]
        reveal(diagram, app: app)
        XCTAssertTrue(app.staticTexts["graphOmissions"].label.contains("2 nodes, 2 edges"))
        capture("Bounded native graph", app: app)
        diagram.tap()
        let center = app.buttons["center-node-1"]
        reveal(center, app: app); center.tap()
        XCTAssertTrue(app.staticTexts["graphCenter"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["graphCenter"].label, "node-1")
        let dependencies = app.buttons["Browse dependencies"]
        reveal(dependencies, app: app); dependencies.tap()
        XCTAssertTrue(app.staticTexts["edgePageTotal"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["edgePageTotal"].label.contains("2 matching edges"))
        XCTAssertTrue(app.buttons["stage-0 → stage-1"].exists)
        app.buttons["Next dependency page"].tap()
        XCTAssertTrue(app.buttons["stage-2 → stage-1"].waitForExistence(timeout: 10))
        app.buttons["Previous dependency page"].tap()
        XCTAssertTrue(app.buttons["stage-0 → stage-1"].waitForExistence(timeout: 10))
        app.buttons["Outgoing"].tap()
        XCTAssertTrue(app.buttons["stage-1 → stage-3"].waitForExistence(timeout: 10))
        app.buttons["Next dependency page"].tap()
        XCTAssertTrue(app.buttons["stage-1 → stage-4"].waitForExistence(timeout: 10))
        app.buttons["stage-1 → stage-4"].tap()
        XCTAssertTrue(app.staticTexts["Predicate, outcome_in"].exists)
        capture("Paged outgoing dependencies", app: app)
        app.buttons["Both"].tap()
        XCTAssertTrue(app.buttons["stage-0 → stage-1"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["edgePageTotal"].label.contains("4 matching edges"))
    }

    func testArrayPolicyExactTaskIndexAndChildPaging() {
        let app = fixtureApp()
        openWorkload("Arrays", name: "Synthetic array", app: app)
        let policy = app.descendants(matching: .any)["arrayPolicy"]
        reveal(policy, app: app)
        XCTAssertTrue(policy.label.contains("prefer_array"))
        XCTAssertTrue(app.descendants(matching: .any)["arrayMode"].label.contains("native"))
        let child = app.buttons["Task 9007199254740993 · node-1"]
        reveal(child, app: app); child.tap()
        let index = app.descendants(matching: .any)["slurmTaskIndex"]
        reveal(index, app: app)
        XCTAssertTrue(index.label.contains("9007199254740993"))
        capture("Exact Slurm task index", app: app)
        let next = app.buttons["Next child page"]
        reveal(next, app: app); next.tap()
        XCTAssertTrue(app.buttons["Task 6 · node-3"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Task 9007199254740993 · node-1"].exists)
    }

    func testArtifactProvenanceAndExpiredLogCursorRecovery() {
        let app = fixtureApp()
        app.tabBars.buttons["Jobs"].tap()
        let job = app.staticTexts["Synthetic alignment run"]
        XCTAssertTrue(job.waitForExistence(timeout: 10)); job.tap()
        let artifacts = app.buttons["Artifact metadata"]
        reveal(artifacts, app: app); artifacts.tap()
        let artifact = app.buttons["synthetic-summary.txt"]
        XCTAssertTrue(artifact.waitForExistence(timeout: 10)); artifact.tap()
        XCTAssertTrue(app.descendants(matching: .any)["artifactRunNumber"].label.contains("9007199254740993"))
        XCTAssertTrue(app.descendants(matching: .any)["artifactExecutionID"].label.contains("execution-fixture"))
        let availability = app.descendants(matching: .any)["artifactAvailability"]
        reveal(availability, app: app)
        XCTAssertTrue(availability.label.contains("metadata_only"))
        XCTAssertTrue(app.staticTexts["File bytes have not been verified. Size and checksum are published metadata."].exists)
        capture("Artifact run provenance", app: app)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let logs = app.buttons["Logs"]
        reveal(logs, app: app); logs.tap()
        let loadedText = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Synthetic stdout log fixture")).firstMatch
        XCTAssertTrue(loadedText.waitForExistence(timeout: 10))
        app.buttons["Read next"].tap()
        let expired = app.staticTexts["This page has expired. Refresh to start a new list."]
        XCTAssertTrue(expired.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Read next"].isEnabled)
        app.buttons["refreshLog"].tap()
        XCTAssertTrue(expired.waitForNonExistence(timeout: 10))
        XCTAssertTrue(loadedText.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["Read next"].isEnabled)
        XCTAssertEqual(loadedText.label.components(separatedBy: "Synthetic stdout log fixture").count - 1, 1)
        capture("Log refresh after cursor expiry", app: app)
    }

    func testTargetGenerationPartitionPagingAndReplacementRecovery() {
        let app = fixtureApp()
        app.tabBars.buttons["More"].tap()
        app.buttons["Targets"].tap()
        let target = app.staticTexts["Synthetic Slurm"]
        XCTAssertTrue(target.waitForExistence(timeout: 10)); target.tap()
        let generation = app.descendants(matching: .any)["targetGeneration"]
        reveal(generation, app: app)
        XCTAssertTrue(generation.label.contains("9007199254740993"))
        let preview = app.staticTexts["targetPartitionPreview"]
        reveal(preview, app: app)
        XCTAssertTrue(preview.label.contains("201"))
        capture("Target generation and bounded partition preview", app: app)
        let browse = app.buttons["Browse all partitions"]
        reveal(browse, app: app); browse.tap()
        XCTAssertTrue(app.staticTexts["batch (default)"].waitForExistence(timeout: 10))
        app.buttons["Next partition page"].tap()
        XCTAssertTrue(app.staticTexts["gpu"].waitForExistence(timeout: 10))
        app.buttons["Previous partition page"].tap()
        XCTAssertTrue(app.staticTexts["batch (default)"].waitForExistence(timeout: 10))
        app.buttons["Next partition page"].tap()
        XCTAssertTrue(app.staticTexts["gpu"].waitForExistence(timeout: 10))
        app.buttons["Next partition page"].tap()
        let changed = app.staticTexts["The target generation changed. Refresh the target before browsing partitions."]
        XCTAssertTrue(changed.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Next partition page"].exists)
        capture("Target generation change requires refresh", app: app)
        app.buttons["restartTargetPartitions"].tap()
        XCTAssertTrue(app.staticTexts["batch (default)"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Previous partition page"].exists)
    }

    func testReportSnapshotFindingsExactCitationAndRevokedAccess() {
        let app = fixtureApp()
        app.tabBars.buttons["Jobs"].tap()
        let job = app.staticTexts["Synthetic alignment run"]
        XCTAssertTrue(job.waitForExistence(timeout: 10)); job.tap()
        let diagnosis = app.buttons["Diagnosis"]
        reveal(diagnosis, app: app); diagnosis.tap()
        let next = app.buttons["Next report page"]
        reveal(next, app: app); next.tap()
        XCTAssertTrue(app.staticTexts["Diagnosis · failed"].waitForExistence(timeout: 10))
        app.buttons["Previous report page"].tap()
        let report = app.buttons["report-90000000-0000-4000-8000-000000000099"]
        XCTAssertTrue(report.waitForExistence(timeout: 10)); report.tap()
        XCTAssertTrue(app.descendants(matching: .any)["reportState"].waitForExistence(timeout: 10))
        let outdated = app.staticTexts["reportOutdated"]
        reveal(outdated, app: app)
        XCTAssertTrue(outdated.label.contains("recorded snapshot"))
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(app.descendants(matching: .any)["reportState"].waitForExistence(timeout: 10))
        let finding = app.buttons["Synthetic execution failed"]
        reveal(finding, app: app); finding.tap()
        let citation = app.buttons["citation-citation-fixture"]
        reveal(citation, app: app)
        capture("Native report findings and provenance", app: app)
        citation.tap()
        XCTAssertTrue(app.staticTexts["citationValue"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["citationValue"].label, "17")
        capture("Native exact sealed citation", app: app)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let denied = app.buttons["citation-citation-denied"]
        capture("Native report return from citation", app: app)
        for _ in 0..<6 { if denied.exists && denied.isHittable { break }; app.swipeDown() }
        reveal(denied, app: app); denied.tap()
        let revoked = app.staticTexts["Your current permissions do not allow this view."]
        XCTAssertTrue(revoked.waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["citationValue"].exists)
        XCTAssertFalse(app.descendants(matching: .any)["reportState"].exists)
        reveal(revoked, app: app)
        capture("Native report access revoked and cached content removed", app: app)
    }

    private func fixtureApp() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--dashboard-ui-fixtures"]
        app.launch()
        XCTAssertTrue(app.staticTexts["fixtureBanner"].waitForExistence(timeout: 10))
        return app
    }
    private func openWorkload(_ kind: String, name: String, app: XCUIApplication) {
        app.tabBars.buttons["Workloads"].tap()
        XCTAssertTrue(app.buttons[kind].waitForExistence(timeout: 5)); app.buttons[kind].tap()
        let workload = app.staticTexts[name]
        XCTAssertTrue(workload.waitForExistence(timeout: 10)); workload.tap()
        XCTAssertTrue(app.staticTexts["Complete source summary"].waitForExistence(timeout: 10))
    }
    private func reveal(_ element: XCUIElement, app: XCUIApplication) {
        for _ in 0..<10 { if element.exists && element.isHittable { return }; app.swipeUp() }
        capture("Unreachable test element", app: app)
        print(app.debugDescription)
        XCTAssertTrue(element.exists && element.isHittable, "Expected reachable element: \(element)")
    }
    private func capture(_ name: String, app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
    }
}
