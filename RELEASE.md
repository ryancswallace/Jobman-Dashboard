# Release engineering

The [approved first-production-release scope](docs/FIRST_RELEASE_SCOPE.md) records
completed product review and the accepted post-release deferrals. Published
full release is selected as `v0.1.0`. Earlier engineering prereleases retain
their immutable identities. Exact release
tuples and acceptance receipts are in
[FINAL_CANDIDATE.md](docs/FINAL_CANDIDATE.md),
[RELEASE_HANDOFF.md](docs/RELEASE_HANDOFF.md) and
[IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md).

## Canonical candidates

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
[candidate procedure](docs/FINAL_CANDIDATE.md) for review, artifact verification and
GitHub publication. Existing accepted evidence
must remain bound to its exact bytes and source tuple.

## Additional local snapshot tooling

`make release-check` validates GoReleaser configuration and candidate boundary
tests. `make release-build` compiles Linux services. `make snapshot` builds web
assets, archives and deb/rpm/apk packages into ignored `dist/`, then checks required
contents and SHA-256 values. `make sbom` adds SPDX JSON for each archive/package;
`make package-smoke` checks Linux package layouts. Re-running SBOM generation refuses
to overwrite previous evidence; use a fresh snapshot directory for a new run.

These are developer snapshots, not canonical release candidates. GoReleaser's
publisher is disabled, and there are no signing or distribution credentials in
these commands. Packages install under `/opt/jobman-dashboard/snapshot` and ship
examples as documentation; they neither enable services nor change the active
candidate, create accounts or run migrations. Container builds use the separate
source Dockerfile and are not pushed automatically. Avoid treating snapshot
metadata as a claim of reproducible, accepted release bytes.

## Publication and later delivery

Corporate identity acceptance, real APNs, managed signing/distribution, pilot
checks and stable Diagnose publication are post-release work under the approved
scope. Their absence does not reopen product review or prevent the first
production release. Preserve compatible dependency pins and report their actual
versions; a release label does not establish untested interoperability.
The protected publisher creates a non-prerelease/latest full release for stable
versions and a prerelease candidate without latest status for RC versions. Both channels use
versioned GHCR images, canonical Linux packages, SPDX inventories and GitHub
attestations, with remote draft verification before publication. Optional
Cloudsmith distribution preserves the selected channel and exact package bytes.
Homebrew and Apple distribution remain separate. [Repository parity](docs/REPOSITORY_SCAFFOLDING.md) records
these intentional differences from the sibling CLI/service releases.

Production deployment/migration, organizational identity/network changes, company
phone distribution and paid account changes retain the approval boundaries in the
adopted [implementation prompt](docs/IMPLEMENTATION_PROMPT.md). Normal local checks,
GitHub CI and an engineering snapshot do not authorize those external actions.
These operational permission boundaries are separate from the completed product
review and the accepted post-release scope.
