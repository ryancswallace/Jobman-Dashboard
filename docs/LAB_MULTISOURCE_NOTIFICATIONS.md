# Two-Control notification acceptance

This opt-in test exercises both deployed Control sources through normal job
admission and cancellation, durable publication and service-only ingestion,
personal rules, and the authenticated inbox. The reviewed v5 run passed against
the deployed two-Control Lab on 2026-10-04; the retained evidence is recorded below.

The scenario is synthetic and performs no subprocess or Slurm execution. It
establishes no APNs, physical-phone, real AD FS or managed-device acceptance.
Existing workload and client tests cover separate requirements.

## Required reviewed state

- Primary deployment `72000000-0000-4000-8000-000000000001`, Control instance
  `e633cf92-258d-48ff-965a-fda88d68ef3a`, research namespace
  `4156b832-9be8-40ff-a471-cb3061b6001d`.
- Secondary deployment `72000000-0000-4000-8000-000000000002`, Control instance
  `a4f0e2ab-7323-4c90-9510-1f073c660f06`, research namespace
  `455525f6-5d8a-4d4f-bea3-2d3f1ed4698f`.
- Both source recovery epochs are1, both feeds active, ordinary delivery hold
  released, current source authorization healthy. The source fixture targets
  used by these scenarios have no execution agent. Existing deployments,
  memberships, rules, source registries and runtime configuration remain intact.
- Synthetic Alice and Bob can both read all research jobs. Alice owns the new
  primary jobs; Bob owns the secondary jobs. Real synthetic PKCE sign-in must
  return their exact verified fixture subjects. Credentials are loaded privately
  by the existing sign-in helper and are never printed.
- The reviewed Control helper `367006818e72315b01860f56f971004e17c24886` is
  already installed at
  `/usr/local/libexec/jobman-dashboard-scale/367006818e72315b01860f56f971004e17c24886/jobman-control-lab-helper`,
  SHA256 `6f511e91434dac2499d464f0b424607fe0b099b966148c992499e5371a333f65`.
  The wrapper pins each running source binary to the reviewed04bd83d build (`7faac82263dfa281d2fec7c3e8a52a55a121294e39e7e2d1706115751d4a2123`),
  verifies its UID/process identity, private source configuration and immutable
  fixture metadata, and requires unchanged process/configuration after each
  normal Store operation. Both the Control and separate LDAP fixture roots must
  have no active directory-scenario or unfinished preparation receipt. The
  legitimate retained scale start/completion markers are preserved. It never
  installs a helper or restarts a service.

Coordinate the run with the Lab operator. Do not overlap directory changes,
source recovery, deployment, scale adoption, restore, or notification scenarios
that share these accounts. Running after scale adoption is supported: discovery
may contain additional namespaces, but this test selects only the two exact
research scopes. No caller-supplied database, namespace or source path is accepted.

## Run and bounds

From the Dashboard repository after review and deployment, with a newly
pre-created canonical owner0700 empty directory immediately under `/private/tmp`
named `jobman-dashboard-notification-receipts-<unique>`. The harness refuses a
nonempty root before sign-in and passes it explicitly to each wrapper call. The
wrapper independently checks owner, mode and canonical identity on every call.
Do not reuse a directory from any prior attempt.

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_RUNTIME=1 \
JOBMAN_DASHBOARD_LAB_MULTISOURCE_NOTIFICATIONS=1 \
JOBMAN_DASHBOARD_LAB_NOTIFICATION_RECEIPTS=/private/tmp/jobman-dashboard-notification-receipts-REVIEWED_UNIQUE \
GOTOOLCHAIN=go1.26.6 GOWORK=off \
go test -race -tags integration ./internal/auth \
  -run '^TestLabTwoControlTerminalNotifications$' -count=1 -v -timeout=13m
