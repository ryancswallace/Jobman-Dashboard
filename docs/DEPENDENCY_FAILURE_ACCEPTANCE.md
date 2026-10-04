# Bounded dependency-failure acceptance

Status: directory outage and same-session recovery acceptance passed on rc.3 in
124.35 seconds after a reviewed recovery-verifier repair. Broker stop/start and
exact-process pause/recovery acceptance also pass in16.83 seconds, including
authorized log recovery after the startup fence. Original failed attempts and
subsequent passing results are retained separately. Database acceptance remains
open. The primary-Control pause extension is implemented and checked offline;
it has no live acceptance result. Root coordinates every Lab invocation after the exact
driver, plan, and recovery actions have been reviewed. This supplements T02, T05, and T11
in [the design](DESIGN.md); it does not replace healthy scale acceptance.

Run four separate scenarios and stop after any unexpected failure. Each scenario
has a fresh immutable receipt, an independent preflight, and cleanup registered
before its first effect. Preserve failed receipts; never create another scenario
to hide a failed attempt.

| Scenario | Outer limit | Intended fault and recovery |
| --- | --- | --- |
| Directory freshness | Six minutes | Interrupt only the primary synthetic LDAPS unit until its actual proof expires; restore the same unit and await complete normal reconciliation. |
| Log broker | Six minutes | Stop/start only the dedicated broker, then pause/continue its exact process in a separate interval to exercise connection unavailability and response timeout. |
| Primary Control response stall | Six minutes | Pause/continue only the exact primary Control process; observe sensitive-route failure and healthy secondary partial results within 25 seconds. |
| Dashboard database | Six minutes | Temporarily reject, then separately drop, only database-bound packets from the Dashboard API/worker UIDs on storage01. Remove only this operation's firewall table. |

Each test reserves an independent 60-second cleanup budget inside its outer limit.
Native and browser sign-in time is included. A phase that lacks time for its full
fault and recovery budget cannot begin. Guest watchdogs remain effective if the
test, SSH connection, or desktop disappears; a host cleanup callback alone is
insufficient.

## Baseline and preservation

Require no concurrent source, directory, recovery, configuration, workload, or
deployment mutations. Read-only checks pin the actual running release, executable
hash, UID, unit, process start, boot ID, configuration revision and exact config
bytes. Pin both immutable Control instances, recovery epochs, namespace allowlists,
schema ledger, runtime-role privileges, delivery hold, directory state/mapping
hashes, original log bytes/run identity, and existing sealed report/citation IDs.
Secret files remain on their existing hosts; receipts contain only hashes and
public identities. Source feed generations/positions may advance monotonically.

Use the same existing Alice/Bob tokens and browser session before and after the
fault. Synthetic Keycloak authentication is not corporate AD FS acceptance.
Record request IDs, status/error codes, latency, source completeness, and current
authorization timestamps. Do not write SQL to age authorization, manufacture an
event, change a grant, reset a cursor, or make a failed check pass.

API, worker and broker configuration/key files remain byte-identical. No migration,
source restart, hold change, registry edit, NFS export/mount change, credential
rotation, scheduler action, or shared PostgreSQL restart belongs to this suite.

## Directory freshness and recovery

The sole affected unit is `jobman-dashboard-lab-directory` on control01. The
secondary directory, both Controls, original services and all other Dashboard
processes remain running. Persist an intent and arm an acknowledged guest recovery
watchdog before stopping the primary directory. The watchdog arms a 180-second
timer for the same unchanged unit; normal recovery should occur sooner. If stop
completion leaves fewer than 125 seconds
to observe freshness expiry, preserve a failed attempt and restore the unit.

Read source-clock `authorizationCheckedAt`/`authorizationExpiresAt` before the
fault. Observe actual passage beyond the 120-second freshness boundary; do not
backdate a database row or merely wait for an invented client timestamp. Failed
directory reads must not refresh proof or erase retained group records.

After expiry, primary-sensitive detail/log/report/citation/inbox reads must return
`503 authorization_unavailable`, with no sensitive result bytes. Previously
authorized URLs and cursors do not bypass the failure. A healthy secondary scope
must remain usable. Aggregates must identify missing primary contributions as
partial rather than presenting them as zero or serving cached primary rows.

Restore the exact unit and allow up to 60 seconds for complete reconciliation.
The same authenticated identities must regain their original roles/capabilities
and source-qualified records with a new verification timestamp. Group and mapping
bytes, source instances/epochs, original report/inbox identities and source process
starts remain unchanged. This tests directory transport failure and natural proof
expiry. Malformed/incomplete LDAP responses and real AD replication remain
separate verification cases.

## Unavailable and slow broker

Use an accepted actual Slurm execution's source-qualified run ID, run number,
execution ID, stderr bytes and sealed diagnosis evidence. Establish a nonempty
range and an authorized continuation before interruption.

