# Testing

| Gate | Command | What it establishes |
| --- | --- | --- |
| Fast portable | `make quick-check` | Go races, web tests, contracts, modules, formats, links, builds |
| Repository | `make repository-check` | Go correctness lint, TypeScript types, Actions, shell, spelling, packaging configuration |
| Full portable | `make check` | Both gates plus production web and Linux cross-builds |
| Database | `make integration-test` | Explicit PostgreSQL-backed storage tests |
| Coverage | `make coverage-check` | Atomic Go profile and aggregate 30 percent floor |
| Fuzz | `make fuzz` | Bounded cursor decoder with two workers |
| Security | `make vulncheck` | Reachable Go and locked npm vulnerability audits |
| Containers | `make docker-check docker-smoke` | Definition validity, non-root identity, metadata/assets, fail-closed startup |
| Packaging | `make snapshot sbom package-smoke` | Local archives/packages, checksum/content checks, SPDX and package layouts |
| Native | `make ios-core ios-simulator` | Swift package tests and unsigned simulator build on macOS |

For a disposable local PostgreSQL database:

```sh
docker compose up -d --wait postgres
export JOBMAN_DASHBOARD_TEST_DATABASE_URL='postgres://postgres:dashboard-synthetic-only@127.0.0.1:55432/dashboard_test?sslmode=disable'
make integration-test
```

These credentials are public synthetic fixtures. Tests create isolated schemas;
do not point them at production. `docker compose stop` preserves the test volume.
Removing a volume destroys its contents and is not a routine check. Database tests
skip when the environment variable is absent; CI supplies it explicitly and runs
coverage with PostgreSQL. `package-smoke` needs `dpkg-deb`, `rpm` and `tar`; Ubuntu
CI installs rpm explicitly. Native package checks inspect layouts without installing
services. GoReleaser may need network access to resolve release history.

Meaningful existing Lab acceptance is cataloged in [LAB_RUN_CATALOG.md](LAB_RUN_CATALOG.md).
Lab fault, restore and workload tests mutate synthetic state and remain separately
authorized, opt-in operations. None is part of generic repository maintenance.
Browser/native UI and physical-device checks have their own documented fixtures
and acceptance evidence; no local unit gate replaces real AD FS, APNs or managed
phone acceptance. Record failures and skips, not just a green aggregate.
