# Deterministic diagnosis persistence

The storage foundation keeps a report and its exact sealed analysis evidence in
one private object. It preserves the original core, analysis-evidence and report
IDs. A task UUID is an application reference and never replaces those IDs.
This foundation is not yet the complete report API or worker integration.

Migration5 adds source-qualified tasks, separately owned requester bindings and
account-scoped hashed idempotency keys. A short transaction admits requests,
deduplicates equivalent source/snapshot/profile/version inputs, binds the verified
requester and records a content-free audit action. The migration identity applies
DDL; runtime roles use ordinary table rights. No source, filesystem, directory or
analyzer work occurs while a database transaction is open.

Admission bounds are512 pending tasks globally,8 per account,32 requester links
per shared task and10 new idempotency keys per account per minute. Replaying the
same key and caller-selected source/job/run/profile returns its existing task,
even when the source revision advances during an HTTP retry; reusing that key
for different caller selections conflicts. The32-link ceiling also applies when reusing a ready task.
Request keys and bindings expire with their task after30days. Limits reject new
work explicitly rather than silently discarding requests.

Workers claim with `FOR UPDATE SKIP LOCKED` and a fresh lease UUID. Leases last
90seconds, while analysis work must be bounded to60seconds. Expired leases can
be reclaimed; every state transition and publication requires the exact current
unexpired lease. At most3 attempts are allowed. Only source/authorization
unavailability is eligible for a scheduled retry; invalid evidence or confirmed
permission loss fails immediately. A worker that loses its final lease becomes
`failed` with a content-free interruption code.

Object roots are private0700 directories and pair files are0600. A write
validates the full evidence/report pairing, enforces the public4MiB evidence and
2MiB report decoding limits, syncs a new private temporary file, and publishes
it with an atomic exclusive hard link. This provides no-overwrite publication
without exposing a partially written pair; the temporary name is removed and
the directory synced before the database can mark the task ready. Use a local
filesystem supporting hard links and fsync. Do not place the private object root
inside the web root or the shared log/artifact store.

Reads verify the bounded regular file, private mode, original semantic seals,
exact pair and all database-recorded object identities/hash. Symlink objects,
source/profile/version mismatches and corrupt documents fail closed. Crash
recovery can verify a completed unpublished pair against its claimed task;
the worker must still own its lease and recheck current source authorization.
Deletion removes one whole pair, and expired task/requester/idempotency rows are
deleted together. A subsequent object janitor must reclaim interrupted orphan
publications after a grace period.

These are integrity and persistence boundaries, not access grants. The service
must check current account, source, namespace, job and evidence/log-profile
permissions before any task, object or citation response. It must compare the
captured metadata fingerprint and recovery epoch to the queued selection before
publishing. The original sealed core does not contain the Dashboard recovery
epoch, so the outer queue envelope alone cannot prove that source is unchanged.

Tests use an immutable synthetic public core fixture with its source commit and
SHA256 recorded in `internal/reports/testdata/manifest.json`. They invoke the
actual public deterministic engine, never a model provider. Real PostgreSQL
tests run in disposable schemas through `scripts/test-lab-postgres.py`; they
cover concurrent deduplication, requester isolation, disabled accounts, stale
lease denial, retry exhaustion and paired retention. Filesystem tests cover
concurrent publication, crash recovery, corruption, private modes and path/source
isolation. Upstream dependencies currently use public immutable development
pseudo-versions; approved compatible release tags remain a release gate.
