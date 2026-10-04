# API authentication-master rotation acceptance

`TestLabAPIAuthenticationMasterRotation` is an opt-in real HTTP protocol and
private-Lab service test. It uses actual Keycloak sign-in, the deployed BFF,
PostgreSQL session records, retained paired reports/citations, and NFS broker
reads. It does not use a browser driver, change host trust/DNS, or establish
corporate AD FS, APNs, managed-iPhone or rendered-browser acceptance.

The Lab's reviewed `rotate-dashboard-auth.py` must first capture the final
configuration8 baseline, produce a reviewed plan, complete fresh preflight and
stage the new private key/drafts. No active API change has happened at that point.
Archive all four implementation/dependency files listed in the Lab runbook and
keep the exact private staging directory. Complete scale and mixed-load tests
before this separate operational exercise. Coordinate exclusive API/configuration
access; source/feed authority, delivery hold and unrelated processes remain pinned.

The test performs these checks:

1. Establishes a real old-key browser session and compares its immutable account
   and preferences with independently verified native identity. The session must
   have at least three minutes of remaining lifetime before rotation.
2. Captures two existing ready reports from a retained actual-subprocess execution,
   all bounded include-log report citations, original NFS bytes and a forward log
   cursor. It neither submits a job nor generates a replacement report. A prior
   Control upgrade may mark a retained report outdated; its flag, version, IDs
   and entire projection must remain identical across authentication rotation.
   Current-report freshness is verified separately in `LAB_REPORT_REFRESH.md`.
3. Starts another browser login under the old key, retaining its state/cookie and
   authorization URL only in memory. The identity-provider step is deliberately
   completed after rotation, producing a fresh code; code expiry cannot masquerade
   as rejection of an old encrypted login state.
4. Runs only the exact reviewed driver `apply`, `restart` and `verify` phases.
   These change API authentication configuration and restart only the API.
5. Requires401 for the nonexpired old session and for the pending old login,
   with no new session cookie. Requires a fresh new-key browser login to succeed
   for the same immutable account and unchanged preferences. The existing signed
   native credential must still resolve that account.
6. Requires original report detail and citation bytes/provenance to remain exact.
   The separately protected NFS log bytes and pre-rotation forward cursor must
   still work under the new browser session.

Cleanup is registered before any login begins. It attempts ordinary logout for
every possibly admitted session, even when an assertion or callback reply fails.
An abandoned pending login is consumed through the normal callback using only
that test's retained state and a fixed invalid provider code. Lost credentials or
unavailable cleanup are reported as uncertainty; they are never called revoked
without checking. Cookies, tokens, passwords and provider responses remain in
memory and are never printed or saved. Cleanup **does not restore the retired
authentication key**. An uncertain rotation retains private receipts and requires
forward recovery with the same new key.

Use exact reviewed paths/digests and a prior successful actual-subprocess receipt
containing `jobs.failure` plus `reports.metadata` and `reports.include_log_tail`:

```sh
JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
JOBMAN_DASHBOARD_LAB_RUNTIME=1 \
JOBMAN_DASHBOARD_LAB_AUTH_ROTATION=1 \
JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_DRIVER=/absolute/archive/rotate-dashboard-auth.py \
JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_STAGING=/absolute/private/rotation \
JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_PLAN_SHA256=REVIEWED_PLAN_SHA256 \
JOBMAN_DASHBOARD_LAB_AUTH_ROTATION_IMPLEMENTATION_SHA256=REVIEWED_IMPLEMENTATION_SHA256 \
JOBMAN_DASHBOARD_LAB_EXECUTION_RECEIPT=/absolute/accepted-execution.json \
env -u GOROOT GOTOOLCHAIN=go1.26.6 go test -race -tags integration \
  ./internal/auth -run '^TestLabAPIAuthenticationMasterRotation$' -count=1 -timeout=6m
```

This is a one-time test: after any apply intent exists, it refuses a new baseline
run. Inspect retained driver receipts and use the Lab's reviewed observation or
continuation workflow instead of regenerating keys or quietly retrying.

The current crypto format has no readable record key ID. Both retired-key and
malformed current-key AEAD/MAC checks fail as invalid credentials401. Real
transport/store failures remain503. The local handler regression explicitly
checks that distinction and that a new-key callback/session still succeeds.

Offline tests with all live opt-ins unset:

```sh
env -u JOBMAN_DASHBOARD_LAB_ROOT -u JOBMAN_DASHBOARD_LAB_RUNTIME \
  -u JOBMAN_DASHBOARD_LAB_AUTH_ROTATION -u GOROOT GOTOOLCHAIN=go1.26.6 \
  go test -race -shuffle=on -tags integration ./internal/auth \
  -run '^TestLabRotation' -count=1
```

Local handler tests use only a loopback TLS fixture. A passing offline test or
prepared plan is not evidence that the live rotation occurred.