```

The new Lab wrapper is
`scripts/dashboard-multisource-notification-scenario.py`; its offline companion
is `scripts/test-dashboard-multisource-notification-scenario.py`. It reuses the
hash-pinned `dashboard-scale-source-common.py` bounded file/process primitives.
Pinned SSH host keys and the existing private connection inventory are required.
Only a fixed root guest operation reads the exact source DSN into an exclusive
private temporary file. The DSN never appears in arguments, receipts or output.
Read-only PostgreSQL barriers run through the existing synthetic pg01 bootstrap
connection in a bounded read-only transaction; they do not grant runtime access.

Each invocation admits two new jobs per source with fresh logged nonce receipts,
using the helper's normal `SubmitJob` and `CancelJob` methods. It creates three
personal rules per account: one cancelled-outcome rule for each source's research
namespace and one my-jobs rule selecting both research scopes. All six activation
boundaries must be active before the first cancellations. Rule creation is never
automatically retried after an uncertain response.

The test verifies:

1. Both members can read each new job, with owner status inverted between sources.
2. After a first cancellation succeeds and its event is validated, one explicit
   idempotency check preserves the original source event UUID, job revision and
   timestamp. An exact source-qualified barrier waits for source
   publication, Dashboard ingestion, completed fanout and no pending evaluations.
3. Each account has one inbox item per first event. Aggregate and single-source
   lists agree. Only that source's test rule contributes; the my-jobs rule
   contributes only for the actual owner. Unrelated existing rules may also match
   and are preserved rather than treated as a test failure.
4. Inbox provenance retains deployment, namespace, Control instance, job, original
   event and recorded time. Cross-account detail/read requests return404. Reading
   Alice's primary item leaves her secondary item and both Bob items unread.
5. After all six test-owned rules stop, the second cancellation in each source
   settles without contributions from those rules. Earlier inbox identities stay
   available. Cleanup deletes only the six owned rules; it never clears history,
   deletes source jobs, edits SQL rows or resets a feed.

There are four jobs, six rules, at most20 pages of50 inbox items per traversal,
two serial45-second sign-ins, an8-minute main context and a separate3-minute
cleanup bound. The13-minute test timeout exceeds their12-minute30-second total. Individual
helper calls take at most55seconds; source operations35seconds and each read-only
SQL call9seconds. Each source's processing barrier is capped at90seconds. The
source helper itself caps retained scenario receipts at100 per fixture root.
No assertion depends on waiting a fixed amount of time without observing the
actual durable completion state.

## Evidence and failure handling

Retain the verbose test log with the exact deployed Dashboard and Control build
receipts. It contains public nonce, job, original event and inbox identities, not
bearer tokens, DSNs, source log contents or provider credentials. Host receipts
are mode0600 under the reviewed temporary root’s `{primary,secondary}` directories;
source helper receipts use the same fresh nonce in their fixed private fixture
root. They are separate from restore and primary-only acceptance host receipts.

A failed or lost preparation reply may have admitted a partial pair of jobs.
Preserve its source pending marker and host evidence; inspect before any retry.
Do not generate another nonce to hide an uncertain preparation. Successful
preparation/cancellation receipts can be inspected read-only. An uncertain
cancellation is never retried by this test or its cleanup. The explicit
idempotency assertion runs only after a successful, validated first cancellation;
it is distinct from retrying a failed or lost response.

Cleanup uses fresh rule revisions. If it cannot confirm a rule stopped, or rule
creation had an unknown outcome, it leaves unused jobs accepted and reports the
receipt for explicit inspection. Otherwise cleanup attempts each never-attempted
unused job once. Cancellation intent is recorded before invoking the helper, so a
failed command, lost response or failed event validation leaves that case and its
receipts for inspection. Successfully completed cases are also skipped during
cleanup, including when their later explicit idempotency assertion fails. Cancelling
unused jobs can trigger unrelated pre-existing rules; those rules are never
disabled by the harness. A failed private-material cleanup
requires inspection; the next invocation fails closed rather than overwriting it.

The wrapper emits only an allowlisted failure stage and code. The test distinguishes
helper execution, guest postflight, SSH transport, host result validation and
immutable receipt retention failures from an event identity mismatch. Subprocess
errors, stderr and private material are never echoed. Both output streams are
bounded; malformed diagnostic frames become a fixed generic failure. This
instrumentation does not establish the cause of a previous live failure. The
read-only barrier separately identifies input, source-query, Dashboard-query and
result-validation stages. Exact built-in exception classes map to fixed codes;
exception text, field names and tracebacks are never emitted. A pending boolean
remains an ordinary successful response, followed by the existing bounded poll.

A fresh run uses two newly generated nonce receipts. Both the harness and explicit
root wrapper exclude every retained prior receipt:

- `7ae1782e0d424061e8f056f7d4dde9bf`, `ecf5578b777a10c0a997e3a15218fcfb`
- `63861951db782332bb16cc5f58a0a5d6`, `28bd57c3d4a8d292c37c0b1d1520c821`
- `8281f530b049327cf138cfeabd32f268`, `e406f287dc15461d0d79351de1d5485c`
- `bb399d49f2dc05be412399b5b474feb5`, `59373e309864f44603cc5fc183e61d4c`

Their original jobs and evidence stay at their original paths. This change does
not copy, repair, migrate or cancel them. A prepare call in the explicit root
also refuses an existing receipt, rather than adopting an uncertain earlier run.

The third run's finite diagnostic located `common_file_identity`. A separate
synthetic19-byte local probe reproduced a transient second hard link in the Lab
tree: unchanged owner0600/content/mtime, but link count1→2 and a changed ctime.
The strict read correctly rejected it; a parallel `/private/tmp` probe retained
link count1. The extra link disappeared after the call. Its creator is unknown.
The explicit root avoids that observed host-directory behavior while preserving
all regular-file, owner,0600, single-link, bounded-size and immutable-byte guards.
No directory permission or source authorization check is relaxed. The wrapper's
legacy default path remains for compatibility; new deployed acceptance uses the
explicit private root. This local finding is not a passing notification result.

The fourth run reached a validated cancellation and explicit idempotency check,
then failed at the read-only barrier with an unclassified guest-preflight error.
Both a fixed read-only SQL probe and the exact original settled-only wrapper later
passed without changing receipt bytes. All published/settled boolean combinations
also pass in the actual shared-global bootstrap regression. The transient cause
remains unresolved; added finite diagnostics are not a speculative repair or a
passing live result. That run and its cleanup receipts stay preserved and excluded.

Offline tests run without live opt-ins:

```sh
GOTOOLCHAIN=go1.26.6 GOWORK=off go test -race -tags integration ./internal/auth \
  -run '^TestLabMultiNotification' -count=1
