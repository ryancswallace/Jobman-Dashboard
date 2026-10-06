# Repository scaffolding parity

Dashboard follows the shared Jobman, Jobman-Control and Jobman-Diagnose repository
baseline, adapted to Go services, React and native iPhone delivery. This comparison
was made against the sibling checkouts on 2026-10-05. Existing acceptance records
remain authoritative; scaffolding is not a new production-readiness claim.

| Sibling infrastructure | Dashboard counterpart |
| --- | --- |
| Editor/Git/ignore conventions | `.editorconfig`, `.gitattributes`, `.gitignore`, allowlisted Docker contexts |
| Agent/contributor/community policies | `AGENTS.md`, `CONTRIBUTING.md`, conduct, security, support, citation, MIT license, notices, changelog |
| Pinned tools and version maintenance | Root language version files, `make setup/tools/versions`, explicit reviewed upgrades |
| Development environment | Locked Go/Node devcontainer, editor extensions, synthetic PostgreSQL Compose and `.env.example` |
| Static analysis | Correctness-focused golangci-lint, TypeScript checks, formatting, actionlint, ShellCheck, vulnerability audits |
| Unit/integration/coverage/fuzz | Existing race/web/native/PostgreSQL suites, coverage floor/profile, bounded browse-state fuzzing |
| Generated contract checks | Existing OpenAPI to TS/Swift generator and boundary suites, repeatable native project generation |
| Documentation checks | Sibling-derived local-link checker, pinned spelling, scheduled/manual HTTPS link checker |
| GitHub issue/PR/community metadata | Templates, CODEOWNERS, labels/labeler, Dependabot for Go/npm/Actions/images/features |
| Repository protection template | `.github/settings.yml`, matching real check names; not applied by tooling |
| Security and maintenance workflows | Go/JS CodeQL, dependency review, Scorecard, scheduled invariants/vulnerability checks |
| Runtime containers | Non-root source build with both services and web assets, read-only smoke checks |
| Release builds/native packages | Existing canonical candidate builder plus explicitly local GoReleaser Linux snapshots and package layout checks |
| Checksums/dependency inventories | Candidate SHA-256/provenance; local snapshot checksums and Syft SPDX inventories |
| Installation/configuration/service examples | Existing `deploy/` role-specific JSON, systemd and PostgreSQL grants; contributor/operator guide index |
| Release and operations documentation | Root release procedure, compatibility, upgrade, troubleshooting and existing detailed operational runbooks |

## Intentional differences and promotion work

- Cobra completion/man-page generators and Core's command-reference Pages site do
  not match Dashboard's browser/native UI. The checked-in documentation index and
  OpenAPI are canonical; no independently hosted Pages site is configured.
- Core process/soak/dogfood utilities and Diagnose provider evaluation/captures stay
  in those products. Dashboard's synthetic Lab workload/scale/fault/restore suites
  already cover its integration tier and remain explicit opt-in operations.
- Native macOS/Xcode checks remain separate from Linux tooling. Swift has no external
  package graph today; add Swift dependency maintenance when one is introduced.
- Go style/quick-fix rewrites are not part of this baseline. Correctness analyzers,
  unused-code checks, race tests and formatting gate the current implementation.
- Linux amd64/arm64 are service release targets. Native macOS/Windows service
  packages are not implied by the siblings' CLI support.
- The committed-source candidate builder remains canonical. GoReleaser snapshots
  remain separate. Controlled RC distribution packages the canonical bytes and
  assembles its published image with `deploy/Dockerfile.release`; the root
  Dockerfile still supports local source builds.
- The main-only release workflow publishes verified engineering RC assets,
  versioned GHCR images, SPDX inventories and GitHub attestations. A separate
  Cloudsmith workflow requires a configured non-stable repository and upload key.
  Automatic semantic version publication, stable/latest promotion, Homebrew and
  Apple distribution remain gated. Failed partial releases retain their version;
  repairs use a new candidate rather than replacing artifacts. See
  [release engineering](../RELEASE.md) and [distribution](DISTRIBUTION.md).
- Documentation is organized around the existing `deploy/` and `scripts/` paths,
  rather than duplicating files under sibling `etc/`/`devel/updates/` paths. There
  is no generic cleanup that could erase developer evidence or configuration.

Revisit these differences when final release distribution is selected or a new
product surface makes the sibling tooling applicable. Do not copy release claims,
secrets, provider behavior or execution authority merely to make file lists equal.
