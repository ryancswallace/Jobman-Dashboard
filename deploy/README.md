# Deployment examples

These are public templates with deliberately unusable identity/secret placeholders.
Copy them into a private operator-managed directory, then follow
[Linux installation](../docs/LINUX_INSTALLATION.md) and
[configuration](../docs/CONFIGURATION.md).

- `config.example.json`: configured API/combined runtime example.
- `worker.example.json`: isolated worker runtime example.
- `log-broker.example.json`: broker TLS, metadata and log mapping example.
- `operator.example.json`: explicit operator configuration example.
- `systemd/`: separate API, worker and broker unit templates.
- [postgres](postgres/README.md): migration/runtime role separation and grants.

Do not enable units or substitute production credentials automatically. Containers
use different web asset paths; see [containers](../docs/CONTAINERS.md).
