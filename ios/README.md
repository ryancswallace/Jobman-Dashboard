# Jobman Dashboard for iPhone

Native SwiftUI client for the private Dashboard API. Development target: iOS 18 or later, iPhone only. The current bundle identifier (`org.jobman.dashboard`) is a development placeholder; it is not a claim that an Apple identifier or signing profile has been registered.

## Build and test

From this directory:

```sh
./scripts/test-core.sh
./scripts/build-simulator.sh
./scripts/test-ui.sh
```

The Xcode project has a shared `JobmanDashboard` scheme and local `DashboardCore` Swift package. Full Xcode is required for iPhone builds and UI tests. The core test script also supports an existing Apple Command Line Tools installation with its bundled Swift Testing framework. Build outputs use a task-specific temporary directory; no signing credential is needed for simulator builds. Xcode needs its normal user-cache and CoreSimulator access.

Run UI tests on an installed simulator, substituting an available device from `xcrun simctl list devices available`:

```sh
IOS_TEST_DESTINATION='platform=iOS Simulator,name=iPhone 18 Pro' ./scripts/test-ui.sh
```

Project source references and the scheme are reproducible with `python3 scripts/generate-project.py`. Run it after adding/removing App or UITests Swift files. The generated OpenAPI client is included through `Sources/DashboardCore/DashboardAPI.generated.swift`, a relative symlink to the single checked-in `contracts/swift/DashboardAPI.generated.swift`. `GeneratedTransport.swift` supplies origin-pinned, authenticated, bounded HTTP behavior; the generated bootstrap result is mapped into application state. Artifact, dependency-edge, target, recorded-run, diagnosis, personal-rule, notification-device and inbox models also use generated types directly. Other application models retain unknown enum values and wide integer wire strings; focused contract tests decode the same workload/neighborhood fixtures with both generated and application types.

## Branding

Dark appearance uses `../assets/logo-transparent-dashboard.svg` in the connection
screen and Overview toolbar. Keep its copies in
`App/Assets.xcassets/DashboardLogoDark.imageset` and
`../web/public/logo-transparent-dashboard.svg` identical to the source when
updating the artwork. The native image preserves the SVG’s original colors and
aspect ratio; light appearance retains the existing branding.

## Local iPhone archive and development export

The app icon uses the existing Jobman purple chevron and green Dashboard grid on
the brand's dark background. `App/Assets.xcassets` supplies the opaque 1024-pixel
icon. Its deterministic CoreGraphics renderer is `scripts/render-app-icon.swift`;
the source logos are unchanged. Regenerate from this directory with:

```sh
xcrun swift -module-cache-path /private/tmp/jobman-dashboard-icon-module-cache scripts/render-app-icon.swift App/Assets.xcassets/AppIcon.appiconset/AppIcon.png
```

Create a Release **unsigned** device archive without signing credentials from a
clean committed checkout (see [final candidate gates](../docs/FINAL_CANDIDATE.md)):

```sh
python3 scripts/test-package-app.py
python3 scripts/package-app.py unsigned --version 0.1.0 --build 1 --output /private/tmp/jobman-dashboard-unsigned-archive
```

The output must be a new absolute directory. It retains the exact command,
intent, build log, archive, and completion receipt with executable/Info.plist
hashes. Unsigned builds use committed Git-archive bytes, exclude ignored local
configuration, and record source SHA/archive hash plus Xcode, Swift and iPhoneOS
SDK versions. `archive-files.json` and the generated unsigned tar are hashed in
the completion receipt; CI retains those exact artifacts. This is provenance,
not a claim of byte reproducibility. An unsigned archive verifies compilation/packaging; it cannot be installed
on a physical iPhone. Failed builds retain their evidence and require a new output
directory. `--dry-run` checks inputs and prints the local command without building.

Development signing uses an already installed Apple Development certificate and
provisioning profile. Supply the approved team, certificate SHA-1, profile UUID
and bundle identifier explicitly:

