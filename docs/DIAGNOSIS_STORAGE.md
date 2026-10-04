# Deterministic diagnosis collection and persistence

Dashboard captures an authorized transactional Control snapshot, seals evidence
with the public Core collector, and invokes the public deterministic Diagnose
engine. It never calls a model provider, launches advice, or reads arbitrary
paths. A report and its exact sealed analysis evidence share one private object;
the original core, analysis-evidence and report IDs remain unchanged.

## Collection and current access

Metadata is the default profile. Explicit `include_log_tail` requires current
`logs.read` in addition to `jobs.read` and `evidence.read`, configured log brokers,
and an operator-supplied value-aware redaction policy. Commands, environment
values, source files and paths are excluded from collection. The source adapter
uses the public strict shared-snapshot decoder, rejects field-name aliases, and
checks configured source/instance/namespace/job/revision/run and recovery epoch.

Each log tail is limited to64KiB, with at most16 broker pages (100 chunks per page)
under the60-second task deadline. A dense stream continues from the broker's
account-bound cursor; missing/corrupt bytes, gaps, an exhausted page budget or a
changed manifest fail the request without producing partial evidence. Pre/post
manifest reads pin run/execution/stream/revision/byte length/completion/epoch.
The broker independently verifies immutable chunk checksums and current access.

Private redaction configuration has `values` and `patterns` arrays. Literal
values and bounded Go RE2 patterns operate across the complete captured tail;
use `(?s)` for regex dot/newline matching. Limits are128 literals,64 patterns,
4KiB per entry,128KiB combined decoded text and16,384 regex instructions. Empty
or empty-matching expressions are rejected. Matches are masked with the same
number of bytes, preserving source offset attribution; matching literal fragments
at bounded tail edges are also masked. No policy or matched text appears in
errors. This is an operator policy, not a guarantee that arbitrary logs contain
no other secrets. Raw logs are still visible only under the configured namespace
log capability.

Cache identity covers the capture-insensitive metadata evidence ID, recovery
epoch, disclosure profile, effective byte budget, and a keyed policy fingerprint,
plus all relevant collector/engine/component versions. The policy identity uses
a purpose-separated HMAC derived from the persistent Dashboard encryption key;
raw policy hashes are not published. Workers recapture and compare this identity
before collection and publication. Old ready reports remain immutable and become
outdated after source facts or component/policy versions change. A changed Control
identity/epoch or deleted/inaccessible job denies access to the old envelope.

Every task/catalog/detail/citation read checks the current local account and
verified alias, current source capabilities and job, and the stored disclosure
profile. Report objects are verified and reauthorized before response. Ownership
is the immutable account, allowing its approved web/native aliases; an authorized
admission refreshes the candidate alias used for background delegation. No bearer
or refresh token is stored in report tasks. Citations resolve only the report's
validated join table and exact sealed item/artifact/enrichment ranges.

## Queue and object boundaries

Migration5 adds tasks, separately owned requester bindings and account-scoped
hashed idempotency keys. A short SQL transaction admits requests, deduplicates
equivalent snapshots, binds the verified requester and records content-free audit.
Source, filesystem, directory and analyzer work occurs outside SQL transactions.

Bounds are512 pending tasks globally,8 per account,32 requester links per shared
task,10 new request keys per account/minute and10,000 retained task families.
The retained quota rejects new work without deleting unexpired history; it bounds
normal retained pair capacity to approximately59GiB at the worst-case encoded
object limit, before filesystem/temporary/orphan overhead. Provision a filesystem
quota and monitor failed cleanup/storage pressure. The32-link ceiling also applies
to ready-object reuse. Replaying a key for the same caller-selected source/job/run/
profile returns the original task even after source revision advancement; a
changed caller selection conflicts. Requests and bindings expire with their task.

Two synchronous worker loops claim using `FOR UPDATE SKIP LOCKED` and fresh lease
UUIDs. Leases last90seconds; individual work has a60-second deadline. Every
transition/publication requires the exact current unexpired lease. A final SQL
check also verifies the publishing account/alias. At most3 attempts are allowed;
only source/authorization unavailability retries. Confirmed permission loss or
changed/invalid evidence fails immediately. Interactive report operations have a
15-second bound. Failed/stale work never produces an apparently ready report.

Object roots must be private0700 directories; pair files are0600. Publication
validates both seals and their pairing, enforces public4MiB evidence/2MiB report
limits, fsyncs a new temporary file, and atomically hard-links it under a task UUID
without overwrite. The temporary name is removed and directory synced before
publishing the database reference. Use private local storage supporting hard links
and fsync, outside the web/log/artifact roots. Reads reject corrupt, linked,
nonprivate, oversized or mismatched objects and verify the recorded file hash.

A worker can recover a complete file published before its prior SQL commit, after
fresh task/source/policy/lease checks. Original IDs remain unchanged. Thirty-day
retention deletes task/requester/idempotency rows together and unlinks their pair.
The incremental janitor scans100 names per pass and reclaims unreferenced objects
or abandoned temporary files only after a one-hour grace period. Any existing
task, including pending work, protects its object. Database failures prevent
orphan deletion. Never retain a citation-bearing report separately from evidence.

## Configuration and verification

Set `reports.objectRoot` and optionally `reports.redactionFile` in the production
configuration. The redaction file must be private and contain only the two JSON
arrays above. An absent policy leaves metadata reports available and explicitly
rejects new log-tail requests. Omission of report configuration supports staged
monitoring upgrades; the initial release requires report storage enabled. The
service derives the companion version from its embedded immutable Go dependency;
local module replacements and workspace `(devel)` dependencies are rejected.
Configured private files, including redaction policies and service keys, must
resolve outside the static web tree. Report object storage must neither contain
nor be contained by that tree. Startup checks resolved aliases; static serving
uses a pinned filesystem root that rejects symlink escapes introduced later.

Tests use an immutable synthetic public Core fixture with source commit/SHA256 in
`internal/reports/testdata/manifest.json`. They run the actual public collector,
redactor, deterministic engine, sealed-pair decoder, log broker and private file
store. `GOWORK=off python3 scripts/test-lab-postgres.py` uses disposable schemas
and private synthetic credentials. Tests cover deduplication, aliases, leases,
revocation, source recovery, retries, retained quota, publication crash recovery
and paired retention. These tests do not establish production AD FS, live Slurm,
Apple delivery or full release acceptance. Compatible released dependency tags
and the broader acceptance plan remain required.
