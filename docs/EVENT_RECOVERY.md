# Event gaps, delivery holds and recovery

The private `jobman-dashboard events` commands use an explicitly reviewed
operational database identity and configured Control service credentials. They
are not exposed as Dashboard
HTTP endpoints. They never submit or change jobs. Output contains source IDs,
plan digests, counters and coverage summaries; opaque feed cursors, directory
principals, job content and credentials stay private.

Normal ingestion pauses when a cursor expires, source recovery epoch or configured
namespace set changes, an event conflicts with its original facts, or retained
storage reaches its bound. It never resets a saved cursor to a fresh head.
Recovery records an immutable reviewed plan and replays original event identities.
An event-fact conflict is quarantined and requires repairing its underlying cause;
these commands cannot override it.

## Inspect, replay and apply a source gap

Run commands on the Dashboard service host with a private API-shaped recovery
configuration naming the exact source registry and the ingestion worker's Control
service identity. Cursors are service-bound; an API caller's identity is not
interchangeable with the worker's. Give this configuration a separately reviewed
database credential with the operational mutation privileges needed by the
selected commands. The split runtime grant profiles and read-only `operator`
profile do not authorize hold changes or general recovery. Keep this credential
out of every serving process; never broaden runtime grants to make an operator
command pass. The recovery configuration retains ordinary strict validation and
source/epoch/revision checks.

Substitute the IDs, generations, revisions and SHA-256 digest returned by each
preceding command. Configuration paths in these examples are illustrative.

```sh
jobman-dashboard events gap --config /etc/jobman-dashboard/dashboard.json \
  --deployment DEPLOYMENT_UUID
jobman-dashboard events plan --config /etc/jobman-dashboard/dashboard.json \
  --deployment DEPLOYMENT_UUID --gap GAP_UUID --generation FEED_GENERATION
jobman-dashboard events status --config /etc/jobman-dashboard/dashboard.json \
  --recovery RECOVERY_UUID
jobman-dashboard events step --config /etc/jobman-dashboard/dashboard.json \
  --recovery RECOVERY_UUID --digest PLAN_DIGEST --pages 50
```

A step consumes at most 50 pages of 200 original events, with a separate short
lease and transaction per page. Repeat until status is `ready`. An interrupted
command retains all committed pages. A replacement plan or changed generation
invalidates old leases. The total plan is bounded to 10,000 pages and the source
retention cap still applies. No network request occurs inside a database
transaction.

Review the plan's original and effective reason, mode, namespace additions and
removals, and suppression policy before stepping/applying. A capacity pause uses
its prior cursor only while the source identity, epoch and scopes remain the
same. If the source changed while capacity was paused, the new plan explicitly
uses retained replay. A different Control instance requires a new configured
Dashboard deployment identity, not accepting the replacement under the old one.
Advance `configurationRevision` when deliberately accepting a newer source
recovery epoch or configuration, then use that configuration consistently.

```sh
jobman-dashboard events reconcile --config /etc/jobman-dashboard/dashboard.json \
  --recovery RECOVERY_UUID --digest PLAN_DIGEST
jobman-dashboard events apply --config /etc/jobman-dashboard/dashboard.json \
  --recovery RECOVERY_UUID --digest PLAN_DIGEST --revision READY_REVISION \
  --acknowledge-gap
```

Reconciliation reads only namespaces explicitly selected by current account-owned
rules, under independently verified current Control permissions. It uses no saved
user token. At most 32 owner/rule/alias candidates per namespace, four concurrent
namespaces, 100,000 total record slots, 50 pages per namespace, and two minutes
bound each comparison. A namespace-wide scan can report `complete`; narrower
watched/my-job intent and exhausted budgets report `partial`. Missing eligible
owners or unverified authority report `unavailable`; confirmed denied owners
report `inaccessible`. No job content is retained in the receipt, and counts are
cleared if authority changes before the final check. This describes available
current state, not proof that every historical transition was delivered.

Apply performs a new bounded reconciliation and rechecks source identity/epoch
before committing the exact already-consumed recovery head. It does not skip to
a newly fetched head. Incomplete current-state coverage blocks apply unless the
operator reviews it and explicitly adds `--allow-incomplete`. Even complete
current-state coverage cannot reconstruct missing events; `--acknowledge-gap`
records that limitation. Namespace removals atomically revoke old rule intervals;
re-adding one does not restore those intervals without explicit user revalidation.

