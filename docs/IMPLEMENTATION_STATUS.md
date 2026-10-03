# Initial release implementation status

Updated: 2026-10-03. Release state: **implementation in progress; no release candidate yet**.

The adopted [implementation prompt](IMPLEMENTATION_PROMPT.md), [requirements](REQUIREMENTS.md), and [design](DESIGN.md) govern this work. The persistent goal remains active through implementation, GitHub delivery, and acceptance. A passing local slice is not release acceptance.

## Baseline and preservation

| Repository | Starting revision | Existing work |
| --- | --- | --- |
| Jobman-Dashboard | `925517d` baseline now pushed | Adopted documentation and existing branding preserved; implementation on `feat/dashboard-foundation`. |
| Jobman | `c0980f3` | Pre-existing modifications to `internal/agent/service.go`, `internal/agent/service_test.go`, `internal/backend/slurm/slurm.go`, and `internal/backend/slurm/slurm_test.go`; do not overwrite or include accidentally. |
| Jobman-Control | `4735be5` | Clean at start. |
| Jobman-Diagnose | `c5db4a8` | Clean at start. |
| Jobman-Lab | `014d50e` | Clean at start; currently outside writable workspace. Read-only inspection permitted; request scoped runtime access for authorized changes. |

Origins verified as the matching `ryancswallace/Jobman*` GitHub repositories. No task-attached worktrees or PRs existed at start. Control implementation PRs and Lab evidence are recorded below. Dashboard application changes are being prepared for their first implementation PR.

Host discovery: Go 1.27.1, Node 26.5.1, npm 11.17.0, Swift 6.3.3 command-line tools. Control pins Go 1.26.6; Dashboard will use that reproducible toolchain baseline. Ryan installed and licensed Xcode **27.0**. The native app now passes a complete unsigned iPhone simulator build; 23 core tests pass. Simulator UI workflow tests are next.

## Work packages

