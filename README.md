# Jobman Dashboard

Private web and native iPhone monitoring for Jobman Control. **Engineering prerelease; integrated and external acceptance remains open.** The applications implement source-qualified monitoring, direct AD role unions, arrays/collections/graphs, safe logs, deterministic diagnosis, and notification rules/history. Actual AD FS, APNs and company-managed iPhone acceptance are required before the initial release is complete.

See [requirements](docs/REQUIREMENTS.md), [design](docs/DESIGN.md), and the live [implementation status](docs/IMPLEMENTATION_STATUS.md) for coverage and remaining acceptance gates.

The [engineering candidate](https://github.com/ryancswallace/Jobman-Dashboard/releases/tag/v0.1.0-rc.1)
contains reproducible Linux amd64/arm64 services and web assets. Follow
[private installation](docs/LINUX_INSTALLATION.md) and the
[operations runbook](docs/OPERATIONS.md); native signing/distribution is a separate
gate. Actual [subprocess](docs/LAB_EXECUTION.md) and [split-runtime](docs/LAB_SPLIT.md)
Lab evidence identifies the exact tested revisions and limitations.

## Development

Use Go **1.26.6** (`go.version`), Node **26.5.1** and the committed npm lockfile. Full Xcode is required for the iPhone simulator and physical-device builds; the native core is also a Swift package.

```sh
make web
make dev
```

Open `http://127.0.0.1:8088`. This explicitly starts a **synthetic fixture environment**, restricted to a loopback IP. Fixture identities and data are not organization authentication. Configured HTTPS mode provides OIDC authentication, durable web sessions, current-authorized Control adapters, logs, artifacts, targets, workload groups, diagnosis, notification rules/history and personal preferences; see [authentication and configuration](docs/AUTHENTICATION.md). Optional services require their explicit private configuration; an unavailable service is displayed as unavailable. Actual AD FS and corporate directory acceptance remain open.

For live web editing, run the fixture backend plus `npm run dev --prefix web`; the Vite configuration proxies same-origin API calls. Never expose the fixture server on a public or organization interface.

```sh
make contracts-check
make check
make ios-core
make ios-simulator
```

Backend tests exercise cross-source paging, account/query/grant/epoch isolation, explicit partial results, schema migration integrity and optimistic preference updates. Database integration tests need `JOBMAN_DASHBOARD_TEST_DATABASE_URL`; each creates and removes its own random schema. With the authorized local Jobman-Lab PostgreSQL VM running, `python3 scripts/test-lab-postgres.py` loads only the synthetic database credential and runs those tests without printing its value.

No command above deploys production, contacts an AI provider, submits jobs, or publishes a release. Internal iPhone signing/distribution and real AD FS/APNs/managed-device acceptance remain release gates.
