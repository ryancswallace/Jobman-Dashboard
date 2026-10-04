# Read-only actual run catalog acceptance

`TestLabActualRunCatalog` is an opt-in HTTP acceptance harness for a Dashboard
candidate and a Control source advertising `bounded-run-catalog`. No live result
has been claimed for this harness. It reads retained, previously accepted actual
subprocess and Slurm executions through ordinary authenticated HTTPS APIs.
It never submits, cancels, retries, imports or changes a workload. It does not
create reports, alert rules or delivery bindings.

The original host receipt is
`/private/tmp/jobman-dashboard-actual-host-acceptance-v1.json`, SHA-256
`defe8baf9deec9cb17e3f62b9ddcea6051ac82d50787ab269ef8a7b689910241`.
The derived Slurm receipt is
`/private/tmp/jobman-dashboard-slurm-report-refresh-control04bd-v1.json`, SHA-256
`929a9e93e83069dc330734b699bd6077432507397e56ec484367c6af954be530`.
Both hashes are compiled into the test. Newer Control versions can make a sealed
report outdated; the test requires its original source/job/run identity and
ready state, not a current Control version or a false freshness claim. The
original receipts and source bytes remain unchanged.

For the accepted subprocess failure and Slurm task2, the harness checks:

- exact source instance, recovery epoch, namespace, target, owner and current run;
- latest-first bounded run catalogs and selected run details against ordinary
  Control reads, including all source-reported execution fields;
- selected `runNumber` log bytes, offsets, execution identity and a contiguous
  follow, for empty stdout and nonempty stderr;
- selected artifact metadata attribution and exact totals, recording zero items
  honestly when no artifacts were published;
- Bob's unauthorized scope responses (`403 forbidden`), wrong authorized
  namespace/job and unknown run responses (`404 not_found_or_inaccessible`), with
  strict sanitized response bodies;
- an actual skipped graph branch with no run and, when present among the five
  original authorized research jobs, imported-only history with no invented run;
- unchanged source-qualified job facts and source identity after the reads.

The retained accepted jobs currently each have one run. Results record actual
run counts and `multiAttemptObserved`; this test does **not** establish selection
of an older real execution. The real PostgreSQL multiple-run tests cover that
contract separately. A future real two-run fixture needs its own reviewed normal
execution plan and accepted receipt before extending this harness. Control's
current execution admission rejects `retry.maxRuns != 1`; changing that policy
or fabricating database rows is not part of this read-only harness.

## Invocation after independent review

Use a clean immutable test binary built from the reviewed Dashboard commit. The
coordinating operator must first approve the exact deployed Dashboard/Control
candidates, source identity and retained input hashes. The source and Dashboard
must remain stable during this test. Keycloak sign-ins use only the existing
synthetic Alice and Bob identities and verified Lab CAs; they do not establish
actual AD FS acceptance.

Set an unused result path inside a pre-created, canonical, current-user-owned
`0700` directory. The test creates a `0600` exclusive `<result>.intent.json`
before sign-in and a separate result on success or ordinary test failure. A
crash leaves the intent. Never rerun the same operation or replace failed
receipts; inspect the failure and prepare a separately reviewed new attempt.

```sh
JOBMAN_DASHBOARD_LAB_RUNTIME=1 \
JOBMAN_DASHBOARD_LAB_RUNS=1 \
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_RUNS_HOST_RECEIPT=/private/tmp/jobman-dashboard-actual-host-acceptance-v1.json \
JOBMAN_DASHBOARD_LAB_RUNS_SLURM_RECEIPT=/private/tmp/jobman-dashboard-slurm-report-refresh-control04bd-v1.json \
JOBMAN_DASHBOARD_LAB_RUNS_RESULT=/private/tmp/REVIEWED_NEW_PRIVATE_DIRECTORY/result.json \
/path/to/reviewed/auth.test -test.run '^TestLabActualRunCatalog$' -test.count=1 -test.timeout=6m
```

Two sign-ins each have a45-second bound. All subsequent reads share a3-minute
context and12-second individual request bound. There are at most100 requests,
4MiB per response,20 runs/job (two10-item pages),200 artifacts/job and five
imported-candidate reads. Calls are sequential with no retries. Results contain
public synthetic IDs, counts and hashes; never bearer credentials, raw log bytes,
commands, file paths from source responses or report contents. The6-minute outer
bound covers setup, sign-in, reads and receipt persistence.

Offline guard tests exercise the actual GET transport, request/response bounds,
strict denial decoding, cancellation/no retry, canonical run fields, ordering,
continuation totals, source attribution and exclusive receipt preservation:

```sh
env -u GOROOT GOTOOLCHAIN=go1.26.6 GOWORK=off \
  go test -tags=integration -race -shuffle=on ./internal/auth \
  -run 'TestLabRuns|TestLabActualRunCatalog' -count=1
```

Without both opt-ins the actual Lab test explicitly skips. A skipped test is not
live run-catalog evidence.
