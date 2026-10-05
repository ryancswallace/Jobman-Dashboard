# Containers

`make docker-image` builds a local, non-root Linux image containing both Go
executables, compiled web assets and license notices. Base images and toolchains
are pinned. `make docker-check docker-smoke` validates definitions, version output,
asset presence and rejection of unconfigured startup. No image is pushed.

The default entry point is `tini -- jobman-dashboard`; pass explicit configured
arguments. For a broker, override the entry point with `/sbin/tini` and pass
`-- jobman-log-broker --config /etc/jobman-log-broker/config.json`. Run API, worker
and broker as separate containers with their own configuration, credentials,
network policy and filesystem access. Do not put production secrets in build args.

Set the API's `webRoot` to `/usr/share/jobman-dashboard/web`. The default container
UID/GID is 10001:10001; choose distinct deployment identities and ACLs for the
three roles where shared host storage is used. Run with a read-only root,
`--cap-drop ALL`, `--security-opt no-new-privileges`, explicit writable state/runtime
mounts, read-only configuration and broker log mounts. Provide TLS and only the
private-network ports required by the configured process. NFS mount and report
permissions remain operator responsibilities; a successful image build does not
verify those production permissions.

The root `compose.yaml` only provides a loopback-published synthetic PostgreSQL
database for tests. It does not deploy Dashboard or configure organization identity.
Fixture mode cannot be made safe by binding a Docker port to loopback: the service
must retain its own loopback-only listener, and must not be exposed publicly.
Use local processes or private editor forwarding for fixture development.

Containers are an additional engineering deployment artifact. The canonical
candidate and validated split-systemd installation remain documented in
[Linux installation](LINUX_INSTALLATION.md) and [process modes](PROCESS_MODES.md).
