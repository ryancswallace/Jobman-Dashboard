# Linux candidate packages and service installation

Status: the engineering candidate is published and its split services pass actual
Lab installation/restart checks. The first release's integrated gates are still
open; a candidate is not production acceptance. Production installation,
AD/AD FS changes and distribution require the approvals in the adopted
implementation prompt. The isolated synthetic Lab has separate testing authority.

Each service can expose its own private operator socket using
[process observability](PROCESS_OBSERVABILITY.md). The systemd templates create
separate mode0700 runtime directories. Configure an explicit socket path inside
the corresponding directory; metrics never share the browser listener.

See [final candidate gates](FINAL_CANDIDATE.md) for the exact-source packaging
sequence, compatible upstream tags and unsigned native provenance. The bundled
[run-selection contract](RUN_SELECTION.md) and [release gap audit](RELEASE_GAP_AUDIT.md)
retain the behavior and acceptance boundaries used for review.

Versioned DEB/RPM/APK packages and GHCR images are described in
[candidate distribution](DISTRIBUTION.md). Those release packages contain the same
canonical archive bytes; local GoReleaser snapshots remain a separate facility.

## Build and verify an exact candidate

Use a clean committed checkout, the exact versions in `go.version`, `node.version`
and `npm.version`, Python3, and the checked-in module/npm locks:

```sh
python3 scripts/build-release.py --version v0.1.0-rc.1 --output-directory /absolute/new/output
```

This builds Linux amd64 and arm64 bundles. Select a single architecture with
`--architecture arm64`. The output directory must not exist. The tool makes a
fresh archive of the committed source, disables Go workspace/replacements and
builds both executables plus the private web assets there. npm lifecycle scripts
are disabled during dependency installation. No developer `node_modules`, local
config, ignored file, credential or `.git` directory is copied. Build diagnostics
use only committed bytes. Internal source file aliases (including the native
generated contract) are materialized from regular entries in that same archive;
external links, link chains and filesystem link traversal are rejected. Diagnostics
refer to public source/dependencies only. A failed build retains partial output;
inspect it and retry into a new directory. No tag, upload, service or installation
is created by this command.

Every bundle includes the web assets, public configuration examples, systemd
templates, explicit PostgreSQL grant tooling, operations documentation,
`build.json`, and a per-file `SHA256SUMS`. The outer output contains archive
checksums. `build.json` records the full Git revision, pinned toolchains,
architecture, Go module versions/checksums and npm lockfile digest. Archive order,
permissions, ownership and timestamps are normalized; Go uses `-trimpath` and a
fixed committed source timestamp. Rebuilding twice is a required release gate,
not an assumption made by the packager.

The build environment permits only tool lookup and verified Go cache locations
from the caller, disables persisted Go configuration, fixes amd64v1/arm64v8.0,
clears experiments and Node preloads, and uses fresh empty npm configuration.
Dependencies come from the public checksum-verified module/npm registries. Build
steps must leave the committed lockfiles unchanged. Corporate mirror support
requires an explicit reviewed configuration, not an ambient registry override.

The existing CI workflow accepts an optional `candidateVersion` on manual
dispatch. Its candidate job waits for backend/web/PostgreSQL and iPhone checks,
builds both architectures twice and compares archive checksums before retaining
the packages as a30day CI artifact. It does not tag or publish a GitHub release:

```sh
gh workflow run ci.yml --ref <reviewed-branch-or-commit> -f candidateVersion=v0.1.0-rc.1
```

Validate the publisher's outer checksum file, extract the intended bundle into
an empty staging directory as an ordinary user, then check its internal hashes:

```sh
sha256sum -c SHA256SUMS
cd jobman-dashboard_v0.1.0-rc.1_linux_arm64
sha256sum -c SHA256SUMS
./bin/jobman-dashboard version
./bin/jobman-log-broker version
```

Execute binaries only on their matching architecture. `version` opens no runtime
configuration or network connection. A candidate manifest records development
dependency pseudo-versions accurately; compatible released upstream tags remain
a separate final-release gate. Never replace a published candidate in place.

## Provision identities and storage

