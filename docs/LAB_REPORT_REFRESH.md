# Refresh retained acceptance reports after a source upgrade

A sealed report retains the versions and snapshot identity used to create it.
After the independently verified Control upgrade from `d332a2b5` to `04bd83d`,
the original actual-Slurm reports remained readable with their exact job/run/
execution and citations, but correctly exposed `outdated: true`. The first mixed
load attempt stopped at that precondition before creating any rule or workload.
Its receipt and journal remain under
`/private/tmp/jobman-dashboard-mixed-53eac75-v1`.

`TestLabRefreshAcceptedSlurmReports` prepares two current diagnosis reports for
the **same previously accepted completed Slurm execution**. It does not submit,
cancel or execute a source job. Root must coordinate this operation after source
upgrades, outside load/fault/configuration changes, and independently review the
exact helper before invocation.

The original full execution receipt is pinned to SHA256
`186719a51c266e8d48157a508d5e485382811e7a60b1fbc32b1a2f1ab2107801`.
The helper validates its original execution against current source-qualified
job/run/owner/target facts. Both old reports must be ready, on that exact revision
and run, and explicitly outdated from an older Control version. The helper
records their complete semantic hashes and every bounded sealed citation hash.

It admits metadata and include-log-tail reports with the fixed keys
`slurm-report-refresh-control04bd83d-v1-metadata` and
`slurm-report-refresh-control04bd83d-v1-include_log_tail`. Intent and returned task
IDs are synced before further work. One explicit idempotency assertion follows
a confirmed admission; an uncertain request stops the test without retry.
Existing actual-Slurm assertions validate deterministic findings, exact redacted
bytes/offsets, source and execution identity, no provider invocation and Bob's
denial. The new reports must be fresh and identify Control version
`dashboard-lab-04bd83db28bc`.

Before and after preparation, original source/log bytes and job facts must match.
Old report/citation hashes and the original receipt must remain unchanged. A new
derived receipt changes only the report IDs and adds a separate `reportRefresh`
record. It retains the original execution verification timestamp rather than
claiming that the entire earlier workload suite was executed again. Load and fault
harnesses must explicitly pin this new reviewed receipt; freshness assertions
are not removed.

Use a new canonical absolute output path. The overall test has a five-minute
ceiling and a four-minute operation context. Preserve the result and progress
journal on failure; do not replace keys or retry an uncertain admission.

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_REFRESH_SLURM_REPORTS=1 \
JOBMAN_DASHBOARD_LAB_REFRESH_ORIGINAL=/private/tmp/jobman-dashboard-actual-slurm-complete-acceptance-v1.json \
JOBMAN_DASHBOARD_LAB_REFRESH_RESULT=/private/tmp/slurm-refreshed-unique.json \
env -u GOROOT GOWORK=off GOTOOLCHAIN=go1.26.6 \
go test -race -tags integration ./internal/auth \
  -run '^TestLabRefreshAcceptedSlurmReports$' -count=1 -timeout=5m -v
```

This preparation does not establish a new workload run, APNs, corporate identity,
browser rendering, device acceptance, or a passing mixed-load result.

## Recorded result

The reviewed frozen helper passes in3.23seconds on October4. The original job
revision7, exact execution and raw stderr remain unchanged; seven historical
report/citation semantic hashes match. New reports identify
`dashboard-lab-04bd83db28bc` and are current. Derived receipt:
`/private/tmp/jobman-dashboard-slurm-report-refresh-control04bd-v1.json`, SHA256
`929a9e93e83069dc330734b699bd6077432507397e56ec484367c6af954be530`.
Its original execution fields and verification timestamp are unchanged. The
separate refresh timestamp is `2026-10-04T13:55:57.856422Z`; no source jobs ran.
Bounded log and progress journal share the same path prefix.
