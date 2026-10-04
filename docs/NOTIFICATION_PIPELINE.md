# Durable notification processing

`events.enabled` starts source ingestion, pending-rule activation and event
evaluation. Device registration is separately controlled by the configured
topic/environment allowlist. APNs delivery starts only for explicitly configured
providers. The inbox remains usable without an APNs provider or registered phone.
Workers join during shutdown before their database and network resources close.

See [rules](NOTIFICATION_RULES.md), [inbox](INBOX.md),
[device revocation](DEVICE_REVOCATION.md), [APNs configuration](APNS_PROVIDER.md)
and [operator recovery](EVENT_RECOVERY.md) for their individual contracts.

## From a source event to an inbox item

Ingestion preserves the original deployment, Control instance and event UUID.
Fanout advances through at most 50 rules per transaction. It creates one durable
evaluation per matching account/source event, with immutable matching rule
versions. Overlapping rules contribute context to one inbox item. No account can
receive an event through another account's source principal or alias.

Before evaluating, a worker obtains a fresh source checkpoint and current
represented-user discovery, reads the current job, then discovers authority
again. Source identity, recovery epoch, principal and authorization version must
remain consistent. A missing job suppresses that event. A verified namespace
denial revokes the corresponding interval; an unavailable or malformed discovery
response defers work and does not fabricate a grant denial.

The database transaction checks the current local account/alias, source registry,
feed generation, restore hold and cutoff, rule intervals, individual denials and
scope-removal watermarks. It checks actual proof expiry again after writes. Inbox
insertion, match context, eligible-device queue records and evaluation completion
commit together. A rollback leaves pending work recoverable.

Pending rules have a separate revision-fenced work queue. Recovery from a source
outage can automatically activate a still-pending interval at a fresh source
boundary. Automatic work cannot revive an interval denied by access loss or
operator scope removal. An explicit user revalidation is required for that.

## From the inbox to APNs

Each queued delivery captures one exact installation, account binding and token
registration version. A claim contains no token. A fresh represented-user check
precedes a short database preparation transaction, which rechecks current rule
intent, device preferences, logout capability readiness, source and restore
fences. Only then does it decrypt the current token and record an unknown attempt.
No database transaction remains open while contacting Apple.

Network deadlines cannot outlive the verified authorization proof, the fresh
source fence or the 24-hour delivery window. Authorization expiry during a database
write rolls back the attempt before a token handoff is returned. A current token
rotation can redirect a retry within the same binding. Account switching cannot
redirect an old delivery to the new binding.

Provider acknowledgement and token invalidation commit atomically. A410 response
cannot invalidate a newer registration, including a refresh with identical token
bytes. Late responses from a superseded delivery lease cannot resolve its
successor. Shutdown still gives an in-flight acknowledgement a bounded chance to
commit; a process crash can leave an unknown attempt. Retrying uses the same
delivery and collapse identifiers, without claiming exactly-once presentation
by iOS.

Provider configuration failures pause every delivery for that topic/environment
for at least 15 minutes, including newly queued devices. Transport uncertainty
retries with bounded backoff. Operator holds stop new preparation; they do not
erase an acknowledgement of a handoff that already happened. Expired pending
deliveries can be resolved locally during an outage or hold, preserving history.

## Operational bounds and evidence

- Up to 32 sources, four simultaneous evaluation operations and four delivery
  operations; network calls share their component's four-slot bound.
- At most 100,000 pending account evaluations, 1,000,000 pending deliveries and
  10,000 candidate accounts per event. Admission and cursor progress are atomic;
  capacity failure cannot skip an event or leave partial fanout.
- Delivery leases last 60 seconds. Each delivery permits at most 128 provider
  attempts. Retry delay is bounded; provider configuration backoff is shared.
- Inbox expiry starts at insertion and lasts 30 days. Original event time is
  retained separately. Events already beyond the delivery deadline can still
  enter the inbox without generating an obsolete push.
- Maintenance resolves at most 500 expired deliveries per pass. Detailed
  retention and operator metrics are separate lifecycle concerns.

Local TLS/HTTP2 provider tests verify signatures and wire behavior. Real TLS
PostgreSQL tests verify leases, atomic rollback, overlapping rules, source/account
isolation, token rotation, stale provider responses, permission loss, restore
holds and mid-write authorization expiry. These tests do not establish real APNs
delivery or company-managed iPhone acceptance. Deployed multi-Control event
acceptance and the agreed load envelope remain separate release gates.