```sh
python3 scripts/package-app.py development --version 0.1.0 --build 1 --output /private/tmp/jobman-dashboard-development-archive --team TEAMID1234 --identity CERTIFICATE_SHA1 --profile PROFILE_UUID --bundle-id org.example.jobman.dashboard
python3 scripts/package-app.py export-development --version 0.1.0 --build 1 --output /private/tmp/jobman-dashboard-development-export --archive /private/tmp/jobman-dashboard-development-archive/JobmanDashboard.xcarchive --team TEAMID1234 --identity CERTIFICATE_SHA1 --profile PROFILE_UUID --bundle-id org.example.jobman.dashboard
```

These examples contain placeholders. The tool never passes provisioning-update or
upload options and does not enroll accounts, create identifiers/certificates, or
change profiles. It verifies iPhone-only archive identity and, for signed builds,
the selected team/bundle, development APNs and debugging entitlements before a
completion receipt. Export requires the completed matching signed archive and
valid code signature. The organization must still select its internal distribution
mechanism and provide approved signing/profile inputs; development export is not
managed-distribution or real APNs acceptance.

## Monitoring refresh and run selection

Job, workload and artifact lists retain one bounded displayed page. Refresh
replaces that page's complete rows and timestamp, so new/removed items cannot be
hidden behind a fresh timestamp. Next/Previous retain at most 64 page cursors;
restart returns to the current first page. Later-page refresh stays at its current
cursor and says that newly arrived first-page items require restart. Expired
cursors require an explicit restart rather than presenting an empty current list.

The Overview offers 24-hour and seven-day terminal windows; drilldowns use the
returned exact interval. Foreground resume rechecks authorization and reloads the
current visible monitoring read even when automatic data refresh is disabled.
A log resume resets the verified stream, stops following and shows the last
successful fetch separately from source capture time. Replaced in-flight reads
cannot overwrite the new request's content or busy state.

Choose a recorded run from job details to pin log/metadata reads to its exact run
number and new diagnosis requests to its run UUID. The catalog is paged and uses
source-reported numbers, including values beyond JavaScript's safe integer range.
A source without the run-catalog capability shows an explicit unsupported error.
The default remains current logs/all artifact metadata; report history remains
all runs. Run metadata dates are not presented as observed execution timestamps.

More → Open a Dashboard link accepts canonical HTTPS job/inbox URLs only from the
connected origin, including its port, without credentials/query/fragment. Existing
custom-scheme links and opaque push inbox IDs still require current sign-in and
source authorization; pasted text is cleared when leaving or backgrounding.

## Synthetic UI mode

Only Debug builds recognize `--dashboard-ui-fixtures`. This explicitly installs an in-process URLProtocol serving synthetic data at `https://dashboard-fixtures.example.test`; every screen displays a **SYNTHETIC PREVIEW** banner. The mode does not authenticate, connect to Control, execute jobs, generate real reports, send pushes, or mutate real services. Release builds exclude the fixture implementation. Without the argument the app starts at the real HTTPS/AD FS connection screen. Fixture tests establish presentation/navigation behavior only.

Rule workflow tests additionally pass `--dashboard-rule-fixtures` to enable a mutable in-process synthetic rule catalog. `--dashboard-rule-no-namespaces` removes synthetic namespace grants to test account-owned stop/delete controls. These arguments never enable production fixture authentication or provider calls.

The simulator build can be installed and launched with:

```sh
xcrun simctl install booted "$TMPDIR/jobman-dashboard-xcode-build/Build/Products/Debug-iphonesimulator/JobmanDashboard.app"
xcrun simctl launch booted org.jobman.dashboard --dashboard-ui-fixtures
```

## Large graph simulator profile

Debug builds additionally recognize `--dashboard-graph-ceiling-fixtures` with
`--dashboard-ui-fixtures`. This lazy fixture represents 10,000 nodes and 100,000
edges using the same forward-DAG topology as the web ceiling test. Requests still
use the real native transport and decoders; the URLProtocol supplies synthetic
wire responses. It creates no jobs, identity session or network connection.
The graph fixture and its environment diagnostic view are excluded from Release.

