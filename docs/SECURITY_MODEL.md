# Security model

Dashboard is monitoring-only but processes sensitive cross-user workload data.
Authentication is selected AD FS/OIDC. Control verifies direct AD group membership
and source-qualified namespace authority. Multiple roles form a permission union;
revoked membership removes access. Every read, cursor continuation, report fetch
and notification must retain current authority checks.

| Boundary | Required property | Detailed contract |
| --- | --- | --- |
| Browser/iPhone to API | TLS, validated identity, protected sessions/tokens | [Authentication](AUTHENTICATION.md) |
| Dashboard to each Control | Distinct source/instance identity and delegated authority | [Purpose keys](PURPOSE_KEYS.md) |
| API/worker to database | Separate least-privilege roles, explicit migrations | [PostgreSQL grants](../deploy/postgres/README.md) |
| Broker to local/NFS logs | Allowlisted immutable metadata, bounded safe reads, ACL service identity | [Log broker](LOG_BROKER.md) |
| Diagnosis storage | Redaction, private objects, current access checks, bounded retention | [Diagnosis storage](DIAGNOSIS_STORAGE.md) |
| Events/notification delivery | Durable replay/deduplication and current recipient/device eligibility | [Pipeline](NOTIFICATION_PIPELINE.md) |
| Device registration/revocation | Account/device binding and durable revocation | [Device revocation](DEVICE_REVOCATION.md) |
| Build/release | Public source inputs, isolated secrets, explicit candidate status | [Release](../RELEASE.md) |

Synthetic fixture mode is loopback-only. It is never an organization identity
substitute. Private source data must not appear in metrics, crash reports, issue
attachments or CI artifacts. User-provided labels, graphs and reports are data,
not executable HTML or privileged instructions. No live model-provider call is
part of the initial Dashboard release.

The detailed [design](DESIGN.md) and [operations](OPERATIONS.md) specify additional
limits. Report vulnerabilities through [SECURITY.md](../SECURITY.md). This document
summarizes implemented boundaries; external identity/device acceptance remains open.
