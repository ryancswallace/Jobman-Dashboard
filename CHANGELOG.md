# Changelog

Notable changes are recorded here. Historical engineering evidence remains in
[implementation status](docs/IMPLEMENTATION_STATUS.md) and
[the candidate record](docs/FINAL_CANDIDATE.md).

## Unreleased

- Document Cloudsmith personal API-key publishing for the free account, including
  GitHub environment setup and coordinated key rotation across Jobman publishers.

## v0.1.0-rc.10

Published [engineering prerelease](https://github.com/ryancswallace/Jobman-Dashboard/releases/tag/v0.1.0-rc.10)
from `5b20dfb7a51d206aeb7132db94d961a14b6ab03e`, with unsigned native version
`0.1.0`, build `12`. All 25 assets and 26 attestations verify; native Linux package
installations, both public container platforms and exact-package Lab activation/
authenticated checks pass. See [release handoff](docs/RELEASE_HANDOFF.md) for hashes
and evidence. RC10 browser rendering was not rerun; external Dashboard acceptance,
stable Diagnose provider gates and optional Cloudsmith setup remain open. RC8's
partial container push and RC9's verified unpublished draft remain immutable.

- Discover release drafts through the authenticated release list and allow
  read-only verification of complete draft assets before publication.

- Verify each container architecture by its own immutable manifest digest so
  release checks also work with Docker image stores that load one platform per digest.

- Add controlled candidate publication, versioned Linux packages and containers,
  package lifecycle verification, SPDX inventories and GitHub attestations.
  Pin the embedded Jobman collector to published v1.9.0 and Diagnose to its
  compatible v0.7.0-rc.1 candidate; stable Diagnose acceptance remains open.

- Clarify web and iPhone navigation, job and run details, alerts, reports, and
  recovery messages using consistent user-facing terminology.

- Show complete submitted commands and working directories in authorized web and
  iPhone job details, retain additional shared status metadata, and separate
  historical-run facts from current job state.
- Preserve target pagination with newer Control creation bounds, including stable
  multi-source pages when targets are added during browsing.

- Upgrade the web build to Vite 8 together with its required React plugin 6 peer,
  TypeScript 7, jest-dom 7 and jsdom 30, retaining the explicit ES2022 output target
  and static deployment model.

- Clarify the limited sample preview and add an opt-in smoke for the configured
  Lab's current monitoring, investigation and personal alert workflows.

- Use the supplied transparent Dashboard logo in the dark web header and native
  iPhone connection/Overview branding.

- Add the shared Jobman repository baseline: community policies, contributor and
  operator documentation, pinned quality tools, devcontainer and container builds,
  dependency/security/maintenance workflows, local snapshot packaging and checks.
- Preserve explicit candidate publication and real identity, APNs and managed-phone
  acceptance gates; repository scaffolding does not promote a stable release.

## v0.1.0-rc.7

Engineering candidate covering private web and native iPhone monitoring,
source-qualified multi-Control views, arrays/collections/graphs, bounded logs,
deterministic diagnosis, alert rules/history and split API/worker/broker operation.
See the [immutable candidate evidence](docs/FINAL_CANDIDATE.md) for source revisions,
validation, artifact provenance and remaining external acceptance. This entry is a
navigation summary, not a replacement for recorded release evidence.