python3 ../jobman-lab/scripts/test-dashboard-multisource-notification-scenario.py
```

These tests validate fixed source identity, actual event/barrier provenance,
source/owner rule contamination, current checkpoint SQL fields, read-only barrier
transactions and private helper transport. They are not deployed acceptance.

## Deployed v5 evidence

The root operator invoked the frozen v5 packet once and the full scenario passed
in 33.49 seconds. The Dashboard test binary came from commit
`01f4bd4ad4deaad725c12c9413ec97b820412882`; the deployed runtime remained RC3
`9b1c65e31db8a849ebe2dfa00caf4474bef8e7d2`, configuration 8, with both reviewed 04bd
Control sources. The packet is retained at
`/private/tmp/jobman-notification-fresh-01f4bd4-zidtfuj6`, including
`live-result.json` and the verbose log (SHA256
`ace4afa2b418b29d175537d18e0b0934a7e924629ec6149d7e9b2980b4b66ce4`).

Fresh receipt IDs were `15bbb7d91b8f78d601de7a64731321d2` and
`e25bda7d53d7659defe7bfb0a957c0b8`. The test verified original source event and
inbox identities for both accounts on both deployments, inverted ownership,
aggregate and single-source agreement, account/read isolation, stopped-rule
suppression, and cleanup of only its owned rules. All 15 original host receipt
files from the four earlier attempts retained their recorded hashes.

This success does not explain the earlier v4 read-only barrier failure. The
reviewed read-only probes later passed, and the exact pending-response bootstrap
regression passed; that historical cause remains unresolved. All earlier source
jobs, host receipts and failure evidence remain retained. This is synthetic
terminal-event acceptance, with no APNs or physical-device claim.
