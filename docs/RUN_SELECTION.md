# Selecting recorded job runs

The run chooser exposes source-recorded references for logs, artifact metadata,
and new diagnosis requests. It does not create runs, reconstruct execution
history, or add a full event timeline.

## API contract

- `GET /api/v1/deployments/{deploymentId}/namespaces/{namespaceId}/jobs/{jobId}/runs`
  returns `RunPage`: `items`, exact decimal-string `total`, optional opaque
  `nextCursor`, `completeness`, one authorized source contribution and `fetchedAt`.
  `limit` defaults to 50 and is bounded to 1–100.
- `GET .../jobs/{jobId}/runs/{runId}` returns `RunDetail` with the exact source
  run and current contribution metadata.
- Each run has a UUID, exact decimal-string number, desired state, phase,
  optional outcome, and record creation/update timestamps. Execution UUID,
  execution phase, target/generation UUIDs, backend and confidence appear only
  when the source reports an assigned execution. A run's update time is not an
  execution start/completion time. Unknown phase/outcome values remain factual
  strings.

The source must advertise Control's additive `bounded-run-catalog` feature.
Older sources return `unsupported_contract`; the UI keeps its existing current
views usable. Empty run catalogs are valid for unassigned or imported jobs and
never imply a fabricated execution.

Pages sort by decreasing run number and capture an initial run-number ceiling.
New runs do not enter an in-progress traversal; refresh starts a new traversal.
The Control selector uses a purpose-separated HMAC with the existing persistent
token key. It expires after ten minutes and binds namespace/job, current
principal, source instance/recovery epoch and grant version. Dashboard wraps it
in an account/query/authority-bound persistent opaque cursor. Every page and
single-run read rechecks current source authorization. A changed grant, source
recovery, expired selector, or conflicting traversal is rejected rather than
silently resuming under new authority. This is a reference traversal, not an
immutable snapshot of changing run state.

## Client behavior

The web chooser loads on demand and keeps at most 64 cursor positions. Selected
run IDs remain in the web URL as `runId`; changing selection discards artifact
continuations. A selected run is resolved through the authorized detail route
before dependent views open. An inaccessible selection cannot silently become
the current run.

Logs send the selected run **number** and verify returned run ID, number and
execution ID before rendering bytes. Their footer separates the last successful
read from captured log metadata. Artifact reads use the same run number and
verify every returned artifact reference. New reports use the selected run
**UUID**. Report history remains explicitly across all runs; each immutable
report shows its original evidence/run references. “Use current defaults”
restores current logs, all artifact runs and recent diagnosis evidence.

Job and inbox detail pages offer ID-only `jobman-dashboard://job/...` and
`jobman-dashboard://inbox/...` links. These carry no credentials. The native app
must resolve them against its configured Dashboard and perform current
sign-in/authorization; a link is never a grant. OAuth uses a separate callback
scheme. Native pasted HTTPS links must match the configured Dashboard origin.

## Verification scope

Control has repository/HTTP/cursor tests, including a disposable PostgreSQL
catalog test for unassigned jobs, historical pages, concurrent new runs,
execution references and current grant/recovery changes. Dashboard has real
loopback mTLS adapter tests, engine cursor/revocation tests, HTTP route bounds,
strict web decoding, selected evidence requests and last-successful-read tests.
Execution of the opt-in PostgreSQL test and deployment of the additive source
endpoint are separate acceptance steps; this document does not claim either
has occurred. Native implementation and signed-device acceptance are tracked
separately. Existing browser policy restrictions remain in force.
