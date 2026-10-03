# Jobman Dashboard for iPhone

Native SwiftUI client for the private Dashboard API. Development target: iOS 18 or later, iPhone only. The current bundle identifier (`org.jobman.dashboard`) is a development placeholder; it is not a claim that an Apple identifier or signing profile has been registered.

## Build and test

From this directory:

```sh
./scripts/test-core.sh
./scripts/build-simulator.sh
```

The Xcode project has a shared `JobmanDashboard` scheme and local `DashboardCore` Swift package. Full Xcode is required for iPhone builds and UI tests. The core test script also supports an existing Apple Command Line Tools installation with its bundled Swift Testing framework. Build outputs use a task-specific temporary directory; no signing credential is needed for simulator builds. Xcode needs its normal user-cache and CoreSimulator access.

Run UI tests on an installed simulator, substituting an available device from `xcrun simctl list devices available`:

```sh
xcodebuild -project JobmanDashboard.xcodeproj -scheme JobmanDashboard \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath "$TMPDIR/jobman-dashboard-xcode-build" \
  CODE_SIGNING_ALLOWED=NO test
```

Project source references and the scheme are reproducible with `python3 scripts/generate-project.py`. Run it after adding/removing App or UITests Swift files. The generated OpenAPI client is included through `Sources/DashboardCore/DashboardAPI.generated.swift`, a relative symlink to the single checked-in `contracts/swift/DashboardAPI.generated.swift`. `GeneratedTransport.swift` supplies origin-pinned, authenticated, bounded HTTP behavior; the generated bootstrap result is mapped into application state. Other application models retain unknown enum values and 64-bit wire strings.

## Synthetic UI mode

Only Debug builds recognize `--dashboard-ui-fixtures`. This explicitly installs an in-process URLProtocol serving synthetic data at `https://dashboard-fixtures.example.test`; every screen displays a **SYNTHETIC PREVIEW** banner. The mode does not authenticate, connect to Control, execute jobs, generate real reports, send pushes, or mutate real services. Release builds exclude the fixture implementation. Without the argument the app starts at the real HTTPS/AD FS connection screen. Fixture tests establish presentation/navigation behavior only.

The simulator build can be installed and launched with:

```sh
xcrun simctl install booted "$TMPDIR/jobman-dashboard-xcode-build/Build/Products/Debug-iphonesimulator/JobmanDashboard.app"
xcrun simctl launch booted org.jobman.dashboard --dashboard-ui-fixtures
```

## Identity and data handling

- `/auth/native/config` supplies the approved AD FS issuer, public client, API audience/scopes and exact `jobman-dashboard-auth://callback` redirect. Discovery and token exchange use verified HTTPS, bounded responses, no redirects and ephemeral sessions. `ASWebAuthenticationSession` performs authorization-code/S256 PKCE sign-in. The app never collects AD passwords, uses an ID token as an API credential, or trusts a decoded token as account identity. Authorized account identity comes from Dashboard bootstrap.
- Refresh credentials are device-only Keychain items accessible while unlocked. Access tokens stay in memory. Jobs, log bytes, reports and API caches are not written to persistent storage. The task-switcher cover conceals content when the scene is inactive.
- Account/scope generations suppress late responses after a switch. Bootstrap refreshes discover grant changes; sensitive views are cleared when current authorization cannot be verified within the server freshness window. The server remains authoritative for every read.
- Log requests use base64 bytes and decimal offsets. The client checks range length/order/stream identity, renders controls inert, caps each response at 256 KiB and the retained log buffer at 2 MiB, and labels evicted or truncated output. Scrolling pauses following; search applies only to loaded text.
- Push payloads contain an opaque inbox ID. Opening it requires a current authenticated read over the private network. Registration binds a token to an account/installation, topic and APNs environment. Notification permission remains opt-in.
- Sign-out removes local credentials and content immediately. A separate, narrow unbind credential may remain in a device-only Keychain queue until `/auth/native/device-revocations` is reachable. It can only revoke its specific account-device binding generation; it cannot authenticate, read jobs or attach another binding. Successful idempotent revocations remove the queue entry. No bearer token is retained for this queue. The UI reports pending server unbinding rather than claiming an offline logout reached the server.

## Signing and internal delivery

Copy `Config/Local.xcconfig.example` to ignored `Config/Local.xcconfig` only after the actual team and app identity are known. Keep keys/profiles outside the repository. Debug uses the development APNs environment; Release uses production. This setting must match the eventual signing profile and provider topic. Production distribution channel, team/account, app icon/export configuration, APNs ownership and real managed-device acceptance remain release work. No distribution archive or company install has been claimed.

The personal test phone can establish development interaction, login and push evidence once authorized signing/VPN/APNs prerequisites exist. It cannot establish acceptance under company management policy.

## Verification evidence

On October 3, 2026:

- `scripts/test-core.sh`: **23 tests passed** using Apple Swift 6.3.3 / bundled Swift Testing. Tests exercise source-qualified IDs/routes, strict callback/state/PKCE behavior, unknown enums and large revisions, nullable aggregate counts, permission freshness and scope/account invalidation, exact log offsets/UTF-8 boundaries/memory limits, bounded graph layout/cycles, generated transport boundaries, safe errors and oversized responses.
- `scripts/build-simulator.sh`: **passed** with Xcode 27.0 (27A266a), including the generated API integration and Debug-only synthetic UI mode. The generic simulator target requires no device signing.
- Xcode project, entitlements, Info.plist and privacy manifest parse successfully. Native UI-test source exists; execution and rendered UI review are tracked separately from the build and core-test results.

These results do not prove real AD FS interoperability, current directory authorization, production log/report APIs, APNs delivery, internal signing/distribution, managed-device behavior, or release readiness. Follow the repository's implementation tracker and T01–T12 acceptance gates for those results.
