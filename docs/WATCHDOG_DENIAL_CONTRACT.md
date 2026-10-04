# Watchdog acceptance denial contract

The first watchdog HTTP preflight on October 4, 2026 failed before `begin`:
it expected HTTP 404 for Bob's access to the accepted operations job's logs.
No timer was armed and no service was stopped by that attempt. Preserve its
failed receipt and one-shot runner; a corrected attempt requires a fresh plan.

The deployed `9b1c65e31db8a849ebe2dfa00caf4474bef8e7d2` contract distinguishes
these reads:

| Read | Required denial |
| --- | --- |
| Source-qualified job logs without the namespace capability | 403 `forbidden`, “This scope is not currently authorized.” |
| Inaccessible retained report or another account's inbox item | 404 `not_found_or_inaccessible`, “The resource is absent or inaccessible.” |

The watchdog harness now checks these exact route-specific contracts before and
after the timer. Both require `Cache-Control: no-store`, no cookies, a canonical
request ID and a strict error envelope. A 403 never substitutes for a report or
inbox 404, and a 404 never substitutes for the log capability denial. A regression
uses the actual Dashboard HTTP handlers and rejects the original incorrect
expectation, crossed routes and duplicate error fields. No production behavior
changes are needed for this correction.

The separate private diagnostic passed against the deployed service in 0.06
seconds: synthetic Bob native sign-in followed by two GETs confirmed exactly
403 `forbidden` for the operations log and 404 `not_found_or_inaccessible` for
the report. Both responses passed the strict-envelope and no-store/no-cookie
checks. The diagnostic recorded only status, an allowlisted error code,
privacy/shape booleans and a response hash; it printed no credentials or response
content. Its retained log SHA-256 is
`23790668b7b5bf5209d9880f5b52b8695569884d68db6f63537762024e65fd9c`.

This confirms the denial contract, not a successful live watchdog intervention.
Keep the failed preflight, this read-only diagnostic and the independently
reviewed fresh timer attempt as separate evidence.
