# Deployed mixed monitoring load

Status: **PASS on October4 at exact9fc4420** with current-version reports. Attempt1 stopped at report preflight before any new rule/job or load and remains preserved. The
opt-in harness is `TestLabDeployedMixedLoad` in
`internal/auth/lab_mixed_load_test.go`. It extends healthy-dependency contention
coverage without changing the separate [accepted-scale test](LAB_SCALE.md).

The cohort has 25 distinct authenticated accounts: 23 scale viewers and Alice and
Bob. It is deliberately **not** the existing 25-scale-viewer acceptance. Scale
viewers retain their direct viewer grants; Alice uses her existing operations
grant; Bob uses his existing research grant and must remain denied access to
primary operations. No directory membership or application grant is changed.

## Preconditions and exact evidence

Run only after the reviewed scale deployment and its own acceptance have passed,
with no concurrent source, identity, configuration, service or recovery changes.
Root coordination authorizes the live invocation separately from code review.
The harness never installs the scale fixture or changes source configuration.

- Both independent Controls must match the public scale fixture, verified public
  CA hashes, immutable instances and recovery epochs. The complete scale fixture
  has 25 signed subjects, ten primary namespaces and five secondary namespaces.
- The isolated host enrollment must match `execution-fixture.json`, the original
  Core `21701b1` executor, its actual target/generation and the existing NFS mapping.
  This creates no new executor, enrollment, target or directory.
- Investigation assets come from the independently accepted complete Slurm run,
  including its repaired Core `807f1f2` provenance. Supply its exact public receipt
  with SHA256 `929a9e93e83069dc330734b699bd6077432507397e56ec484367c6af954be530`.
  This derived receipt preserves the original execution evidence and uses two
  current-version reports prepared after the Control upgrade, with all historical
  reports/citations unchanged; see [report refresh](LAB_REPORT_REFRESH.md).
  The harness pins task2 to its accepted runner-completion execution, then reads
  the current source-qualified job and verifies its owner, target generation and
  exact run ID/number/execution. Its report must name that same run before sampling.
- API and worker must be unheld with healthy ingestion/evaluation. No provider
  credentials or real APNs delivery are required or asserted.
- Retain the reviewed running binary/configuration hashes, source identities,
  resource inventory and test commit alongside the output. This harness does not
  independently prove unchanged running binary hashes or service process IDs.

The existing native PKCE helper signs in to synthetic Keycloak. Passwords remain
in the authorized private Lab credential mechanism, and bearer tokens stay in
memory. Interactive identity-provider time is excluded from data-request samples.
An outer eight-minute Go deadline includes sign-in and bounds the entire test.
This is not corporate AD FS acceptance.

## Work profile and limits

The two modes each contain a 90-second observation window:

| Work | Fixed bound |
| --- | --- |
| One-Control metadata selection | Ten primary scale namespaces:100,000 imported history records and500 admitted active jobs |
| Two-Control metadata selection | Primary01–05 plus secondary01–05, with the same100,000/500 total |
| Scale readers | 23 accounts,18 rounds at five-second intervals, list50 + detail + overview per round |
| Alice investigations |18 rounds: actual Slurm stderr tail/follow, array pages of two children, graph neighborhood capped at two nodes/one edge, sealed report, sealed citation, and the new burst collection's eight-child foreground page |
| Bob investigations |18 rounds: authorized research list/detail and explicit denied actual log/report requests |
| Actual event burst | One host collection per mode, eight children, maximum two active, six success/two exit7 failure;16 new jobs total |
| Child execution | Six one-second output iterations, native `/bin/sh`, relative workspace, run timeout30s, one run, duplicate-risk rejection |
| Driver HTTP | Shared28-request in-flight ceiling,12s per request,4MiB response/64KiB request body limits |
| Observation | One bounded read-only SSH/SQL observer, six samples at15s intervals plus final proof per mode |
| Samples | At most4,096 bounded aggregate samples per mode; every request failure remains in statistics |

The host burst runs only in the existing operations namespace, outside the scale
query scopes. The extra investigation namespaces and up to eight real active jobs
are intentional contention beyond the metadata selection. No imported history is
presented as an actual run or as a terminal notification event.

The actual Slurm stream is already complete. The first request checks its exact
real stderr bytes; every response, including the first, must name the exact run
ID, run number and execution established before load. Subsequent cursor follows
require empty, contiguous ranges. Missing or mismatched attribution is rejected
without advancing any cursor or offset. This exercises authenticated broker/NFS follow
requests concurrently with new host execution and publishing. It does not claim
that a growing Slurm stream was observed or that a 2MiB client buffer was rendered.

Array paging cycles through all five literal task indexes without lost or repeated
children. Graph reads assert exact omitted counts. Repeated report and citation
reads must match their initial semantic JSON hashes. These are reads of already
sealed actual-execution reports, not a diagnosis-worker throughput benchmark.
Bob's research metadata can include existing synthetic observations; it is not
used as proof of actual execution.

## Events, freshness and evidence

Before admitting each collection, the test creates one personal Alice rule and
waits for its namespace activation. Rule creation has no invented idempotency
guarantee. The exclusive progress journal records its intended name before the
request and returned ID before any source submission.

Each mode uses one fixed request and idempotency key:
`dashboard-mixed-v1-one-control` or `dashboard-mixed-v1-two-controls`. Changing a
key or request to hide a failed attempt is prohibited. A prior terminal admission
fails as non-fresh; an interrupted attempt requires inspection before any retry.

The ordinary Control collection must finish with six actual successes and two
actual failures. Each child's exact target generation, backend, run, observed
completion and Dashboard owner/source projection must agree. No lifecycle time
is inferred from absence or scheduler assumptions.