`GraphCeilingFixtureTests` independently checks the topology, traverses every
node and dependency through bounded decoded pages, verifies source-qualified
identity and exact totals/omissions, and rejects incompatible cursor queries and
oversized bounds. Its fixture source is a test-only symlink to the Debug source,
so tests do not maintain a second implementation.

`GraphCeilingUITests` exercises node/dependency page replacement, exact source
counts/predicates, the 200-node/500-edge diagram, selected accessibility traits,
and list recentering. Run the three standard workflows with Xcode's
`-only-testing:DashboardUITests/GraphCeilingUITests` filter. The fourth workflow
is deliberately skipped without its display-profile runner:

```sh
python3 scripts/test-graph-accessibility.py --device <booted-iPhone-simulator-UUID>
```

The runner captures the existing simulator's Dynamic Type and Reduce Motion
values, sets the largest accessibility text size and Reduce Motion, and restores
both exact values in `finally`, including after test failure. It refuses unknown
baselines and retains before/after JSON, screenshots and an xcresult in a unique
temporary directory. The test asserts the actual SwiftUI environment before
navigation; a settings write or skipped test is not acceptance. No host-wide
preferences change. Do not run another test/configuration process on the same
simulator concurrently. This checks rendered simulator behavior, not VoiceOver,
Voice Control, actual Control authorization or a managed device. Evidence and
remaining gates are in [graph client acceptance](../docs/GRAPH_CLIENT_ACCEPTANCE.md).

## Identity and data handling

