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

The first full release is selected as `v0.1.0`, with Core `v1.10.1` and Diagnose
`v0.7.0-rc.3` embedded and Control `v0.3.0` selected separately. The
[release page](https://github.com/ryancswallace/Jobman-Dashboard/releases)
records publication status. Earlier RC12 and RC11 artifacts remain immutable.
Full-release manifests record `releaseState: release`, the scope document digest
and `finalReleaseEligible: true`. This is scoped product acceptance; compiled
dependency stability and deployed Control interoperability are recorded
separately and retain their actual values.

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
| Deployment configuration | Select and validate actual organization values using the delivered schemas, presentation and native-profile tooling. | Structural validation does not establish organization approval or connectivity. |
| Accessibility and pilot | Additional manual browser/iPhone accessibility coverage, organization pilot and deployment-specific performance validation. | Existing automated and synthetic results apply only to their recorded scope. |
| Operations | Additional private-hosting allocation, organization DNS/TLS/VPN/NFS and secrets setup, monitoring, backup/recovery exercises and operating ownership. | A deployment still needs the concrete configuration and credentials for each enabled service; absent integrations remain unavailable. |
| Other unfinished work | Any implementation, validation, packaging expansion or integration not already in the baseline, except the agent reliability change. | No unimplemented or unverified behavior is implied by the first-production-release label. |

Existing platform permission requirements still apply when an actual production,
identity, network, Apple-account or distribution action is performed. Review is
not reopened merely because a postponed component is unavailable. Operational
instructions describe how to enable a component when its later work is scheduled;
they do not create a new first-release acceptance checklist.
