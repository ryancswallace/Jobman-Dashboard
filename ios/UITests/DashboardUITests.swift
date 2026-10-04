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

    func testRuleCatalogPagingCreateAllTerminalRevalidateAndDelete() {
        let app = ruleFixtureApp()
        openRules(app)
        let next = app.buttons["Next rule page"]
        reveal(next, app: app); next.tap()
        XCTAssertTrue(app.staticTexts["Page two rule"].waitForExistence(timeout: 10))
        app.buttons["Previous rule page"].tap()
        app.buttons["newRule"].tap()
        let name = app.textFields["ruleName"]
        XCTAssertTrue(name.waitForExistence(timeout: 10)); name.tap(); name.typeText("Every future outcome"); app.buttons["ruleKeyboardDone"].tap()
        XCTAssertFalse(app.buttons["saveRule"].isEnabled)
        app.buttons["ruleScope"].tap(); app.buttons["Namespace jobs"].tap()
        XCTAssertTrue(app.staticTexts["Follow every member’s eligible jobs in the selected namespaces."].exists)
        tapRuleSwitch(app.switches["ruleScope-10000000-0000-4000-8000-000000000001-20000000-0000-4000-8000-000000000001"], app: app)
        let every = app.switches["Every terminal outcome"]
        tapRuleSwitch(every, app: app)
        XCTAssertTrue(app.staticTexts["Includes success, unsuccessful outcomes, cancellation, and future unknown terminal outcomes."].exists)
        capture("Native explicit alert rule editor", app: app)
        app.buttons["saveRule"].tap()
        let created = app.staticTexts["Every future outcome"]
        XCTAssertTrue(created.waitForExistence(timeout: 10)); created.tap()
        XCTAssertTrue(app.staticTexts["Status: pending"].waitForExistence(timeout: 10))
        app.buttons["Revalidate selected scopes"].tap()
        XCTAssertTrue(app.staticTexts["Status: active"].waitForExistence(timeout: 10))
        capture("Native rule activation status", app: app)
        let delete = app.buttons["Delete alert rule"]
        reveal(delete, app: app); delete.tap()
        app.buttons["Delete rule"].tap()
        XCTAssertTrue(app.buttons["newRule"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Every future outcome"].exists)
    }

    func testRuleHiddenReferencesAllowStopAndConflictRequiresExplicitReview() {
        let app = ruleFixtureApp()
        openRules(app)
        app.staticTexts["Synthetic unavailable scope"].tap()
        XCTAssertTrue(app.buttons["ruleEnabled"].waitForExistence(timeout: 10))
        let edit = app.buttons["editRule"]
        reveal(edit, app: app); XCTAssertFalse(edit.isEnabled)
        capture("Native hidden rule scope and safe controls", app: app)
        for _ in 0..<5 { if app.buttons["ruleEnabled"].isHittable { break }; app.swipeDown() }
        app.buttons["ruleEnabled"].tap()
        XCTAssertTrue(app.buttons["Enable monitoring"].waitForExistence(timeout: 10))
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let conflict = app.staticTexts["Synthetic conflict rule"]
        XCTAssertTrue(conflict.waitForExistence(timeout: 10)); conflict.tap()
        reveal(app.buttons["editRule"], app: app); app.buttons["editRule"].tap()
        XCTAssertTrue(app.textFields["ruleName"].waitForExistence(timeout: 10))
        app.buttons["saveRule"].tap()
        let reload = app.buttons["Discard edits and reload"]
        XCTAssertTrue(reload.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["saveRule"].isEnabled)
        capture("Native rule revision conflict", app: app)
        reload.tap()
        XCTAssertTrue(app.textFields["ruleName"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.textFields["ruleName"].value as? String, "Updated elsewhere")
        XCTAssertTrue(app.buttons["saveRule"].isEnabled)
        app.buttons["saveRule"].tap()
        XCTAssertTrue(app.staticTexts["Updated elsewhere"].waitForExistence(timeout: 10))
    }

    func testWatchJobShortcutPreservesExactScopeAndRequiresSave() {
        let app = ruleFixtureApp()
        app.tabBars.buttons["Jobs"].tap()
        let job = app.staticTexts["Synthetic watched job"]
        XCTAssertTrue(job.waitForExistence(timeout: 10)); job.tap()
        let watch = app.buttons["Watch this job…"]
        reveal(watch, app: app); watch.tap()
        XCTAssertTrue(app.textFields["ruleName"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.textFields["ruleName"].value as? String, "Watched job")
        let uuid = app.staticTexts["40000000-0000-4000-8000-000000000001"]
        reveal(uuid, app: app)
        XCTAssertTrue(app.buttons["saveRule"].isEnabled)
        capture("Native watched job explicit draft", app: app)
        app.buttons["saveRule"].tap()
        openRules(app)
        let created = app.staticTexts["Watched job"]
        XCTAssertTrue(created.waitForExistence(timeout: 10)); created.tap()
        XCTAssertTrue(app.staticTexts["Status: pending"].waitForExistence(timeout: 10))
        reveal(uuid, app: app)
        XCTAssertTrue(uuid.exists)
    }

    func testUncertainRuleCreationCannotDuplicateAndSceneClearsDraft() {
        let app = ruleFixtureApp()
        openRules(app)
        app.buttons["newRule"].tap()
        let name = app.textFields["ruleName"]
        XCTAssertTrue(name.waitForExistence(timeout: 10)); name.tap(); name.typeText("Uncertain create"); app.buttons["ruleKeyboardDone"].tap()
        tapRuleSwitch(app.switches["ruleScope-10000000-0000-4000-8000-000000000001-20000000-0000-4000-8000-000000000001"], app: app)
        app.buttons["saveRule"].tap()
        XCTAssertTrue(app.staticTexts["uncertainRuleCreation"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["saveRule"].isEnabled)
        capture("Native uncertain rule creation requires catalog check", app: app)
        app.buttons["Close and check rules"].tap()
        XCTAssertTrue(app.staticTexts["Uncertain create"].waitForExistence(timeout: 10))
        app.buttons["newRule"].tap()
        XCTAssertTrue(name.waitForExistence(timeout: 10)); name.tap(); name.typeText("Private unsaved draft")
        XCUIDevice.shared.press(.home); app.activate()
        XCTAssertTrue(app.buttons["newRule"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.textFields["ruleName"].exists)
        XCTAssertFalse(app.staticTexts["Private unsaved draft"].exists)
        capture("Native background clears rule draft", app: app)
    }

    func testPendingRuleCreateCannotDismissAndBackgroundNeverRetries() {
        let app = ruleFixtureApp()
        openRules(app); app.buttons["newRule"].tap()
        let name = app.textFields["ruleName"]
        XCTAssertTrue(name.waitForExistence(timeout: 10)); name.tap(); name.typeText("Deferred create"); app.buttons["ruleKeyboardDone"].tap()
        tapRuleSwitch(app.switches["ruleScope-10000000-0000-4000-8000-000000000001-20000000-0000-4000-8000-000000000001"], app: app)
        app.buttons["saveRule"].tap()
        XCTAssertFalse(app.buttons["Cancel"].isEnabled)
        XCTAssertTrue(app.descendants(matching: .any)["savingRule"].exists)
        XCTAssertFalse(app.buttons["saveRule"].isEnabled)
        let header = app.navigationBars["New alert rule"]
        header.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)))
        XCTAssertTrue(name.exists)
        XCTAssertFalse(app.buttons["Cancel"].isEnabled)
        capture("Native pending create prevents accidental dismissal", app: app)
        XCUIDevice.shared.press(.home); app.activate()
        XCTAssertTrue(app.buttons["newRule"].waitForExistence(timeout: 10))
        XCTAssertFalse(name.exists)
        XCTAssertTrue(app.staticTexts["Deferred create"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts.matching(identifier: "Deferred create").count, 1)
        capture("Native interrupted create checked once in catalog", app: app)
    }

    func testRuleControlsRemainUsableWithoutNamespaceAccess() {
        let app = XCUIApplication()
        app.launchArguments = ["--dashboard-ui-fixtures", "--dashboard-rule-fixtures", "--dashboard-rule-no-namespaces"]
        app.launch()
        XCTAssertTrue(app.staticTexts["fixtureBanner"].waitForExistence(timeout: 10))
        openRules(app)
        app.staticTexts["Synthetic unavailable scope"].tap()
        XCTAssertTrue(app.buttons["ruleEnabled"].waitForExistence(timeout: 10))
        app.buttons["ruleEnabled"].tap()
        XCTAssertTrue(app.buttons["Enable monitoring"].waitForExistence(timeout: 10))
        let edit = app.buttons["editRule"]
        reveal(edit, app: app); XCTAssertFalse(edit.isEnabled)
        capture("Native account-owned rule controls without namespaces", app: app)
        let delete = app.buttons["Delete alert rule"]
        reveal(delete, app: app); delete.tap(); app.buttons["Delete rule"].tap()
        XCTAssertTrue(app.buttons["newRule"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Synthetic unavailable scope"].exists)
    }

    func testRuleAuthorityFailureClearsCurrentDetails() {
        let app = ruleFixtureApp()
        openRules(app)
        app.staticTexts["Synthetic access revoked"].tap()
        // A global authority rejection purges the entire navigation generation.
        XCTAssertTrue(app.buttons["Settings"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Synthetic access revoked"].exists)
        XCTAssertFalse(app.buttons["ruleEnabled"].exists)
        XCTAssertFalse(app.buttons["editRule"].exists)
        capture("Native revoked rule detail removed", app: app)
    }

    private func tapRuleSwitch(_ element: XCUIElement, app: XCUIApplication) {
        reveal(element, app: app)
        element.coordinate(withNormalizedOffset: CGVector(dx: 0.90, dy: 0.5)).tap()
    }

    private func ruleFixtureApp() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--dashboard-ui-fixtures", "--dashboard-rule-fixtures"]
        app.launch()
        XCTAssertTrue(app.staticTexts["fixtureBanner"].waitForExistence(timeout: 10))
        return app
    }
    private func openRules(_ app: XCUIApplication) {
        app.tabBars.buttons["More"].tap()
        app.buttons["Alert rules"].tap()
        XCTAssertTrue(app.buttons["newRule"].waitForExistence(timeout: 10))
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
