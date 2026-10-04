# Durable event ingestion

`events.enabled: true` starts an independent source-feed worker for each
configured Control. Each source requires the `durable-monitoring-events`
capability and explicit service-key `events.read` permission. Human role grants
do not grant feed access. The private runtime uses the service-only adapter in
[EVENT_SOURCE.md](EVENT_SOURCE.md); it never retains user tokens to ingest events.
Omitting this configuration supports staged upgrades. The initial release
requires ingestion and the downstream notification pipeline enabled.

Four global worker slots and two source transport slots bound I/O. A source
operation has a 20-second total budget under a 30-second database lease. Healthy
idle feeds poll every five seconds; backlog pages poll every second. Failures
back off to 64 seconds. At most 32 fixed workers exist, one per configured
source; shutdown cancels and joins them. No source/network work occurs within a
database transaction, and no APNs or user's directory outage blocks ingestion.

Migration 6 adds feed state, minimal original source events and monitoring gaps.
A new source establishes its first durable checkpoint at the source's current
head, without importing prior history. Rule activation must wait until this
initialization has committed, then capture its own source-clock activation
boundary. Signing in alone never creates a subscription.

Each accepted page inserts events and advances the continuation in one short
transaction. A crash before commit replays from the previous cursor. Source
generation, lease, status and prior cursor fence old workers. Positions must
increase within an epoch. Empty visible pages can advance over publications in
other namespaces. Original event identity is
`(deploymentId, controlInstanceId, eventId)`; epoch and position do not change it.
Replayed identities preserve processed state. A conflicting factual payload
rolls back the whole page and pauses ingestion with a recorded gap.

Changing the configured namespace set, source identity/recovery epoch, invalid
cursor, retention gap or exhausted capacity pauses normal continuation. The
original checkpoint/cursor and coarse reason remain in an explicit gap record.
Restart never obtains a new head for an existing source. Current service
authority and source identity are checked on every request. Raw event facts and
service cursors are private worker data, never user-read authorization.

Retained minimal events are bounded to one million per source and 32 sources.
Each payload is at most 4 KiB; operational sizing must include indexes and
database overhead. Capacity pauses ingestion instead of silently dropping work.
Unprocessed events are preserved until evaluation records a disposition.
Processed event identities persist for the largest observed source replay
retention plus one day, extended on replay. Retention increases protect all
existing identities without a table-wide rewrite. Cleanup requires a recently
successful active source, so an outage cannot discard identities before a
changed source replay window is learned. Indexed fixed-time seeks delete at most
500 eligible rows per pass while keeping the source's admission counter exact.
Source events and future 30-day user inbox expiry are separate lifecycles.

Tests exercise real PostgreSQL transactions: concurrent leases, abandoned
leases, injected failure between event insertion and checkpoint publication,
replay with different positions, identity conflicts, cross-source equal IDs,
scope changes during a lease, source epochs, explicit gaps, capacity, pending
work retention, increased/decreased replay retention and source outages.

Explicit operator replay, reconciliation, restore holds and resume are described
in [EVENT_RECOVERY.md](EVENT_RECOVERY.md). Notification evaluation, inbox and APNs
integration are still being completed. Paused feeds must not be manually reset in
SQL to pretend complete delivery. No release-readiness claim follows from these
tests.
