# Actual subprocess acceptance

The opt-in integration harness uses normal authenticated Control APIs and a
separate real Jobman agent to exercise Dashboard against executed synthetic jobs.
It does not insert observations, edit source databases or rewrite log manifests.
The original Lab agents and services remain intact.

## Prerequisites and scope

Follow Jobman-Lab's `DASHBOARD_EXECUTION.md` to provision the isolated
`dashboard-execution-host` target and preserve its public enrollment receipt.
Apply its exact target-generation mapping to both Dashboard and the log broker
with current configuration hashes and increasing revisions. Check normal source,
directory, NFS and Dashboard health before submitting workloads. Coordinate the
test with service upgrades, backup/restore and membership-change exercises.

The harness admits exactly eleven fixed-idempotency jobs: four individual
success/failure/timeout/cancellation jobs, a three-child collection and a
four-node dependency graph. Each workload has one run at most, a timeout no
longer than 30 seconds, and bounded agent log/artifact output. The collection
uses individual execution, concurrency two and continuation after failure. The
graph exercises failure, any-terminal and success predicates, including one
unsatisfied branch that must be skipped without an execution record.

The cancellation applies only to the named synthetic job after a real running
observation. Retried requests retain the same sealed documents and idempotency
keys. Do not change those keys to conceal a failed acceptance run. Preserve the
jobs, original events, source bytes and public receipts for investigation.

## Run

Use a new absolute result path. It contains only public fixture/job/group/report
identities and a verification timestamp; bearer tokens remain in memory.

```sh
env -u GOROOT GOWORK=off GOTOOLCHAIN=go1.26.6 \
  JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
  JOBMAN_DASHBOARD_LAB_EXECUTION_RUN=1 \
  JOBMAN_DASHBOARD_LAB_EXECUTION_RESULT=/absolute/new-public-result.json \
  go test -tags integration -race -count=1 -v ./internal/auth \
    -run '^TestLabActualHostWorkloadsAndDiagnosis$' -timeout 8m
```

`TestLabActualExecutionRequestsBoundedAndStable` checks request sealing and
bounds offline. The actual test has a six-minute overall context and bounded
per-state waits. Without the explicit run variable it skips without submitting.

## Recorded passing execution

The actual scenario passed on 2026-10-04 at 06:06:25 UTC in **29.38 seconds**
(race package 30.714 seconds). Source instance
`e633cf92-258d-48ff-965a-fda88d68ef3a` and recovery epoch1 were unchanged.

| Component | Exact tested identity |
| --- | --- |
| Dashboard and broker | `6aa32eb89989e79e9f5bb31a59a04801fdeca961`; combined API config4, broker config3 |
| Control | `d332a2b569333ae8aed9c2fc9ebc648d8eb5ba4e`; migration21 |
| Actual agent | Core `21701b191cd4e4e063d26cad7dae31c35db6bc8c`; Linux arm64 SHA256 `e955472e6174a03503548137397328a4570e8e257372ac4409ed75a8130dfa72` |
| Target generation | `3ae8ed72-6d05-4af6-9fce-2f1c54df4b02` |
| Collection | `b14c7512-8c10-42fa-bc1e-db62b21e77fd` |
| Dependency graph | `03c9e4f4-db13-4142-8d10-2a90845db54d` |
| Failed job | `4cb16535-7551-4312-8b84-29549e880ebb` |
| Metadata report task | `b9a060f0-d12f-44ce-a35e-0e4c256134df` |
| Redacted-log report task | `13e8df55-55be-4f67-905c-a698a03df6e5` |

The test verifies actual process-start/completion provenance, original run and
execution IDs, outcomes, current owner and source-qualified Dashboard detail.
Collection children and graph edges paginate without duplicates or missing
rows; bounded graph neighborhoods report exact omitted counts.

Producer manifest identities, lengths and checksums match known command output.
Both nonempty stderr and empty stdout pass through the deployed mTLS broker and
NFS reader. Diagnosis produces real deterministic metadata and redacted-log
reports, preserves sealed source/run/evidence identities, and returns exact
citation bytes and original offsets. Re-reading both original streams after
diagnosis proves the source output remains unchanged. Bob's unauthorized job,
log and report requests are denied throughout.

Evidence files are retained locally:

- `/private/tmp/jobman-dashboard-actual-host-acceptance-v1.json`, SHA256
  `defe8baf9deec9cb17e3f62b9ddcea6051ac82d50787ab269ef8a7b689910241`.
- `/private/tmp/jobman-dashboard-actual-host-acceptance-v1-run.log`, SHA256
  `4919d7f72de298b83eed0c516ae9403558f728f1c7e281c06e7ca9a2cb1560cc`.

These results establish actual subprocess integration. Slurm arrays, the new
split-runtime deployment, two-Control aggregation, accepted-scale load, browser
rendering, actual AD FS and physical APNs/managed-phone acceptance are separate
gates. The test's synthetic Keycloak sign-in does not establish AD FS acceptance.
