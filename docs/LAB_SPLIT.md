# Packaged API and worker acceptance

The isolated synthetic Lab now runs the exact Linux arm64 engineering candidate
`b8f25afdd90f83b4602f32440a89e74dfa866b6c` as separate API and worker processes.
The broker uses the same candidate. This is the published
[v0.1.0-rc.1](https://github.com/ryancswallace/Jobman-Dashboard/releases/tag/v0.1.0-rc.1)
package; it does not establish production or physical-device acceptance.

## Cutover and permission boundaries

Jobman-Lab's independently reviewed split driver applied a held cutover from the
combined runtime and schema17 to split configuration5 and migrations1–18. It
preserved the old binary/configuration, private PostgreSQL backup, report files,
purpose-key identities, source instance and source recovery epoch. The legacy
combined unit is stopped. Original Control, Keycloak and directory processes
remain intact; the isolated Dashboard Control was restarted to load its additive
API and worker registrations.

| Boundary | Actual evidence |
| --- | --- |
| API | UID/GID21904; dedicated database role; no background work or report writes |
| Worker | UID/GID21905; explicit ingestion, notification evaluation, reports and retention components |
| Shared reports | Worker-owned files0640/directories0750; reader group21906; API reads without mutation permission |
| Database | Exact migration18 ledger, separate TLS password logins, executable least-privilege grants and protected-operation denial |
| Secrets | API/session material and worker purpose keys have distinct private owners; cross-role reads denied |
| Health | Private Unix liveness sockets and the operator status snapshot respond; actual database ledger is compatible |
| Broker | Separate API/worker callers, signed provenance, existing original log mapping preserved |

The completed pre-cutover database dump was175,297bytes, SHA256
`29568b8a26c835c5e0c4e53fc765376a22d6b04e61d6305cfa5ecbf0929c4c2a`.
Six original report objects totaling77,285bytes were backed up and converted
without changing sealed content. The reviewed plan digest is
`b429f3dd8a6fa3aaad8772357be686c36614f8f959829f49ec7f23a192ffcbf5`.
Private plans, credentials, backups and receipts must not be committed or uploaded.

Initial activation stopped safely before unit installation because standalone
configuration validation required socket directories that systemd had not yet
created. A separately reviewed repair created only the two exact private runtime
directories, then the unchanged plan completed. The future driver now creates and
validates these directories before standalone validation; its regression suite
has22 passing offline groups. The exact originally applied driver remains archived
with hashes for receipt verification and recovery.

## Worker restart and report persistence

`TestLabSplitReportsSurviveWorkerRestart` passed in2.36s (race package3.629s) on
2026-10-04. It uses real synthetic Keycloak PKCE identities and normal Dashboard
HTTP endpoints. It reads the original metadata and log reports from the
[actual subprocess scenario](LAB_EXECUTION.md), verifies Bob's denial, and selects
the already-executed collection failure job
`299c5dc7-71ef-4e67-89a3-3124f61b6df7` for new analysis.

The test verifies the API's actual executable/UID and stops only the reviewed
worker. With that worker stopped, two requests remain durably queued, idempotent
retries retain their task IDs, and the API still serves original NFS log bytes.
After restarting the same worker, both requests finish with deterministic sealed
reports, original source/run identity, correct citation IDs, redaction, broker log
evidence and Bob denial. The old report content remains byte-identical. Cleanup
starts the same worker on failure; it never changes jobs, grants, event cursors,
delivery holds or configuration.

The initial test failed because it assumed a Dashboard build change invalidated
report equivalence. The unchanged immutable **Diagnose dependency** and source
snapshot correctly reused the old ready report. No application change was
needed. The corrected scenario uses an existing unreported job and checks its
empty report catalog before stopping the worker. Both failed and passing evidence
are retained; no old task or idempotency binding was deleted.

```sh
env -u GOROOT GOWORK=off GOTOOLCHAIN=go1.26.6 \
  JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
  JOBMAN_DASHBOARD_LAB_SPLIT_RESTART=1 \
  JOBMAN_DASHBOARD_LAB_SPLIT_COMMIT=b8f25afdd90f83b4602f32440a89e74dfa866b6c \
  JOBMAN_DASHBOARD_LAB_EXECUTION_RECEIPT=/absolute/actual-host-receipt.json \
  go test -race -tags integration -count=1 -v ./internal/auth \
    -run '^TestLabSplitReportsSurviveWorkerRestart$' -timeout 4m
```

This scenario is intentionally single-use per source fixture: once reports exist,
it fails before stopping the worker. Preserve the records instead of clearing them
to force another pass. A subsequent exercise needs a separately reviewed executed
job and distinct fixed request identities. Without the explicit opt-in it skips.

Passing log: `/private/tmp/jobman-dashboard-split-report-restart-v2.log`, SHA256
`0418cd5bbaaa3291aeea0bf77fa70e985264454159382cd4dfc89e3e27ba3a99`.
The full authentication integration/race package also passes with live scenarios
disabled (1.881s). The test source SHA256 is
`a6474b342478209f7777af87c13cbbfd2668559b1fb1c923be8a37fc253a5ad0`.

## Event identity recovery

The original feed cursor was bound to the combined service identity. Switching to
the worker's new identity correctly paused ingestion with
`event_cursor_scope_changed`. Recovery uses the worker's exact credentials while
ordinary delivery hold generation2 remains set; it never resets to a fresh head.
See [the operator recovery procedure](EVENT_RECOVERY.md).

Reviewed retained replay scanned15 original events in one page and added one
previously unseen event from the earlier synthetic diagnostic fixture. All14
pre-cutover event identities, all4 inbox identities and zero delivery identities
match their saved counts and SHA256 digests. Current-state reconciliation reports
unavailable in both scopes because there are no enabled rules or eligible rule
owners; this is not complete historical coverage or a demonstrated source outage.
Apply completed at recovery revision3 with the exact reviewed digest, using the
explicit incomplete-coverage acknowledgment. The feed is active with no unresolved
gap; hold generation2 remains set and existing inbox/delivery identities are
unchanged. Explicit hold release is a separate operator step.

This checkpoint does not cover a database restore, combined-runtime fallback,
secret rotation, second Control, integrated load or actual APNs delivery.
