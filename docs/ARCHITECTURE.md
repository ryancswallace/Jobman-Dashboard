# Implemented architecture and repository map

Dashboard combines Go API/worker/log-broker processes, PostgreSQL state, a React
web client and a native SwiftUI iPhone client. Control remains authoritative for
source metadata and namespace access; Jobman owns execution; Diagnose supplies
deterministic interpretation. The full target architecture is in [DESIGN.md](DESIGN.md);
implementation and acceptance coverage are in [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md).

| Area | Source and responsibility |
| --- | --- |
| Runtime entry points | `cmd/jobman-dashboard`, `cmd/jobman-log-broker` |
| Identity/delegation | `internal/auth`, `internal/runtimeconfig` |
| Multi-source monitoring | `internal/control`, `internal/monitoring`, `internal/httpapi` |
| Durable state | `internal/store`, forward-only SQL migrations |
| Logs and diagnosis | `internal/logs`, `internal/reports` |
| Events and alerts | `internal/events`, `internal/notifications`, `internal/push` |
| Operational status | `internal/observability`, `internal/operations` |
| Web | `web/src`, generated TypeScript contracts |
| iPhone | `ios/App`, `ios/Sources/DashboardCore`, generated Swift contracts |
| Contract source | `api/openapi.json`, `scripts/generate-contracts.py` |
| Installation examples | `deploy/`, role-specific systemd units and PostgreSQL grants |
| Contributor tooling | `Makefile`, `devel/`, `.devcontainer/`, `.github/` |

Use [process modes](PROCESS_MODES.md) for API/worker/broker separation and
[security](SECURITY_MODEL.md) for authority boundaries. No browser or iPhone
receives PostgreSQL, AD service, broker or APNs provider credentials.