First stop only `jobman-dashboard-lab-broker` on control01, with a 120-second
restart watchdog. Respect its existing 90-second systemd stop policy; a timed-out
client never authorizes repeating the stop. Count the outage only after systemd
confirms that the unit is inactive. Restore and verify it before beginning the
slow-response interval. For the latter, pause the exact verified broker PID and
arm a 45-second continuation
watchdog bound to its boot ID/process start; do not signal a reused PID. A broker
pause is a real stalled dependency, not a sleep in the test client.

At most four concurrent log requests may be in flight. Require a real sanitized
HTTP 503 within 10 seconds, no leaked private paths/credentials/log bytes, and no
successful cursor advance over the failure. Concurrent metadata/liveness reads
remain responsive. Process restoration is followed by a positive authorized read:
retry the unchanged requested range for at most 15 seconds and 16 attempts, keeping
the same credential and recording every response. Only strictly sanitized
`503 source_unavailable` or `503 authorization_unavailable` responses permit a
retry. A broker restart intentionally imposes a delegation startup fence; process
liveness alone does not prove that this fence has elapsed. Authentication failures,
changed session cookies, malformed bodies and transport errors still fail the
exercise immediately. Require HTTP 200 within the bound, then verify exact bytes,
checksums, source/run identity and contiguous offsets. A new broker
process is expected only in the stop/start interval; the pause interval preserves
its process identity.

This is slow/unavailable broker acceptance. A separate reviewed hard-NFS test now
passes using a private mount/network namespace, actual RPC READ interception,
a 2,001 ms bounded timeout, helper reaping and exact original-byte recovery. The
independent watchdog and ordinary private unmount both complete. It makes no
claim that the kernel wait was intrinsically unkillable. See the October4
hard-NFS evidence in [implementation status](IMPLEMENTATION_STATUS.md).

## Slow primary Control

The `control` scenario contains only `control_pause`. Its sole signal target is
`jobman-dashboard-lab-control` on control01, UID 21902. No secondary source,
directory, broker, original Control or Dashboard process is stopped or restarted.
The acknowledged 45-second watchdog, boot ID, unit/executable hashes, UID, PID and
process-start checks are the same safeguards used for broker pause. Recheck the
identity immediately before SIGSTOP and SIGCONT; after continuation require the
exact same identity. Recovery reads only local process/material state until
SIGCONT completes: contacting the paused source would obstruct recovery.
Generic status remains available after recovery: it reports `paused: false` only
after matching the restored receipt and exact current process/local material.
The acceptance harness requires `paused: true` before requesting recovery.

The ordinary observation interval is at most 25 seconds, including the final
local-only proof that the pinned process remains stopped. No more than four
checks run concurrently across all protected, healthy-scope and liveness reads.
Each check has a ten-second caller budget within that shared interval. The source
adapter bounds response headers to five seconds and total transport to eight;
monitoring discovery has its own 1.5-second budget. Do not relax these product
limits or replace the fault with a client-side delay.

Native Alice and her existing web session must receive only sanitized 503 responses
for primary job metadata, the retained log cursor, sealed report and citation,
and inbox list/detail. Route-specific current-authority behavior matters:
metadata/inbox return `authorization_unavailable`; the log/report/citation source
adapter returns `source_unavailable` for the stalled transport. Neither 401 nor 403,
an empty successful page, a cleared session cookie, private error text, or a
successful stale result counts as an unavailable source. Bob's previously
accessible primary research job must fail safely too.

The same three credentials must still read secondary research metadata. Both
native and web aggregate job pages must equal the healthy secondary-only baseline;
all returned rows are explicitly source-qualified. Both aggregate overviews must
match only the healthy secondary counts for one fixed microsecond-precision time
window. Require exactly two contribution records: explicit primary
`authorization_unavailable` without stale `asOf`, and secondary `available` with
current provenance. No unavailable primary counts may be added to the subtotal.
Public liveness and private process/SQL readiness remain 200; the latter does not
claim that an external Control dependency is ready.

After exact-process continuation, the original job facts, source-qualified log
bytes/run/offsets, pre-fault cursor, inbox record/read state, sealed report/citation,
immutable accounts, roles/capabilities and web session must remain usable. The
final driver proof checks unchanged source/configuration/database authority and
monotonic feed progress. No fixture submission, notification rule, grant update,
source restart or feed recovery is part of this scenario.

## Dashboard database connection failures

On storage01, create one uniquely named temporary nftables table with exactly one
output rule selecting UIDs 21904/21905, IPv4 destination 10.77.0.20 and TCP port 5432.
The first interval rejects with a TCP reset; the second drops packets. Require
the installed nftables capability in preflight and reject unsupported hosts.
Never flush a ruleset, alter PostgreSQL roles/HBA, kill shared database sessions,
or stop its container. Existing unrelated firewall rules remain unchanged.

