# Private deployment operations

This runbook applies to the implemented API, worker and broker services. The
engineering candidate has synthetic Lab evidence, not production acceptance.
Assign a named on-call owner for the Dashboard service, identity/directory,
Control sources, PostgreSQL/backups, NFS, and Apple signing/push before production
rollout. Those assignments and the proposed RPO15minutes/RTO4hours remain open.
The role names below identify responsibility, not an assertion that someone has
accepted it.

## Establish the scope of an incident

Record the installed build manifest, configuration revision, affected deployment
UUIDs, first observed time, safe error class and recent approved change. Keep
private configuration, keys, raw database errors and job/log content out of tickets
and exported telemetry. Read the process's private `/livez`, `/readyz`, and
`/metrics`, then the [stored operator status](OPERATOR_STATUS.md). Use the
dedicated read-only operator credential for status; no runtime grant widening.

Liveness proves only that the local handler responds. API/worker readiness checks
local startup, runtime database reachability and the exact migration ledger.
Broker readiness does not read NFS. Source/directory/provider failures can occur
while all three processes are ready. A stored checkpoint is a past observation;
inspect its age before treating it as current.

Confirm one authorized user workflow in the affected source and one unaffected
source, when available. A partial aggregate must identify missing contributions;
an inaccessible source is never a healthy zero. Avoid repeated restarts until
the failing dependency or local invariant is understood.

## Incident actions

| Symptom and responsible role | Inspect and recover | Required confirmation |
| --- | --- | --- |
| AD FS sign-in or signing-key failure — identity owner | Verify private DNS, time, TLS chain, exact issuer/client/audience/redirect registration and configured immutable claims. Review discovery/JWKS availability and approved key rollover with the identity owner. Preserve issuer/algorithm/audience validation. | Fresh browser and native S256 sign-in; wrong client/audience/ID token rejected. Existing source authorization remains independently enforced. |
| Directory outage or removed membership still appears — directory and Control owners | Inspect Control's last complete direct-membership verification, reconciliation health and replication visibility. Restore the approved directory endpoint/bind credential/CA. The120second proof freshness limit must fail closed; do not extend it or synthesize a proof. | Existing-token reads and background work revoke after actual group removal; unaffected memberships remain. Regrant requires a new complete proof. Record per-change lag, not a whole scenario duration. |
| One Control unavailable — Control owner | Check that source's TLS, service registration, contract and expected instance/epoch. Restore the same source and its approved endpoint. Keep other sources serving explicit partial results. | Authenticated discovery, list/detail and logs recover with current permissions; aggregates no longer omit the source. |
| Control restored or instance changed — Control and Dashboard owners | Inspect original identity and recovery epoch. A restore needs explicit cursor recovery; a replacement instance needs a new Dashboard deployment UUID. Do not delete a registry/ledger or reuse an old deployment identity for a new instance. | Reviewed source pins and increasing configuration revision; stale cursors rejected; [event recovery](EVENT_RECOVERY.md) applied explicitly. |
| Delegated service trust revoked or compromised — security and source owners | Disable the affected source-side service registration first and stop the affected worker/caller if necessary. Preserve the evidence and other source registrations. Stage distinct replacement certificate/signing material and approved operations/scopes. | Old certificate/assertion denied; new interactive and worker assertions obey their separate scopes and current user authority. |
| Missing or inaccessible logs — NFS and producer owners | Check exact deployment, target generation, logical store/version, source manifest, mount and designated reader ACL. Verify as the reader identity. Preserve root-squash and private unrelated files; never recursively widen user storage permissions. | Existing and newly produced chunks are readable only by the authorized reader; empty completion is distinguished from missing/unavailable data. |
| Corrupt chunk or invalid manifest — producer/Control/storage owners | Retain immutable subject, declared checksum and safe failure state. Repair the producer/publication/storage cause using its supported procedure. Do not rewrite a manifest checksum to make corrupted bytes pass. | Original identity/size/checksum checks pass, or the application continues reporting unavailable/corrupt evidence without serving bytes. |
| Slow/stalled NFS or `reader_busy` — storage owner | Inspect mount/server health and bounded reader-child slots. Restore the mount/server and verify killed children have actually reaped. Repeated broker restarts can leave uninterruptible kernel tasks; they are not recovery. | Reader slot use returns to its bound; bounded reads complete; timeout/busy states stay distinct and do not advance a cursor over missing bytes. |
| Report failures or missing/corrupt objects — Dashboard/storage owners | Inspect safe task failure, queue age, source authority, local disk/inodes and paired-object permissions. Preserve objects and task references. Restore the matching verified pair from a coordinated backup or request a new report against current evidence. | Evidence/report seals and citation identities validate; API still cannot mutate objects; no report claims usable citations after their evidence is lost. |
| Event gap, paused feed or queue growth — Dashboard/Control owners | Inspect gap reason, source head/retention observation, queue ages, current authorization and capacity. Apply the bounded recovery procedure; it retains original event identities and records incomplete history. | Original inbox/event deduplication preserved, no silent jump to a fresh head, active exact-source feed, and explicit hold release where required. |
| APNs failures or absent phone presentation — push and device owners | Separate provider failure from OS presentation. Inspect safe provider reason, topic/environment, key validity, cooldown and network route. Verify phone permission, binding and device state using the approved physical test. Never log tokens. | Real provider acceptance and phone presentation measured separately, including closed-app/background operation and current-access checks. |
| Database outage, lock pressure or schema mismatch — DBA/Dashboard owners | Inspect bounded pool/queue metrics, database TLS/availability and exact migration ledger. Recover the database or complete the reviewed forward migration. Do not edit the ledger, bypass schema checks, or grant runtime DDL. | Correct schema/checksums, all least-privilege role checks and representative authenticated workflows pass. |
| Private socket stale after unclean shutdown — service owner | Verify the former owning process has stopped and the path is the expected private endpoint. Remove only that proven stale socket, preserving its parent owner/mode; start the same service. | New process owns the endpoint; private liveness/readiness work and the socket is not exposed through the public origin. |
| iPhone VPN/DNS/certificate failure — network/device owners | Confirm the approved private origin, VPN route, DNS and managed trust installation. Use device diagnostics without exporting credentials or job data. Preserve TLS validation; no public proxy or certificate bypass. | Native sign-in and authorized read over the real VPN; foreground reconnect refreshes current authority and clears stale sensitive content when required. |