- `/auth/native/config` supplies the approved AD FS issuer, public client, API audience/scopes and exact `jobman-dashboard-auth://callback` redirect. Discovery and token exchange use verified HTTPS, bounded responses, no redirects and ephemeral sessions; discovered authorization/token endpoints must match the configured issuer origin. Authorization, code exchange and refresh all bind the configured API resource. `ASWebAuthenticationSession` performs authorization-code/S256 PKCE sign-in. The app never collects AD passwords, uses an ID token as an API credential, or trusts a decoded token as account identity. Authorized account identity comes from Dashboard bootstrap.
- Refresh credentials are device-only Keychain items accessible while unlocked. Access tokens stay in memory. Jobs, log bytes, reports and API caches are not written to persistent storage. The task-switcher cover conceals content when the scene is inactive.
- Account/scope generations suppress late responses after a switch. Bootstrap refreshes discover grant changes; sensitive views are cleared when current authorization cannot be verified within the server freshness window. The server remains authoritative for every read.
- Targets display source-qualified configuration, exact generation numbers, provider/runtime/platform capabilities and named storage references. Preview partitions are capped at 200; complete generation-pinned pages replace the current page. Target replacement or cursor expiry requires an explicit target refresh before partition history restarts. Unusually long provider regions show a disclosed 512-character preview while preserving the source value. Configured state is never presented as agent health or cluster capacity.
- Workload summaries show source totals, per-source observation times and revisions independently of the loaded child page. Array policy/mode and exact Slurm task indices remain distinct from child positions. Graph neighborhoods use the bounded 200-node/500-edge API and disclose source omitted counts; diagram and accessible node-list selection use immutable job IDs, never display labels. Incoming/outgoing dependencies page separately and retain their predicates, outcomes and source states.
- Artifact rows show exact run number, run ID, execution ID and target-generation provenance. `metadata_only` means published size/checksum metadata; file bytes are not verified or downloaded.
- Log requests use base64 bytes and decimal offsets. The client checks range length/order/stream identity, renders controls inert, caps each response at 256 KiB and the retained log buffer at 2 MiB, and labels evicted or truncated output. Scrolling pauses following; search applies only to loaded text. Cursor expiry, stream changes and log gaps stop continuation until an explicit Refresh clears the cursor, byte buffer/decoder, offsets and execution identity together.
- Report history pages are bounded to 20 source/job-qualified tasks. Queued tasks remain distinct from original sealed report/evidence IDs; selected detail polls only pending work while the app is active. Backgrounding clears local report content; foregrounding immediately rechecks access. Request generations prevent older reads from suppressing or overwriting foreground results. Findings show supporting and contradicting evidence, analyzer confidence basis, text-only actions/retry advice, missing evidence, omissions, redactions, versions, actual runs and disclosure provenance. Citation reads reauthorize the task and resolve exact stored item JSON or sanitized artifact bytes; original offsets are shown only when available without inventing a byte mapping. Citation errors clear both citation and parent report content. Request retries retain idempotency keys after uncertain transport/availability failures and create new keys after definitive source/revision/permission rejection. Explicit log inclusion requires server-side operator redaction configuration.
- Personal alert rules use the generated API for bounded 20-row pages, create, full edit, enable/disable, explicit revalidation and deletion. The editor covers watched jobs, current/future jobs submitted by the user, and all namespace jobs; every namespace is explicitly selected. Watched jobs retain deployment/namespace/UUID identity, with 100-job bounds. Six individual terminal outcomes, unsuccessful/success/cancellation presets and all-terminal including future outcomes are available. No rule is created on sign-in, and Watch this job opens an unsaved draft. Names are bounded by 120 UTF-8 bytes.
- Current rule projections expose per-namespace activation status/times and hidden-reference counts. Hidden or unknown intent cannot be reconstructed for a full edit; dedicated stop/delete operations remain available using current account/token authorization even when namespace access is unavailable. Empty grant refreshes clear namespace content on transition without repeatedly destroying those account-owned controls. Rule requests check session/content/scene generations before send and publication; backgrounding clears editors, lists and details. Namespace selectors never reuse expired bootstrap labels. Cancel and interactive sheet dismissal are disabled while a mutation is pending; unavoidable account or scene transitions still clear the view without retrying the write. Conflicts require explicit reload/review; uncertain creates cannot be resubmitted until the user closes the editor and checks the refreshed catalog, because creation has no idempotency contract.
- Inbox reads use the generated current-authority API with 20-item cursor pages, explicit all-versus-empty namespace selection, an unread filter and exact scoped unread counts. A partial response identifies unavailable sources/scopes or current job details; total authorization failure clears history/counts and offers explicit retry without displaying a false zero or automatically remounting. Row navigation passes only the opaque inbox ID and fetches detail anew. Read/unread writes are idempotent; denied/missing/expired responses clear all retained detail and original rule names. Original matching rule versions, event/observed-completion times and source IDs remain distinct from current job metadata. Missing/unavailable jobs never reuse a cached name. Delivery records disclose handoff status without claiming presentation on a phone. Scene/account/scope/session changes clear content, foreground reads reauthorize, and local expiry timers remove expired items. Inbox state is memory-only.
- Push payloads contain only an opaque inbox route; opening it requires a current authenticated read over the private network. Cold-launch taps retain the opaque ID until navigation subscribes. APNs registration callbacks capture a token in memory and never attach an account. The device screen reads current system permission; requesting permission remains an explicit action. The installation UUID and independent 32-byte proof live in a nonsynchronizing, device-only Keychain, separated by canonical Dashboard origin, bundle topic and APNs environment. Foreground/permission-completion registration uses current system permission independently of an account view’s generation, so an OS authorization sheet cannot strand an approved registration. Automatic foreground work only refreshes an existing current-account token and permission; it cannot attach, switch accounts, enable, unmute or undo remote removal.
- Device attachment and account switching require explicit forms and confirmation. The bounded 50-device list supports labels, enabled/muted settings and confirmed removal, with exact revision checks. Uncertain mutations require reloading and reviewing current state; no blind binding retry occurs. Account, content and scene generations suppress old responses, discard forms and stop phase-two admission. Cancel and sheet dismissal remain disabled during a pending explicit mutation. Device controls use current account authorization independently of namespace-bootstrap freshness.
- Before attachment, the client generates and durably saves a separate 32-byte revocation credential and UUID, reserves it through the authenticated API, validates and durably acknowledges the exact receipt, then rechecks the initiating session before binding. Existing stored credentials are verified against the current binding generation; upgrades without a usable credential require explicit secure setup. Readiness comes from server device responses. No signing or provider credential is embedded.
- Sign-out deletes login credentials and content immediately, retaining only narrow generation-specific revocations and minimal origin/installation bookkeeping in a single atomic device-only Keychain ledger. Queued records erase their previous account ID; realm/installation metadata remains only for safe unresolved-legacy cleanup. Interrupted pending reservations from a previous process are queued before new admission. Active and queued records share a 32-record ceiling reserved before a new operation, so ordinary offline sign-out cannot overflow it. Anonymous revocation requests contain only the credential ID/secret, with no bearer, cookie, origin, token, proof or conditional header. Acknowledged successful revocations remove exactly that ledger entry; unknown responses for an unacknowledged legacy-existing binding remain explicitly unconfirmed. Authenticated proof inspection of an unbound installation or a confirmed revision-checked removal safely clears its old ledger entries. Repeated failures preserve the queue for future foreground connections. Queued old generations cannot detach a new account binding. If secure storage is unavailable, sign-out still clears local authentication and honestly reports that server unbinding could not be queued. These semantics implement [the native revocation protocol](../docs/DEVICE_REVOCATION.md).


