# Isolated deployed backup and restore acceptance

The independently reviewed restore harness has completed the recorded Lab
acceptance below. It runs against the existing synthetic Lab and a separately provisioned
clone; it never restores a database, changes a service or configuration, edits a
source feed, applies recovery, or releases a hold. The reviewed Lab restore driver
and explicit operator recovery commands perform those actions between test phases.

The tested candidate is `b8f25afdd90f83b4602f32440a89e74dfa866b6c`, with schema18.
For a new exercise, review the current source acceptance and freeze the exact live
source/configuration set. The harness requires both reviewed Control instances,
unchanged epochs, and complete source namespaces. Do not reuse the old split
revision5 snapshot. The exact backup plan and material inventory must be reviewed
before any backup, clone provisioning or restore operation.

The primary API is `https://dashboard.lab.test:8443`; the clone is the same
verified hostname on port38443, both explicitly dialed at10.77.0.10. The clone uses
its own PostgreSQL database/roles, service identities, private roots and report
storage. It has ingestion, notification evaluation and reports only, with no
delivery component or APNs credentials. Native audience tokens obtained by actual
synthetic Keycloak PKCE remain only in test memory. This does not prove browser
redirect configuration, corporate AD FS, a managed phone or Apple delivery.

## Individually authorized phases

Use a new, real operator-owned0700 directory for scenario receipts. Each phase
uses exclusive0600 files and refuses to overwrite previous evidence. Do not retry
an uncertain mutating phase blindly: inspect its pending receipt, source helper
receipts and owner-authorized rule listing. A lost rule-create response can require
manual cleanup by its exact nonce/name; rule creation does not claim idempotency.
No phase deletes or replaces preexisting jobs, rules, reports, objects or databases.

Common environment, with paths chosen by the operator:

```sh
export JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab
export JOBMAN_DASHBOARD_LAB_RESTORE=1
export JOBMAN_DASHBOARD_LAB_RESTORE_SCENARIO=/private/operator/new-restore-scenario
```

Select exactly one phase and one test per invocation on macOS or Linux, using the
repository-pinned Go version. A phase has a six-minute context; use an eight-minute
outer test deadline. The integration build tag is mandatory.

| Phase | Test | Required result before proceeding |
| --- | --- | --- |
| `prepare` | `TestLabRestorePrepare` | Two fresh helper scenarios/four owned accepted jobs, one Alice cancellation rule, E1 original event + exactly one read inbox, two sealed report profiles and all citation hashes, both independent source baselines |
| Operator backup | Reviewed Lab driver | Fresh exact snapshot/plan; bounded coordinated dump/objects/material backup and verified unchanged primary restart |
| `after-backup` | `TestLabRestoreAfterBackup` | Exact completed backup and restart receipt; E2 normally cancelled/processed on primary strictly after backup, primary owned rule stopped, externally retained UTC cutoff with two-second clock margin |
| Operator restore | Reviewed Lab driver | Empty isolated clone restored; exact schema, stable row IDs and both source checkpoint/cursor/position hashes verified before hold; all feeds paused and external floor set before clone startup |
| `held` | `TestLabRestoreHeld` | Original E1/read state, report/citation semantic hashes and idempotency keys preserved; Bob cannot read Alice's history; E2 absent; both source gaps paused and floor retained |
| Operator recovery | Explicit reviewed recovery CLI | Plan/step/reconcile/apply each source independently, at most50pages/step and1,000total; original-event replay and explicit incomplete-coverage decisions; global hold remains set |
| `replayed` | `TestLabRestoreReplayed` | Both feeds active with unchanged identities/scopes and nondecreasing positions; E1 one original inbox; E2 original UUID retained with suppression and no inbox; global hold unchanged |
| Operator release | Separately reviewed clone-only config CAS and CLI | Startup flags changed safely, readiness verified, explicit resume advances exactly one hold generation while preserving the floor |
| `resumed` | `TestLabRestoreResumed` | E3 source time exceeds cutoff plus margin; original event processed and creates one new clone inbox; E1 unchanged and E2 still absent; report pairs still usable |
| `cleanup` | `TestLabRestoreCleanup` | Only the two copies of the owned personal rule deleted, fourth owned job cancelled after deletion; no removed-rule contribution; receipts/data retained |

Example first phase:

```sh
JOBMAN_DASHBOARD_LAB_RESTORE_PHASE=prepare \
go test -tags integration -race -count=1 ./internal/auth \
  -run '^TestLabRestorePrepare$' -timeout 8m
```