## Restoring Dashboard PostgreSQL

Before starting split processes against a restored database, explicitly establish
the durable hold with the privileged operational configuration and commands below.
Set API `events.deliveryHold: true` and worker `deliveryHold: true` in their
respective private configurations. Split startup asserts an already-established
hold and refuses to start if it is absent; runtime roles cannot establish or
release it. Legacy combined `serve` mode retains its earlier behavior of persisting
a configured hold before starting worker loops when its database credential has
that authority. A false setting never releases an already persisted hold.
Local `hold` and `hold-status` require no readable Control credentials or network
connection to Control.

```sh
jobman-dashboard events hold-status --config /etc/jobman-dashboard/dashboard.json
jobman-dashboard events hold --config /etc/jobman-dashboard/dashboard.json \
  --generation HOLD_GENERATION --restore-through 2026-10-04T02:00:00Z
```

Use an uncertainty cutoff obtained outside the restored database, covering the
last time notifications might have been handed off before the restore. The example
time is not a recommended value. A backup's stale cursor or local delivery ledger
cannot establish that cutoff. The restore command atomically raises a monotonic
suppression floor, pauses all retained source feeds, invalidates leases and
supersedes unfinished recovery plans. New sources inherit the floor. Replay and
ordinary delayed publication both suppress events whose original Control recorded
time is at or before it. Pending evaluator/provider work must additionally consult
the global hold and cutoff immediately before committing or handing off.

A hold cannot recall an already in-flight APNs request. Stop workers and allow the
bounded requests to finish before a controlled restore/cutover; record the last
possible handoff time independently. When the prior delivery period is uncertain,
prefer a conservative later cutoff and record the resulting alert gap.

Recover each paused source with the plan/step/reconcile/apply sequence. The
persisted restore floor automatically becomes part of every new plan. The optional
`plan --restored --suppress-through RFC3339_TIME` records explicit restore intent;
it is not a substitute for the global hold. `--suppress-all-recovered` suppresses
all events seen during that particular replay as an additional conservative choice.
It does not narrow any persisted cutoff.

After every configured source is active again, change only the API and worker
startup hold flags to false in exact reviewed configurations, validate them and
restart the affected processes while the database hold remains set. Update the
operational configuration's `events.deliveryHold` to false as well, then explicitly
resume with its privileged credential and the current hold generation:

```sh
jobman-dashboard events resume --config /etc/jobman-dashboard/dashboard.json \
  --generation HOLD_GENERATION --acknowledge-gap
```

Resume fetches fresh checkpoints for the exact retained source set and checks
current source registry/configuration, active feed state and preserved suppression
floors in one transaction. It never moves any source cursor or erases uncertainty.
A configured-source removal needs a deliberate retirement procedure before a
hold can be released; silently ignoring its retained feed is rejected.

## Capacity pressure

`events prune --deployment DEPLOYMENT_UUID --generation FEED_GENERATION` with the
same `--config` may remove at most 500 expired, already-processed events during a
capacity pause, after a fresh source identity/configuration check. It never drops
pending work or identities still inside the replay safety window. A successful
prune supersedes unfinished plans so a new reviewed digest includes the advanced
deduplication floor. Reinspect the gap and plan again.

If all retained events are pending or unexpired, cleanup cannot make space safely.
Resolve the stalled dependency/evaluation backlog or wait for safe expiry; do not
reset SQL cursors or delete deduplication rows manually. Capacity-only evaluation
may drain durable pending work only with unchanged current source identity, epoch,
scopes and authorization. A global restore/delivery hold always takes precedence.

## Current validation boundary

Recovery, cutoff inheritance, global holds, lease fencing, atomic rollback,
source-set checks and bounded represented-user reconciliation have unit/race and
real synthetic Lab PostgreSQL coverage. Deployed worker-service identity replay
and explicit apply preserve original event/inbox identities in
[split acceptance](LAB_SPLIT.md). Full database-restore/evaluator/provider recovery
exercises remain separate release gates. There is no claim of complete historic
delivery or exactly-once notification presentation.