Device simulator workflows additionally require `--dashboard-device-fixtures` with the general fixture flag. They use an in-memory synthetic installation and revocation ledger, synthetic APNs token/permission, and a local URLProtocol service. No real Keychain proof, OS permission dialog, APNs registration or server mutation is involved. Release excludes this fixture implementation. Inbox simulator workflows use `--dashboard-inbox-fixtures` with the general fixture flag, an isolated URLProtocol history service, and synthetic opaque notification IDs; they exercise no APNs provider or real user history.

## Signing and internal delivery

Copy `Config/Local.xcconfig.example` to ignored `Config/Local.xcconfig` only after the actual team and app identity are known. Keep keys/profiles outside the repository. Debug uses the development APNs environment; Release uses production. This setting must match the eventual signing profile and provider topic. Production distribution channel, team/account, app icon/export configuration, APNs ownership and real managed-device acceptance remain release work. No distribution archive or company install has been claimed.

The personal test phone can establish development interaction, login and push evidence once authorized signing/VPN/APNs prerequisites exist. It cannot establish acceptance under company management policy.

## Verification evidence

On October 3, 2026:

- `scripts/test-core.sh`: **60 tests passed** using Apple Swift 6.3.3 / bundled Swift Testing. Tests exercise source-qualified IDs/routes, strict callback/state/PKCE behavior and issuer-origin/resource binding, unknown enums and large revisions/run numbers, distinct observed/recorded lifecycle facts, nullable aggregate counts, permission freshness and scope/account invalidation, exact log offsets/UTF-8 boundaries/memory limits, bounded graph layout/cycles, actual group/graph/artifact contract decoding and scope/identity validation, explicit log discontinuity recovery, generated transport boundaries, safe errors and oversized responses; report source/task/evidence binding, full finding references, exact sealed citation ranges and wide numeric JSON, bounded report pages, and uncertain-versus-definitive request retry semantics. Rule tests cover redacted/unknown edit prevention, exact source-qualified watched jobs, UTF-8 and count limits, outcome-mode transitions, wide revision CAS, and generated enabled-only/no-body revalidation transport semantics. Device tests cover installation/origin isolation, canonical private proofs, owner inspection, token-only refresh, bounded DTOs, acknowledged admission before activation, sign-out/background cancellation, generation-specific revocation completion, legacy uncertainty, safe capacity/removal recovery exact generated conditional headers, and the permission/foreground registration policy.
- `scripts/build-simulator.sh`: **passed** with Xcode 27.0 (27A266a), including the generated API integration and Debug-only synthetic UI mode. The generic simulator target requires no device signing. A separate unsigned Release simulator build also passed, excluding the DEBUG fixture implementation.
- Xcode project, entitlements, Info.plist and privacy manifest parse successfully. `scripts/test-ui.sh`: **seven iPhone 18 Pro / iOS 27.0 UI tests passed**, full run ending **21:09 EDT**: real connection without fake sign-in; synthetic tab navigation; exact array policy/mode/task index and child paging; artifact provenance and expired log cursor refresh; bounded graph/list recentering and incoming/outgoing/both dependency pagination; exact target generations, bounded partition preview, paging and generation-replacement recovery; bounded report history, foreground revalidation, expanded findings, exact sealed citations and clearing cached report/citation content on a permission denial. Screenshots of array indices, artifact provenance, refreshed logs, bounded diagrams and expanded dependency predicates were inspected. The graph test caught SwiftUI automatic row activation opening the adjacent job link instead of recentering; independent borderless hit targets fixed it before the passing run. Target generation/partition and replacement-error screenshots were also inspected. Report findings, the exact sealed item citation and the post-revocation screen were inspected. Report UI testing caught a DisclosureGroup accessibility identifier masking nested citation links; retaining the header’s natural accessible label preserves individually reachable citation links. The final report-only rerun after foreground request-generation changes also passed at **21:11 EDT**. These tests do not exercise live services. The script uses a separate temporary DerivedData directory and disables verbose simulator diagnostics on failure; ordinary result bundles, test output and screenshot attachments remain available.