The read-only observer is pinned to pg01 and two fixed synthetic database names.
It accepts exactly eight distinct job UUIDs and returns only those jobs' original
terminal event IDs, run IDs, outcomes, source recorded/published times and Alice's
inbox IDs/times. Both database transactions enforce read-only isolation, a three-
second statement deadline and500ms lock deadline; each subprocess is bounded to
five seconds and the whole SSH call to15s. Queries return at most nine rows so an
unexpected duplicate is rejected rather than truncated into success. Queue
counts come from the singleton quota row, avoiding an unbounded table count.
No payload, command, log bytes, access token, alias, DSN or cursor is exported.

Source event/run IDs must be distinct, current and non-imported, with no recovery
suppression. All eight events must be processed and present in one current-
authorized Alice inbox each, with this mode's rule among the original matches.
Final API reads cross-check the SQL proof, including source timestamps and IDs.

Recorded latency measures:

- HTTP timings include request, bounded response read, decode and validation.
  Scale list/detail/overview must each have414 successful samples and p95<=2s.
  Investigation operations must each have18 successful samples; their latency
  distributions are reported separately.
- Foreground freshness uses the first observed terminal Dashboard child and the
  original Control `recordedAt`. A conservative server-minus-client clock-offset
  upper bound comes from read-only observer timestamps bracketed by client send
  and receive. Negative or ambiguous measured durations fail instead of being
  clamped. The eight-event nearest-rank p95 is their maximum and must be<=10s.
- Inbox persistence uses the original source event time and durable inbox creation
  time on the same Lab PostgreSQL host. Its eight-event p95 must be<=30s. This is
  conservative relative to later feed publication. No APNs handoff or phone
  presentation latency is measured.

The observer samples queue quota counts and fails at exhausted capacity or an
unexpected source/hold state. It does not establish a continuous memory maximum,
queue-age percentile, allocator bound or recovery during a dependency outage.

## Execution and failure handling

Example invocation after review and live coordination:

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_MIXED_LOAD=1 \
JOBMAN_DASHBOARD_LAB_MIXED_SLURM_RECEIPT=/private/tmp/jobman-dashboard-slurm-report-refresh-control04bd-v1.json \
JOBMAN_DASHBOARD_LAB_MIXED_RESULT=/private/tmp/dashboard-mixed-unique-result.json \
GOWORK=off GOTOOLCHAIN=go1.26.6 \
go test -race -tags integration -run '^TestLabDeployedMixedLoad$' \
  -count=1 -v -timeout=8m ./internal/auth
```

Both the result and adjacent `.progress.jsonl` file must be new. Receipts contain
public IDs, hashes, aggregate measurements and sanitized failures. Preserve them
on all failures; a hard process timeout may leave only the synced progress log.
The first failed load check cancels further mode requests. There is no automatic
new mode, new scenario key, provider action, source reset or service restart.

A bounded cleanup attempts to disable only this mode's returned personal rule ID
with the current revision, recording whether the stop was confirmed. Jobs, logs,
inbox records, reports and receipts remain as evidence. If rule creation loses its
response, inspect the journal's exact rule name through the same account before
manual cleanup. No bearer is persisted to enable later cleanup.

Offline `TestLabMixed*` tests cover immutable execution requests, cancellation and
limiter capacity, missed-poll backpressure, atomic log-follow identity/ranges,
and complete original-event provenance. They never invoke the live test.

Slow Control/NFS/directory/APNs/database injections, worker restarts, resource
profiling and client rendering remain separately reviewable T11 phases. Their
configuration deltas, time limits, restoration proofs and process ownership must
be explicit; delaying this test client is not evidence of a slow backend.

## Recorded mixed-load result

Exact harness `9fc4420942fb50df1bb46d30fe39da0164a02c2e` (race binary SHA256
`55b68c795bb6a96a0326ec03bb3d56deb19736d675c44d8f624abd9d10ad3f7b`)
passes both90-second modes in183.70seconds. Dashboard rc.3/configuration8 and
both upgraded04bd83d Controls remain the runtime under test. There are25distinct
signed-in viewers and peak25concurrent HTTP requests, below the28limit.

| Mode | List p95 | Detail p95 | Overview p95 | Timed requests/errors |
| --- | --- | --- | --- | --- |
| One Control |1091.00ms|298.24ms|1251.58ms|1422/0|
| Two Controls |961.49ms|358.14ms|1133.48ms|1422/0|

Each mode contains414samples per metadata operation and18per investigation
operation. Across both modes, all16actual host jobs finish with twelve successes
and four intended exit7 failures. Original event/run identities, exactly one
current-authorized inbox per job and matched test rule are verified. Conservative
foreground freshness is at most4912.85ms; inbox persistence at most8825.698ms,
below10s/30s. Seven queue observations per mode include the final proof. Both
owned rules are confirmed stopped; jobs/events/inbox/logs remain as evidence.

Evidence directory: `/private/tmp/jobman-dashboard-mixed-current-zbem6a1u`.
`acceptance.json` SHA256:
`ad7e70e347b4c1e40e0fbe61c7ebe7573b1a7ab6e99a3d7fea9fbee67b085865`.
Journal SHA256:
`7ae119dc78829a69c10e6910af671cd2efabbd575bcb69d389b186f5c05f22af`.
Log SHA256:
`fb4d63bed8313a224c5702effe478eef8f54553f721d41f8b2bf6417ea56af53`.
Window: `2026-10-04T14:01:12.35362Z`–`14:04:16.052076Z`.
The earlier preflight failure remains under
`/private/tmp/jobman-dashboard-mixed-53eac75-v1`; the admission keys were unchanged
and had not been used before the passing attempt. This evidence does not establish
browser/native rendering latency, APNs, corporate identity or hard-NFS recovery.
