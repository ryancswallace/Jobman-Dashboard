# Release engineering

Dashboard remains an engineering prerelease. The authoritative release tuple,
acceptance receipts and external blockers are in
[FINAL_CANDIDATE.md](docs/FINAL_CANDIDATE.md),
[RELEASE_HANDOFF.md](docs/RELEASE_HANDOFF.md) and
[IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md).

## Canonical candidates

`scripts/build-release.py` builds clean committed-source Linux amd64/arm64 bundles
with web assets, role-specific deployment examples, runbooks, license notices,
checksums and provenance. It accepts only `vX.Y.Z-rc.N`, enforces pinned toolchains
and immutable dependencies, and records `finalReleaseEligible: false`.
Use `make candidate VERSION=vX.Y.Z-rc.N OUTPUT_DIR=/absolute/new/directory`.
Do not reuse an existing version/output or silently include untracked files.

The existing `Dashboard CI` dispatch accepts candidate and explicit native version
inputs, builds Linux bundles twice and compares checksums, and retains an unsigned
native archive. It does not publish or sign. Follow the complete
[candidate procedure](docs/FINAL_CANDIDATE.md) for review, artifact verification and
separately authorized GitHub prerelease publication. Existing accepted evidence
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

## Promotion boundaries

Actual AD FS/direct AD, real APNs, signing/internal distribution, company-managed
phone and pilot checks remain required. Compatible reviewed upstream releases and
explicit dependency repinning also remain necessary. No semantic-release hook,
`latest` repair, package-registry upload, Homebrew formula, Apple distribution,
Sigstore attestation or stable tag is automatically produced by this scaffold.
Add those only against the accepted final artifact contract and configured
publisher identities. [Repository parity](docs/REPOSITORY_SCAFFOLDING.md) records
these intentional differences from the sibling CLI/service releases.

Production deployment/migration, organizational identity/network changes, company
phone distribution and paid account changes retain the approval boundaries in the
adopted [implementation prompt](docs/IMPLEMENTATION_PROMPT.md). Normal local checks,
GitHub CI and an engineering snapshot do not waive them.
