# Release packaging and publication

The protected `release.yml` publisher accepts full releases (`vX.Y.Z`) and
engineering candidates (`vX.Y.Z-rc.N`). Follow [distribution](DISTRIBUTION.md)
to publish and verify a version after exact-source checks. The
[first-release scope](FIRST_RELEASE_SCOPE.md) records approved product coverage.
Keep version-specific verification in the [release record](RELEASE_HANDOFF.md).

## Compatible upstream versions

Dashboard embeds Core and Diagnose as immutable Go modules. `build.json`
records the compiled modules and their checksums and includes the
`upstreamReleasePins` summary.
`stableVersions` checks these two modules. An unrelated transitive dependency
can use an immutable pseudo-version. Missing or inconsistent first-party binary
metadata fails the build. Candidate metadata records `finalReleaseEligible: false`.
Full-release metadata binds the approved scope document.

Control runs separately. Record its exact released version and advertised
contracts, including `bounded-run-catalog`, monitoring reads, durable terminal
events, log/artifact metadata and source identity. Verify both source deployments
for claims about multi-source behavior. Bind original-event, cursor binding,
current authorization and historical-run evidence to those exact versions.
The release builder leaves `controlCompatibilityVerified` false. See
[compatibility](COMPATIBILITY.md) and the [Lab catalog](LAB_RUN_CATALOG.md).

## Validation-only candidates and local builds

For a build without publication, dispatch `ci.yml` with `candidateVersion`,
explicit `nativeVersion` and `nativeBuild` at the reviewed branch or tag. A candidate
request includes the unsigned native archive and builds both Linux architectures
twice after backend/web/PostgreSQL and native checks. This path retains CI
artifacts for inspection.

```sh
gh workflow run ci.yml --ref "$REVIEWED_REF" \
  -f candidateVersion=v0.1.1 -f nativeVersion=0.1.1 -f nativeBuild=16
```

Verify outer/inner Linux checksums, exact build metadata and native tar/inventory
hashes against the completion receipt. Inspect the committed run-selection,
installation and distribution guides. These checks do not transfer acceptance
from older versions to newly built bytes.

Local equivalents are `python3 scripts/build-release.py --version <version>
--output-directory <new-absolute-directory>` and, from the same clean source,
`python3 ios/scripts/package-app.py unsigned --version <numeric-version> --build
<numeric-build> --output <new-absolute-directory>`. To package canonical Linux
archives locally, use `make release-packages INPUT_DIR=<candidate-directory>
OUTPUT_DIR=<new-package-directory>`. See [Linux installation](LINUX_INSTALLATION.md)
and the repository's `ios/README.md` for prerequisites and explicit
signed development commands.

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
Organization signing and integration work is scheduled after the first release;
those physical requirements still apply whenever an installable native build is
actually produced.