Before `after-backup`, also set `JOBMAN_DASHBOARD_LAB_RESTORE_OPERATION` to the
reviewed driver's private operation directory. Its coherent-backup and restart
receipts must be present, recent, and successful; a watchdog-fired backup is not
accepted. The resulting `lost-interval.json` supplies the externally recorded
`externalCutoff` for the clone's `--restore-through`, never a timestamp inferred
from the restored database. Do not delay restore beyond the driver's bounded
cutoff window; investigate/replan if it expires.

The test leaves service retirement to the operator. Stop/disable only the exact
clone units after acceptance. Deleting clone database/roles/private keys is a later
receipt-bound operation, not automatic test cleanup. Never remove the primary's
source identities because the clone used restored copies.

## What the assertions establish

All three source events come from normal `SubmitJob`/`CancelJob` operations through
the existing reviewed helper, not SQL job/event insertion. These jobs do not run
subprocesses or Slurm. E1 and E2 use the first helper scenario's two jobs; E3 and the
unused cleanup job use the second. Normal completion replay must return the same
original event UUID. The namespace cancellation rule is filtered only to cancelled
outcomes; unrelated rules are preserved and are not assumed absent.

Metadata and explicit log-tail reports use the recognized synthetic failed-job
fixture. The API reopens/verifies paired private objects and current source
authorization. The harness compares the entire report projection and each citation
using semantic JSON SHA256, checks actual report/evidence/task/run identities,
verifies log redaction, and reuses the exact request/idempotency key. A changed body
with that key must conflict. Reports may legitimately reuse an already equivalent
ready task; the newly retained idempotency binding still must survive restore.

A separate fixed-database proof runs only read-only repeatable-read SQL against
`jobman_dashboard` or `jobman_dashboard_restore` on pinned pg01. It has a three-second
statement deadline,500ms lock deadline, exact selected event UUIDs, at most two
source rows (a third causes rejection), and bounded output. It returns source
identity/epoch/scopes, hashes of opaque checkpoints/cursors, numeric positions,
hold/floor, selected event suppression/processing state and Alice's matching inbox
IDs. It reads no source payloads, aliases, tokens, sessions or credential files and
performs no database mutation. Local synthetic PostgreSQL bootstrap access is used
only for this bounded evidence; runtime roles remain separately restricted.

The driver proves exact checkpoint hashes while writers are quiescent. Later
running-feed assertions allow positions to advance; ordinary append generation is
not mistaken for corruption. Both sources must recover independently. Only primary
E1/E2/E3 are newly generated in this exercise; secondary continuity is source,
checkpoint and retained-identity evidence, not a fabricated second-source event.

The clone's provider exclusion is enforced by reviewed deployment configuration;
these tests assert inbox behavior, not exactly-once provider delivery. Unknown
historical delivery outcomes remain governed by the retained restore floor. Any
partial/unavailable source, mismatched sealed pair, unexpected namespace/epoch,
missing original event or expired/uncertain receipt fails the phase without a
cursor reset, source relabeling, schema repair or hold release.

Offline guard tests and integration-tagged race compilation can run without live
opt-ins. They check private receipt ownership/modes/links/size, fixed source proof
validation and bounded read-only SQL structure. They are not live restore evidence.

## Recorded Lab acceptance — 2026-10-04

The complete sequence passes on Dashboard b8f25af/configuration7/schema18 with
the f297840 harness and two independent Control sources. Prepare/after-backup/
held/replayed/resumed/cleanup take8.55/8.52/0.98/0.81/4.61/6.46seconds respectively.
Original identities, read state, paired reports and citations survive; replayed
lost-interval E2 remains suppressed, and fresh post-cutoff E3 produces one inbox.
Three source/namespace scopes lack current-state reconciliation coverage and were
explicitly acknowledged before apply; no complete-coverage claim follows.

The initial empty-schema restore failure and exact reviewed continuation remain
part of the evidence. The future Lab driver preserves the provisioned DDL-owned
empty `public` schema, because an archive's schema TOC entry does not guarantee a
schema-creation command. Never use that repair to adopt a populated target or
silently repeat an uncertain restore.

After exact clone-only startup flag changes and separate generation4→5 resume,
cleanup removed the owned rule copies and cancelled the unused job. An ordinary
generation5→6 hold preceded stopping the two clone units. The clone database,
objects and private receipts remain; primary processes and hold=false/generation3
are unchanged. The backup watchdog did not fire, so its intervention path remains
a separate fault-test gate. No APNs provider delivery was exercised.

Private consolidated receipt:
`/private/tmp/jobman-dashboard-restore-complete-acceptance-v1.json`, SHA256
`1d6344ebba3ae1e91475e11e86ba0614496f3b4ea7a58d681a15888e6c18c50f`.
The Lab's `docs/DASHBOARD_RESTORE_RELEASE.md` describes the separately reviewed
prepare/configure/observe/retire helper and its exact-plan/deadline checks.
