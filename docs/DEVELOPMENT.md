# Development

Use exact versions from `go.version`, `node.version` and `npm.version`. Python
3.11+ supports tooling; full Xcode on macOS supports iPhone builds. `make versions`
shows pinned quality tools. `make setup` downloads verified Go modules, installs
quality tools in ignored `bin/`, and installs the npm lockfile. ShellCheck is an
additional host prerequisite. The [devcontainer](../.devcontainer/README.md) includes it.

```sh
make setup
make web
make dev
# Separate terminal, optional hot reload:
npm run dev --prefix web
```

Open `http://127.0.0.1:8088` (built web) or Vite's printed loopback URL. Fixture mode
uses synthetic identities/data and refuses non-loopback listeners. Never expose it
with a public tunnel or proxy. Configured mode uses real TLS/OIDC and private
configuration; see [authentication](AUTHENTICATION.md).

Use `make quick-check` for Go race tests, web tests, contract checks, module/format
checks, local documentation links and builds. `make check` adds lint, workflow and
shell checks, spelling, snapshot configuration, web build and Linux cross-builds.
`make vulncheck coverage-check fuzz docker-check docker-smoke snapshot` are further
security, coverage, fuzz, container and packaging gates; CI runs them in separate
jobs/schedules. Docker must be running for container checks. Network access is
needed to install tools, audit dependencies, fetch images and resolve modules.

`make ios-core ios-simulator` runs separately on macOS. See the [native guide](../ios/README.md)
for simulator UI tests, signing and explicit packaging inputs. Linux cannot build
the iPhone app. Neither Swift package tests nor an unsigned archive establish
physical-device or APNs acceptance.

`make contracts` regenerates clients from `api/openapi.json`. `make update` also
regenerates the Xcode project; a second run must produce identical output.
Never edit generated contracts directly. Version upgrades are reviewed changes
across version files, container digest pins, devcontainer arguments and module
metadata; `make update` deliberately does not fetch the newest versions.

See [testing](TESTING.md), [repository parity](REPOSITORY_SCAFFOLDING.md), and
[release procedures](../RELEASE.md). There is no broad cleanup target: remove only
known task-owned build artifacts, preserving local configuration and evidence.
