# Remaining product work found in the release audit

The October 4 audit compares R01–R22 and WP01–WP15 with the authored design,
current client code, and the evidence in `IMPLEMENTATION_STATUS.md`. This is a
work list, not release acceptance. Existing installation, schema refusal, NFS
stall, source-fault, restore-watchdog, live graph, and final-candidate work
remains tracked separately; this audit does not duplicate those operations.

| Requirement | Concrete gap | Required repair and evidence |
| --- | --- | --- |
| R05/R08, WP11–WP12 | Native workload/artifact polling updates only already retained IDs, discards additions/removals, and advances the page's fetch time. Accumulated job pages also retain rows outside the refreshed first page. | Bounded paging state that preserves navigation, admits current-page changes, offers an explicit restart for new results, and labels retained-page freshness honestly. Test added, removed, changed, empty and paged responses. |
| R08, WP12 | Returning to the foreground refreshes overview/jobs, but manual-refresh job/workload/log views can retain their old resource data. Log views do not show a last successful read time. | Reauthorize and read the visible resource on foreground return, including manual mode; preserve paused-follow intent. Show fetch time separately from source capture/evidence time. Test background transitions and stale failures. |
| R06/R09/R21, DESIGN §5.1 | Only the current run reference is discoverable. Reports accept a manually supplied UUID; logs and artifact APIs accept a run number, but neither client has a bounded run chooser. | Add authorized bounded run catalog/detail and source-qualified selection for logs, artifacts and report requests. Preserve current-run defaults; no full event timeline. Verify paging, current authority, cursor scope and historical-run provenance with disposable PostgreSQL and client tests. |
| R03, DESIGN §11.3 | Registered native content links work, but web has no “Open in app” action and native has no pasted canonical HTTPS-link entry point. | Add both affordances with canonical source identities, strict configured-origin validation, no credentials, and current authorization through sign-in. Test invalid/external links and account changes. |
| R04, WP12 | Native overview always uses the default completion window; web offers 24 hours and seven days. | Add equivalent bounded window selection and exact-window drill-down tests. |
| R18, WP01/WP12/WP15 | Native release preparation has no app-icon asset catalog, unsigned physical-device archive pipeline, or parameterized archive/export tooling. Existing CI builds a simulator app; version/build defaults are fixed. | Prepare reusable assets, validated version/build inputs, an unsigned Release device archive and inspectable manifest, and explicit signing/export instructions. Do not invent the organization’s bundle/team/channel or embed signing material. |

These repairs are unblocked implementation work. Their automated evidence does
not replace actual AD FS acceptance, signed APNs delivery, supported-browser
visual/assistive-technology checks, or installation/accessibility on company
managed iPhones. The recorded browser policy block remains in force; no alternate
driver or origin is an acceptance workaround. The Apple distribution decision,
signing/APNs ownership and real device inputs remain external gates.

R01–R02/R07/R10–R17/R19–R20/R22 have corresponding implementation and focused
tests; their remaining system evidence is recorded in the status document.
This audit is not a claim that every production acceptance gate for those
requirements has passed. Upstream protected reviews, released dependency pins,
and a final clean candidate still govern release completion.
