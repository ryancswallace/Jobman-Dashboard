# API and worker process boundaries

Status: API/worker startup, role-specific local configuration, purpose-key loading
and verified delegation provenance are implemented with focused regression tests.
Least-privilege database grants and the split deployment acceptance below remain
separate gates. The combined `serve` mode remains compatible; this document does
not claim that the split has been exercised in the Lab or in production.

DESIGN §§2.1, 2.2 and 12 require independently deployed API and worker processes,
with credentials appropriate to each role. Durable database leases already
coordinate work; process separation must preserve their authority and recovery
checks rather than introduce a second queue or in-memory ownership system.

## Modes and startup

| Mode | Responsibilities | Inbound application listener |
| --- | --- | --- |
| `serve` | Existing API and configured worker components together; compatibility mode | HTTPS |
| `api` | Web/native authentication, reads, authorized rule/device/preferences mutations, report enqueue/read, inbox read state | HTTPS |
| `worker` | Only the explicitly selected components below | None |
| `check-config` | Strict role-specific configuration and local material validation; no external request or state mutation | None |
| `migrate` | Forward migrations using a separately supplied DDL identity | None |
| `status` | Local database observation in JSON or Prometheus format | None |
| `events` | Explicit operator recovery/hold commands with their existing approval receipts | None |
| `keys export` | Explicit offline export of existing purpose-derived keys into a new private directory | None |

Worker configuration selects a nonempty, duplicate-free list of at most five
components: `ingestion`, `notifications`, `delivery`, `reports`, `retention`.
`notifications` includes pending-rule activation and per-account evaluation.
The initial topology may use one API and one worker bundle; smaller component
sets permit independent service identities and credentials without changing the
durable queues. Selecting delivery without configured APNs providers must be an
explicit configuration error, not a worker that silently does nothing. Inbox-only
installations select ingestion and notifications without delivery.

API mode starts no ingestion, activation, evaluation, provider, report-analysis or
retention loop. Worker mode does not instantiate the OIDC authenticator, open web
assets or read browser TLS/client-secret/session-encryption files. It uses the
trusted configured issuer only when resolving previously verified local aliases;
there are no stored user access or refresh tokens. Every represented-user read
still obtains current Control authorization.

The application fails before listening or starting work on invalid local
configuration/material or a mismatched schema ledger. A source, directory or
provider outage encountered by a running worker retains bounded retry/hold
behavior; it does not restart a healthy process into a crash loop. Shutdown
cancels and joins workers before closing their database pools or object roots.
Keep the service stop budget longer than a 60-second report task plus bounded
cleanup, and allow database leases to expire after an abrupt process failure.

## Credentials and source authority

All roles pin the same canonical deployment UUIDs, expected Control instances,
allowed namespaces and current configuration revision. A role may have fewer
sources, but it cannot quietly repurpose a deployment UUID or roll its revision
backward. Register distinct mTLS certificates and delegation keys for independently
managed roles. The current source protocol signs `interactive` or `worker` in the
actor assertion; adapters fix this value at construction, never from HTTP input.
Event feed requests remain service-only assertions without a represented actor.

| Role | Control operations | Private material beyond its own database URL |
| --- | --- | --- |
| API | Namespace/jobs/groups/targets/logs/artifacts/evidence reads; event checkpoint reads for immediate rule activation | Web TLS, OIDC web secret and session key; device token encryption key when devices enabled; report fingerprint policy key and read-only report storage when reports enabled; broker/log cursor keys when logs enabled |
| Ingestion | `events.read` only | Its source mTLS/delegation keys |
| Notifications | `namespace.read`, `jobs.read`, `events.read` | Its source mTLS/delegation keys; device topic policy, without token decryption or Apple key |
| Delivery | `namespace.read`, `jobs.read`, `events.read` | Its source keys, device token read key ring and the approved APNs signing key |
| Reports | `namespace.read`, `jobs.read`, `evidence.read`, `logs.read` | Its source and broker keys, redaction policy/fingerprint key, worker log cursor key, private report object writer |
| Retention | No source network operation; uses stored source-health fences | Its database URL and report object writer only when report retention is selected |
| Log broker | `namespace.read`, `logs.read` | Separate broker server/source identities, local identity ledger and designated log-reader ACL identity |

