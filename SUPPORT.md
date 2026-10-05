# Maintenance and support

Jobman Dashboard is an engineering prerelease with best-effort support. The latest
candidate and `main` are the active development targets. Stable compatibility and
production readiness remain subject to the [release gates](docs/RELEASE_HANDOFF.md).

For a reproducible issue, provide Dashboard and Control revisions, browser/iOS
version, OS/architecture, installation method, affected feature, safe error code,
and expected versus actual behavior. A synthetic reproduction is preferred.

Do not post credentials, database URLs, private paths, log content, AD group
membership, notification/device tokens or raw support bundles. Security reports
belong in the [private reporting process](SECURITY.md).

Start with [troubleshooting](docs/TROUBLESHOOTING.md), [operations](docs/OPERATIONS.md)
and the [documentation index](docs/README.md). Use
[GitHub issues](https://github.com/ryancswallace/Jobman-Dashboard/issues) for safe
bug reports and feature requests. Upstream execution faults belong in Jobman;
authority/metadata faults belong in Control; deterministic diagnosis faults belong
in Diagnose, with a sanitized reproducer and exact compatible revisions.
