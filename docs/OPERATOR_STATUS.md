# Restricted operator status

The local `status` command produces JSON or Prometheus text from a bounded
database snapshot, using `operations.WriteStatus` and `Store.OperationalStatus`:

```sh
jobman-dashboard status --config /etc/jobman-dashboard/config.json
jobman-dashboard status --config /etc/jobman-dashboard/config.json --format prometheus
```

Run it as an authorized operator with access to the private configuration and
runtime database credential. It reads no identity, source, signing, encryption
or server TLS key material, performs no network dependency probes, and makes no
registry or data changes. The command has an eight-second total deadline; failures
return a nonzero exit status without credential-bearing error details. Restrict
saved output to operators. There is no application API route or metrics listener
in this command; do not place its output on a public health endpoint.

Only the configured deployment set is selected, with at most 32 canonical UUIDs.
An empty set means no sources. Metric labels contain those stable deployment
UUIDs and fixed component/state/error classes. Display names appear only in
escaped JSON. Account, job, event, namespace, installation, provider-topic,
signing-key and token identities are absent, as are reusable cursors, source
payloads and raw upstream/database errors.

The snapshot reports:

- Stored feed state, safe error class, successful-ingestion time, retained event
  count and observed replay-retention high-water.
- The stored source checkpoint's observation time, unpublished backlog and
  oldest unpublished recorded time. These observations can be stale.
- Local unprocessed events and fanout still pending, with insertion ages.
- Evaluation and delivery pending, due and actively leased counts, plus oldest
  insertion time. Due means the retry time arrived without an active lease;
  authorization, source health, provider cooldown and delivery holds are separate
  gates, so it is not a promise that a worker may run the item immediately.
- Global activation work pending/due/leased counts. Its oldest timestamp is the
  current rule's update time, a lower-bound proxy for queue age because this queue
  has no independent insertion timestamp.
- Durable delivery hold, generation and restore suppression cutoff, together
  with each feed's prune and suppression watermarks.
- Fixed-component APNs aggregates of locally recorded provider failures,
  cooldown entries, last failure and longest remaining cooldown. A provider
  acceptance is not proof of presentation on a phone.

The database transaction uses repeatable-read isolation without worker row locks,
a five-second total context deadline, a two-second statement timeout and a
500-millisecond lock timeout. Queries use source-qualified pending indexes and
return at most the configured source set. Global activation/provider aggregates
remain timeout-bounded. Load measurements at the agreed scale are still required;
no new index or schema is added merely to claim that acceptance.

Missing feed rows are `uninitialized`, with no fabricated queue or checkpoint
values. A failed query, invalid stored checkpoint or cancelled request produces
an unavailable error and no partial output. JSON counts are exact decimal strings;
Prometheus ingestion uses its normal floating-point sample representation. Missing
ages are omitted instead of being reported as zero. Age calculations clamp future
clock-skewed timestamps to zero; JSON retains the actual observed timestamps.

This is stored operational evidence, not live dependency readiness or a liveness
probe. It does not expose an opaque source retained-lower-bound cursor or infer a
timestamp from it. API/source latency, authorization revocation lag, log/report
failure instrumentation and cumulative retention-deletion counters remain separate
instrumentation work. Retention routines currently return per-pass counts, not
durable totals; this snapshot does not fabricate cumulative counters from them.

Unit tests cover exact counts, escaped display text, bounded labels, invalid
snapshot rejection, missing feeds, cancellation and writer errors. Opt-in real
PostgreSQL tests cover source isolation, pending/fanout/lease counts, hold and
provider state, activation age, malformed checkpoints and bounded table-lock
failure. The latter use `JOBMAN_DASHBOARD_TEST_DATABASE_URL` and isolated test
schemas, like the rest of the store suite.