Persist the complete expected table semantics and arm an acknowledged 45-second
guest removal watchdog before applying its atomic rule batch. Confirm packet
counters increased so a harmless no-op cannot count as a fault. Recovery removes
only the exact operation-owned table, refusing drift. The ordinary test interval
is at most 25 seconds before requesting restoration.
The independent 45-second timer also invokes recovery if the host disappears.

Native bearer and existing browser-session requests must return sanitized 503,
not 401, an empty successful page, or unauthorized cached data. The DROP case must
finish within 17 seconds: production API/auth dependency contexts are 15 seconds,
ahead of the 20-second socket write deadline. Static/liveness stays 200; private
readiness must report 503 within its two-second dependency probe budget plus one
second of transport allowance. Database connections are bounded; observe pool
and process recovery without restarting the API/worker.

Before injection, activate one test-owned personal rule and prepare one fresh
normal Control job through the pinned existing helper. During the first database
fault, perform its ordinary authorized terminal transition. Control's database
remains reachable. After connectivity returns, verify the original event ID is
ingested and matched once into the current-authorized inbox, without resetting a
feed or inventing an event. Disable/delete only the known test-owned rule with
fresh revision checks; preserve the job, event and inbox as evidence. An uncertain
rule admission or cleanup stops the test and retains its exact intent for review.

Allow 30 seconds for same-session HTTP recovery and 60 seconds for event settlement.
Confirm unchanged processes, schema, roles, hold, key/config hashes, source
identities and monotonic feed progress. A report claim now has a separate
five-second database budget; its admitted analysis retains its independent task
lease. Failed/ambiguous claims rely on existing lease expiry, not an invented task
acknowledgement or deletion.

## Recovery protocol and evidence limits

Begin/recover/watchdog operations share an exclusive guest lock. Under that lock,
recheck the remaining deadline and pinned state immediately before any effect.
Persist and fsync intent first. A late begin must not reapply a fault after its
watchdog already restored service. Require durable acknowledgement of the exact
active timer and its target service before the effect. Explicit recovery starts
that same pre-armed systemd
service; it does not depend on a surviving SSH process. The timer remains armed
and later verifies the durable result. A receipt proves the guest recovery
service ran; it does not claim the timer expired when recovery was invoked early.
Timer creation is bounded to seven seconds, scheduling precision to one second,
and the recovery service to 30 seconds. Preserve its first sanitized failure
receipt alongside any subsequent successful restoration.

Lost replies lead to inspection or recovery of the existing receipt. They never
authorize a second stop, repeated pause, new firewall rule, or automatic fallback
configuration. Root reviews drift or an unconfirmed restoration before another
scenario begins. Keep both guest and host recovery evidence.

The Go harness bounds HTTP concurrency, body sizes, request time, source IDs and
retained samples. Driver failures expose only an allowlisted phase, one finite
category (`deadline`, `cancelled`, `exit`, `output_bound`, `json` or
`unexpected_stderr`), and a reviewed literal driver code or `withheld`. A code is
accepted only from the exact single-line Python failure format and the fixed
vocabulary in `labFaultDriverCodes`; arbitrary/multiline stderr and unknown codes
are never copied. Child output is drained with 32 KiB stdout and 64 KiB stderr
retention limits. Child cancellation retains the existing phase budget with at
most two additional seconds to close inherited pipes. Offline tests run real
local Python children for success, malformed JSON, known/hostile diagnostics,
output overflow and deadlines. A finite failure never proves whether a mutation
was admitted: inspect and recover the original receipt, without repeating begin.

The Python driver bounds SSH/process output, command duration,
file types/owners/modes and accepted phase inputs. Offline tests must reproduce
late-begin/watchdog interleavings, wrong/reused process identity, uncertain replies,
firewall drift, permission/umask errors and unauthorized route/data responses.

These tests do not establish physical iPhone presentation, real APNs handoff,
corporate AD FS, hard-NFS stalls, or client rendering under outage. Those gates
remain separately recorded. No live outcome may be inferred from offline tests.

## Reviewed execution sequence

The driver is `scripts/dashboard-dependency-faults.py` in Jobman-Lab. The other
three new scripts are its pure plan module, fixed guest implementation and offline
regressions. Snapshot makes no guest changes; prepare is local-only. The root
operator must review the generated plan and implementation hashes, then run stage
separately. The Go harness cannot stage or install anything.

