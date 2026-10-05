# Upgrading and recovery

Use the exact versioned procedure in [Linux installation](LINUX_INSTALLATION.md)
and [operations](OPERATIONS.md), with compatible dependencies from
[release status](FINAL_CANDIDATE.md). Capture current binary/configuration revisions,
backups, service identities and acceptance evidence before an approved change.

Validate configuration for each process role, apply migrations only with the
explicit migration identity, and validate API/worker/broker status and web/native
compatibility after activation. Do not infer rollback safety from a symlink change:
forward-only migrations may require restoring the matching database and object
state from a tested backup. [Lab restore evidence](LAB_RESTORE.md) documents tested
synthetic recovery and its limitations.

Snapshot packages are developer artifacts and do not switch the active installation
or run migration hooks. Do not install one over an accepted candidate as an
unreviewed upgrade. Production migration/deployment still requires the approved
scope and recovery plan from [the implementation prompt](IMPLEMENTATION_PROMPT.md).
