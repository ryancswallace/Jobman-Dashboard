# Separate runtime database grants

`grants.json` enumerates every SELECT/INSERT/UPDATE column for schema18; wildcard
grants are rejected, so future columns stay unreadable until a reviewed update.
`grants.py` emits a transactional SQL plan for one existing login identity and one
process profile. It reads no credentials, connects to no service and never creates
roles or changes `PUBLIC`, default privileges, the legacy combined role, or other
roles. The DDL operator applies the reviewed plan with `psql`; runtime processes
must never receive that operator credential.

```sh
python3 deploy/postgres/grants.py --schema public --role dashboard_api \
  --component api > /private/operator-staging/api-grants.sql
# Use the approved local PostgreSQL credential mechanism, not a password in argv.
psql -X -v ON_ERROR_STOP=1 --file /private/operator-staging/api-grants.sql
```

Profiles are `api`, `ingestion`, `notifications`, `delivery`, `reports`, `retention`
and `operator`. A worker bundle repeats `--component`, for example ingestion plus
notifications. API and operator profiles cannot be bundled with workers. Use
separate login roles for separately deployed processes; a combined worker's
privileges are precisely the union of its selected components. `notifications`
includes activation and evaluation. The operator profile is read-only health
observation, **not** authority to execute recovery or release delivery holds.

Before applying, the DBA provisions a new `LOGIN NOSUPERUSER NOCREATEDB
NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS` role, its own password or
certificate authentication, `CONNECT` to the selected database, TLS-only HBA,
and a trusted schema search path. The role must have no memberships or object
ownership. Use a dedicated application schema without `PUBLIC` table/column or
schema-creation grants. The renderer checks these conditions transactionally,
rejects database/schema creation authority, clears stale direct table **and
column** grants for that one role/schema, and installs the selected capability
set. Reapply after reviewed forward schema changes; it grants no rights to future
tables. It does not change other-schema privileges: start with a new role and
review cluster-wide grants separately.

Database-level `TEMPORARY` policy belongs to the DBA. PostgreSQL normally grants
it through `PUBLIC`; revoke it in a dedicated production database if runtime temp
objects are prohibited. The per-role plan does not silently revoke shared database
policy. No application operation requires temporary tables. An explicit trusted
`search_path` and no writable application schema prevent untrusted persistent
objects from replacing application relations. None of these functions uses
`SECURITY DEFINER`.

The `serve` compatibility role is left untouched. API and worker
`deliveryHold=true` require an already-established durable operator hold; these
roles cannot set or release that hold. Execute recovery/hold commands with a
separately reviewed operator mutation identity. No grant here bypasses application
account, alias, namespace, rule, source, device, lease or recovery validation.
SQL privileges separate process capabilities; they do not implement per-user row
security inside a compromised authorized process.

## Granted capabilities

| Profile | Writes | Deliberately excluded |
| --- | --- | --- |
| API | Verified account insert/display name, alias insert; login/session/preferences/cursors; source pins; personal rule/device lifecycle; inbox read state; report admission | Directory/account binding edits, account enable/disable, event publication, delivery/provider state, report worker state, holds |
| Ingestion | Source pins; feed lease/checkpoint/error/gap admission; original events and replay-retention refresh | Accounts/aliases/auth, rules/devices/inbox, event processed state, delivery |
| Notifications | Source pins; rule activation versions/work; revocation denial; fanout/evaluation, event completion, inbox and delivery enqueue, pending quotas | Identity/auth/preferences edits, device settings/token replacement, provider attempts, holds |
| Delivery | Source pins; current-denial revocations; delivery leases/attempts/provider backoff; exact-token invalidation and installation revision | Identity/auth/preferences edits, device binding/settings/token replacement, installation proof hash, inbox read state, holds |
| Reports | Source pins; report task lease/state/object/failure | Identity/auth, requester alias changes, report admission, rule/device/event mutation |
| Retention | Bounded auth/cursor/report/inbox/history/event cleanup, activation retirement, delivery expiry and quota/progress | Identity/alias/settings/session edits, hold/source authority changes, new work |
| Operator | None | Row locks, credentials, account identity, event payloads, device tokens, mutations |

Detailed column sets are authoritative in `grants.json`. Notifications inspect
whether encrypted device tokens exist but have no decryption key; installation
and offline-revocation proof hashes are excluded. Delivery receives encrypted
tokens and its purpose-separated read key but cannot replace them. Trigger
functions run with invoker rights; retention's supporting reads and explicit
retention function execution are included. Audit inserts use PostgreSQL generated
identity defaults, which require no direct sequence privilege.

## Migration and lock-only columns

PostgreSQL requires UPDATE on at least one column for row-locking SELECT clauses.
Schema18 adds `runtime_lock boolean GENERATED ALWAYS AS (true) STORED` to the15
tables with genuine lock-only callers. `UPDATE(runtime_lock)` permits locking
without permission to change real data. Setting any non-DEFAULT value is rejected;
DEFAULT leaves the constant unchanged. Existing history/hold triggers may reject
even that no-op. No trigger on these tables refreshes authorization, expiry or
source timestamps. Activation rows deliberately have no generated guard because
their retirement trigger compares complete pre-generated `NEW` records.

PostgreSQL17 rewrites a table when adding this stored generated column. Apply18
with API/workers stopped or during an approved maintenance window; size the time,
WAL and disk headroom using a restored copy at agreed scale. This implementation's
small disposable-schema tests do **not** establish production migration duration.
Existing mutable grants continue to work and legacy ciphertext/identity rows are
not transformed. See PostgreSQL's [privileges](https://www.postgresql.org/docs/17/ddl-priv.html)
and [UPDATE](https://www.postgresql.org/docs/17/sql-update.html) documentation.

## Validation

Run `python3 deploy/postgres/test_grants.py` for renderer input and bundle checks.
`TestRuntimeRoles*` uses actual random password-authenticated login roles and a
random disposable schema. It covers API authentication/state, rule/device/report
admission, event ingestion, activation/evaluation, delivery and token invalidation,
report completion, status reads and actual retention trigger paths. It also tests
protected writes, operator row-lock denial, all15 generated guard defaults and
non-default rejection, stale column-grant removal, future-column denial and the immutable migration
ledger. Ordinary DB tests skip these role tests unless their supplied test login
is a superuser; the normal suite never elevates its own identity.

For the explicitly authorized synthetic Lab, run
`python3 scripts/test-lab-postgres-roles.py`. It reads only the known synthetic
bootstrap key, suppresses credentials and connection URLs, and creates/removes
random roles and schemas. It does not change current runtime grants, HBA, live
schema or PostgreSQL settings. CI's disposable PostgreSQL superuser runs the same
tests through the ordinary backend suite. Deployment under these roles and
production-sized migration timing remain separate acceptance steps.
