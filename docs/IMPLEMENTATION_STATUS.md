# Initial release implementation status

Updated: 2026-10-03. Release state: **implementation in progress; no release candidate yet**.

The adopted [implementation prompt](IMPLEMENTATION_PROMPT.md), [requirements](REQUIREMENTS.md), and [design](DESIGN.md) govern this work. The persistent goal remains active through implementation, GitHub delivery, and acceptance. A passing local slice is not release acceptance.

## Baseline and preservation

| Repository | Starting revision | Existing work |
| --- | --- | --- |
| Jobman-Dashboard | `925517d` baseline now pushed | Adopted documentation and existing branding preserved; foundation merged through PR1; authentication and feature integration continuing. |
| Jobman | `c0980f3` | Pre-existing modifications to `internal/agent/service.go`, `internal/agent/service_test.go`, `internal/backend/slurm/slurm.go`, and `internal/backend/slurm/slurm_test.go`; do not overwrite or include accidentally. |
| Jobman-Control | `4735be5` | Clean at start. |
| Jobman-Diagnose | `c5db4a8` | Clean at start. |
| Jobman-Lab | `014d50e` | Clean at start; currently outside writable workspace. Read-only inspection permitted; request scoped runtime access for authorized changes. |

Origins verified as the matching `ryancswallace/Jobman*` GitHub repositories. No task-attached worktrees or PRs existed at start. Control implementation PRs and Lab evidence are recorded below. Dashboard foundation [PR1](https://github.com/ryancswallace/Jobman-Dashboard/pull/1) merged as `649184cf25b1198e4ae1a76dc8061e9801a69641` after independent review and green checks at `6908a8396d5e8275b150ca2f522a736d65e4e5ed`. Authentication [PR2](https://github.com/ryancswallace/Jobman-Dashboard/pull/2) merged as `6ce3cb4b017608f210e9d878ca219074d374c1ec` after independent review and [green backend/web and iPhone CI](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37155680658) at `3288b9413d2a8fcde2f89e7f08eea3977bd406ec`. Monitoring/log [PR3](https://github.com/ryancswallace/Jobman-Dashboard/pull/3) merged as `418e3b88b76d2e268f537d7d9d843ecfa594b6cd` after independent review and [green CI](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37159069366) at `16d06abdc00c37516e247b1dd8c0fd5290035072`. Artifact metadata and native group integration continue on `feat/dashboard-metadata`.

Host discovery: Go 1.27.1, Node 26.5.1, npm 11.17.0, Swift 6.3.3 command-line tools. Control pins Go 1.26.6; Dashboard will use that reproducible toolchain baseline. Ryan installed and licensed Xcode **27.0**. The native app now passes a complete unsigned iPhone simulator build; 23 core tests pass. Two simulator UI workflow tests passed on iPhone 18 Pro / iOS 27; rendered screenshots were inspected and layout/list-loading defects fixed. Native investigation checkpoint now has 33 passing core tests, five simulator UI workflows, and an unsigned generic iPhone build after OAuth and workload/log integration.

## Work packages

| Package | State | Current work / remaining acceptance |
| --- | --- | --- |
| WP01 | In progress | Go fixture service, authored OpenAPI, generated TS/Swift DTOs and clients, two-source fixtures, PostgreSQL migrations, React/SwiftUI projects and CI authored; backend/web and unsigned native CI passed at `de383e5` and native fixes `9c00f2f`. |
| WP02 | In progress | OIDC web/native validation, PKCE browser flow, audited durable sessions, CSRF, verified account aliases, source-bound delegation and Control adapter implemented/tested. Real AD FS and integrated directory validation remain. |
| WP03 | In progress | Control agent: contributing membership grants, exact capability union, current-principal discovery and migration compatibility. Direct LDAP reconciliation, freshness and active revocation implemented in PR20; real AD acceptance remains. |
| WP04 | In progress | Control owner/lifecycle/filter/summary contracts tested and pushed; Dashboard aggregation/cursors tested against fixtures. Production HTTP adapter now tested with real loopback mTLS; actual Control directory/delegation integration remains. |
| WP05 | In progress | Control bounded catalogs/children/dependencies/neighborhoods pushed and Lab-tested; Dashboard bounded catalogs, detail, dependency and neighborhood adapters/routes plus web inspection implemented; native integration is implemented; actual multi-Control acceptance remains. |
| WP06 | In progress | Bounded Control manifests PR21; source-bound mTLS log broker, isolated no-follow checksum reads, authorization-bound cursors and deployment CLI implemented. Core security review and actual local mTLS integration pass. Producer ACL policy and actual Control/NFS monitoring integration now pass; failure/load acceptance remains. |
| WP07 | In progress | Core schema2/shared collector PR53 including citation-ID hardening at `62ac89b` passes full CI and independent review. Published compatible module remains gated by required GitHub review. |
| WP08 | In progress | Public deterministic Diagnose library/shared reports delivered in PR10 at `2eabdfde1c2d24da95fff32550023fb955b287b0`, all CI checks pass; independent review fixes verified. Reproducible public core pseudo-version used with GOWORK=off; approved released core tag, durable Dashboard tasks and final integration remain. |
| WP09 | Not implemented | Transactional terminal events, serialized durable feed and replay-safe ingestion. |
| WP10 | In progress | Personal preferences wired to authenticated account/revision with database persistence. Rule/inbox/device/APNs/event workers remain. |
| WP11 | In progress | Web agent: native web navigation, transport/state, accessible workflow screens. |
| WP12 | In progress | Native agent: Swift core, SwiftUI iPhone project, OAuth/Keychain and source-qualified state. |
| WP13 | In progress | Migration runner and runtime DB foundation, reproducible build/check commands and CI authored. Strict production config, secret-file loading, direct HTTPS modes, explicit migration identity, runtime schema verification and authentication runbook now implemented; packaging/full operations remain. |
| WP14 | Not implemented | Integrated T01–T12 evidence, independent security review, load/restore/pilot. |
| WP15 | External inputs pending | Internal channel/signing/APNs/managed-device ownership unknown; implement reproducible development packaging first. |

## Requirement coverage

All requirements remain open until their implementation and required verification are complete. The design's traceability table supplies the full WP/test mapping.

| Requirement | State | Primary implementation |
| --- | --- | --- |
| R01 | Open | Authentication and private connectivity |
| R02 | In progress | Authorized discovery and capability union |
| R03 | In progress | Web/iPhone navigation and source-qualified routes |
| R04 | Open | Complete scoped summaries |
| R05 | Open | Bounded job inventory/filtering |
| R06 | Open | Factual job details |
| R07 | In progress | Separate phase/outcome/intent/confidence DTOs |
| R08 | In progress | Cancellable foreground refresh and authorization expiry |
| R09 | Open | Safe incremental logs |
| R10 | In progress | Bounded authorized artifact metadata, actual run identity, web pagination/filtering and explicit unverified bytes; native/live-source acceptance remains. |
| R11 | Open | Target configuration |
| R12 | Open | All agreed alert scopes and outcomes |
| R13 | Open | Durable deduplicated inbox and delivery |
| R14 | Open | Notification disclosure and revocation |
| R15 | Open | Personal settings |
| R16 | In progress | Direct AD grants, union, removal and freshness |
| R17 | Open | Multi-Control namespace aggregation |
| R18 | In progress | Native app; internal delivery unverified |
| R19 | Open | Collections and Slurm arrays |
| R20 | Open | Dependency graphs |
| R21 | Open | Real deterministic diagnosis and citations |
| R22 | Open | Independent authorized source connections |

## Acceptance evidence

T01–T12: **not yet passed for an integrated release**. Fixtures, Keycloak, mocks, and simulator tests never establish real AD FS, APNs, NFS, Slurm or company-managed device acceptance.

| Slice | Tested evidence | Delivery |
| --- | --- | --- |
| Control authorization | `make quick-check`; real PostgreSQL isolated-schema integration including migration, union, overlapping grant removal, last-grant revocation and current user; local full checks through release/build, Docker smoke unavailable locally. | Updated `b6ce8dd8b1e2e7bb8d92a3aaa59fc64b7d563bd5`; [PR16](https://github.com/ryancswallace/Jobman-Control/pull/16); [CI run](https://github.com/ryancswallace/Jobman-Control/actions/runs/37151811228). Independent review of this commit found no blocking defects; CI green. Main update and malformed-query hardening reviewed. GitHub requires formal approval; user asked to arrange eligible review. No merge. |
| Control monitoring | Real Lab integration: summary/drill-down agreement, completion half-open boundary, original owner versus history importer, instance identity and actual agent lifecycle timestamps. Quick/full non-Docker checks pass, 65.0% coverage. | `7ac9353b28b3248cda1300e785e43d7ff7b0cf0c`; [stacked PR17](https://github.com/ryancswallace/Jobman-Control/pull/17). No merge. |
| Dashboard backend | `go test -race ./...`; merge retains buffers, distinct duplicate IDs, source outage partials, cursor account/query/grant/epoch isolation, replay/back navigation, expiry, no execution API, strict queries and 300-poll quota regression. Foundation `de383e5` and native fixes `9c00f2f` pass CI; authentication integration has passing local focused/race tests and a passing full `make check`. | [PR1](https://github.com/ryancswallace/Jobman-Dashboard/pull/1), [latest foundation CI](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37154025641). Production auth/adapter now wired; live directory source validation pending. Latest auth CI: https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37154960613. |
| Dashboard database | `python3 scripts/test-lab-postgres.py` passes on PostgreSQL **17.6 (Debian 17.6-2.pgdg13+1)**: migration replay/integrity, optimistic cursor/preference updates, expiry/cleanup. Each test creates/removes only a random disposable schema. | Synthetic Lab pg01, no Control schema changes by Dashboard tests. |
| Web | Production build and 41 focused tests passed before a further passing log cursor-recovery regression; browser inspected two-source overview, job filters, duplicate-ID drill-down, source breadcrumbs, missing timestamps and dark appearance. | Root fixture API on loopback; remaining services show unavailable. |
| Native | 33 core tests, five iPhone 18 Pro / iOS 27 UI tests and full Xcode 27.0 unsigned simulator build pass. Bounded graph rendering, exact citations, device revocation, generated client integration and DEBUG fixture UI tests authored. | UI screenshots inspected; real AD FS/APNs and physical/managed-device acceptance pending. |
| Contract generation | Deterministic generator check; eight generator tests; generated Swift standalone compile/smoke; generated TS client transport test. | `api/openapi.json` and checked-in `contracts/`. |

Lab preflight: nine existing VMs were suspended; host has 48 GiB RAM and 287 GiB free disk. Only pg01, storage01 and control01 have been resumed. pg01 suspended clock was corrected to host UTC October 3 before expiry checks. storage01 NTP is synchronized (stratum 3), control01 synchronized via storage01 (stratum 4); NFS and synthetic Control/Keycloak are active. Remaining VMs stay suspended. Lab PostgreSQL now has CA-verified TLS through a reload, preserving the existing Control database and connectivity. A separate `jobman_dashboard` database has distinct DDL/runtime identities, TLS-only Dashboard access, and no runtime DDL authority. Seven host pgx database tests pass with `sslmode=verify-full` and the dedicated DDL identity. Storage/control have synthetic reader UID 21901 and scoped access/default ACLs. Actual NFS 4.2 ACL translation showed that old 0700/0600 producer modes mask inherited read grants; the producer opt-in policy is being implemented and must pass real NFS acceptance. No Lab destruction or production changes performed.

## Decisions and external gates

- API baseline: `jobman.dashboard/v1`; shared OpenAPI; immutable source-qualified IDs; decimal-string revisions/offsets; unknown status text remains representable. Discovery timestamps distinguish database snapshots from authoritative directory verification.
- Production fails closed unless configured trust and authentication exist. Synthetic fixture mode must be explicit, loopback-only and visibly labeled.
- Ryan accepted the action to install/open Xcode. Account/license acceptance remains his action; CI also builds an unsigned simulator app.
- Actual AD FS issuer/version/client/audience/immutable-identity claims and directory endpoints/group IDs are needed for integration acceptance. Lab Keycloak is only a test identity provider.
- APNs ownership/credentials/egress, Apple internal distribution/signing/MDM, managed test phone and renewal/removal owners remain unknown.
- Production hosting and operating ownership remain unspecified. No production action is authorized without the concrete final approval described in the prompt.

## Current ownership and next actions

Root owns diagnosis queue/storage/service, documentation, integration and review. The web agent owns Lab runtime/revocation integration and scoped log-reader fixes. Control agent owns the stacked Control source-contract branches. The native agent owns the bounded target vertical slice across Dashboard backend, contracts, web and iPhone. Agents coordinate scoped commits on the shared branch. The four pre-existing core user files remain untouched.

1. Finish bounded target catalogs and review/CI; preserve the currently deployed exact monitoring build until coordinated source/client upgrades.
2. Verify existing-token group revocation/restoration through the deployed stack, then upgrade only the isolated Control fixture for target and diagnosis contracts.
3. Finish transactional diagnosis snapshots, immutable paired report storage, durable tasks and report/citation APIs; integrate both clients with the real deterministic engine.
4. Build transactional diagnosis snapshots, durable reports, terminal event feed, replay-safe inbox/rules/APNs paths, with independent security and migration review.
5. Complete packaging, load/restore/upgrade and T01–T12 acceptance; finish all unblocked release-candidate work while required upstream formal approvals and real AD FS/APNs/managed-device inputs remain pending.

Control bounded groups: `01814ec`, [stacked PR18](https://github.com/ryancswallace/Jobman-Control/pull/18); real PostgreSQL tests and non-Docker full checks pass. Delegation PR19 is green at `ccf6570236d0e04868eea1d6c83e9b460e7e08ed`. Directory [PR20](https://github.com/ryancswallace/Jobman-Control/pull/20) is independently reviewed and [green](https://github.com/ryancswallace/Jobman-Control/actions/runs/37157347772) at `397e21439fdb9c581e06633a696cd84674be3549`: adopted legacy aliases revoke correctly, and LDAP BER frames/nodes/responses are bounded before decoding. Formal GitHub approvals remain required; no bypass or upstream merge.

Bounded manifests [PR21](https://github.com/ryancswallace/Jobman-Control/pull/21), `8d1a6d0b6eea4f3e3e1e470c0788c55b535b38fe`, adds logs/artifact metadata and migration18. Author's real Lab PostgreSQL, race, 68.8% coverage and eight cross-build checks pass; independent review and exact-head CI pass. Review fixes bound encoded artifact pages to2MiB and pin execution/name cursor ordering to C collation, with worst-case escaped-key/punctuation pagination regressions.

## Monitoring and log implementation evidence

- WP05: aggregate workload totals and per-source observations, source-qualified bounded children, exact Slurm task indexes including zero, required dependency counts, paged predicates and bounded neighborhoods are implemented. Group engine cursors bind account/query/scope/source identity/epoch/grants, reauthorize cached rows and remove failed-source rows. Web fixtures were visually inspected in Chrome; this does not establish live Control acceptance.
- WP06: browser/native API receives only bounded bytes and opaque cursor state. Dashboard resolves operator routing; each storage broker independently reauthorizes the signed actor with Control before and after an isolated filesystem read. TLS identity, deployment, namespace, job/run/execution/stream/sequence, approved store generation, exact object-key prefix, checksum and size are checked. No client-supplied paths or artifact downloads.
- Broker filesystem reads use directory-relative no-follow opens, regular-file and identity checks, bounded helper processes and byte caps. A timed-out NFS helper retains its concurrency slot until reaped. Missing new chunks may retry twice without advancing the cursor. Malformed zero-byte chunks are rejected; exact JWT expiry no longer permits replay after cache eviction.
- Independent log-core review found no unresolved blockers after those fixes. A real private-CA mutual-TLS service test passes with signed delegation, isolated file reads, missing files and permission loss before response. This is local transport/filesystem evidence; actual Control and NFS acceptance is pending.
- Separate broker executable/configuration and persistent locked source-identity ledger are implemented. Restarted brokers reject assertions predating the startup fence; source instance/epoch/config rollback checks persist across restarts. A separate independent review at16d06ab found no blocking startup/configuration/ledger/wiring findings; focused race checks pass.
- Full local `make check` passed Go format/vet/race, deterministic contract generation and its eight tests, 42 web tests and both executable builds. Artifact metadata implementation subsequently passes full make check with43 web tests and a production build. Seven dedicated TLS Lab PostgreSQL tests pass; default local tests correctly skip PostgreSQL when no test DSN is configured.
- The 320-namespace aggregate query now has a bounded 96 KiB query budget and 128 KiB HTTP header budget, with regression coverage. Deployment proxy request-line limits must support that documented scope size.
- Implementation uses unreleased development contracts; public dependency tags, integrated tests and production acceptance remain gates.

## Authentication integration evidence (current work)

- Actual local TLS RSA/JWKS tests: PKCE/state/nonce, web authorized-party claims, native API audience/client/directory claims, duplicate-cookie rejection, CSRF/Origin, sign-out and replay. No real AD FS claim-shape acceptance is implied.
- Actual local mutual-TLS Control adapter tests: source pins, independent grants, preserved canonical owner IDs and verified directory aliases, stale/changed authorization, redirects, query fidelity and large revisions.
- Synthetic Lab PostgreSQL: seven authentication/cursor/preference test cases pass, including alias conflicts, idle/session revocation, atomic content-free audit, 20-session account cap, cleanup, exact migration checks, and monotonic source epoch pinning.
- Browser sessions intentionally do not retain refresh tokens; expiry is bounded by eight hours and shorter signed/token endpoint policy. Renewal uses AD FS SSO. Native refresh remains device-only. Design and `AUTHENTICATION.md` updated.
- Core shared evidence: `62ac89b14547a5fc9c1b9e2852832d66498361c2`, [PR53](https://github.com/ryancswallace/Jobman/pull/53). Independent review found no remaining blocker including the citation-ID uniqueness follow-up. Exact head passed [full CI](https://github.com/ryancswallace/Jobman/actions/runs/37154190572); required formal GitHub review remains before merge/release.

Foundation independent review fixes are included in tested `6908a83`: obsolete graph-worker layouts no longer expose removed nodes; merged pagination matches Control descending UUID ties; source refill failures remove already-emitted source rows, and cached replay checks each row against current grants. Regression tests pass, review complete, PR1 merged. Migration commands now also reject unknown/newer migration ledgers before applying DDL (real Lab PostgreSQL test passed).

Diagnose [PR10](https://github.com/ryancswallace/Jobman-Diagnose/pull/10): `2eabdfde1c2d24da95fff32550023fb955b287b0`, all CI checks green, GOWORK=off full make check, 92.8% coverage, deterministic evaluation 72/72. Independent review fixes verified: selected-run dependencies, metadata-only cancellation and capture-time-independent schema2 semantic IDs. No merge/release. Core released-tag transition, formal approval, and existing companion provider-release evaluation remain gates.

## Producer ACL and artifact follow-up

- Core log-reader [PR54](https://github.com/ryancswallace/Jobman/pull/54), stacked on PR53, contains reviewed producer policy `e0c0fa52ea4e0e3101919f3b7f4cd19d3842f561` plus strict-field/spelling follow-up `662fe9b`. Logical store-bound opt-in, exact POSIX/Linux NFSv4 ACL verification and descriptor-relative immutable publication leave artifact payloads private. Four pre-existing user files remain outside all commits. Focused race/Linux lint/full unit checks passed; full make check reached93.3% coverage and documentation. Spelling failures were fixed. Build/release-check/cross-builds pass; Docker documentation/smoke checks unavailable locally and retained in CI.
- Real Lab POSIX and NFS4.2 producer checks passed, including a repeat on binary SHA256 `9dee499361b5c7a73dac64254210ec5153ebc1e4c170312059073559598a6740`: Alice publishes, UID21901 reads only canonical logs, private artifacts/Bob/root-squashed reads and broker writes are denied. This establishes producer/filesystem behavior, not full Dashboard/Control/broker acceptance.
- Independent Lab provisioning review found an ACL-mask recalculation issue: removal of the temporary named-reader entry could broaden unrelated existing grants. The helper now preserves masks with `-n` and refuses parent traversal changes that would unmask unrelated execute permissions; seven offline regressions and actual access/default mask probes pass. Lab PR1 is independently reviewed and CI green at `f1846e33fa75a4d5279419341b9feccf3c5c1fac`, including the API-audience access-token-only correction. Existing incompatible legacy trees remain excluded without changing their permissions.
- Isolated Keycloak web/native S256 clients, API audience and two synthetic users now have an admin-only immutable directory GUID claim. Existing clients/users are preserved. Actual native S256 PKCE exchange now passes with signed API/client/GUID validation, ID-token nonce/audience separation, ID-token rejection as an API credential, and consumed-code replay denial. The test uses verified Lab TLS with a test-only socket mapping and no host DNS/trust changes. Deployed browser/session integration remains pending; Keycloak is not AD FS acceptance.
- Dashboard artifact implementation and tests are described in [ARTIFACT_METADATA.md](ARTIFACT_METADATA.md). Source/run/epoch/grant isolation and revocation regressions, real TLS adapter and HTTP tests pass; web build and43 tests pass. Native group/artifact integration is active. Targets, reports, durable events/alerts and integrated release gates remain open.

- Artifact independent review found and fixed contradictory source totals: pages cannot contain more rows than the full count or advertise a continuation when their row count already exhausts it. Exact large decimal values remain intact. Focused Control/monitoring/authentication/HTTP race suites pass; independent review has no unresolved blockers. Native bearer validation also rejects interactive-client audiences even when an issuer incorrectly includes the API audience.
- Core PR54 at662fe9b passes Linux tests/e2e, all eight builds, macOS/Windows race and documentation/release CI; the Linux artifact package coverage89.12% fails its90% minimum. New interrupted/truncated/oversized write and ACL-change-before-publication regressions pass as ordinary Alice on Lab Linux. Coverage repair is in progress; the gate remains enabled. The strict policy-field delta was independently reviewed with no blockers.
- Isolated Control fixture [PR22](https://github.com/ryancswallace/Jobman-Control/pull/22), `4b04d197fd030ea1f440384f5474243cd20cedd8`, has green CI and is running at the additive Lab18443 endpoint with loopback LDAPS18636. Existing Control/Keycloak remain active. Fixture observations and logs are explicitly synthetic, not actual Slurm execution acceptance. Dashboard and broker deployment is in progress.

## Deployed monitoring checkpoint — October 3

- Dashboard [PR4](https://github.com/ryancswallace/Jobman-Dashboard/pull/4) includes artifact/auth changes `5e25534850aa1231ebb7ff05ff9386867bf12f22`, native investigation `f6018d6e1285c04692e47e10f2b868523ddef246`, and reviewed traversal-only directory support `9064ff400ce2976fc922ac6ec3dae3e6be034c81`. Exact-head [backend/web and iPhone CI](https://github.com/ryancswallace/Jobman-Dashboard/actions/runs/37162682868) pass. Targets and diagnosis are still being implemented.
- Isolated Lab runtime [PR2](https://github.com/ryancswallace/Jobman-Lab/pull/2) at `5f5c9eb44be898fb5c05a3de4a3c2b80a0062b81` is independently reviewed and [CI green](https://github.com/ryancswallace/Jobman-Lab/actions/runs/37162057601). It verifies exact executable/web archive digests, private credentials, separate migration identity and systemd service UIDs. Reapply preserves original services.
- Production Dashboard and broker executables at `9064ff4` are deployed over verified Lab TLS. Actual Alice/Bob PKCE, PostgreSQL identity mapping, Control delegation and LDAPS discovery pass. Both users read the exact synthetic NFS bytes and empty terminal streams; Bob can read Alice's jobs/logs in research and receives403 for operations. Collection/array/graph catalogs, children, neighborhoods and artifact metadata pass. These are synthetic source observations, not real Slurm execution or AD FS acceptance.
- The NFS integration found a real reader issue: outer parents allowed traversal but not directory listing. Linux O_PATH and Darwin O_SEARCH now traverse pinned no-follow directory descriptors without broadening ACLs. Unprivileged Linux/NFS and macOS tests verify successful exact reads, symlink denial at every boundary and revoked search access.
- Run deployed checks with `JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab JOBMAN_DASHBOARD_LAB_RUNTIME=1 GOWORK=off GOTOOLCHAIN=go1.26.6 go test -tags=integration -race -run '^TestLab(NativePKCEAndDashboardTokenValidation|DeployedMonitoringAndCrossUserLogs)$' -count=1 -v ./internal/auth`. Tests load synthetic credentials privately and do not print token/log payloads.
- Core log-reader PR54 coverage repair `21701b191cd4e4e063d26cad7dae31c35db6bc8c` now passes [all checks](https://github.com/ryancswallace/Jobman/actions/runs/37161131566), including the unchanged coverage gate. [Independent review](https://github.com/ryancswallace/Jobman/pull/54#issuecomment-5974644205) has no blocking findings. Formal upstream approvals still gate merging/release.
- Control target [PR23](https://github.com/ryancswallace/Jobman-Control/pull/23) at `7320151070c15683543de3ea3ab1b6eab834328e` is independently reviewed; bounded target/partition queries and real PostgreSQL/full non-Docker checks pass. Dashboard target integration is in progress.
- Diagnose bounded evidence decoder [PR11](https://github.com/ryancswallace/Jobman-Diagnose/pull/11), `f47b6058c06e084fdb3ef54149b0a1b2dfc91ba5`, passes independent review, full checks (92.8% coverage), over4.1million bounded fuzz inputs and [CI](https://github.com/ryancswallace/Jobman-Diagnose/actions/runs/37162936101). Dashboard development now consumes this public immutable pseudo-version without sibling replacements; compatible released tags remain required for release.
- Diagnosis foundation under active review: private report/evidence pair publication and crash recovery preserve original seals; source/disclosure/version mismatches and corrupt/nonprivate/linked files are rejected. Real PostgreSQL tests pass concurrent request deduplication, requester ownership, expired-lease fencing, bounded retries and paired retention. Worker and HTTP integration are not yet implemented.
