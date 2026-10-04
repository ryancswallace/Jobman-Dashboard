# Control event source adapter

`internal/events.Source` provides `SourceID`, a sorted/cloned `NamespaceIDs` set,
`Checkpoint`, and bounded `Read` operations. `control.NewEventSource` creates a
separate mutually authenticated HTTPS transport and two background request slots;
it does not share interactive request capacity. Each operation has a 15-second
deadline, individual HTTP requests have an eight-second deadline, redirects are
disabled, and responses are bounded to 2 MiB, depth 16 and 10,000 JSON values.
Pages contain at most 200 events. No operation resets a caller's cursor.

The source is pinned to an operator-configured deployment UUID, Control instance,
CA, certificate, delegation audience and explicit namespace UUID set. The adapter
checks the `durable-monitoring-events` capability before dispatch and verifies
instance/epoch consistency against the actual checkpoint/page. A configured
identity verifier may additionally enforce persisted source registration. Its
explicit source-recovery errors remain recovery errors, not generic transport
failures. Namespace-set changes are visible through `NamespaceIDs` before any
old continuation is consumed.

`DelegationSigner.AuthorizeEvents` signs only a nonempty unique subset of the
signer's pinned namespace whitelist. Assertions have `operation: events.read`,
`mode: worker`, and `sub == iss`, and omit `actor` entirely. They use fresh random
assertion IDs, pinned Ed25519 keys, a certificate thumbprint, and at most a
60-second lifetime. Ordinary represented-user signing still rejects `events.read`.
The source performs no `/me` call: service ingestion remains independent of user
directory availability. Event data confers no user-read or notification-delivery
authority.

Checkpoint and event counters use canonical nonnegative decimal strings bounded
by signed int64; revisions, run numbers, event positions and epochs are positive.
The adapter preserves the original event UUID, owner, actual optional run pair,
phase/outcome, observation/recording timestamps, and import/reconciliation flags.
Deployment and Control instance are added from verified source configuration and
response identity. Epoch is checkpoint state and is deliberately excluded from
the stable `(deploymentId, controlInstanceId, eventId)` event identity.

`Checkpoint.Validate`, `Event.Validate`, and `Page.Validate` are also usable by
durable ingestion. They check identity, configured scope membership, bounds,
ascending unique positions and event UUIDs, paired optional run fields, and at
most 4,096 encoded bytes per minimal event. Unknown bounded phase/outcome tokens
remain factual values. Feed JSON rejects duplicate, unknown and case-alias field
names; missing import/reconciliation flags are invalid, not assumed false.

Control's `event_cursor_expired`, `source_recovery_changed`, and
`event_cursor_scope_changed` conflicts produce typed `events.RecoveryError` values.
An invalid authenticated cursor, including after Control cursor-key rotation,
produces `invalid_cursor`. All unwrap to `ErrRecoveryRequired`. Consumers must
pause, record a gap and reconcile explicitly; they must not fetch a new head
automatically or turn retained history into fresh notifications. Source errors
never expose upstream messages or assertion/cursor contents.

This adapter does not own durable journal transactions, rule matching, user
authorization for delivery, or APNs. Those are separate workers with their own
lease and recovery contracts. Tests use real synthetic mTLS and signed assertions;
they do not establish production AD FS or APNs compatibility.