The API's event-checkpoint capability is deliberate: current rule activation
captures a fresh source boundary. Removing it would require making all API-created
rules pending for worker activation, a separate behavior change. No event feed
payload becomes a public API merely because the API process holds that service
capability. Operation allowlists are registered at Control and at the broker;
process configuration cannot expand a remote service's authority by itself.

Private key separation must not silently reset encrypted devices, authentication
sessions or report fingerprints. The combined configuration may retain its
documented legacy derivations. Before splitting, export only the existing
purpose-derived bytes into dedicated private files through a reviewed offline
operation, preserving key IDs and derivation format. Validate that old device
ciphertexts still decrypt and old redaction fingerprints remain identical. Never
give a delivery worker the browser/session root key merely to preserve tokens.
Retain previous token read keys for an explicit rotation interval; no key material
is printed or placed in a manifest, CLI argument or repository. The completion
manifest records only purpose-key paths and preserved key IDs, never key bytes.

## Database grant matrix

Runtime identities are not database owners and cannot create, alter or drop
schema objects, modify the migration ledger, grant privileges, or bypass row
security. Use explicit table and column grants; do not copy the synthetic Lab's
convenient `ALL TABLES` DML grant into production. PostgreSQL row locks require
UPDATE authority as well as SELECT, including `FOR SHARE`; grants must account
for that without granting worker updates to identity or session contents.

| Table family | API | Ingestion | Notifications | Delivery | Reports | Retention |
| --- | --- | --- | --- | --- | --- | --- |
| Migration ledger | Read | Read | Read | Read | Read | Read |
| Accounts/aliases | Authenticate and maintain verified mappings | None | Read/current-row locks only | Read/current-row locks only | Read only | Read/current-row locks only |
| Sessions/login attempts/preferences/cursors | API-specific CRUD | None | None | None | None | Bounded expiry deletes; cursor quotas as required |
| Source identity pins | Read/advance current identity fence | Read/advance | Read/advance | Read/advance | Read/advance | Read only |
| Feeds/events/gaps | Read fresh activation checkpoint; no ingestion loop | Claim, append and safe gap state | Read/row locks; event completion | Read/row locks | None | Bounded guarded cleanup |
| Rules/versions/activations/revocations | Owner-authorized mutations | None | Match/read, activation/revocation writes | Match/read, current denial writes | None | Bounded history retirement |
| Evaluation/fanout/work quota | Only effects of API rule/device mutations where required | None | Claim and atomic inbox/delivery enqueue | Delivery quota updates | None | Guarded expiry/quota updates |
| Inbox/match context | Authorized reads/read-state updates | None | Atomic insert and private match context | Current authorized lookup | None | Expiry deletion |
| Devices/revocation journal | Authorized registration/settings/revocation | None | Current binding/version/readiness reads and row locks | Current token reads, invalidation and row locks | None | Only documented bounded lifecycle cleanup |
| Delivery/attempts/provider health | Authorized inbox status reads | None | Atomic enqueue | Claim, preflight, attempts, acknowledgements/backoff | None | Guarded expiry/journal cleanup |
| Report task/requester/idempotency | Enqueue/read and owner binding | None | None | None | Lease/analyze/complete/fail | Expiry/reference cleanup |
| Content-free audit | Insert API actions | Appropriate source actions only | Insert activation-denial actions | Insert device invalidation actions | Only defined actions | Bounded age deletion |

This matrix is the review boundary; executable role grants are being developed
and tested separately. Final grant scripts must
be derived from actual statements, including column-restricted lock privileges,
identity sequences and trigger dependencies. The `status --operator-config` command uses a separate minimal observation
configuration and read-only database identity; legacy `--config` remains supported.
Do not add operator grants to the API role. A restricted process-owned health or
metrics listener is a separate integration seam, not part of the status command. Tests must run real API and worker
operations under those roles, and independently prove denial of account identity,
alias, session, migration-ledger and unrelated table mutation. A role that cannot
perform its required transaction must fail closed; a broad grant is not a fix.

## Configuration examples

Validate each role without external requests:

```sh
jobman-dashboard --mode check-config --check-mode api --config /etc/jobman-dashboard-api/config.json
jobman-dashboard --mode check-config --check-mode worker --config /etc/jobman-dashboard-worker/config.json
jobman-dashboard --mode api --config /etc/jobman-dashboard-api/config.json
jobman-dashboard --mode worker --config /etc/jobman-dashboard-worker/config.json
```

