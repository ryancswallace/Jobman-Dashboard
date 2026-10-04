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

Project source references and the scheme are reproducible with `python3 scripts/generate-project.py`. Run it after adding/removing App or UITests Swift files. The generated OpenAPI client is included through `Sources/DashboardCore/DashboardAPI.generated.swift`, a relative symlink to the single checked-in `contracts/swift/DashboardAPI.generated.swift`. `GeneratedTransport.swift` supplies origin-pinned, authenticated, bounded HTTP behavior; the generated bootstrap result is mapped into application state. Artifact, dependency-edge, target and diagnosis models also use generated types directly. Other application models retain unknown enum values and wide integer wire strings; focused contract tests decode the same workload/neighborhood fixtures with both generated and application types.

## Synthetic UI mode

Only Debug builds recognize `--dashboard-ui-fixtures`. This explicitly installs an in-process URLProtocol serving synthetic data at `https://dashboard-fixtures.example.test`; every screen displays a **SYNTHETIC PREVIEW** banner. The mode does not authenticate, connect to Control, execute jobs, generate real reports, send pushes, or mutate real services. Release builds exclude the fixture implementation. Without the argument the app starts at the real HTTPS/AD FS connection screen. Fixture tests establish presentation/navigation behavior only.

The simulator build can be installed and launched with:

```sh
xcrun simctl install booted "$TMPDIR/jobman-dashboard-xcode-build/Build/Products/Debug-iphonesimulator/JobmanDashboard.app"
xcrun simctl launch booted org.jobman.dashboard --dashboard-ui-fixtures
```

## Identity and data handling

- `/auth/native/config` supplies the approved AD FS issuer, public client, API audience/scopes and exact `jobman-dashboard-auth://callback` redirect. Discovery and token exchange use verified HTTPS, bounded responses, no redirects and ephemeral sessions; discovered authorization/token endpoints must match the configured issuer origin. Authorization, code exchange and refresh all bind the configured API resource. `ASWebAuthenticationSession` performs authorization-code/S256 PKCE sign-in. The app never collects AD passwords, uses an ID token as an API credential, or trusts a decoded token as account identity. Authorized account identity comes from Dashboard bootstrap.
- Refresh credentials are device-only Keychain items accessible while unlocked. Access tokens stay in memory. Jobs, log bytes, reports and API caches are not written to persistent storage. The task-switcher cover conceals content when the scene is inactive.
- Account/scope generations suppress late responses after a switch. Bootstrap refreshes discover grant changes; sensitive views are cleared when current authorization cannot be verified within the server freshness window. The server remains authoritative for every read.
- Targets display source-qualified configuration, exact generation numbers, provider/runtime/platform capabilities and named storage references. Preview partitions are capped at 200; complete generation-pinned pages replace the current page. Target replacement or cursor expiry requires an explicit target refresh before partition history restarts. Unusually long provider regions show a disclosed 512-character preview while preserving the source value. Configured state is never presented as agent health or cluster capacity.
- Workload summaries show source totals, per-source observation times and revisions independently of the loaded child page. Array policy/mode and exact Slurm task indices remain distinct from child positions. Graph neighborhoods use the bounded 200-node/500-edge API and disclose source omitted counts; diagram and accessible node-list selection use immutable job IDs, never display labels. Incoming/outgoing dependencies page separately and retain their predicates, outcomes and source states.
- Artifact rows show exact run number, run ID, execution ID and target-generation provenance. `metadata_only` means published size/checksum metadata; file bytes are not verified or downloaded.
- Log requests use base64 bytes and decimal offsets. The client checks range length/order/stream identity, renders controls inert, caps each response at 256 KiB and the retained log buffer at 2 MiB, and labels evicted or truncated output. Scrolling pauses following; search applies only to loaded text. Cursor expiry, stream changes and log gaps stop continuation until an explicit Refresh clears the cursor, byte buffer/decoder, offsets and execution identity together.
- Report history pages are bounded to 20 source/job-qualified tasks. Queued tasks remain distinct from original sealed report/evidence IDs; selected detail polls only pending work while the app is active. Backgrounding clears local report content; foregrounding immediately rechecks access. Request generations prevent older reads from suppressing or overwriting foreground results. Findings show supporting and contradicting evidence, analyzer confidence basis, text-only actions/retry advice, missing evidence, omissions, redactions, versions, actual runs and disclosure provenance. Citation reads reauthorize the task and resolve exact stored item JSON or sanitized artifact bytes; original offsets are shown only when available without inventing a byte mapping. Citation errors clear both citation and parent report content. Request retries retain idempotency keys after uncertain transport/availability failures and create new keys after definitive source/revision/permission rejection. Explicit log inclusion requires server-side operator redaction configuration.
- Push payloads contain an opaque inbox ID. Opening it requires a current authenticated read over the private network; cold-launch taps retain only that opaque ID until navigation subscribes. Registration binds a token to an account/installation, topic and APNs environment. Automatic token updates omit `enabled`, preserving an existing device preference and leaving a new binding disabled. Only the explicit Enable action sends `enabled: true`. Notification permission remains opt-in.
- Sign-out removes local credentials and content immediately. A separate, narrow unbind credential may remain in a device-only Keychain queue until `/auth/native/device-revocations` is reachable. It can only revoke its specific account-device binding generation; it cannot authenticate, read jobs or attach another binding. Successful idempotent revocations remove the queue entry. No bearer token is retained for this queue. The UI reports pending server unbinding rather than claiming an offline logout reached the server.

