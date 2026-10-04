# Personal alert rules

Rules are opt-in and belong to a verified Dashboard account. Signing in creates
no rule. A rule selects explicit source-qualified namespaces and either watched
jobs, the user's jobs (including future jobs), or every member's namespace jobs.
Canonical submitting principals are independently verified in each Control;
Dashboard account IDs and identity-provider subject strings are never substituted
for Control owner IDs.

The outcome editor supports the six known individual outcomes (`success`,
`failure`, `cancelled`, `timed_out`, `aborted`, `lost`) and `all_terminal`, which
also covers future unknown terminal outcomes. The unsuccessful preset selects
failure, timeout, abort and loss. It does not silently include cancellation.
Inactive editor selections are cleared when changing scope or outcome mode.

## Activation and current authority

Each selected namespace has its own immutable activation interval. Initial
activation requires current represented-user namespace/job authority, an already
initialized durable feed, and a fresh source checkpoint between two authorization
checks. The persistence transaction rechecks source identity, epoch and configured
namespace membership. The boundary records the source's own clock and opaque
checkpoint. An unavailable feed produces pending monitoring; it never invents
coverage of the outage period.

An eligible source event must have been recorded strictly after activation.
Observed completion time and feed position are not activation substitutes. Import
and bootstrap reconciliation events are excluded. Scope/outcome/enable edits
create new intervals; a name-only edit can preserve them. Explicit same-Control
source recovery can retain an interval for late original events, after the
separate recovery protocol has reconciled and resumed the feed.

Confirmed user grant loss records an irreversible interval denial independently
of history/rate quotas. Unavailable directory authority defers access instead of
pretending to confirm loss. An operator removing a namespace writes a bounded
source/namespace denial watermark in the same transaction as recovery apply.
The original database creation time of an interval determines whether that
watermark revokes it; renaming the rule cannot erase the denial. Regrant or
configuration re-add requires explicit revalidation and a fresh interval.

Matching, delivery and inbox reads must additionally verify current account,
source and namespace authority. Stored rules, historical matches, event ingestion
and a visible rule are not delivery authority. Each delivery must consult both
individual denials and scope-removal watermarks. These checks are required even
if an older immutable snapshot still says its interval was active.

## API and client behavior

The rule service returns a public projection with no account/directory/principal
IDs, activation UUIDs or feed cursors. Current forbidden/unavailable namespace
and watched-job references are omitted; separate counts explain incomplete
scope visibility. Clients must not reconstruct omitted references or silently
replace a full rule with its redacted projection. A dedicated revision-checked
enable/disable operation lets an owner stop monitoring during source outages.
Deletion also needs no source connection.

Listing uses at most 20 full rules per page and opaque ten-minute continuations
bound to account, query kind and page size. Traversal is by stable rule UUID;
concurrent changes are reauthorized on every page. Source proofs and feed
snapshots are shared only inside one bounded request, with at most 32 sources and
four concurrent network operations. Feed snapshots precede a final source
permission check; current local account/alias/revision and denial checks follow
network reads. Proof expiry is checked again during projection. Revalidating
pending scopes groups checkpoint work by source, so one unavailable source does
not discard healthy source progress.

A full edit or delete uses optimistic revision comparison. Conflicts require
reloading and reviewing current intent. No client may supply server activation
provenance. Public mutations affect personal monitoring intent only; they never
submit, cancel or modify a job.

## Bounds and durable history

- 100 live rules per account; 320 explicit namespaces across at most 32 sources.
- 100 watched jobs per rule; 120 UTF-8 bytes in its display name.
- 64KiB input and 512KiB private persisted snapshot limits.
- 10 regular mutations per minute; 1,000 regular retained rule versions per account.
- An additional200 versions are reserved for actual disable/delete operations,
  enough to stop and remove every live rule. No-op retries consume no reserve.
- Confirmed denial records bypass edit quotas. History exhaustion cannot restore
  denied access; an explicit reactivation may remain blocked until safe retention
  can reclaim history.

Rule versions, activation intervals and denial records have a separate lifecycle
from the 30-day inbox. They must not be pruned while delayed evaluation, existing
matches or source replay still depend on them. Retention and evaluation integration
remain in progress at this checkpoint; these bounds are not a claim that inbox
or push delivery is implemented.