Omitting `--check-mode` validates the combined compatibility configuration. A
worker config is rejected in API mode and vice versa. `migrate` continues to use
the API-shaped public configuration and a separate explicit DDL URL file; worker
mode rejects the migration credential flag.

The API uses the existing strict `deploy/config.example.json` shape, with its own
database credential, API source/broker identities, and no APNs signing providers.
Use separate absolute paths under `/etc/jobman-dashboard-api` or the service's
credential directory. Public trust files can be shared read-only.

The worker configuration has no `publicOrigin`, `listen`, `webRoot`, `serverTLS`,
interactive `oidc` registration or master `encryption` field. A minimal
ingestion configuration is:

```json
{
  "configurationRevision": 2,
  "databaseURLFile": "/run/credentials/dashboard-ingestion/database-url",
  "components": ["ingestion"],
  "controls": [
    {
      "id": "11111111-1111-4111-8111-111111111111",
      "name": "Private Control",
      "origin": "https://control.example.internal",
      "expectedInstanceId": "22222222-2222-4222-8222-222222222222",
      "namespaceIds": ["33333333-3333-4333-8333-333333333333"],
      "trustRootsFile": "/etc/jobman-dashboard/trust/control-ca.pem",
      "clientCertificateFile": "/etc/jobman-dashboard-ingestion/client.pem",
      "clientKeyFile": "/run/credentials/dashboard-ingestion/client-key.pem",
      "delegationKeyFile": "/run/credentials/dashboard-ingestion/delegation-key.pem",
      "delegationKeyId": "ingestion-key-1",
      "serviceId": "dashboard-ingestion",
      "audience": "urn:jobman:control:dashboard"
    }
  ]
}
```

A notification evaluator selects `"components": ["notifications"]`, adds
`"identityIssuer": "https://adfs.example.internal/adfs"`, uses its separately
registered source identity, and may add the same allowed device topic policy as
the API. It does not require APNs keys or token decryption keys.

[worker.example.json](../deploy/worker.example.json) is a complete structural
example for an inbox/report worker bundle. It deliberately has no delivery
component or APNs key. Replace all identities, paths and the example numeric
worker UID/reader GID with provisioned values; structural validation does not
assert that any remote grant, OS identity or private material exists.

A delivery-only worker uses `"components": ["delivery"]`, the trusted
`identityIssuer`, its registered controls, and this notification block. It omits
reports, log brokers and log cursor material:

```json
{
  "deviceTopics": [{"topic": "org.example.dashboard", "environment": "sandbox"}],
  "tokenEncryption": {
    "current": {"keyId": "preserved-device-key-id", "keyFile": "/run/credentials/dashboard-delivery/token-0.key"},
    "previous": []
  },
  "apns": [{
    "topic": "org.example.dashboard",
    "environment": "sandbox",
    "teamId": "AAAAAAAAAA",
    "keyId": "BBBBBBBBBB",
    "privateKeyFile": "/run/credentials/dashboard-delivery/apple-key.p8"
  }]
}
```

The displayed Apple IDs are placeholders, not approved provider credentials.
API device configuration uses the same dedicated token ring and allowed topics,
with `apns` absent or empty. API mode rejects nonempty providers before loading
any provider file. Notification evaluation alone accepts the topic list but
rejects token encryption/provider keys. Legacy `previousTokenKeys` are never
accepted by worker configuration.

For report workers, `reports.policyKeyFile` accompanies `reports.redactionFile`;
`logCursorKeyFile` is required whenever remote broker mappings are configured.
The API and report worker must share the exported policy key and identical policy
to preserve snapshot fingerprints. Dedicated log keys preserve existing cursor
compatibility. New installations provision fresh purpose keys explicitly. A
retention-only process may use `controls: []`; when source-event retention is
needed, list the configured source descriptors. Only their deployment IDs are
used, and their key files need not be mounted or readable. Report retention needs
only `reports.objectRoot` and its access profile, without policy or source keys.
Unknown or unused worker credential fields fail validation.

To migrate legacy derived keys without resetting encrypted state:

```sh
jobman-dashboard keys export --config /etc/jobman-dashboard/legacy.json --output-directory /private-operator-directory/new-purpose-keys
```

