# Agent instructions for Jobman Dashboard

## Product and repository boundaries

Dashboard is a private Go/PostgreSQL monitoring service, React web application,
and native SwiftUI iPhone application. Read `docs/REQUIREMENTS.md`,
`docs/DESIGN.md`, and `docs/IMPLEMENTATION_STATUS.md` before changing behavior.
Implemented code and verified evidence establish current behavior; the design
alone does not prove release readiness. Preserve the monitoring-only boundary.
Core owns execution, Control owns deployment authority and source metadata,
and Diagnose owns deterministic interpretation. No live AI provider belongs here.

## Working safely

- Inspect `git status` first; preserve unrelated tracked, untracked and ignored work.
- Never print or commit credentials, local configuration, AD data, raw logs,
  private reports, signing identities, APNs keys or developer-local environment files.
- Use synthetic fixtures. Live Lab work needs explicit session authorization;
  do not infer authorization to reset Lab state from permission to run unit tests.
- Follow the user's authorized scope for GitHub work. Otherwise do not commit,
  push, publish, deploy or change repository settings without an explicit request.
- Never bypass branch protection or mark real AD FS/APNs/device acceptance complete
  using simulator, Keycloak or mocked evidence. See `RELEASE.md`.

## Implementation

Keep handlers thin; authorize each request using source-qualified deployment and
namespace identity. Preserve direct-group permission unions and active-session
revocation. Treat logs, artifacts, graph labels, reports and notification data as
untrusted. Bound reads, pagination, queues, concurrency, subprocesses and retries.
Use context cancellation, contextual errors and private atomic file writes.
Do not hold database transactions across network/filesystem operations.
Forward-only committed migrations are immutable; add a new migration instead.

`api/openapi.json` is the contract source. Run `make contracts` and
`make contracts-check`; do not hand-edit generated TS/Swift files. Change the
Xcode generator before regenerating its project. Shared contract changes need
both web and native verification. Keep secret-bearing runtime configuration out
of examples and container build contexts.

## Tooling and verification

Exact toolchains live in `go.version`, `node.version`, and `npm.version`.
Pinned quality tools install into ignored `bin/` through `make setup`.
Use `make help` and `docs/DEVELOPMENT.md` for checks. Run focused checks during
iteration, then the applicable full gate. Do not disable checks to hide failures.
Go tests must be deterministic and race-safe; PostgreSQL tests require an
explicit disposable test database. Real Lab tests remain opt-in and separate.
Linux containers cannot establish native iPhone build or device acceptance.

Keep Actions SHA-pinned, permissions minimal, untrusted PR code outside privileged
jobs, scripts bounded and repeatable, and runtime images non-root. `make update`
regenerates contracts/project files; it does not upgrade versions or mutate Git.
Never run cleanup over unknown local data. Explain exact failures, skips and
unverified scope in the handoff.

## Documentation and release work

Update the relevant runbook, documentation index and changelog for user-visible
changes. Keep chronological acceptance evidence intact. Do not copy private Lab
outputs into public docs. `docs/REPOSITORY_SCAFFOLDING.md` records sibling parity.
`make snapshot` builds local engineering artifacts only; the canonical candidate
builder remains `scripts/build-release.py`. No automatic stable publication is
configured. Signing, production deployment, distribution, paid services and
external acceptance retain the approval boundaries in the adopted implementation
prompt. Do not apply the repository settings template merely because it exists.
