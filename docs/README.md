# Dashboard documentation

Start with [installation](INSTALLATION.md), [development](DEVELOPMENT.md),
[configuration](CONFIGURATION.md), [testing](TESTING.md) and
[operations](OPERATIONS.md). [Architecture](ARCHITECTURE.md), [API](API.md),
[security](SECURITY_MODEL.md) and [compatibility](COMPATIBILITY.md) describe the
implemented boundaries. See [repository parity](REPOSITORY_SCAFFOLDING.md) for
shared infrastructure and intentional differences.

The [requirements](REQUIREMENTS.md) and [design](DESIGN.md) specify the full target;
[implementation status](IMPLEMENTATION_STATUS.md), [release handoff](RELEASE_HANDOFF.md)
and [candidate evidence](FINAL_CANDIDATE.md) record what has been verified.
Historical Lab records are version-specific evidence, not instructions to rerun
old destructive scenarios. Use the [Lab catalog](LAB_RUN_CATALOG.md).

## Complete guide and evidence index

- [Dashboard wording and terminology](UI_LANGUAGE.md)
- [Client accessibility acceptance](ACCESSIBILITY.md)
- [API and client contracts](API.md)
- [APNs provider transport](APNS_PROVIDER.md)
- [Implemented architecture and repository map](ARCHITECTURE.md)
- [Artifact metadata](ARTIFACT_METADATA.md)
- [Authentication and private configuration](AUTHENTICATION.md)
- [Compatibility](COMPATIBILITY.md)
- [Configuration](CONFIGURATION.md)
- [Containers](CONTAINERS.md)
- [Bounded dependency-failure acceptance](DEPENDENCY_FAILURE_ACCEPTANCE.md)
- [Jobman Dashboard first-release design](DESIGN.md)
- [Development](DEVELOPMENT.md)
- [Native binding and offline sign-out protocol](DEVICE_REVOCATION.md)
- [Deterministic diagnosis collection and persistence](DIAGNOSIS_STORAGE.md)
- [Durable event ingestion](EVENT_INGESTION.md)
- [Event gaps, delivery holds and recovery](EVENT_RECOVERY.md)
- [Control event source adapter](EVENT_SOURCE.md)
- [Final candidate packaging and release gates](FINAL_CANDIDATE.md)
- [Graph client acceptance at the supported ceiling](GRAPH_CLIENT_ACCEPTANCE.md)
- [IMPLEMENTATION_PROMPT](IMPLEMENTATION_PROMPT.md)
- [Initial release implementation status](IMPLEMENTATION_STATUS.md)
- [Authorized notification history](INBOX.md)
- [Installation](INSTALLATION.md)
- [Job details and submitted commands](JOB_DETAILS.md)
- [API authentication-master rotation acceptance](LAB_AUTH_ROTATION.md)
- [Actual subprocess acceptance](LAB_EXECUTION.md)
- [Deployed mixed monitoring load](LAB_MIXED_LOAD.md)
- [Two-Control notification acceptance](LAB_MULTISOURCE_NOTIFICATIONS.md)
- [Deployed notification acceptance](LAB_NOTIFICATIONS.md)
- [Refresh retained acceptance reports after a source upgrade](LAB_REPORT_REFRESH.md)
- [Isolated deployed backup and restore acceptance](LAB_RESTORE.md)
- [Read-only actual run catalog acceptance](LAB_RUN_CATALOG.md)
- [Deployed accepted-scale validation](LAB_SCALE.md)
- [Packaged API and worker acceptance](LAB_SPLIT.md)
- [Synthetic Lab web-session protocol acceptance](LAB_WEB_SESSION.md)
- [Linux candidate packages and service installation](LINUX_INSTALLATION.md)
- [Storage-local log broker](LOG_BROKER.md)
- [Durable notification processing](NOTIFICATION_PIPELINE.md)
- [Notification retention](NOTIFICATION_RETENTION.md)
- [Personal alert rules](NOTIFICATION_RULES.md)
- [Private deployment operations](OPERATIONS.md)
- [Restricted operator status](OPERATOR_STATUS.md)
- [API and worker process boundaries](PROCESS_MODES.md)
- [Private process observability](PROCESS_OBSERVABILITY.md)
- [Separate process keys](PURPOSE_KEYS.md)
- [Remaining product work found in the release audit](RELEASE_GAP_AUDIT.md)
- [Jobman Dashboard initial-release handoff](RELEASE_HANDOFF.md)
- [Repository scaffolding parity](REPOSITORY_SCAFFOLDING.md)
- [Jobman Dashboard initial requirements](REQUIREMENTS.md)
- [Selecting recorded job runs](RUN_SELECTION.md)
- [Security model](SECURITY_MODEL.md)
- [Target monitoring](TARGETS.md)
- [Testing](TESTING.md)
- [Troubleshooting](TROUBLESHOOTING.md)
- [Upgrading and recovery](UPGRADING.md)
- [Watchdog acceptance denial contract](WATCHDOG_DENIAL_CONTRACT.md)

- [Candidate distribution](DISTRIBUTION.md): attested Linux packages, containers, personal API-key setup for the shared Cloudsmith repository, RC10 publication evidence and versioned installation.