The destination must be new and its parent private and owned by the invoking
operator. Read the private completion manifest, distribute only each role's
needed key files through the normal secret mechanism, and preserve key IDs. A
partial export is retained for operator inspection and is never overwritten by
retry. No existing key or ciphertext is changed.

## Report object access and service isolation

Current owner-only report storage uses a 0700 directory and 0600 immutable paired
objects. Keep that default. A different worker UID cannot write objects that an
API UID can read under those modes merely by sharing a path.

Separate OS identities require an explicit, validated private shared-group profile
on a supported local Linux filesystem (not the NFS log store):
worker-owned 0750 root, worker-owned 0640 immutable objects, a dedicated reader GID,
API read/traverse access, and no unrelated group/other access. A read-only object
constructor must not create, write, sweep or remove files. The API's service mount
is read-only; report writer/retention units alone get the required writable mount.
No automatic recursive chmod or broad ACL changes are part of application startup.
For example, `"objectAccess": {"mode": "shared_group", "workerUid": 21002,
"readerGid": 21003}` binds the root and objects to the provisioned numeric
identities. Supported filesystem types are local Linux ext4, XFS, tmpfs and
Btrfs. Overlay, NFS, FUSE and the shared profile on Darwin are rejected. The
opened file descriptor must prove absence of the POSIX access ACL, and directories
must also prove absence of a default ACL; an unsupported or denied xattr probe
fails closed. Unexpected UID/GID/modes also fail closed. Record and verify conversion of any existing object root as a separate operator
step with worker hold and immutable-object checksum checks.

Use separate unprivileged service users, private credential directories,
`NoNewPrivileges`, `ProtectSystem=strict`, a narrow writable report path, and
appropriate outbound network policy. Worker processes have no inbound application
listener. Restricted health/metrics are a distinct operator surface, not a public
API. A common service UID can be used only as an explicitly documented transitional
trust boundary; it does not establish OS credential isolation.

## Staged migration and acceptance

1. Preserve the exact reviewed combined build, configuration, database backup and
   immutable report objects. Record source pins, key IDs and schema version without
   copying secrets into the record. Establish a delivery hold if restore or uncertain
   prior provider handoff is involved; a clean planned restart alone is not a replay.
2. Provision separate DB/service identities and narrow remote operation grants.
   Prepare purpose keys without resetting existing encrypted state. Validate configs
   and secret-file access as each service UID before stopping the combined process.
3. Stop the combined service. Apply only forward migrations with the DDL identity,
   install explicit runtime grants, and verify denied privileges. Do not run old
   binaries against an unsupported newer ledger or silently downgrade the schema.
4. Start API-only. Verify authentication and authorized reads/mutations; report
   requests remain queued while analysis is stopped. Device registration must work
   without an APNs key in the API process.
5. Start the selected workers. Verify current offline-user authorization, original
   event identity, overlap deduplication, report pair checksums and correct worker
   delegation mode. Start delivery only with approved provider credentials/routes.
6. Exercise abrupt termination and expired-lease takeover for each component,
   concurrent worker replicas, API/worker restart order, source and directory outages,
   authorization revocation, device switch/revocation and retained delivery hold.
   Prove API report reads cannot write storage and workers cannot use interactive
   secrets or mutate protected DB identity/session fields.
7. In split deployment, retention runs only in the selected worker; independently bound each maintenance
   component so one slow pass cannot starve all later cleanup. Check pending-work
   protection and retention watermarks under source outage and database restore.
8. Record the exact artifacts, config revisions, service UIDs, tested grants and
   results. A restore hold is released only by the existing explicit reconciliation
   procedure. No process startup automatically clears it. In API/worker mode, `deliveryHold:
   true` only requires an already persisted operator hold and fails before starting
   if absent; it never mutates operator hold state. The legacy combined `serve`
   mode retains its existing establish-hold behavior.

An isolated Linux tmpfs test has exercised actual distinct worker/API/unrelated
UIDs, immutable pair reads, DAC write/unlink denials and fail-closed ACL handling.
That storage test did not alter deployed service identities and does not establish
whole-process split acceptance.

Actual AD FS, APNs background delivery, company-managed iPhone distribution and
agreed-scale load evidence remain separate release gates. Process separation and
synthetic Lab operation alone do not establish those deployment-specific results.
