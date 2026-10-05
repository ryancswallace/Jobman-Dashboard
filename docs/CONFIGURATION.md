# Configuration

Production processes read explicit JSON configuration and secret files. Start
from the role-specific examples in [deploy](../deploy/README.md); copy and edit
them outside the repository. Placeholder values are intentionally unusable.
Do not substitute fixture authentication for organization access.

[Authentication](AUTHENTICATION.md) covers AD FS/OIDC, service registration,
delegation, audience and directory claims. [Process modes](PROCESS_MODES.md)
describes API and worker configuration. [Purpose keys](PURPOSE_KEYS.md) documents
separate cryptographic authorities and rotation. [Log broker](LOG_BROKER.md)
explains filesystem mappings and ACLs. [APNs](APNS_PROVIDER.md) documents notification
provider prerequisites.

Validate with the matching process's `--mode check-config`; API and worker checks
have distinct role requirements. Configuration checking does not migrate state
or establish external acceptance. Never place actual JSON, keys or database URLs
inside image layers, Git, issue reports or CI artifacts. `.env.example` only
configures the synthetic Compose database port; it is not runtime production config.
