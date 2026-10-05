# Contributing to Jobman Dashboard

Start with the [development guide](docs/DEVELOPMENT.md) and the
[repository map](docs/ARCHITECTURE.md). Follow the [code of conduct](CODE_OF_CONDUCT.md)
and report vulnerabilities through [SECURITY.md](SECURITY.md).

Use the exact Go, Node and npm versions in the root version files. The Linux
devcontainer supports services and web development; native work requires macOS
and full Xcode. Install project tools with `make setup`, then use `make quick-check`
while iterating and `make check` before submitting. `make ios-core ios-simulator`
is the separate native gate. Database integration requires an explicitly supplied
disposable database; see [testing](docs/TESTING.md).

Keep changes focused. Include tests for meaningful behavior and update contracts,
runbooks and [CHANGELOG.md](CHANGELOG.md) when needed. Use Conventional Commit
prefixes (`fix:`, `feat:`, `docs:`, `test:`, `ci:`, `chore:`). They organize history;
they do not currently trigger automatic publication.

Pull requests should explain the concrete problem, resulting behavior,
compatibility/security implications and verification. Include exact skipped
checks. Never attach private logs, tokens, database URLs, AD membership details,
APNs material, signing identities or raw production evidence.

See [release procedures](RELEASE.md) before changing packaging, workflows or
release metadata. Engineering candidates are not completed production releases.
