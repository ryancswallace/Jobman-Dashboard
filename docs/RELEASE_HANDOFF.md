# Jobman Dashboard initial-release handoff

Status: **release candidate; final acceptance remains open**. This handoff is
being completed as the exact candidate is exercised in the synthetic Lab. See
[implementation status](IMPLEMENTATION_STATUS.md) for the chronological evidence,
retained failures and requirement/work-package coverage. Nothing in this document
authorizes a production installation or company app distribution.

## Candidate and source

| Component | Exact candidate | Delivery / evidence |
| --- | --- | --- |
| Dashboard API, worker, broker and web | `v0.1.0-rc.6`; `633b5e3fdc08cc973e9f318faefccc298c713295` | [Passing candidate CI37225710875](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37225710875); exact Lab upgrade, large-graph traversal and recorded-run GET acceptance pass; publication pending |
| Native iPhone app | Marketing version`0.1.0`, build`8`; same Dashboard source | Same passing CI; verified unsigned arm64 iPhoneOS archive; signing/device acceptance pending |
| Jobman Control | `63641e922a4452bd6d63d6c41c5726b49fbfa6cf` | [PR29](https://github.com/ryancswallace/Jobman-Control/pull/29); full CI, PostgreSQL run-catalog tests and both Lab source upgrades pass |
| Jobman producer ACL/Slurm integration | `7eae7cc7164a308ca8d12de9011610ef6177a7a0` | [PR54](https://github.com/ryancswallace/Jobman/pull/54); CI and independent review pass; formal approval remains |
| Jobman Diagnose integration | `883f563b981e7fbd6ab6cbdb16d4489bbaf757fd` | [PR11](https://github.com/ryancswallace/Jobman-Diagnose/pull/11); CI and independent review pass; formal approval remains |

Dashboard embeds immutable public development module versions, with verified
checksums: Core`v1.8.1-0.20261003211041-62ac89b14547` and
Diagnose`v0.6.1-0.20261003234632-f47b6058c06e`. These differ from the separate
producer/Control executable revisions above. Final release requires compatible
published stable upstream tags and renewed dependency/compatibility checks.
GitHub branch protection and required eligible approvals remain enabled.

Verified candidate archives:

| File | SHA-256 |
| --- | --- |
| `jobman-dashboard_v0.1.0-rc.6_linux_amd64.tar.gz` | `ff7ba705c377595020454c658da3e2feaaf29b8e5d4cbb9e414e737149e3905f` |
| `jobman-dashboard_v0.1.0-rc.6_linux_arm64.tar.gz` | `c705c532b7c6261bc191e7cfa5a92dc7c1e1927368a37207245f38292cb8ad3c` |
| `JobmanDashboard-unsigned.xcarchive.tar.gz` | `46c546a2dc9f6278b52987989329e3384af7fc091c91ac811cce0f775957b707` |

Both Linux architectures were built twice in CI with identical archive checksums.
Each contains55 files and54 verified internal checksum entries. Bundled docs and
deployment examples match the committed source; both retain the same18-migration
ledger. The native archive's11-file inventory, source archive and executable/Info
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
cd jobman-dashboard_v0.1.0-rc.6_linux_arm64
sha256sum -c SHA256SUMS
./bin/jobman-dashboard version
./bin/jobman-log-broker version
```

For source checks, use the exact Go/Node/npm versions recorded in `go.version`,
`node.version`, and `npm.version`, a clean checkout and the committed dependency
locks. CI supplies an isolated PostgreSQL17.6 database to the test environment:

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
terminal events and inbox deduplication,25-viewer metadata/mixed workload checks,
authentication-key rotation, restore/replay, a fresh installation with compatible
upgrade/rollback, actual150-second recovery-watchdog intervention, and bounded
directory, broker, database, slow-Control and hard-NFS fault recovery. Original
failed attempts remain identifiable in the status document.

The packaged API and worker also refused a synthetic newer-schema ledger, with
unchanged database and configuration; the status document retains the two harness
failures and independently reviewed continuations that established this result.
Exact RC6 upgrade, recorded-run GET acceptance and complete large-graph traversal pass. The final web refresh-preference correction passes173 tests and independent review; the next candidate package is being prepared.
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