- The full simulator regression suite subsequently passed **13 tests with zero failures**, ending **22:53 EDT**. The six added rule workflows exercise explicit namespace and all-terminal selection, 20-row pagination, revalidation/activation and delete, hidden-reference full-edit prevention with safe stop, revision conflict/review, uncertain-create catalog recovery, foreground/background clearing, a watched-job draft, global authorization purge, and account-owned stop/delete with no namespace access. Rendered editor, activation, hidden-reference, conflict, watched-job and uncertainty screens were inspected. Test data is served only by the explicit local DEBUG rule fixture. A focused two-test rerun after independent review passed at **22:55 EDT**, including a delayed create response: Cancel and swipe dismissal stay disabled while pending, backgrounding clears the draft, and the admitted create appears exactly once on return with no automatic retry. The suite now contains 14 unique UI tests.

- Native device verification on October 4: four focused simulator workflows passed for explicit attachment/removal, explicit account switch/remote removal, existing-binding upgrade with disabled/muted settings preserved, and background cancellation before reservation acknowledgment. The full **20-test** regression run subsequently passed **19 tests**; its single failure was an XCTest center tap that did not activate the visible Sign out label. Video and accessibility hierarchy confirmed no sign-out action occurred. The same offline sign-out workflow passed at **00:08:58 EDT** after targeting the visible label; it verifies immediate account-content purge with only the narrow revocation queued during a synthetic outage. All 20 unique workflows therefore have passing evidence, but the initial full run is not claimed as entirely green. Focused background/uncertain-admission checks also passed after permission lifecycle fixes. Rendered attachment, switch, disabled/muted preferences, removal, canceled reservation, uncertainty and offline sign-out screens were inspected. Final core count is 60; unsigned Release compilation passes. Actual system permission prompts, APNs delivery and physical Keychain/device acceptance are outside these fixtures.

- Native inbox/device checkpoint on October 4: **65 core tests passed** and the unsigned Release simulator build passed. The combined **24-workflow** simulator run passed **23 tests**; the existing report/citation test missed a transient forbidden message that a successful periodic job refresh replaced. The recording confirms the report/citation were purged and the notice was displayed. The unchanged isolated case passed at **00:44:46 EDT**. A DEBUG-only manual-data-refresh fixture then made that assertion deterministic while retaining independent authorization polling; the focused case passed at **00:47:56 EDT**. The full run is not claimed as 24/24 green. All four new inbox workflows passed: bounded pages, exact original rule version/read state, explicit empty versus all scope, authorization outage without false zero/automatic retry, revoked detail purge, and opaque notification navigation with fresh foreground data. Core coverage adds strict inbox identity/scope/expiry/count checks and 64-page navigation eviction. Final partial/empty history, matched-rule, delivery, outage, revoked detail, foreground destination and offline sign-out screenshots were inspected. Real APNs and company-managed-device acceptance remain external gates.

