# Jobman Dashboard initial-release handoff

## Current candidate — RC10

Published source: `5b20dfb7a51d206aeb7132db94d961a14b6ab03e`, including merged
[PR19](https://github.com/ryancswallace/Jobman-Dashboard/pull/19),
[PR20](https://github.com/ryancswallace/Jobman-Dashboard/pull/20) and
[PR21](https://github.com/ryancswallace/Jobman-Dashboard/pull/21).
RC10 includes unsigned iPhone marketing version `0.1.0`, build `12`.
[Main CI](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37514864598)
and [repository checks](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37514864586)
passed, together with PR21 checks and independent review.
[RC10](https://github.com/ryancswallace/Jobman-Dashboard/releases/tag/v0.1.0-rc.10)
was published as a prerelease at `2026-10-06T19:08:37Z` by
[release run37515898448](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37515898448).
All 25 release assets and 26 asset/container attestations passed independent
verification. Six real package installations each verified all 88 canonical files
and modes; anonymous pulls, payload comparison and runtime checks passed on both
container platforms. The native archive is unsigned and non-installable.

| Artifact | Verified SHA-256 or manifest digest |
| --- | --- |
| `jobman-dashboard_v0.1.0-rc.10_linux_amd64.tar.gz` | `bbbac74c570b90e14774af0c44c574fbc0e5cf18b1f69b151a337483c52e6a07` |
| `jobman-dashboard_v0.1.0-rc.10_linux_arm64.tar.gz` | `67b593fdbf5e1fe840662fab4202aa6819d6b2af9da7197cf9dce6519e82bfe1` |
| `JobmanDashboard-unsigned.xcarchive.tar.gz` (build `12`) | `7c00b2cdd8b4097d9ece9734b926ce696504ef00f6dba975b0fbe84a7b663170` |
| `ghcr.io/ryancswallace/jobman-dashboard:v0.1.0-rc.10` | `sha256:7b8687c7b14869ff7afd9d02c471c33e5fb3206fd8842650b02a90afd402ed70` |

Exact-RC10 Lab staging and activation passed for API, worker and broker; all
running release binaries match the verified Linux archive. The only application
configuration change was API `webRoot`; no migrations were run, Control process
IDs were preserved, and rollback material remains private. Authenticated acceptance passed
46 GETs, 30 exact command reads, eight namespace denials, the 32-request preview
catalog/rule workflows, and metadata/log reports with sealed citations in
5.753 seconds. The web-session test initially skipped because its opt-in was
missing; an explicit rerun passed in 0.384 seconds. All seven served HTTPS static
web files match the source. The first `/index.html` probe returned an empty 301;
the harness was corrected to request `/` and require 200, with no app change.
Rendered-browser testing was not rerun on RC10; earlier Chrome evidence remains
tied to its recorded engineering deployment.

The sanitized [distribution verification summary](evidence/rc10-distribution.json)
and [Lab verification summary](evidence/rc10-lab.json) retain the inspectable public
results. The original independent artifact receipt has SHA-256
`54c5987db869c060364df03d36eec03d1b7d0aece5b0a4e38a8e085fcf2e6b46`.
Detailed deployment/test logs, rollback material, initial skips and harness
failures remain private; raw logs and private configuration are not release
assets. This post-publication record supplements the immutable bundles, which
retain their original build-time documentation. Older acceptance below does not
imply additional RC10 reruns.
See [distribution](DISTRIBUTION.md) for signed provenance/checksum verification,
version-owned packages, non-root containers and explicit service activation.

| Upstream | Exact selected source and current evidence |
| --- | --- |
| Embedded Core `v1.9.0` | `c0c651f8a555bb56d1bdb1e65f56c47c288dd285`; published stable module |
| Deployed Control `v0.2.0` | `733dea9001116ebd9327351e40168491f780b870`; both Dashboard Lab sources verified; pre-RC10 checks passed 46 authenticated GETs, 30 exact command reads and eight namespace denials |
| Embedded Diagnose `v0.7.0-rc.1` | `d9021503db6346d363a82d381bd400da1542f04f`; published candidate; real-provider acceptance and compatible stable publication remain open |

RC8 failed after pushing its immutable container index
`sha256:c29824b09c16c30064de593f30fcd764abe0a047871cc4d0492a36e61c1ca8d5`;
no GitHub RC8 tag/release was created. RC9 reached tag/draft creation and retained
all 25 assets, then failed draft discovery. The new read-only verifier subsequently
validated every RC9 asset attestation and the container digest/source; all six
packages contain the verified 88-file payload and native build `11` passed its
checks. The verified RC9 draft (ID `405033021`) remains unpublished and immutable.
Original [RC8](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37508828710)
and [RC9](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37512314090)
runs and their failures remain evidence; neither is relabeled as a successful release.

Final acceptance remains open for corporate AD FS/direct AD, real APNs, signed
company-managed-device delivery, manual accessibility and pilot/operating owners,
plus stable Diagnose acceptance. The earlier synthetic Lab Chrome-access blocker
and old PR-approval backlog are resolved; their RC7 statements below are historical.
The Dashboard `main` environment now has its personal Cloudsmith API key.
Authentication passed in run `37522208724`, but the original `jobman/dashboard`
destination was unavailable. The operator selected the existing `jobman/stable`
repository used by sibling publishers. Run `37532251312` uploaded the amd64 DEB;
its verification lookup exposed incompatible filename escaping. The query is
corrected to match the sibling publishers. Run `37534170921` verified DEB/RPM
and uploaded the amd64 APK, exposing Cloudsmith Alpine filename normalization.
The publisher now identifies APKs by name, version and architecture with the
original checksum. All three amd64 packages are present; the arm64 packages and
all-six workflow verification remain pending. This destination does not change Dashboard prerelease status or imply
production or company-device distribution approval.

## Historical RC7 handoff — retained unchanged

The following record preserves the original RC7 tuple, hashes and acceptance
statements. Current versions and outstanding actions are summarized above.

Status: **release candidate; final acceptance remains open**. This handoff is
supported by exact candidate checks in the synthetic Lab. See
[implementation status](IMPLEMENTATION_STATUS.md) for the chronological evidence,
retained failures and requirement/work-package coverage. Nothing in this document
authorizes a production installation or company app distribution.

## Candidate and source

| Component | Exact candidate | Delivery / evidence |
| --- | --- | --- |
| Dashboard API, worker, broker and web | `v0.1.0-rc.7`; `d10fb5f813efa3599a0bc5f79b3ca88826210510` | [Passing candidate CI37229091125](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37229091125); packages verified; all 13 Lab upgrade phases and 57 authenticated run/log GET checks pass |
| Native iPhone app | Marketing version `0.1.0`, build `9`; same Dashboard source | Same passing CI; verified unsigned arm64 iPhoneOS archive; signing/device acceptance pending |
| Jobman Control | `63641e922a4452bd6d63d6c41c5726b49fbfa6cf` | [PR29](https://github.com/ryancswallace/Jobman-Control/pull/29); full CI, PostgreSQL run-catalog tests and both Lab source upgrades pass |
| Jobman producer ACL/Slurm integration | `7eae7cc7164a308ca8d12de9011610ef6177a7a0` | [PR54](https://github.com/ryancswallace/Jobman/pull/54); CI and independent review pass; formal approval remains |
| Jobman Diagnose integration | `883f563b981e7fbd6ab6cbdb16d4489bbaf757fd` | [PR11](https://github.com/ryancswallace/Jobman-Diagnose/pull/11); CI and independent review pass; formal approval remains |

Dashboard embeds immutable public development module versions, with verified
checksums: Core `v1.8.1-0.20261003211041-62ac89b14547` and
Diagnose `v0.6.1-0.20261003234632-f47b6058c06e`. These differ from the separate
producer/Control executable revisions above. Final release requires compatible
published stable upstream tags and renewed dependency/compatibility checks.
GitHub branch protection and required eligible approvals remain enabled.

Published artifacts: [v0.1.0-rc.7 prerelease](https://github.com/ryancswallace/Jobman-Dashboard/releases/tag/v0.1.0-rc.7). The ten uploaded assets were downloaded and verified against the reviewed checksums; the tag resolves to the exact candidate source.

Verified candidate archives:

| File | SHA-256 |
| --- | --- |
| `jobman-dashboard_v0.1.0-rc.7_linux_amd64.tar.gz` | `cb459bb5d8672a80306532951ffa5ade899764af71d3fe10c0268d83b0009e4b` |
| `jobman-dashboard_v0.1.0-rc.7_linux_arm64.tar.gz` | `19b3ded0315e4781f07f3885fdca360371c0ac0d28f19abf9a6c53d0ad7e9956` |
| `JobmanDashboard-unsigned.xcarchive.tar.gz` | `10b0a93c676749d966a9a8e3a43963a122c896c570448f63963f0dd4063b5650` |

Both Linux architectures were built twice in CI with identical archive checksums.
Each contains 58 files and 57 verified internal checksum entries. Bundled docs and
deployment examples match the committed source; both retain the same 18-migration
ledger. The native archive's 11-file inventory, source archive and executable/Info
hashes were independently verified. Native packaging is not claimed to be byte
reproducible, signed, installable, or approved for internal distribution.

## Installation and testing

Use [Linux installation](LINUX_INSTALLATION.md) for separate service identities,
private TLS, database roles, explicit forward migrations and grants, source
registration, secret material and systemd configuration. Examples require real
operator inputs; copying them unchanged is not a working production deployment.
Use [operations](OPERATIONS.md), [event recovery](EVENT_RECOVERY.md),
[purpose keys](PURPOSE_KEYS.md) and [authentication](AUTHENTICATION.md) for backup,
restore, rotation and access revocation. Keep previous immutable packages and
current keys/configuration when planning a compatible upgrade or rollback.

After obtaining an approved package, verify its outer checksum before extraction
and its inner checksum before executing binaries on the matching architecture.
Extract the verified archive as an ordinary user into a new, empty staging
directory, preserving the package directory name, before the `cd` below:

```sh
sha256sum -c SHA256SUMS
cd jobman-dashboard_v0.1.0-rc.7_linux_arm64
sha256sum -c SHA256SUMS
./bin/jobman-dashboard version
./bin/jobman-log-broker version
```

For source checks, use the exact Go/Node/npm versions recorded in `go.version`,
`node.version`, and `npm.version`, a clean checkout and the committed dependency
locks. CI supplies an isolated PostgreSQL 17.6 database to the test environment:

```sh
npm ci --prefix web
make check
npm run build --prefix web
./ios/scripts/test-core.sh
python3 ios/scripts/test-package-app.py
./ios/scripts/build-simulator.sh
```

Database tests explicitly skip without their configured disposable database;
a local skip is not equivalent to passing CI's PostgreSQL checks. The iOS scripts
need Xcode for simulator/device builds. See [iOS instructions](../ios/README.md)
for UI tests and explicit development archive/export commands. A personal-iPhone
build still needs the approved team, bundle identifier, certificate and profile;
no paid enrollment, account agreement, provisioning change or signed export has
been performed.

Lab integration commands are opt-in and must use an immutable reviewed binary
and fresh operation-specific receipts. See [actual execution](LAB_EXECUTION.md),
[mixed load](LAB_MIXED_LOAD.md), [recorded runs](LAB_RUN_CATALOG.md),
[graph acceptance](GRAPH_CLIENT_ACCEPTANCE.md) and [restore](LAB_RESTORE.md).
Do not rerun a consumed one-shot operation or replace failed evidence.

## Acceptance summary and limits

Implemented web and native workflows include source-qualified namespace and
cross-Control views; jobs and recorded runs; logs and artifact metadata; targets;
Slurm arrays, collections and dependency graphs; deterministic diagnosis and
citations; all agreed personal alert scopes/outcomes; durable inbox, preferences
and notification-device lifecycle. The product remains monitoring-only.

The retained Lab evidence includes actual subprocess and Slurm executions,
cross-user local/NFS log access, two-Control authorization and aggregation,
direct-group capability unions and active revocation, real diagnosis/citations,
terminal events and inbox deduplication, 25-viewer metadata/mixed workload checks,
authentication-key rotation, restore/replay, a fresh installation with compatible
upgrade/rollback, actual 150-second recovery-watchdog intervention, and bounded
directory, broker, database, slow-Control and hard-NFS fault recovery. Original
failed attempts remain identifiable in the status document.

The packaged API and worker also refused a synthetic newer-schema ledger, with
unchanged database and configuration; the status document retains the two harness
failures and independently reviewed continuations that established this result.
Exact RC7 deployment passes all 13 upgrade phases. API, worker and broker match
the verified arm64 binaries; schema 18, configuration 8 and both source authorities
remain unchanged. Its authenticated recorded-run smoke passes 57 GET requests
in 0.854317 seconds. The reused immutable test binary comes from RC6 source
`633b5e3`; its authentication test source and Go dependencies are unchanged.
Complete 10,000-node/100,000-edge graph traversal and 64-page Back replay passed
on RC6/Control636 in 51.31 seconds. RC7 adds the reviewed web refresh repair
(173 web tests pass) and packaged handoff/run/graph guides. Go, native, contract
and migration source is unchanged; earlier fault and load scenarios remain tied
to their recorded versions rather than being claimed rerun on RC7.

This separately attached handoff records post-build evidence. The immutable Linux
bundles retain the build-time handoff from the exact candidate source.
The current Control executor supports one run per submitted job. Actual retained
jobs therefore demonstrate single-run source integration; real PostgreSQL and
client fixtures separately test selection among multiple recorded runs. No retry
execution feature or invented historical execution is claimed.

Web fixture rendering and automated accessibility checks, native simulator
workflows and inspected screenshots establish their stated synthetic coverage.
Live browser access is blocked by the current browser policy and needs an
approved environment. Simulator/Keycloak evidence does not establish actual
AD FS, APNs, physical Keychain behavior, manual accessibility or company-managed
phone acceptance.

## Inputs needed to complete the release

| Prerequisite | Concrete input / action | Owner |
| --- | --- | --- |
| Corporate identity | AD FS issuer/version, registered web/native clients and audience/claim mappings; approved direct-AD endpoint, group mapping and test identities; run live sign-in, union and active-revocation checks | Ryan to identify IAM/AD FS owner |
| Apple push | Approved APNs team/topic/key credentials, provider egress and device connectivity; verify background delivery, denied/revoked access and notification navigation on a real phone | Ryan to identify Apple/network owners |
| Signed internal iPhone delivery | Existing distribution channel/MDM, approved app identifier, signing certificate/profile and managed test iPhone; sign/export, install and test under actual management policy | Ryan and company device-management owner |
| Browser and accessibility | Approved live-browser test environment plus manual web/iPhone accessibility checks | Ryan and test participants |
| Upstream delivery | Eligible non-author GitHub approvals; protected merges and compatible stable releases, followed by Dashboard dependency updates and required checks | Repository owners / eligible reviewers |
| Pilot and operations | Private hosting allocation, operating/renewal owners and pilot acceptance; approve the concrete production installation and recovery plan | Ryan to identify infrastructure/operator owners |

Production AD/AD FS/MDM/network changes, production deployment/migration, paid
capacity/enrollment and company distribution require the concrete final approval
specified in the adopted implementation prompt. Candidate GitHub delivery and
synthetic Lab work are already authorized. Final release must retain its gates
until the required external checks have actually succeeded.
