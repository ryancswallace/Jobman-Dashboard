# Repository development tooling

`Makefile` is the supported entry point. Tool versions are pinned there and install
under ignored `bin/`. The sibling-derived Go documentation checker lives in
`docscheck/`; it excludes generated, dependency and local configuration directories.
`check-coverage.awk` enforces the shared coverage floor.

`artifacts.py` verifies local GoReleaser checksums, platforms and required archive
contents, and can generate SPDX inventories with Syft. `container-smoke.sh`
checks image identity/assets and fail-closed startup. `package-smoke.sh` inspects
package layouts without installing services. These scripts never publish artifacts.

Existing canonical candidate and contract generators remain in `scripts/`.
Native generators/build helpers remain in `ios/scripts/`. Do not create duplicate
implementations just to match sibling directory names. `make update` regenerates
contracts and the Xcode project; it does not change toolchain versions or release
status. See [development](../docs/DEVELOPMENT.md) and [release](../RELEASE.md).
