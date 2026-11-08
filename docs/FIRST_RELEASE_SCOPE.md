# First production release scope

Decision date: November 8, 2026. Decision owner: Ryan Wallace.

Product review: **complete**

Ryan approved the implemented Dashboard baseline for the first production
release, including its known limitations. Agent reliability is the corrective
work included in this release. All other unfinished implementation, integration,
acceptance, and operational expansion moves to releases after the first
production release. Those follow-ups are not conditions for this release and
must not be presented as outstanding first-release review or acceptance work.

This decision supersedes the broader completion criteria in the original
requirements, design, implementation prompt, gap audit, and release handoffs.
The feature requirements remain the product roadmap; the implemented subset is
the first-release scope. Existing authentication, authorization, TLS, data
isolation, and applicable automated quality checks remain in force. Unavailable
integrations fail closed or display unavailable; accepting a limitation does
not authorize weakening a security control.

## Included baseline and evidence

The baseline includes the implemented web and native iPhone monitoring clients,
source-qualified jobs and groups, logs, deterministic reports, notification
rules/history, and the Linux packaging/container tooling. Agent renewal and
expired-credential recovery are addressed through the associated reliability
change; its exact revisions and verification belong in the implementation
record, not an inferred claim that every historical agent build is fixed.

The latest published artifact is still
[RC11](https://github.com/ryancswallace/Jobman-Dashboard/releases/tag/v0.1.0-rc.11).
This scope decision does not rename that release, rewrite its immutable bytes,
or claim a stable release has already been published. Candidate metadata such as
`finalReleaseEligible: false` records the candidate builder's original semantics;
it is not a renewed requirement for the postponed acceptance work.

Published tests and receipts retain their actual source, environment, results,
and limitations. Ryan's completed review and acceptance of reduced scope are a
product decision, not fabricated evidence that AD FS, APNs, managed devices, or
other untested integrations passed.

## Post-release follow-up

| Area | Later-release work | First-release limitation |
| --- | --- | --- |
| Corporate identity | Actual AD FS/direct AD configuration and interoperability, direct-group mapping and revocation acceptance in the organization. | Corporate sign-in and directory-backed access require valid configuration and are not established by synthetic Keycloak tests. |
| Apple push | APNs ownership, credentials, connectivity, and physical-device background-delivery acceptance. | Background push may be unavailable; implemented rule processing/inbox behavior does not prove Apple delivery. |
| Managed iPhone delivery | Internal channel/MDM selection, signing/export, managed install/upgrade/removal and device acceptance. | The published native archive is unsigned and cannot be installed directly. A personal development build is separate from managed distribution. |
| Diagnose | Live-provider acceptance, compatible stable publication and subsequent dependency updates. | The baseline uses the recorded compatible Diagnose candidate. Dashboard deterministic reports do not establish optional provider acceptance. |
| Deployment configuration | Finish and deliver the unreleased configuration/schema, organization presentation, branding and native-profile work across the repositories. | Those local changes are outside the published baseline and are not silently included in release artifacts. |
| Accessibility and pilot | Additional manual browser/iPhone accessibility coverage, organization pilot and deployment-specific performance validation. | Existing automated and synthetic results apply only to their recorded scope. |
| Operations | Additional private-hosting allocation, organization DNS/TLS/VPN/NFS and secrets setup, monitoring, backup/recovery exercises and operating ownership. | A deployment still needs the concrete configuration and credentials for each enabled service; absent integrations remain unavailable. |
| Other unfinished work | Any implementation, validation, packaging expansion or integration not already in the baseline, except the agent reliability change. | No unimplemented or unverified behavior is implied by the first-production-release label. |

Existing platform permission requirements still apply when an actual production,
identity, network, Apple-account or distribution action is performed. Review is
not reopened merely because a postponed component is unavailable. Operational
instructions describe how to enable a component when its later work is scheduled;
they do not create a new first-release acceptance checklist.
