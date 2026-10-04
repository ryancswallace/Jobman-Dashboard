# Private process observability

Configured API, combined `serve`, worker, and log-broker processes can each expose a separate operator Unix socket. The socket observes the process that owns it. It does not report another API or worker as healthy, and it has no route in the browser-facing application.

Add this optional object to that process's configuration:

```json
"observability": {
  "socketPath": "/run/jobman-dashboard-api/operator.sock",
  "operatorConfigFile": "/etc/jobman-dashboard/operator.json"
}
```

Provision the socket's parent directory as mode `0700`, owned by the process UID, on a local filesystem. The path must be absolute, canonical, at most 100 bytes, and have no symbolic-link parent aliases. The endpoint is mode `0600`. Startup refuses any existing path and does not remove another process's socket. Shutdown removes only its own unchanged socket inode. After an unclean shutdown, an operator must verify the owning process has stopped before removing its stale endpoint. Unsupported platforms fail closed when this option is enabled; omission preserves existing runtime behavior.

Every process needs a different socket directory. API/web configuration rejects the resolved socket directory, operator configuration, and the nested operator database credential inside or overlapping the static tree. Existing static confinement continues to apply. The socket is not a public TCP listener and must not be reverse-proxied to application users.

For example, as the service UID or a suitably authorized local operator:

```sh
curl --unix-socket /run/jobman-dashboard-api/operator.sock http://localhost/livez
curl --unix-socket /run/jobman-dashboard-api/operator.sock http://localhost/readyz
curl --unix-socket /run/jobman-dashboard-api/operator.sock http://localhost/metrics
```

Only GET without query parameters/body is accepted. Requests have fixed response/header/time bounds and four concurrent handler slots.

## Liveness and readiness

`/livez` means the process's private HTTP handler responds. It does not prove successful user authentication, source availability, or job execution.

`/readyz` remains unavailable until required local startup succeeds and becomes unavailable while draining. API and worker readiness checks their own runtime database connection and exact migration schema under a two-second total context. Concurrent probes coalesce; results are cached for one second. A broker reports completed local initialization and responsiveness only. Broker readiness performs no NFS read or filesystem probe that could block on an unavailable mount.

Control, directory, OIDC and APNs outages are separate observations. They do not directly fail process readiness or cause an operator to restart otherwise functioning workers. A source identity/schema incompatibility still fails the original authorization/runtime gate; instrumentation does not relax it.

## Metrics and bounds

`/metrics` exports Prometheus text from memory. Requests never initiate database or source reads. In-memory counters start at process launch and are not durable billing or audit records. Labels are fixed component/operation/result/mode values and configured deployment UUIDs (at most 32), plus bounded immutable build information. No account, user, namespace, job, event, token, cursor, private filename, provider-key identifier, arbitrary URL, or raw error appears.

Observed operations have counters, bounded latency buckets, last-observed timestamp/result, and byte counters where actual chunk bytes are available. Absence means no observation; it is not a zero-latency success. The registry has a hard series-key cap and reports dropped observations.

| Requirement | Observation and limits |
| --- | --- |
| API latency/error | Completed HTTP request families `api`, `auth`, `static`, `other`; fixed status classes. No individual route/job labels. |
| Source latency | Actual Control calls, configured deployment and fixed operation family, trusted interactive/worker/service mode. Transport errors and denials remain distinct fixed results. |
| Authorization freshness | Oldest verified directory proof, earliest expiry and observation timestamp from the last complete successful namespace discovery. This is proof age, **not measured AD membership-change to revocation lag**. |
| Compatibility | Observed source instance/contract mismatches and completed local ledger version/checksum mismatches (database outages remain unavailable); configuration revision and binary/Go/platform build metadata. |
| Logs | Broker range states, remote and storage-local chunk read latency/state/returned byte counts; includes checksum corruption, missing chunk, mapping, busy and timeout states. No log text. |
| Diagnosis | Claimed report task duration/result and private object publish/read/recover/remove/sweep operation results. Empty worker polls are not task failures. |
| Notification queues | Optional sampled stored event/fanout/evaluation/delivery/activation backlog, leases, age, hold and cutoffs; stored source checkpoint/backoff is explicitly a last observation. |
| APNs | Actual provider attempt result/latency and event-recorded to provider-acceptance latency. This does **not** prove OS delivery or phone presentation. Stored provider failures/backoff remain a separate aggregate. |
| Database pressure | Current process pgx pool occupancy and cumulative acquisition/wait statistics; no query on scrape. |
| Report pressure | Optional sampled pending/leased report tasks and oldest pending age within configured sources, plus the global 512 pending-task bound. This is queue pressure, not disk capacity. |
| Maintenance | Per-operation retention duration/result. Durable retention cutoffs remain in the stored operator projection. |

No filesystem free-byte/inode gauge is claimed. Report object errors and queue pressure are available, while host/storage capacity requires a host agent or an independently bounded local filesystem probe. Actual directory-change revocation lag and phone notification presentation require the deployment acceptance exercises. OIDC health is observed through authentication request outcomes; no synthetic sign-in or credential refresh is launched by the metrics endpoint.

## Optional stored-status sampler

`operatorConfigFile` is optional. It uses the strict [dedicated operator configuration](OPERATOR_STATUS.md) and a **separate read-only database identity**; the runtime role receives no additional grants. Its configured sources must be a subset of this process's source set. Never copy a DDL or runtime credential into this file.

The optional pool is limited to two connections, created lazily. Local malformed configuration/private files fail startup; an unavailable operator database does not prevent the process serving its normal purpose. Exactly one sample runs every 15 seconds under a five-second total context, with bounded read-only SQL statement and lock timeouts. It reads the existing status projection and report queue-pressure columns only. There are no source HTTP calls inside the sample or SQL transaction.

`jobman_dashboard_operator_snapshot_configured`, `..._available`, and `..._age_seconds` distinguish omitted observation, last-success freshness and current sample failure. A failed refresh marks the sample unavailable. Previous safe metrics remain for at most 60 seconds with their age and unavailable flag, then are omitted. Stored feed timestamps are never promoted to live source readiness; missing feed state remains explicitly uninitialized. JSON operator status remains available through the separate `status` CLI.

All observer hooks are optional and do not alter authorization, source epoch checks, current grants, file checksum validation, immutable report seals, lease fences, or delivery acknowledgements. Observer setters are startup-only and must be called before their component is shared with goroutines.
