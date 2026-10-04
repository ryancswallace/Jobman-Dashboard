# Storage-local log broker

The broker reads immutable Control log chunks from explicitly approved local or NFS roots. Dashboard assembles byte ranges through authenticated broker calls; the web and iPhone receive bytes, original offsets and stream state. They receive no storage roots, object keys, certificates or download URLs. Artifact metadata uses a separate bounded Control path.

Implementation status: the file/process, delegation, cursor and broker transport paths have focused tests and independent security review. Actual Control manifests and newly produced cross-user NFS chunks pass [subprocess acceptance](LAB_EXECUTION.md); [split acceptance](LAB_SPLIT.md) verifies separate interactive/worker callers. The full failure/load matrix remains open. This document does not establish release acceptance.

## Placement and credentials

Build both executables with `make build`. Install `jobman-log-broker` on a Linux host that can reach the approved store roots. macOS safe-open support is available for development. Other platforms fail closed. A workload host with local-only logs requires a broker there or an explicitly approved read-only mount.

Use a designated service identity with read/traverse ACLs across the approved workload-user roots. Mount the data read-only where possible and retain NFS root-squash. The service needs network access to its configured Controls and Dashboard callers. Its private source-identity ledger belongs on **local disk**, separate from NFS log mounts, in a precreated directory owned by the service with mode `0700`.

Use [the broker configuration example](../deploy/log-broker.example.json) and the `logBrokers`/`logMappings` sections of [Dashboard configuration](../deploy/config.example.json). Replace every example identity, origin, store name, certificate and key path. Empty `targetGenerationId` explicitly permits a mapping for any generation in that source; otherwise use the exact immutable target-generation UUID. A generation-specific mapping takes precedence. Logical store versions remain distinct; duplicates are rejected. Configure multiple broker routes when stores live on different hosts.

Trust runs in two separate directions:

1. Dashboard presents its broker-specific mTLS client certificate and an Ed25519 assertion to the broker. Register its exact certificate fingerprint (loaded from the public leaf certificate), public signing key, key ID, service ID, broker audience, deployment and namespace allowlist in `services`.
2. The broker presents its own independently registered mTLS certificate and delegation signing key to Control. Limit that Control registration to namespace discovery and log reads in the intended namespaces. Control still checks the represented user's current directory grants.

TLS roots are explicitly configured. Redirects, ambient proxies, general HTTP forwarding and unverified identity aliases are unsupported. Private keys use owner-only regular files; public certificates may be readable. Each Control's instance ID, recovery epoch and configuration revision are pinned in the broker's atomically persisted local ledger. Keep that ledger during upgrades; do not delete it to bypass rollback/reassignment rejection.

After restart, a broker rejects assertions issued before its new startup fence (approximately six seconds, including JWT timestamp precision). This prevents replay-cache reset from reviving prior assertions. Assertions expire within 60 seconds and are single-use; the bounded in-memory replay cache retains them through the exact accepted clock-skew boundary. A client retries by minting a new assertion.

## Startup

```sh
make build
bin/jobman-log-broker --config /etc/jobman-log-broker/config.json --mode check-config
bin/jobman-log-broker --config /etc/jobman-log-broker/config.json
bin/jobman-dashboard --config /etc/jobman-dashboard/config.json --mode check-config
```

`check-config` validates structure and local key material. It does not contact Control, read log roots, prove ACL inheritance, or alter the broker ledger. Service startup checks the private state directory and locks it against a second process. The process never reads another service's database or accepts a filesystem path from an API caller.

For existing and future log files, provision and verify the designated reader's ACLs. A default POSIX ACL alone does **not** override files created with mode `0600` or directories created with `0700`: the ACL mask can remove the effective named-reader rights. The Jobman producer must use the explicit supported reader policy and verify inherited ACLs; private behavior elsewhere must remain unchanged. This producer policy passes actual synthetic NFS new-file checks. Validate the same behavior on each deployment's store; a recursive permission widening workaround is not a substitute.

## Read boundaries

Dashboard obtains a bounded manifest using a tail size, original byte offset or sequence selector. Every referenced object must match the source's canonical namespace, job, execution, stream and exact sequence prefix. Each storage-local request carries only that immutable subject and sequence. The broker obtains its own authorized manifest, resolves an operator-owned root, and reads at most one 256 KiB chunk.

Every path component is opened relative to an already-open directory descriptor with `O_NOFOLLOW`; symlinks are rejected even when they remain within the approved root. Nonregular files are rejected. The reader checks opened size, file identity and modification metadata, then verifies the full declared SHA-256 before returning any range. Dashboard independently verifies returned chunk length/checksum before assembly.

Filesystem operations run in separate short-lived helper processes. Default configuration permits four readers and a three-second read deadline. A timed-out child is killed, but its slot remains occupied until the OS reaps it. An uninterruptible NFS kernel wait therefore consumes a bounded slot instead of creating unlimited stuck readers. Exhaustion returns `reader_busy`; operators must restore the mount/host and confirm blocked processes are reaped. Repeatedly restarting the service can leave killed NFS tasks behind and is not a recovery procedure.

The broker checks current Control authorization after preparing the chunk. Dashboard also rechecks before publishing a range. A revocation or restore discards prepared bytes. Continuations are signed, 15-minute, user/source/namespace/run/execution/authorization-bound locations; they never grant access. Appending immutable chunks preserves the continuation. Source recovery, permission changes and stream replacement invalidate it.

Missing, corrupt, inaccessible, busy, timeout, not-captured and complete-empty states remain distinct. A recently captured missing file is retried at most twice within the request deadline. No failed read advances over missing bytes. Requests and visible client buffers remain bounded at 256 KiB and 2 MiB respectively.

## Validation and operations

Run `go test -race ./internal/logs ./internal/auth ./internal/control ./internal/operations ./internal/httpapi` with the pinned toolchain. The broker transport test uses real loopback mTLS, an isolated file-reader child and final authorization revocation; it waits for the real restart fence.

Record actual installation evidence for both existing and newly produced cross-user files, local and NFS mappings, root-squash, zero-byte completion, missing/corrupt chunks, wrong generation/store mapping, source revocation during reads and stalled-mount recovery. Never retain raw job content in service logs or CI artifacts. Keep generated Lab credentials outside Git.

Dashboard supports 320 selected namespaces and bounds encoded query strings at 96 KiB, with a 128 KiB HTTP header budget. Any reverse proxy must permit a single request line covering that bounded selection; otherwise a valid organization aggregate may be rejected before it reaches Dashboard. Broker chunk requests remain a separate 4 KiB JSON body with 16 KiB headers.