Use an organization-approved supported Linux host with systemd, private DNS/time,
verified TLS and PostgreSQL17. The first topology uses one API plus an explicit
worker bundle. The broker runs where approved logs are locally mounted. Containers
are optional; these service packages support host UID and filesystem ACL isolation.

Provision separate unprivileged service identities:

| Identity | Access |
| --- | --- |
| `jobman-dashboard-api` | Its own TLS/OIDC/session/source/broker keys; device encryption and report policy keys when enabled; report read group only |
| `jobman-dashboard-worker` | Selected components' source/broker/purpose/provider keys; report writer root when reports/retention selected |
| `jobman-dashboard-report-readers` | Dedicated report read/traverse group; no unrelated users |
| `jobman-log-broker` | Its own mTLS/delegation keys, private source-identity ledger, designated log-reader ACLs |
| Separate database/operator identities | DDL, API, each selected worker component or bundle, read-only operational status; no shared runtime superuser |

The supplied worker unit is a bundle. For independent component credentials,
copy the template into separately named units/users/config directories and select
only that component. Report retention must use the same pinned report writer UID
or a separately designed storage service; group membership does not confer write
authority. Do not add the API to the worker's primary group.

Preprovision `/var/lib/jobman-dashboard/reports` only when reports are enabled,
on supported local Linux storage, owned by the worker UID and dedicated reader
GID with mode0750 and no access/default ACL. Configure the exact numeric IDs in
`reports.objectAccess` for API and workers. Objects are immutable paired files
mode0640. Existing0700/0600 stores need a separately reviewed migration with
workers stopped, backup/checksum evidence and explicit ownership conversion;
startup never recursively changes them. See [process boundaries](PROCESS_MODES.md)
and [diagnostic storage](DIAGNOSIS_STORAGE.md).

Provision `/etc/jobman-dashboard-api`, `/etc/jobman-dashboard-worker`, and
`/etc/jobman-log-broker` separately, owned by the relevant service with mode0700;
private files must be mode0600. Root-managed secret mechanisms may instead provide
equivalent files through service credentials. Examples contain placeholders and
cannot be installed unchanged. Keep public trust anchors readable only where
needed and private material outside every web root. Never place secrets in unit
arguments, environment values, Git, package archives or diagnostic output.

Before splitting an existing installation, preserve all key IDs and encrypted
data with the reviewed [purpose-key migration](PURPOSE_KEYS.md). Workers must not
receive the browser session root. API mode rejects APNs signing configuration.

## PostgreSQL and source grants

Provision a private TLS-only database and separate DDL owner. Create ordinary
LOGIN, NOINHERIT runtime roles with no database/schema ownership, memberships,
superuser, CREATEDB, CREATEROLE, REPLICATION or BYPASSRLS. Provision their login
secrets through the approved secret manager. Runtime grants do not create roles,
passwords or HBA rules. Remove inappropriate PUBLIC schema/table privileges as a
separate DBA operation before applying the reviewed role plans.

Run forward migrations with the DDL credential and a validated public API-shaped
configuration. That mode does not read the API's other credentials:

```sh
/opt/jobman-dashboard/current/bin/jobman-dashboard --mode migrate --config /etc/jobman-dashboard-api/config.json --migration-database-url-file /private/operator/dashboard-ddl-url
```

Render exact grants from the package, review the SQL, and apply using the DBA's
private libpq service/password files. Never pass a password-bearing DSN on the
command line:

```sh
python3 deploy/postgres/grants.py --schema public --role dashboard_api --component api > /private/operator/dashboard-api-grants.sql
PGSERVICEFILE=/private/operator/pg_service.conf psql 'service=dashboard_ddl' -X -v ON_ERROR_STOP=1 -f /private/operator/dashboard-api-grants.sql
```

Use the grant tool's `--help` and [grant runbook](../deploy/postgres/README.md) for worker
bundles, rejected elevated roles and the read-only operator profile. Runtime roles
cannot apply migrations, alter identities outside their functions, or release a
delivery hold. Grant changes and new columns must ship together with their
reviewed schema version; never broaden runtime grants to work around a failure.

Register each source identity at Control and each caller at the broker with the
documented operation/namespace allowlists. Separate certificates and signing keys
permit independent revocation. Keep stable deployment UUIDs, expected Control
instances, audiences and configuration revisions aligned. An endpoint change is
not permission to substitute a different Control store.

