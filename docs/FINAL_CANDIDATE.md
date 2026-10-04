# Final candidate packaging and release gates

This is an offline packaging procedure. It does not authorize a release tag,
upload, source deployment, production installation, or Apple distribution.
A successful candidate build proves the recorded artifacts and checks; it does
not complete the external acceptance gates in
[the implementation status](IMPLEMENTATION_STATUS.md).

The curated Linux documentation includes the
[recorded-run contract](RUN_SELECTION.md) and
[release gap audit](RELEASE_GAP_AUDIT.md). The audit records the gaps found at that
review; use the current status and subsequent evidence to assess their closure.
Neither document turns a pending acceptance into a passing one.

## Compatible upstream versions

Candidate builds accept checksum-verified, immutable development pseudo-versions.
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

## Exact candidate procedure

1. Finish scoped reviews and required CI, commit the intended source, and record
   its full Git SHA. Build from a clean checkout of that SHA; preserve unrelated
   developer files separately instead of adding them to a release.
2. Select the next unused candidate version and explicit native numeric marketing
   version/build. Record their mapping to the same source SHA. Never overwrite an
   existing artifact or infer native version numbers from a prerelease string.
3. Dispatch the existing `ci.yml` workflow at that SHA with `candidateVersion`,
   `nativeVersion` and `nativeBuild`. A candidate dispatch includes the unsigned
   iPhone archive. The Linux candidate job waits for backend/web/PostgreSQL and
   native checks, builds both Linux architectures twice, and requires matching
   archive checksums.
4. Retain the CI run URLs, artifact SHA-256 values, full source SHA and toolchain
   records. Verify outer and inner Linux checksums, exact build metadata, and the
   native tar/inventory digests against its completion receipt. Check the included
   run-selection and release-gap documents are the committed versions.
5. Independently review the resulting tuple and plan any separately authorized
   acceptance against those exact bytes. Keep prior successes, failures and
   partial receipts; a new candidate does not inherit unperformed acceptance.

Local equivalents are `python3 scripts/build-release.py --version <new-rc>
--output-directory <new-absolute-directory>` and, from the same clean source,
`python3 ios/scripts/package-app.py unsigned --version <numeric-version> --build
<numeric-build> --output <new-absolute-directory>`. See
[Linux installation](LINUX_INSTALLATION.md) and the repository's `ios/README.md`
for prerequisites and explicit signed-development commands.

## Unsigned native provenance

Unsigned packaging uses only a clean committed Git archive, materializing safe
internal aliases from that archive. Ignored local signing/configuration files
and untracked assets cannot enter this source snapshot. The build intent and
receipt record the Git commit, source-archive hash, actual Xcode/Swift/iPhoneOS SDK
versions, requested app identity/version/build, and built executable/Info hashes.
The completion receipt also binds the full archive inventory and the produced
`JobmanDashboard-unsigned.xcarchive.tar.gz`. The inventory covers resources,
symbols and internal aliases, with bounded entry and content sizes.

The CI job uploads that exact tar, inventory, intent, receipt and build log; it
does not create a second unrecorded tar. Native archives are not claimed to be
byte-reproducible. An unsigned archive is not an installable IPA and proves no
signing, APNs, physical-device, company-managed-device or distribution behavior.
Existing organization inputs and external acceptance remain required.
