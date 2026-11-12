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

The first full release was `v0.1.0`, with Core `v1.10.1` and Diagnose
`v0.7.0-rc.3` embedded and Control `v0.3.0` selected separately. See the
[release record](RELEASE_HANDOFF.md) for stable releases and verification. Earlier RC12 and RC11 artifacts remain immutable.
Full-release manifests record `releaseState: release`, the scope document digest
and `finalReleaseEligible: true`. This is scoped product acceptance; compiled
dependency stability and deployed Control interoperability are recorded
separately and retain their actual values.

Published tests and receipts retain their actual source, environment, results,
and limitations. Ryan's completed review and acceptance of reduced scope are a
product decision, not fabricated evidence that AD FS, APNs, managed devices, or
other untested integrations passed.

## Validation scope

The first release includes deterministic diagnosis and the implemented web and
native monitoring workflows. The iPhone archive is unsigned. Test receipts
identify the versions, environments and integrations exercised. See the
[implementation record](IMPLEMENTATION_STATUS.md) for the validation history and
[compatibility guide](COMPATIBILITY.md) for the released dependency pins.

The [installation guide](LINUX_INSTALLATION.md) and
[native packaging guide](../ios/README.md) cover deployment prerequisites.
