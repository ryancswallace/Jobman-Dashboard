# Release engineering

The [approved first-production-release scope](docs/FIRST_RELEASE_SCOPE.md) records
completed product review and the accepted post-release deferrals. See the
[release record](docs/RELEASE_HANDOFF.md) for exact stable and historical
release identities, and the [implementation record](docs/IMPLEMENTATION_STATUS.md)
for chronological acceptance evidence.

## Canonical release builds

`scripts/build-release.py` builds clean committed-source Linux amd64/arm64 bundles
with web assets, role-specific deployment examples, runbooks, license notices,
checksums and provenance. It accepts `vX.Y.Z` full releases and `vX.Y.Z-rc.N`
candidates and enforces pinned toolchains and immutable dependencies. Stable
builds record `releaseState: release`, scoped `finalReleaseEligible: true` and
the scope document digest. Candidate eligibility remains false. Compiled
dependency stability and deployed Control compatibility are separate facts.
Use `make candidate VERSION=v0.1.0 OUTPUT_DIR=/absolute/new/directory`.
Do not reuse an existing version/output or silently include untracked files.

The existing `Dashboard CI` dispatch accepts candidate and explicit native version
inputs, builds Linux bundles twice and compares checksums, and retains an unsigned
native archive. That validation-only path does not publish or sign. The separate manual
`release.yml` workflow builds, verifies, attests and publishes the selected channel;
see [distribution](docs/DISTRIBUTION.md). Follow the complete
[release procedure](docs/FINAL_CANDIDATE.md) for review, artifact verification and
GitHub publication. Existing accepted evidence
must remain bound to its exact bytes and source tuple.

## Additional local snapshot tooling

`make release-check` validates GoReleaser configuration and candidate boundary
tests. `make release-build` compiles Linux services. `make snapshot` builds web
assets, archives and deb/rpm/apk packages into ignored `dist/`, then checks required
contents and SHA-256 values. `make sbom` adds SPDX JSON for each archive/package;
`make package-smoke` checks Linux package layouts. Re-running SBOM generation refuses
to overwrite previous evidence; use a fresh snapshot directory for a new run.

These snapshots are for developer verification. GoReleaser's
publisher is disabled, and there are no signing or distribution credentials in
these commands. Packages install under `/opt/jobman-dashboard/snapshot` and ship
examples as documentation; they neither enable services nor change the active
release, create accounts or run migrations. Container builds use the separate
source Dockerfile and are not pushed automatically. Avoid treating snapshot
metadata as a claim of reproducible, accepted release bytes.

## Publication and later delivery

Corporate identity acceptance, real APNs, managed signing/distribution, pilot
checks are post-release work under the approved scope. Review any update to the
embedded Diagnose module against Dashboard's deterministic report and evidence
contracts. Preserve exact dependency pins and report their versions.
The protected publisher creates a non-prerelease/latest full release for stable
versions and a prerelease candidate without latest status for RC versions. Both channels use
versioned GHCR images, canonical Linux packages, SPDX inventories and GitHub
attestations, with remote draft verification before publication. Optional
Cloudsmith distribution preserves the selected channel and exact package bytes.
Homebrew and Apple distribution remain separate. [Repository parity](docs/REPOSITORY_SCAFFOLDING.md) records
these intentional differences from the sibling CLI/service releases.

## Operational approval

Obtain approval for the exact action and recovery plan before production deployment
or migration, organizational identity/network/device management changes, or
company phone distribution. Confirm the approval scope before spending money,
accepting account or license agreements, enrolling in an Apple program, or adding
paid capacity. Live Lab work requires explicit session authorization under
[AGENTS.md](AGENTS.md).