## Signing and internal delivery

Copy `Config/Local.xcconfig.example` to ignored `Config/Local.xcconfig` only after the actual team and app identity are known. Keep keys/profiles outside the repository. Debug uses the development APNs environment; Release uses production. This setting must match the eventual signing profile and provider topic. Production distribution channel, team/account, app icon/export configuration, APNs ownership and real managed-device acceptance remain release work. No distribution archive or company install has been claimed.

The personal test phone can establish development interaction, login and push evidence once authorized signing/VPN/APNs prerequisites exist. It cannot establish acceptance under company management policy.

## Verification evidence

On October 3, 2026:

- `scripts/test-core.sh`: **42 tests passed** using Apple Swift 6.3.3 / bundled Swift Testing. Tests exercise source-qualified IDs/routes, strict callback/state/PKCE behavior and issuer-origin/resource binding, unknown enums and large revisions/run numbers, distinct observed/recorded lifecycle facts, nullable aggregate counts, permission freshness and scope/account invalidation, exact log offsets/UTF-8 boundaries/memory limits, bounded graph layout/cycles, actual group/graph/artifact contract decoding and scope/identity validation, explicit log discontinuity recovery, generated transport boundaries, safe errors and oversized responses; report source/task/evidence binding, full finding references, exact sealed citation ranges and wide numeric JSON, bounded report pages, and uncertain-versus-definitive request retry semantics.
- `scripts/build-simulator.sh`: **passed** with Xcode 27.0 (27A266a), including the generated API integration and Debug-only synthetic UI mode. The generic simulator target requires no device signing.
- Xcode project, entitlements, Info.plist and privacy manifest parse successfully. `scripts/test-ui.sh`: **seven iPhone 18 Pro / iOS 27.0 UI tests passed**, full run ending **21:09 EDT**: real connection without fake sign-in; synthetic tab navigation; exact array policy/mode/task index and child paging; artifact provenance and expired log cursor refresh; bounded graph/list recentering and incoming/outgoing/both dependency pagination; exact target generations, bounded partition preview, paging and generation-replacement recovery; bounded report history, foreground revalidation, expanded findings, exact sealed citations and clearing cached report/citation content on a permission denial. Screenshots of array indices, artifact provenance, refreshed logs, bounded diagrams and expanded dependency predicates were inspected. The graph test caught SwiftUI automatic row activation opening the adjacent job link instead of recentering; independent borderless hit targets fixed it before the passing run. Target generation/partition and replacement-error screenshots were also inspected. Report findings, the exact sealed item citation and the post-revocation screen were inspected. Report UI testing caught a DisclosureGroup accessibility identifier masking nested citation links; retaining the header’s natural accessible label preserves individually reachable citation links. The final report-only rerun after foreground request-generation changes also passed at **21:11 EDT**. These tests do not exercise live services. The script uses a separate temporary DerivedData directory and disables verbose simulator diagnostics on failure; ordinary result bundles, test output and screenshot attachments remain available.

These results do not prove real AD FS interoperability, current directory authorization, production log/report APIs, APNs delivery, internal signing/distribution, managed-device behavior, or release readiness. Follow the repository's implementation tracker and T01–T12 acceptance gates for those results.