| Package | State | Current work / remaining acceptance |
| --- | --- | --- |
| WP01 | In progress | Go fixture service, authored OpenAPI, generated TS/Swift DTOs and clients, two-source fixtures, PostgreSQL migrations, React/SwiftUI projects and CI authored; first integrated CI pending. |
| WP02 | In progress | Source-scoped Ed25519/mTLS-bound read signer and tests implemented; Control verifier/alias policy agreed. AD FS web/native server authentication and integration remain. |
| WP03 | In progress | Control agent: contributing membership grants, exact capability union, current-principal discovery and migration compatibility. Directory reconciliation/revocation still required. |
| WP04 | In progress | Control owner/lifecycle/filter/summary contracts tested and pushed; Dashboard aggregation/cursors tested against fixtures. Production source adapter/delegation integration remains. |
| WP05 | In progress | Control bounded catalogs/children/dependencies/neighborhoods in progress; client views authored. |
| WP06 | Not implemented | Bounded manifests, authorized safe broker, cross-user local/NFS acceptance. |
| WP07 | In progress | Core agent implementing schema2/shared collector with schema1 preservation; exact public DTO contract pending. |
| WP08 | Not implemented | Public deterministic Diagnose library, durable report tasks and exact citation validation. |
| WP09 | Not implemented | Transactional terminal events, serialized durable feed and replay-safe ingestion. |
| WP10 | Not implemented | All rule scopes/outcomes, inbox/device/APNs/preferences persistence and workers. |
| WP11 | In progress | Web agent: native web navigation, transport/state, accessible workflow screens. |
| WP12 | In progress | Native agent: Swift core, SwiftUI iPhone project, OAuth/Keychain and source-qualified state. |
| WP13 | In progress | Migration runner and runtime DB foundation, reproducible build/check commands and CI authored. Production configuration/packages/runbooks remain. |
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
| R10 | Open | Bounded artifact metadata |
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
| Control authorization | `make quick-check`; real PostgreSQL isolated-schema integration including migration, union, overlapping grant removal, last-grant revocation and current user; local full checks through release/build, Docker smoke unavailable locally. | `7ab34c40a22ff5386414b32487d44be07b438ae3`; [PR16](https://github.com/ryancswallace/Jobman-Control/pull/16); [CI run](https://github.com/ryancswallace/Jobman-Control/actions/runs/37149374447). Independent review of this commit found no blocking defects; CI green. GitHub requires formal approval and current-base update; no merge. |
| Control monitoring | Real Lab integration: summary/drill-down agreement, completion half-open boundary, original owner versus history importer, instance identity and actual agent lifecycle timestamps. Quick/full non-Docker checks pass, 65.0% coverage. | `7ac9353b28b3248cda1300e785e43d7ff7b0cf0c`; [stacked PR17](https://github.com/ryancswallace/Jobman-Control/pull/17). No merge. |
| Dashboard backend | `go test -race ./...`; merge retains buffers, distinct duplicate IDs, source outage partials, cursor account/query/grant/epoch isolation, replay/back navigation, expiry, no execution API, strict queries and 300-poll quota regression. Local uncommitted implementation; CI revision pending. | Foundation branch; production authentication and source adapter not integrated. |
| Dashboard database | `python3 scripts/test-lab-postgres.py` passes on PostgreSQL **17.6 (Debian 17.6-2.pgdg13+1)**: migration replay/integrity, optimistic cursor/preference updates, expiry/cleanup. Each test creates/removes only a random disposable schema. | Synthetic Lab pg01, no Control schema changes by Dashboard tests. |
| Web | Production build and 35 focused tests pass; browser inspected two-source overview, job filters, duplicate-ID drill-down, source breadcrumbs, missing timestamps and dark appearance. | Root fixture API on loopback; remaining services show unavailable. |
| Native | 23 core tests and full Xcode 27.0 unsigned simulator build pass. Bounded graph rendering, exact citations, device revocation, generated client integration and DEBUG fixture UI tests authored. | Simulator UI tests in progress; real AD FS/APNs and physical/managed-device acceptance pending. |
| Contract generation | Deterministic generator check; eight generator tests; generated Swift standalone compile/smoke; generated TS client transport test. | `api/openapi.json` and checked-in `contracts/`. |

Lab preflight: nine existing VMs were suspended; host has 48 GiB RAM and 287 GiB free disk. Only pg01 was resumed. Its suspended clock was September 17 with chrony unsynchronized; corrected to host UTC October 3 before continuing expiry checks. Ongoing time synchronization remains an installation check. No Lab destruction or production changes performed.

## Decisions and external gates

- API baseline: `jobman.dashboard/v1`; shared OpenAPI; immutable source-qualified IDs; decimal-string revisions/offsets; unknown status text remains representable. Discovery timestamps distinguish database snapshots from authoritative directory verification.
- Production fails closed unless configured trust and authentication exist. Synthetic fixture mode must be explicit, loopback-only and visibly labeled.
- Ryan accepted the action to install/open Xcode. Account/license acceptance remains his action; CI also builds an unsigned simulator app.
- Actual AD FS issuer/version/client/audience/immutable-identity claims and directory endpoints/group IDs are needed for integration acceptance. Lab Keycloak is only a test identity provider.
- APNs ownership/credentials/egress, Apple internal distribution/signing/MDM, managed test phone and renewal/removal owners remain unknown.
- Production hosting and operating ownership remain unspecified. No production action is authorized without the concrete final approval described in the prompt.

## Current ownership and next actions

Root owns Dashboard backend, authored `api/`, root configuration, CI, documentation and integration. Web agent owns `web/`, generated `contracts/` and generator scripts plus the completed strict-query parser patch; native agent owns `ios/`; Control agent owns current stacked authorization/monitoring/groups branches. Web agent next independently reviews Control PR16. Agents do not commit shared Dashboard work independently.

1. Push first Dashboard foundation PR and complete CI/simulator tests. Keep all acceptance gates open; address review findings before merge.
2. Update/review Control PR16, retain required GitHub review approval; integrate PR17 monitoring and PR18 bounded groups, then delegation and directory enforcement.
3. Complete Lab host/resource preflight and obtain scoped runtime write permission for its authorized configuration.
4. Build evidence/report and durable event/alert paths in dependency order; publish compatible upstream modules before release builds.
5. Run focused tests and independent reviews per slice, push reviewable PRs, then complete cross-repository acceptance and release preparation.

Control bounded groups: `d2587d192699a59f4dd662194641f2cc59dea709`, [stacked PR18](https://github.com/ryancswallace/Jobman-Control/pull/18); real PostgreSQL group tests and non-Docker full checks pass. Core shared-evidence agent owns `diagnostic/` changes and must preserve all four pre-existing core user edits.