## Install and start

Copy a verified immutable bundle under `/opt/jobman-dashboard/releases/<version>`
owned by root and unwritable by service users. Point `current` at that version
through a prepared symlink and atomic rename. Preserve the previous exact bundle
and manifest. Do not unpack over the active release or a private configuration
directory. Install the matching systemd templates only after their paths, users
and mount policy are reviewed; no package script starts a service automatically.

The templates use `ProtectSystem=strict`, private temporary directories,
`NoNewPrivileges`, no capabilities, bounded descriptors/tasks and a90second stop
budget. The API has a read-only report mount; the worker has only that writable
data path; the broker has only its private state directory. Adjust mount paths to
the actual approved log/report roots. `ProtectHome=true` intentionally prevents
reading home-mounted logs until a specific reviewed mount policy permits them.
Private TLS ports must be unprivileged because no bind capability is granted.

When observability is configured, standalone validation needs the socket parent
to exist before systemd has created its `RuntimeDirectory`. Provision only the
configured parent directories with the exact service owner/group and mode0700;
for the supplied units these are `/run/jobman-dashboard-api`,
`/run/jobman-dashboard-worker`, and `/run/jobman-log-broker`. Verify existing
directories and reject an unexpected owner, mode or symlink. Do not recursively
change permissions on an existing tree. Systemd maintains these directories on
subsequent service starts.

Validate as each service identity before enabling units:

```sh
sudo -u jobman-dashboard-api /opt/jobman-dashboard/current/bin/jobman-dashboard --mode check-config --check-mode api --config /etc/jobman-dashboard-api/config.json
sudo -u jobman-dashboard-worker /opt/jobman-dashboard/current/bin/jobman-dashboard --mode check-config --check-mode worker --config /etc/jobman-dashboard-worker/config.json
sudo -u jobman-log-broker /opt/jobman-dashboard/current/bin/jobman-log-broker --mode check-config --config /etc/jobman-log-broker/config.json
```

`check-config` validates local structure/key material; it does not prove directory,
database, source, APNs or storage authorization. Start the API first to register
the configured source pins, then the selected workers and broker. Verify actual
authenticated reads, report enqueue/completion, inbox behavior and restricted
operator status before enabling restart/boot policy. Delivery is enabled only
when its provider credentials and approved egress are available.

The API serves its own verified HTTPS endpoint. An organization-managed reverse
proxy may front it, preserving the configured public origin, same-origin browser
calls and verified upstream TLS. Size request-line/header limits for the documented
320-namespace selection (96KiB query/128KiB header budget), and retain bounded
timeouts. No public source endpoint, CORS workaround or CDN is required.

## Upgrade, rollback and recovery

1. Record exact manifests, source pins, key IDs, config revisions, schema ledger
   and previous service states. Back up PostgreSQL, paired report files,
   configuration and recoverable keys together through the approved private
   system. Do not copy credentials into the operational evidence record.
2. Validate new packages/configuration and prepare service paths/grants. Review
   migration locking/rewrite requirements and schedule the bounded maintenance
   window. Stop workers and API before applying unsupported schema changes.
3. Apply forward migrations only with the DDL identity, then role grants. Start
   the new API/workers and verify liveness, readiness, authorized workflows and
   delivery state. Retain failure evidence and the previous package.
4. A binary rollback is safe only if its schema compatibility check accepts the
   current ledger. Never bypass that check or edit the ledger to downgrade.
   Otherwise restore the coordinated database/object/config/key backup in the
   approved recovery window, using the compatible previous binaries.
5. A restored database starts with delivery held. Reconcile exact source
   checkpoints, retained events and ambiguous provider attempts through
   [event recovery](EVENT_RECOVERY.md). Startup must not clear the hold, and a
   restored backlog must not be sent blindly. Verify source authorization and
   immutable report/evidence pairs before explicitly releasing it.

The proposed RPO15minutes/RTO4hours still require an operating owner and measured
restore acceptance. A package build alone proves neither. Actual AD FS, APNs,
company-managed iPhone distribution, agreed load and the remaining T01–T12 gates
are tracked in [implementation status](IMPLEMENTATION_STATUS.md).
