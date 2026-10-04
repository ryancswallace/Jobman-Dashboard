# Notification retention

Migration 17 separates private inbox content from delivery and subscription
history. `Store.PruneNotifications` performs one bounded local maintenance pass;
it makes no Control, directory or APNs request. Runtime scheduling is a separate
integration step. Operators must use this maintenance path, rather than deleting
rule snapshots or resetting event checkpoints directly in SQL.

| Data | Earliest retirement | Additional gates |
| --- | --- | --- |
| Inbox content and matched-rule context | 30 days from insertion | No pending delivery references the inbox |
| Original source/event and account deduplication | Largest observed source replay window plus 5 days, at least 35 days since last sighting | Active source refreshed within 60 seconds; no pending fanout, account evaluation or delivery; no delivery hold |
| Known provider attempts | Same source horizon, measured from delivery resolution | Active source refreshed within 60 seconds; no hold |
| Resolved delivery | Same source horizon, measured from resolution | Active source refreshed within 60 seconds; no hold; no unknown attempt |
| Old rule snapshots | Largest observed window across configured feeds plus 5 days, at least 35 days | Not current; no evaluation/inbox references; all feeds current; no hold |
| Retired activation payload and denial override | Same global history horizon | No retained snapshot, current selection, evaluation or inbox reference |

Source retention is a durable high-water: a later smaller advertised value
cannot shorten protection for existing data. A source outage pauses journal
cleanup until its current replay window is known. A global hold also pauses
journal, deduplication and authorization-history cleanup. Expired inbox content
may still be removed during a hold once all its deliveries have resolved.
Pending work never expires merely because its source event is old. Local sender
maintenance may first resolve an obsolete pending delivery as expired without
claiming that APNs accepted it.

Delivery rows keep an immutable `original_inbox_id`; only their nullable live
inbox reference is cleared when inbox content expires. A resolved delivery with
any unknown provider attempt keeps its minimal delivery identity and unknown
attempt rows indefinitely. Its completed, known attempts may retire after the
normal horizon. This preserves crash uncertainty without retaining inbox text,
rule-match payloads or device tokens inside the journal. An unknown outcome is
never rewritten to accepted or rejected by cleanup.

An activation's original authority timestamp lives on the activation row. A
current interval can therefore survive retirement of the rule snapshot that
first introduced it, while restore floors and denial checks still use its actual
origin. Indexed snapshot-to-activation references protect intervals used by any
retained version, including name-only revisions. Once all references retire,
only the activation payload and its denial override are removed. A permanent
identity tombstone retains the activation UUID, rule/account/source/namespace
IDs, creation revision and original timestamp. Database triggers prohibit
identity deletion, payload restoration and references to retired intervals.
An old activation UUID can never be admitted as a new subscription.

Cleanup frees the 1,000 normal retained-version allowance and 200 stop-version
reserve as old unreferenced snapshots expire. It preserves current snapshots
and removes a deleted rule only after its remaining history and references are
eligible. No quota delays an immediate authorization denial or local stop.

Each pass has a 10-second deadline and limits work to 100 inboxes, 500 known
attempts and 100 resolved deliveries, followed by at most 20 accounts. Per account,
it handles at most 20 historical versions, 20 deleted rules, 100 activation payloads
and 100 denial overrides. A durable round-robin account cursor prevents older
accounts with live history from starving later accounts. Both normal source cleanup and explicit capacity-recovery cleanup handle
at most 500 eligible events per pass with the same history and pending-work gates. Transactions follow the shared
hold→work-quota→account→source lock order; cleanup never waits for external I/O.

The lifetime activation identity rows and unresolved provider journals are
deliberately not covered by a hard lifetime row bound. Disk sizing must include
their cumulative growth, indexes, retained device identities and audits. Payload
and pending-work limits do not imply bounded lifetime database storage. Operators
should monitor table size and vacuum behavior; safely archiving permanent
identity or uncertainty records needs a separate reviewed design.

Real PostgreSQL tests cover separate inbox/journal lifetimes, pending evaluation
and delivery protection, unknown handoff followed by a known retry, source
outage and retention increases, restore holds, rule quota recovery, original
authority timestamps, retired-ID non-reuse, historical match references,
deleted-rule cleanup and round-robin maintenance. These are isolated synthetic
Lab tests and do not establish real APNs delivery acceptance.
