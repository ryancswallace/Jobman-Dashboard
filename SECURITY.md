# Security policy

## Supported versions

Unreleased `main` and the latest engineering release candidate receive best-effort
security fixes. No stable production release or support SLA is established.
See [release status](docs/FINAL_CANDIDATE.md) for outstanding acceptance.

## Private reporting

Report vulnerabilities through a
[private GitHub security advisory](https://github.com/ryancswallace/Jobman-Dashboard/security/advisories/new).
Include the affected version, deployment model, impact and a minimal synthetic
reproduction. Do not open a public issue containing exploit details before
coordinated disclosure, or include tokens, credentials, private logs, AD records,
APNs keys, device identifiers or production database contents.

## Trust boundaries

Dashboard can disclose jobs, logs, artifacts, diagnostic reports and notification
history to authorized namespace members. All members can view others' jobs and
logs within their namespace. Direct AD group permissions form a union; removal
must revoke access. Reads and notification delivery must recheck current authority.
A read-only product can still expose sensitive information if those boundaries fail.

Use TLS, private-network access, restricted database identities and protected
service configuration. The log broker's broad filesystem ACL access makes it a
sensitive boundary. Fixture mode is loopback-only and must never be exposed through
a proxy or container publishing. Consult the [security model](docs/SECURITY_MODEL.md).
