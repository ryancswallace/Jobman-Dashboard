# API and client contracts

The checked-in [OpenAPI document](../api/openapi.json) is the wire contract.
[Contract instructions](../contracts/README.md) describe deterministic generation
of TypeScript and Swift models. Run `make contracts-check` to verify checked-in
clients and boundary tests, and `make contracts` after editing the source.

Keep source-qualified identifiers, stable error classifications, current
authorization and bounded continuation semantics consistent across web/iPhone.
Do not expose upstream credentials or private error bodies. Add meaningful
cross-client tests for contract changes. A generated model alone does not prove
that a service implements or authorizes an operation.

Feature contracts are documented in [run selection](RUN_SELECTION.md),
[targets](TARGETS.md), [artifact metadata](ARTIFACT_METADATA.md),
[notification rules](NOTIFICATION_RULES.md), and [inbox](INBOX.md).