- A fresh full suite on exact rc.2 source `42d153b4672aeb5cdb2d7395f052b8c6a095f5e1` passed **24 tests with zero failures in553.953s**, ending **06:07:34 EDT on October4**. Result bundle `/private/tmp/jobman-dashboard-rc2-ui-42d153b.xcresult` and log `/private/tmp/jobman-dashboard-rc2-ui-42d153b.log` are retained. Rendered overview, graph, report findings, partial inbox, authorization outage, rule editor, delivery preferences and offline sign-out screenshots were inspected. This supersedes the earlier combined-run failure while retaining its diagnostic history. These are synthetic simulator workflows; manual VoiceOver/largest Dynamic Type, physical Keychain/APNs and managed-device acceptance remain separate gates.


- Native audit checkpoint on October4: the full iPhone18Pro simulator run completed **32 tests, one intentional accessibility-profile skip, zero failures in733.333s**. It covers the prior workflows plus page replacement, foreground manual reads, pasted connected-origin links and selected historical runs. Subsequent review tightened run ID/Int64/execution/source validation; all **81 core tests** passed. The selected-run UI retest passed, and two new focused regressions passed for a manual7-day overview refresh queued behind a job-page read (**43.415s**) and authorization purge during a page read followed by a successful manual retry (**22.733s**). The first queued-window test queried a standalone numeric label even though SwiftUI exposes the whole button; its failure is retained and the corrected combined-label assertion passed without weakening the expected168 value. This is not claimed as a fresh34-test full-suite run. Rendered page, paused-log freshness, run selection/report prefill,7-day overview and post-authorization job screens were inspected.
- The final **unsigned Release iPhone archive** passed with explicit version`0.1.0`, build`6`, iPhone-only device family and compiled AppIcon. Five packaging guard tests passed under Python3.9. Signed archive/export commands were not invoked. Existing CI now runs those guards in its iPhone job; manual dispatch with `nativeArchive=true` (or a nonempty candidateVersion) also builds and retains a tarred unsigned `.xcarchive`, build intent, verified metadata receipt and build log. `nativeVersion` and `nativeBuild` are explicit dispatch inputs. The artifact is not an installable or distributable IPA and does not prove physical-device, signing, APNs or managed-device behavior.

These results do not prove real AD FS interoperability, current directory authorization, production log/report APIs, APNs delivery, internal signing/distribution, managed-device behavior, or release readiness. Follow the repository's implementation tracker and T01–T12 acceptance gates for those results.

Packaging requires explicit numeric marketing version and build values. The archive receipt records both and verifies them against the built Info.plist; export rejects a mismatched selection. Version uses three canonical 0–9999 components; build uses a positive 1–9999 component and up to two 0–99 components. Signing and export remain explicit local operations with pre-existing credentials.

## Submitted job inspection

Job detail separates the current job snapshot from facts captured when a recorded run is selected. Target name, partition, workload digest and confidence-update time are shown when supplied; older responses remain readable with unavailable fields. Selected-run metadata timestamps are not presented as execution lifecycle times.

The submitted execution specification uses the generated API model. Its dedicated scrollable screen shows the executable and every ordered argument, including empty arguments and multiline shell-wrapper scripts. Values are selectable literal text; control/direction characters use visible Unicode escapes. No argument bytes are discarded. The explicit **Copy argument vector as JSON** action preserves original strings and uses a local-only clipboard entry that expires after 60 seconds. It does not construct a shell-quoted command or execute anything. The submitted working directory is a portable logical path, not an inferred runtime directory. Environment values are excluded, and unavailable specifications show the source's reason.

The DEBUG-only `--dashboard-inspection-fixtures` option supplies synthetic command/metadata and unavailable-source cases. Core tests cover backward-compatible decoding, exact JSON argument-vector round trips, empty arguments, multiline scripts, Unicode/control text and a 65,536-byte argument. Simulator workflows inspect the full script tail and selected-run/source distinctions; these fixtures do not constitute live Control or physical-device acceptance.

Validation on 2026-10-06: 86 core tests passed and the final unsigned simulator build succeeded with Xcode 27.0. The two new inspection workflows passed on iPhone 18 Pro / iOS 27.0 after correcting combined accessibility-label selectors; the existing recorded-run log/artifact/diagnosis workflow also passed. Five captured fixture screenshots were inspected. The full simulator suite and physical-device checks were not repeated for this change.
