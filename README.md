# Jobman Dashboard

Private web and native iPhone monitoring for Jobman Control. **Under implementation; not a supported release or production deployment.** The full scope includes source-qualified monitoring, direct AD role unions, arrays/collections/graphs, safe logs, deterministic diagnosis, and background notifications.

See [requirements](docs/REQUIREMENTS.md), [design](docs/DESIGN.md), and the live [implementation status](docs/IMPLEMENTATION_STATUS.md) for coverage and remaining acceptance gates.

## Development

Use Go **1.26.6** (`go.version`), Node **26.5.1** and the committed npm lockfile. Full Xcode is required for the iPhone simulator and physical-device builds; the native core is also a Swift package.

```sh
make web
make dev
```

Open `http://127.0.0.1:8088`. This explicitly starts a **synthetic fixture environment**, restricted to a loopback IP. Fixture identities and data are not organization authentication. The server does not start in production mode until the real configuration/authentication integration is delivered. The current backend implements bootstrap, overview, jobs, and job detail; other implemented client screens report unavailable while their services are being integrated.

For live web editing, run the fixture backend plus `npm run dev --prefix web`; the Vite configuration proxies same-origin API calls. Never expose the fixture server on a public or organization interface.

```sh
make contracts-check
make check
make ios-core
make ios-simulator
```

Backend tests exercise cross-source paging, account/query/grant/epoch isolation, explicit partial results, schema migration integrity and optimistic preference updates. Database integration tests need `JOBMAN_DASHBOARD_TEST_DATABASE_URL`; each creates and removes its own random schema. With the authorized local Jobman-Lab PostgreSQL VM running, `python3 scripts/test-lab-postgres.py` loads only the synthetic database credential and runs those tests without printing its value.

No command above deploys production, contacts an AI provider, submits jobs, or publishes a release. Internal iPhone signing/distribution and real AD FS/APNs/managed-device acceptance remain release gates.
