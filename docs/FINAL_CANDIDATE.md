# Final candidate packaging and release gates

This procedure distinguishes local/CI validation from the controlled engineering
RC publisher. Local builds and `ci.yml` candidate dispatches do not tag or publish.
The main-only `release.yml` workflow publishes a reviewed RC after exact-source
checks and protected-environment approval; see [distribution](DISTRIBUTION.md).
Neither path authorizes production installation, source deployment or Apple
distribution, or completes the external acceptance gates in
[implementation status](IMPLEMENTATION_STATUS.md).

The curated Linux documentation includes the
[recorded-run contract](RUN_SELECTION.md) and
[release gap audit](RELEASE_GAP_AUDIT.md). The audit records the gaps found at that
review; use the current status and subsequent evidence to assess their closure.
Neither document turns a pending acceptance into a passing one.

## Compatible upstream versions

Candidate builds accept checksum-verified immutable released versions, prereleases
and development pseudo-versions. The current source pins Core `v1.9.0` and
Diagnose `v0.7.0-rc.1`; Control `v0.2.0` is verified deployed on both Dashboard Lab
sources. Diagnose real-provider acceptance and a compatible stable release remain
open; see the exact tuple and pending evidence in [release handoff](RELEASE_HANDOFF.md).
Final release requires reviewed, published compatible stable tags for Jobman Core,
Jobman Control and Jobman Diagnose, followed by explicit dependency-pin updates
and the required checks. Do not substitute the latest old tag merely to make a
version string stable: it must include the reviewed behavior used by Dashboard.

Dashboard embeds Core and Diagnose as Go modules. Their exact compiled versions
and checksums appear in `build.json`, along with `upstreamReleasePins`. Its
`stableVersions` checks these two first-party dependencies only; an unrelated
transitive dependency may legitimately use an immutable pseudo-version. Missing
or inconsistent first-party binary metadata fails the build. A stable version
string alone does not prove that a tag is published, protected review passed, or
the tuple is compatible. `controlCompatibilityVerified` and
`finalReleaseEligible` remain false in engineering candidate manifests.

Control is a separately deployed API, not a Dashboard Go dependency. Record the
exact released Control tag/commit and its advertised compatible contracts,
including `bounded-run-catalog`, monitoring reads, durable terminal events,
log/artifact metadata and source identity. Verify both source deployments where
multi-source behavior is claimed. Keep the original-event, cursor binding,
current authorization and historical-run evidence associated with those exact
versions. A module-only check cannot satisfy this gate.

The current builder accepts only `vX.Y.Z-rc.N`. Adding final-version publication
support is a separate reviewed step after the release gates close; there is no
flag that waives upstream or external acceptance.

## Controlled RC publication

1. Merge the reviewed source and wait for the latest successful push runs of
   Dashboard CI, repository checks, CodeQL, fuzz and Scorecard at that exact main
   commit. Record the full Git SHA; source advancement requires a newly tested
   selection.
2. Choose the next unused `vX.Y.Z-rc.N` and an explicit native build greater than
   the previous published build. RC7 used marketing version `0.1.0`, build `9`;
   the selected RC9 mapping is marketing version `0.1.0`, build `11`. The release
   workflow derives the three-component marketing version from the selected RC
   and requires the operator-provided native build. Do not infer the native build
   from the RC suffix.
3. Dispatch the publisher from main:

   ```sh
   gh workflow run release.yml --repo ryancswallace/Jobman-Dashboard --ref main \
     -f version=v0.1.0-rc.9 -f nativeBuild=11
   ```

4. The workflow builds canonical Linux archives and DEB/RPM/APK packages twice,
   compares checksums, builds an unsigned native archive from the same source,
   and exercises packages on amd64/arm64 in all three target distributions.
   Review and approve the resulting `main` environment deployment. Publication
   rechecks exact-source gates before proceeding.
5. The approved job assembles versioned GHCR images from the canonical Linux
   bytes, checks container payloads/runtime behavior, generates SPDX inventories
   and attestations, stages a draft and verifies downloaded assets and their
   publisher/source attestations before publishing a prerelease. It never tags
   `latest` or promotes a stable release. Retain the run URLs, exact asset set,
   source SHA, SHA256SUMS and immutable container digest.
6. Record independent artifact review and separately authorized acceptance against
   those exact bytes in [release handoff](RELEASE_HANDOFF.md). Keep prior successes,
   failures and partial receipts. If publication partially fails, retain its
   reserved version and evidence, repair the cause and choose a new RC; do not
   replace published tags/assets/images. Optional Cloudsmith distribution runs
   separately for a published candidate.

## Validation-only candidates and local builds

For a build without publication, dispatch `ci.yml` with `candidateVersion`,
explicit `nativeVersion` and `nativeBuild` at the reviewed source. A candidate
request includes the unsigned native archive and builds both Linux architectures
twice after backend/web/PostgreSQL and native checks. This path retains CI
artifacts; it does not create versioned native Linux packages, publish a GitHub
release or push containers.

```sh
gh workflow run ci.yml --ref <reviewed-branch-or-commit> \
  -f candidateVersion=v0.1.0-rc.9 -f nativeVersion=0.1.0 -f nativeBuild=11
```

Verify outer/inner Linux checksums, exact build metadata and native tar/inventory
hashes against the completion receipt. Inspect the committed run-selection,
release-gap and distribution guides. These checks do not transfer acceptance
from older versions to newly built bytes.

Local equivalents are `python3 scripts/build-release.py --version <new-rc>
--output-directory <new-absolute-directory>` and, from the same clean source,
`python3 ios/scripts/package-app.py unsigned --version <numeric-version> --build
<numeric-build> --output <new-absolute-directory>`. To package canonical Linux
archives locally, use `make release-packages INPUT_DIR=<candidate-directory>
OUTPUT_DIR=<new-package-directory>`. See [Linux installation](LINUX_INSTALLATION.md)
and the repository's `ios/README.md` for prerequisites and explicit
signed-development commands.

## Unsigned native provenance

Unsigned packaging uses only a clean committed Git archive, materializing safe
internal aliases from that archive. Ignored local signing/configuration files
and untracked assets cannot enter this source snapshot. The build intent and
receipt record the Git commit, source-archive hash, actual Xcode/Swift/iPhoneOS SDK
versions, requested app identity/version/build, and built executable/Info hashes.
The completion receipt also binds the full archive inventory and the produced
`JobmanDashboard-unsigned.xcarchive.tar.gz`. The inventory covers resources,
symbols and internal aliases, with bounded entry and content sizes.

The validation-only CI job retains that exact tar, inventory, intent, receipt and
build log; it does not create a second unrecorded tar. The release publisher
retains the original tar and inventory with sanitized `native-provenance.json`,
excluding raw logs, runner paths and build commands from release assets. Native
archives are not claimed to be byte-reproducible. An unsigned archive is not an installable IPA and proves no
signing, APNs, physical-device, company-managed-device or distribution behavior.
Existing organization inputs and external acceptance remain required.