## Credential rotation

Prepare exact old/new key IDs, file ownership, affected processes, public trust
registrations, configuration revisions, verification commands and recovery steps
before an operator change. Keep a private backup of the current configuration and
recoverable keys. Compare the exact live file hash before replacement, write a
new same-owner private file and atomically replace it; reject drift. Apply one
reviewed source/key scope at a time and preserve unrelated registrations.

For Control or broker delegation, register the replacement public key and client
certificate through the supported source configuration before switching the
caller's private material. The registration must retain the correct audience,
service mode, namespace allowlist and operation allowlist. Advance source
configuration revisions together where required. Test new authorized reads and
old-key rejection after retirement. Changing an event service identity may
invalidate its cursor even when namespace IDs are unchanged; use explicit retained
replay under a delivery hold, as exercised in [split acceptance](LAB_SPLIT.md).

For Dashboard device-token encryption, use the separate purpose-key ring. Only
the current key encrypts new registrations; retain old read keys until no live
binding needs them. A token refresh reseals it with the current key. The API and
delivery worker need matching ring IDs, while other workers receive no device
keys. [Purpose-key migration](PURPOSE_KEYS.md) preserves old ciphertext without
sharing the authentication master with workers.

Changing the web authentication encryption key establishes a new session security
epoch. Plan user reauthentication; do not describe that change as transparent
session continuity. Report-policy keys pin private redaction fingerprints;
changing one can make stored analyses outdated without authorizing any change to
their sealed content. Log-cursor keys similarly affect continuation validation.

APNs key/topic/environment changes belong only in the delivery worker and the
approved allowed-topic configuration. Coordinate actual Apple operations with its
account owner, including retirement and renewal. Offline key/config validation
does not prove real APNs authorization. The API must continue rejecting provider
signing material. See [provider behavior](APNS_PROVIDER.md).

For database credentials, provision and validate a distinct least-privilege login,
then switch only the intended process's private URL file. Preserve verified TLS,
schema compatibility, configured component grants and the separate migration and
operator roles. Retire old login access only after the new process is verified
and its recovery plan permits it. No password or DSN belongs in shell arguments.

## Coordinated backup and restore

Back up the database, paired report/evidence objects, configuration and recoverable
keys together with exact package/schema manifests. A completed backup requires
successful process exit, bounded size/time, durable file sync, checksum, archive
listing and a completion receipt. Preserve an interrupted partial file as failed
evidence; never promote it solely because the path exists. Verify free capacity
and migration rewrite/backup headroom before maintenance.

Before a controlled restore, stop workers and let bounded provider requests
finish. Record the last possible handoff time independently of the backup. Use
that external time for the conservative restore suppression floor; a restored
database's old attempt ledger cannot prove no delivery occurred after its backup.
Restore into an isolated disposable target first and confirm exact migration,
configuration/key compatibility, report seals, role denials and fresh source
authorization. Keep delivery held throughout. Do not start a delivery-enabled
clone beside the original service.

Follow [installation and rollback](LINUX_INSTALLATION.md) for schema compatibility
and [event recovery](EVENT_RECOVERY.md) for hold generation, restore cutoffs,
original-identity replay, reconciliation and explicit resume. A source/key/mapping
change after a prepared fallback makes its configuration revision potentially
stale. Regenerate and review fallback configuration at a revision greater than
both live and stored registry revisions; never restore an older file over a newer
registry or downgrade a migration ledger.

Record elapsed recovery time, restored data boundary, report/object consistency,
idempotent request behavior, session reauthentication where keys change, retained
inbox identities and provider uncertainty. Proposed recovery objectives require
measured exercises and an operating owner's acceptance. Current Lab split/restart
evidence is not a full database restore or real device-delivery exercise.
