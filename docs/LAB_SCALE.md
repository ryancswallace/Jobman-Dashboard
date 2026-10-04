# Deployed accepted-scale validation

Status: **harness implemented; integrated fixture preparation and execution are
pending**. The source repository measurements in `IMPLEMENTATION_STATUS.md` are
separate evidence. Neither those measurements nor this harness establishes web
or iPhone rendering latency, corporate AD FS behavior, or a production capacity
guarantee.

The accepted envelope is ten namespaces, 10,000 retained jobs per namespace,
500 active jobs and 25 concurrent viewers. The deployed HTTP test uses 25 distinct
synthetic Keycloak subjects, each with its own signed immutable directory GUID,
native S256 sign-in and Dashboard account. Actual Control directory reconciliation
must give them only direct viewer grants to the test namespaces. Interactive
identity-provider time is outside the timed data requests.

## Fixture and isolation

The proposed additive fixture uses ten new namespaces in the existing isolated
primary Control and five in the independently provisioned secondary Control.
Each has 10,000 explicitly imported synthetic success records and 50 admitted
pending jobs on a dedicated target without an enrolled execution agent. This is
metadata and query acceptance; the fixture must not pretend these jobs executed.
The same namespace names, `dashboard-scale-01` onward, occur in both Controls.
Their database-generated namespace/job IDs and source identities stay independent.

The one-Control case selects all ten primary namespaces. The two-Control case
selects primary01–05 and secondary01–05. Each selected scope therefore has exactly
100,000 retained records and 500 active jobs. The complete databases contain more
fixtures, including the original investigation workloads; those are outside the
selected scope and must not be deleted or changed to simplify counts.

Prepare the fixture through separately reviewed Lab/Control helpers before
enabling the test. Required boundaries:

- Create only the 25 named `dashboard-scale01`–`dashboard-scale25` Keycloak users,
  with fresh private passwords and GUIDs in the fixed `74000000-0000-4000-8000-…`
  range. Preserve existing accounts, clients, mappers and realm configuration.
- Create only new scale namespaces, targets and jobs. Bootstrap through normal
  Control operations, then adopt the new namespaces through the ordinary direct
  directory mapping and reconciliation path. Do not manufacture verified grants,
  change existing users' memberships, or seed Dashboard account rows.
- Bulk imported history must carry imported provenance, have no claimed run or
  execution, and emit no invented terminal event. Admit active jobs through the
  ordinary Control store API. Record exact counts and source instance/epoch.
- Extend existing source trust and Dashboard namespace allowlists additively,
  with reviewed current-config comparisons, monotonic revisions and explicit
  event-feed recovery if the namespace binding changes. Preserve delivery hold
  state and all previous configurations/receipts. Never reset a feed or silently
  replay historical alerts.
- Check host/guest/database capacity first; keep resource and operation bounds.
  Retain partial preparation evidence. No full-Lab reset, production identity
  action or automatic cleanup of preexisting data is part of this fixture.

The public `.lab/dashboard/scale-fixture.json` uses the strict `labScaleFixture`
shape in `internal/auth/lab_scale_test.go`: version1, synthetic flag, mode
`imported-history-no-execution`, UTC preparation/history timestamps, 25 user
records and two source records. Each source contains its deployment, independent
instance, recovery epoch, exported public CA hash and namespace records with admitted/imported sample
job IDs and counts. It contains no passwords or tokens. Programs load passwords
using the Lab's private Dashboard credential mechanism; credentials never enter
the public receipt or test log.

Public CA files are copied through the reviewed fixture procedure to
`.lab/dashboard/scale/control-primary-ca.crt` and `control-secondary-ca.crt`.
The harness verifies their exact hashes and calls each pinned Control's actual
TLS capabilities endpoint before and after each timed mode. Signed OIDC subjects
must match the fixture subjects exactly; syntactically valid fixture IDs alone
are insufficient evidence.

## HTTP measurement

`TestLabDeployedAcceptedScale` checks each real account's bootstrap and fresh
viewer grants, then runs 25 concurrent readers. Each reader performs eight
iterations of a 50-row aggregate list, source-qualified job detail and complete
aggregate overview, for 200 samples per operation in each source mode. Detail
requests alternate between admitted and imported jobs. List validation checks
ordering, duplicate IDs, bounded rows and exact source scope. Overview validation
requires all500 active and100,000 terminal records, with no missing contribution
reported as zero.

After the timed modes, one namespace in each Control is traversed completely in
pages of at most200 jobs. Both10,050-job traversals check cross-page ordering,
duplicate IDs, source identity, admitted/imported provenance and exact totals.
These sequential correctness reads are excluded from the latency sample.

Timings cover the HTTPS request, bounded read, decode and validation. Every
sample, including a failed request, stays in percentile accounting. Any HTTP,
authorization, completeness or count error fails the run; any operation above
the2s p95 HTTP budget also fails. A failed first mode stops further load and
retains the partial result. Passing HTTP response times leave the client-render
portion of the2s first-useful-view acceptance target unverified.

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_SCALE=1 \
JOBMAN_DASHBOARD_LAB_SCALE_RESULT=/private/tmp/dashboard-scale-unique-result.json \
GOWORK=off GOTOOLCHAIN=go1.26.6 \
go test -tags=integration -race -run '^TestLabDeployedAcceptedScale$' \
  -count=1 -v ./internal/auth
```

Use a new result path for every attempt. The result records the public fixture
hash, timestamps, completed identity count, sample/error counts, p50/p95/max
latencies, maximum response bytes, full-traversal totals and whether the run passed. Record the exact
running Dashboard/Control commits, configuration revisions, resources, source
identity checks and CI alongside the result. A stale fixture receipt alone does
not prove that the deployed source or configuration still matches it.

Additional T11 acceptance still required: simultaneous log follows and group
drill-downs, terminal-event bursts, slow dependency behavior, memory/queue bounds,
and actual client rendering/accessibility under load. Keep those results distinct
from this metadata HTTP slice.