```sh
python3 scripts/test-dashboard-dependency-faults.py
python3 scripts/dashboard-dependency-faults.py snapshot \
  --lab-root /absolute/jobman-lab --revision REVIEWED_40_CHARACTER_DASHBOARD_SHA \
  --output /absolute/new-private-snapshot.json
python3 scripts/dashboard-dependency-faults.py prepare \
  --lab-root /absolute/jobman-lab --scenario directory \
  --revision REVIEWED_40_CHARACTER_DASHBOARD_SHA \
  --snapshot /absolute/new-private-snapshot.json --staging /absolute/new-plan-directory
# Review plan.json and review.json before the next command.
python3 scripts/dashboard-dependency-faults.py stage --apply \
  --lab-root /absolute/jobman-lab --staging /absolute/new-plan-directory \
  --fault directory_stop --expected-plan-sha256 REVIEWED_PLAN_SHA256 \
  --expected-implementation-sha256 REVIEWED_IMPLEMENTATION_SHA256
```

Use a fresh separately reviewed plan for `broker`, `database` or `control`, with
first fault `broker_stop`, `database_reject` or `control_pause`, respectively.
Never alter a prepared plan or reuse another scenario's result path. The snapshot/plan must name the exact deployed revision
containing the reviewed 15-second API/auth and five-second claim deadlines before
running database DROP acceptance.

The Go tests are `TestLabDependencyDirectory`, `TestLabDependencyBroker` and
`TestLabDependencyDatabase` in `internal/auth/lab_dependency_failures_test.go`,
and `TestLabDependencyControl` in `internal/auth/lab_control_fault_test.go`.
Run one exact test per process with `-tags integration -race -count=1 -timeout=6m`.
Required environment variables contain paths, scenario names and public digests;
none contains a token, password or cookie:

- `JOBMAN_DASHBOARD_LAB_ROOT`: the authorized Lab checkout.
- `JOBMAN_DASHBOARD_LAB_DEPENDENCY_FAULT`: exactly `directory`, `broker`, `database` or `control`.
- `JOBMAN_DASHBOARD_LAB_FAULT_DRIVER`: the reviewed archived driver path.
- `JOBMAN_DASHBOARD_LAB_FAULT_PLAN`: its private staged plan directory.
- `JOBMAN_DASHBOARD_LAB_FAULT_PLAN_SHA256` and
  `JOBMAN_DASHBOARD_LAB_FAULT_IMPLEMENTATION_SHA256`: reviewed digest values.
- `JOBMAN_DASHBOARD_LAB_FAULT_RESULT`: a new private output filename.
- `JOBMAN_DASHBOARD_LAB_FAULT_SLURM_RECEIPT`: accepted complete-array receipt,
  fixed current-report SHA256 `929a9e93e83069dc330734b699bd6077432507397e56ec484367c6af954be530`.
- `JOBMAN_DASHBOARD_LAB_SECONDARY_FIXTURE`: the accepted secondary public fixture.
- Database case only: `JOBMAN_DASHBOARD_LAB_NOTIFICATION_RECEIPTS`, a new empty
  owner0700 canonical `/private/tmp/jobman-dashboard-notification-receipts-<unique>`
  directory. The captured path is passed explicitly to every normal-cancellation
  wrapper call; the eight retained failed-scenario IDs remain excluded. Directory,
  broker and Control cases do not use it. See `LAB_MULTISOURCE_NOTIFICATIONS.md`.

The main context ends 65 seconds before the six-minute outer deadline; cleanup
gets an independent 60 seconds. Sign-in/baseline must finish within 90 seconds,
and each fault checks its remaining observation/restoration reserve again. A slow
setup or stop can therefore fail safely before the intended observation; it must
not be presented as a successful dependency test.

Database acceptance uses the separately reviewed normal-cancellation helper and
pins both its Python wrapper and common module. A reviewed change to that helper
requires an explicit dependency update before this harness is frozen. Its two
jobs use inert fixture placement and no executor. One personal watched-job rule
is activated before the fault and deleted using fresh CAS afterward. The first
job transitions during the fault, with the original event ID and exactly one
inbox identity verified after recovery; the second job is cancelled after the
rule is stopped. Failed or uncertain helper calls preserve their existing source
receipts and jobs for review, without automatic resubmission.

After an unexpected result, use only the existing plan's `status` and reviewed
`recover --apply` path. `begin` permanently refuses a repeated pending admission.
Recovery removes only its predeclared exact firewall table, even if the apply
reply was lost; counter changes do not authorize changing the rule semantics.
The driver closes the operation only after all fault-specific restoration proofs
and the final unchanged database authority check. No pending evidence is deleted.

After the source upgrade, use the separately reviewed refreshed Slurm receipt
`/private/tmp/jobman-dashboard-slurm-report-refresh-control04bd-v1.json`, SHA256
`929a9e93e83069dc330734b699bd6077432507397e56ec484367c6af954be530`.
It retains the original actual execution evidence and refreshed diagnosis pairs;
all original reports/citations remain intact. See [report refresh](LAB_REPORT_REFRESH.md).
Earlier directory/broker results retain their original receipt pins.
