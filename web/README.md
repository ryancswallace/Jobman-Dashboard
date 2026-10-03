# Jobman Dashboard web

React, TypeScript, and Vite client for the private Dashboard API. The production build is static and can be served by the Go Dashboard service at the same origin as `/api/v1` and `/auth`. No Node production server, external scripts/CDNs, service worker, or persistent browser API cache is required.

## Development

Use the repository's pinned Node toolchain when provided (initial local validation: Node 26.5.1, npm 11.17.0).

```sh
cd web
npm ci
npm run dev
```

Vite binds to `127.0.0.1`; its development proxy forwards `/api` and `/auth` to `127.0.0.1:8088`. Run the explicit synthetic backend separately from the repository root:

```sh
env -u GOROOT GOTOOLCHAIN=go1.26.6 \
  GOCACHE=/private/tmp/jobman-dashboard-go-cache \
  go run ./cmd/jobman-dashboard --fixture --listen 127.0.0.1:8088
```

Fixture mode is controlled by the server and is visibly labeled in the client. Network or authentication failure never switches to fixture data. The fixture environment does not establish AD FS, AD membership, Control, NFS, diagnosis, APNs, or production release acceptance.

## Checks

```sh
npm test
npm run typecheck
npm run build
npm run format:check
```

`npm test` exercises source-qualified duplicate IDs, unknown lifecycle values, cancellation intent, null counts, safe redirects, CSRF/session transport, stale-request cancellation, revoked-data clearing, bounded log buffers, stream gaps and terminal controls, plus rendered job, group and alert-editing workflows. Group tests verify source-qualified duplicate wrapper IDs, partial subtotals, exact large decimal counts, zero indices, bounded neighborhood queries, omission labels, source dependency counts and filtered edge pagination. Build output is in `dist/` and is not committed.

## Client structure

- `src/lib/transport.ts`: same-origin HTTP, no-store, cancellation, CSRF, revision and idempotency headers; distinct error classes.
- `src/lib/api.ts`: transport/application-model boundary; primary DTOs use the generated TypeScript models in `../contracts/`. The generated callable client also uses the secure transport adapter.
- `src/lib/session.tsx`: authorization freshness, account/source/scope isolation, active-scope expiry and memory purge.
- `src/lib/useResource.ts`: one active read per view, hidden-tab polling suspension, bounded retry delay, obsolete-response rejection and current-scope request cancellation.
- `src/pages/`: Overview, Jobs/detail, workloads, Targets, Inbox, alert rules, Settings.
- `src/components/LogViewer.tsx`: authorized bounded base64 reads, incremental UTF-8 decoding, plain-text rendering, offset/gap/execution checks, pause/resume and loaded-text search.
- `src/lib/workloads.ts`: generated workload/dependency/neighborhood DTO normalization; source counts and decimal values remain intact.
- `src/components/GraphView.tsx` and `GraphInspector.tsx`: bounded selected-node neighborhoods, worker layout with obsolete-result rejection, complete incoming dependency counts, paginated incoming/outgoing edge inspection and equivalent accessible lists.
- `api-contract-notes.md`: secondary endpoint field sets; `../api/openapi.json` is the authoritative contract and `make contracts-check` checks deterministic generation.

All job and namespace access remains server-enforced. Scope selection cannot grant access. Broader Control roles never add execution controls to this application.

The browser stores no API data or credentials in Web Storage. A single benign sign-out-pending flag keeps private views cleared across reloads when the server cannot confirm sign-out; a successful sign-out removes it.

## Integration and acceptance status

Implemented frontend workflows call the actual API, including secondary surfaces for logs, artifacts, reports/citations, groups, alerts, inbox, devices, and preferences. An unavailable endpoint displays a classified error. Their end-to-end completion requires corresponding production server implementations and the acceptance gates in `../docs/DESIGN.md`.

Initial browser verification against the explicit two-source fixture backend covered overview counts, distinct links for duplicate job IDs, running-phase filtering, and job detail showing unavailable lifecycle facts separately from cancellation intent/confidence. Dark-theme rendering was inspected and repaired. This is development evidence, not release acceptance. Full browser matrix, keyboard/VoiceOver/WCAG review, large-graph and NFS integration, real sessions/revocation, and all secondary end-to-end workflows remain integration gates.

The group slice also passed Chrome inspection against the built web assets served by the explicit loopback fixture API: two-source graph catalog, source-scoped summary, selected-node worker layout, bounded neighborhood lists, and source predicate states. Fixture HTTP tests cover catalog and child continuations, array task index zero, incoming edges and exact neighborhood omissions. Real delegated Control/LDAP and the full large-workload acceptance matrix remain outstanding.
