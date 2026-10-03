# Artifact metadata

The authenticated job artifact endpoint returns bounded published metadata across
actual runs, with an optional positive decimal `runNumber` selector. `limit` is
1–100 (default50); `cursor` is an opaque Dashboard continuation. Each row retains
its actual run UUID/number, execution UUID and target generation, declared name,
exact decimal size, SHA-256 checksum and publication time. `total` is the complete
filtered source count at the response's source observation time.

`availability: metadata_only` means the authorized Control manifest contains the
metadata. Neither this endpoint nor its clients read the artifact payload or
establish backing storage availability. The response has no object key, storage
root, store mapping or download URL. Logs use the separate independently
authorized broker described in [LOG_BROKER.md](LOG_BROKER.md).

Every page discovers current source authority and checks `artifacts.read` before
and after its source read. Continuations bind the authenticated account, job,
actual-run filter, page size, deployment/namespace, source instance/recovery epoch
and authorization version. Changing any of those requires a fresh first page.
Opaque source continuation values stay in bounded Dashboard cursor storage.
Expiry and quota behavior match ordinary monitoring cursors; replay reauthorizes.

Control's additive `/artifact-metadata` route limits each source page to100 items
and2MiB of encoded metadata, including escaped object keys that are stripped by
the Dashboard adapter. Execution UUID/name ordering uses explicit byte collation
independent of database locale. Later publications may appear while browsing;
this is a live keyset view, not a point-in-time historical export.

The web view keeps selected run/page in its URL, displays exact sizes and actual
execution identities, and labels bytes as unverified. It offers explicit refresh
and bounded next/first-page controls. Source failures remain errors rather than
empty-success lists. Native integration is tracked in IMPLEMENTATION_STATUS.md.

Validation includes source-identity/grant/epoch/run/query cursor isolation,
revocation during preparation, unsupported contracts, malformed source pages,
contradictory-total rejection, real TLS adapter selectors/large sizes, and HTTP rejection of storage-path input.
A43-test web suite includes artifact page/run navigation. Chrome inspection of
the explicit two-source synthetic fixture verified50+5 rows, scope breadcrumbs,
actual run labels and the bytes-unverified disclosure. Real Control integration
remains a separate acceptance gate.
