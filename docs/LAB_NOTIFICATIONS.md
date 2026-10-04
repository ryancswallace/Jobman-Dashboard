# Deployed notification acceptance

`TestLabDeployedNotificationsFromControlTerminalEvents` is an opt-in integration
test against the isolated, deployed Dashboard and Control. It uses actual
Keycloak authorization-code/PKCE sign-in, verified TLS, Control delegation,
synthetic direct-directory authorization, PostgreSQL, the normal Control event
publisher, Dashboard ingestion, rule activation/evaluation and public inbox APIs.
It does not instantiate a local Dashboard handler or insert notification rows.

The separate reviewed Lab helper creates two Alice-owned jobs in the existing
`dashboard-research` namespace using `SubmitJob`. It later cancels only the
selected job using `CancelJob`. The normal terminal-transition trigger creates
the original outbox event. The existing publisher and Dashboard workers must
process it. No agent, target, role/group mapping, pre-existing job or public
production configuration is changed. Jobs, original events and scenario receipts
remain as synthetic evidence. This is cancellation/observation acceptance, not
actual subprocess or Slurm execution acceptance.

Before running, the operator must deploy the reviewed notification-capable
Dashboard with its complete compatible migration ledger and `events.enabled`
(or the separate worker's `ingestion` and `notifications` components), authorize the existing
Dashboard service's `events.read` scope on the isolated Control, and verify the
feed is active. This scenario expects the Lab configuration without real APNs
provider credentials. Physical Apple delivery and managed-device acceptance are
separate gates.

The reviewed Control helper is installed as:

```text
/usr/local/libexec/jobman-dashboard-lab/jobman-control-notification-helper-<40-character-commit>
```

It must be a root-owned regular file with mode 0755. The public host manifest
`.lab/dashboard/notification-helper.json` contains exactly `revision`, `sha256`
and `sourceRevision`; the last field pins the running isolated Control revision.
The Lab script checks the binary hash, source revision, database name, instance,
TLS connection, migration set, directory enforcement and explicit deployment.
Private credentials are loaded only through the normal synthetic Lab mechanisms
and never emitted. No migrations or service restarts occur inside the scenario.

Run from the Dashboard repository after the reviewed deployment:

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_RUNTIME=1 \
JOBMAN_DASHBOARD_LAB_NOTIFICATIONS=1 \
go test -tags integration -race -count=1 ./internal/auth \
  -run '^TestLabDeployedNotificationsFromControlTerminalEvents$' -timeout 8m
```

The test creates four personal Alice rules and two personal Bob rules. It waits
for actual source-clock activation, then checks overlapping namespace, watched
job and own-job matches; a selected-success rule must not match a cancellation,
and Bob's own-job rule must not match Alice's job. Each authorized account gets
one inbox item for the original source/event identity. It verifies matched-rule
context, actual job/run identity, read/unread updates and cross-account 404s.

After stopping the rules that would match a second job, the test cancels that
job and checks that no new inbox item appears. This is not a timing guess: the
read-only `settled` barrier requires that exact event to be present in Control's
published feed and Dashboard's durable journal, with fanout complete and no
pending account evaluation. Earlier inbox history remains visible after the
rules stop. Repeating a completion uses Control's normal idempotency identity and
must return the same event UUID.

The wrapper protocol is deliberately small:

```sh
python3 scripts/dashboard-notification-scenario.py prepare --receipt <32-lowercase-hex>
python3 scripts/dashboard-notification-scenario.py complete <receipt> first
python3 scripts/dashboard-notification-scenario.py settled <receipt> first
python3 scripts/dashboard-notification-scenario.py complete <receipt> stopped
python3 scripts/dashboard-notification-scenario.py settled <receipt> stopped
```

`prepare` returns synthetic-mode, helper/source/deployment/epoch/namespace pins
and two exact job UUIDs. `complete` adds original event UUID, recorded timestamp,
outcome, revision and optional actual run/execution UUIDs. `settled` returns only
receipt, case, event UUID and a boolean. The host retains these bounded public
receipts beneath `.lab/dashboard/notifications/`. A private source pending receipt
precedes preparation writes. Partial preparation requires inspection and blocks
automatic new preparation; it is never silently reset. Repeated complete calls
are safe. The source admits at most 100 retained scenario manifests.

Cleanup deletes only rules created by this run, using fresh owner-authorized
revisions. A failed cleanup identifies the synthetic rule UUID for inspection.
The harness never prints bearer tokens, passwords, helper stderr or raw source
documents. It bounds response size, helper output, API/helper deadlines and inbox
traversal. A positive test result does not prove real AD FS, actual workload
execution, APNs delivery, multi-Control isolation or company-managed iPhone
behavior; those remain separate acceptance exercises.

## October4 deployed result

The complete live scenario passes in16.63s (Go race package total17.868s) against
Dashboard `6aa32eb89989e79e9f5bb31a59a04801fdeca961`, source Control
`d332a2b569333ae8aed9c2fc9ebc648d8eb5ba4e`, separately installed scenario helper
`cb1bfa66816694c2727f5c1e21fae4bd68067095`, and Lab wrapper
`1b83ba78ecbc27e5a2abb81130fcef6351cdc356`. All assertions above passed,
including the processing barrier before the stopped-rule negative checks.

An earlier run failed only because the harness reused a response object and
retained an omitted `readAt` value when marking unread. Resetting that response
object preserved every behavioral assertion; no deployed service was changed
between the failing and passing runs. Source jobs/events/receipts were retained,
and test-owned rules were deleted through the authenticated API.

The exact split candidate `b8f25afdd90f83b4602f32440a89e74dfa866b6c` also passes
the complete scenario in13.64s (race package14.961s), with configuration5 and
migrations1–18, after explicit worker-service cursor recovery and hold release.
It uses a fresh nonce `fcfa6fa7078d2c8df29726fe464a8829`; no old completed job was
reused to claim new ingestion. All prior assertions and cleanup pass. The new
public receipt and first/stopped event records remain under the Lab's
`.lab/dashboard/notifications/`, with log
`/private/tmp/jobman-dashboard-split-notifications-live.log`. This proves the
separate worker/database/source identities support the integrated path;
real APNs delivery remains unverified.
