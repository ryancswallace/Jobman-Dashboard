# Two-Control notification acceptance

This opt-in test exercises both deployed Control sources through normal job
admission and cancellation, durable publication and service-only ingestion,
personal rules, and the authenticated inbox. It is prepared for independent
review; **no live result is claimed by this document**.

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
  The wrapper pins each running source binary to the reviewed d332a2b5 build,
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

From the Dashboard repository after review and deployment:

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_RUNTIME=1 \
JOBMAN_DASHBOARD_LAB_MULTISOURCE_NOTIFICATIONS=1 \
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
are mode0600 under `.lab/dashboard/multisource-notifications/{primary,secondary}`;
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
instrumentation does not establish the cause of a previous live failure.

A fresh run uses two newly generated nonce receipts. It does not reopen the
original uncertain primary receipt `7ae1782e0d424061e8f056f7d4dde9bf` or secondary
receipt `ecf5578b777a10c0a997e3a15218fcfb`. Those original jobs and retained evidence
require separate explicit review; this harness does not repair or cancel them.

Offline tests run without live opt-ins:

```sh
GOTOOLCHAIN=go1.26.6 GOWORK=off go test -race -tags integration ./internal/auth \
  -run '^TestLabMultiNotification' -count=1
python3 ../jobman-lab/scripts/test-dashboard-multisource-notification-scenario.py
```

These tests validate fixed source identity, actual event/barrier provenance,
source/owner rule contamination, current checkpoint SQL fields, read-only barrier
transactions and private helper transport. They are not deployed acceptance.
