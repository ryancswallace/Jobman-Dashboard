# Graph client acceptance at the supported ceiling

T06 requires navigation of 10,000 nodes and 100,000 edges, complete source counts,
source-computed predicates/readiness, and accessible alternatives in both clients.
T10 additionally covers account/scope isolation, cancellation, keyboard navigation,
focus, display settings and assistive technology. Evidence at one layer does not
complete the others.

## Existing source evidence

Control's opt-in `TestGraphNavigationCeilingIntegration` builds a synthetic graph
in a disposable PostgreSQL schema and traverses all nodes/edges. It also checks
bounded 200-node/500-edge neighborhoods, exact omission counts, endpoint integrity
and authorization denial. The fixture creates retained metadata; it does not
submit or execute 10,000 workloads. Recorded source timings are in
[implementation status](IMPLEMENTATION_STATUS.md). This does not prove HTTP,
browser or native rendering behavior.

## Reproducible web component evidence

From `web/`, run the normal checks and the separately bounded full traversal:

```sh
npm test
npm run typecheck
npm run build
npm run format:check
VITE_GRAPH_CEILING_ACCEPTANCE=1 npm test -- \
  --run src/pages/GraphNavigation.test.tsx -t 'walks all 200'
VITE_GRAPH_CEILING_ACCEPTANCE=1 npm test -- \
  --run src/pages/GraphNavigation.test.tsx -t 'audits the full 200'
```

The synthetic source in `src/test-graph-fixture.ts` lazily produces pages for a
10,000-node/100,000-edge forward DAG: a 9,999-edge root star and 90,001 additional
forward edges. It has no credentials, jobs execute nowhere, and responses carry
explicit synthetic names. Every generated node belongs to the same fixed
source-qualified graph. Fixture totals, uniqueness, direction and index bounds
are checked independently of the React screens.

`GraphNavigation.test.tsx` mounts the actual workload detail, graph inspector and
diagram components. Requests pass through the production browser transport and
wire decoders. The bootstrap fixture also uses the production decoder; the test
injects an account context rather than claiming an actual sign-in. A test worker
adapter invokes the production layout function asynchronously. Production still
runs that function in a Web Worker; jsdom does not prove off-main-thread behavior.

The regular suite checks:

- Exact 10,000/100,000 totals, bounded 50-row child and 100-row dependency pages,
  and source-qualified job links.
- Keyboard Enter/Space recentering, programmatic `aria-current` on the diagram
  and list controls, source-provided readiness, and exact neighborhood omissions.
- Next/first child pages, next dependency pages, incoming/outgoing selection and
  cursor reset when the query changes. A new page replaces the previous rows.
- Account changes abort outstanding reads. A delayed old response cannot restore
  private rows after a new account receives an authorization failure.
- Automated accessibility checks on populated exploration and cleared/error
  states, using the existing WCAG rule set. Color contrast remains excluded only
  because jsdom cannot evaluate it.
- The actual layout function caps its output at 200 nodes, handles cycles and
  outside-page references finitely, and does not calculate source readiness.

A separate opt-in axe pass audits the full 200-node/500-edge SVG with the unchanged
rule set and a 60-second test budget; this is not a source/interaction latency gate.

The opt-in navigation test walks every page through the actual React controls: 200 child
pages and 1,000 dependency pages. It checks all 10,000 unique source-qualified job
links and 100,000 unique edge endpoint pairs, final absence of continuation,
per-response byte bounds and maximum page cardinality. It has a five-minute
ceiling and never sleeps or bypasses failed assertions. Its retained ID sets are
part of the test oracle, not a production client cache. This is component/DOM
navigation evidence, not a browser heap measurement or an API latency benchmark.

No test-only graph generator is imported by production code. The worker layout
module is the only shared helper. Passing this suite does not claim live-source,
rendered-browser, screen-reader, physical-iPhone or WCAG conformance acceptance.

## Recorded offline result

The first full-traversal oracle exceeded its unchanged 300-second ceiling while
recomputing jsdom accessibility queries for every row. That failed log is retained
at `/private/tmp/jobman-dashboard-graph-full-navigation-v1-timeout.log`. The revised
oracle retains every button transition and count/uniqueness assertion, but reads
row membership directly from the table DOM. Role, keyboard and axe assertions
remain in the regular component tests. All 1,200 pages pass in 29.98 seconds
(31.34-second Vitest run); evidence is
`/private/tmp/jobman-dashboard-graph-full-navigation-v2.log`. This timing describes
the offline test harness only; it is not a client/network latency result.

The separately bounded full SVG audit passes with all 200 node controls and 500
edges using the unchanged accessibility rules: 16.08 seconds of test execution,
17.26 seconds total. Evidence is
`/private/tmp/jobman-dashboard-graph-ceiling-accessibility.log`. The default
five-second unit-test budget was insufficient for this larger axe audit; its
separate 60-second budget does not change any product performance target or
exclude an accessibility rule.

## Remaining integrated checks

A future live profile must provide a separately reviewed immutable public receipt
for its synthetic graph, exact source instance/recovery epoch, deployment and
namespace, complete 10,000/100,000 counts, expected graph revision and known node
IDs. Provisioning that profile is a separate scoped operation; these client tests
perform no Lab mutations and do not repurpose existing execution fixtures.

Before and after real client navigation, the HTTP harness must verify the current
source fingerprint and signed account, decode current namespace authorization and
capabilities, and pin the graph identity. It must walk bounded opaque cursors,
compare complete counts and edge endpoints, reject cross-source/cross-query cursor
reuse, and test a denied account. Authorization/source failure remains an error or
explicit partial result, never a fabricated empty complete graph. A recovery
must use fresh current authority; no cached token, old grant or guessed ID may
stand in for that evidence.

Run and retain these distinct client checks after that profile is available:

| Client | Required evidence | Current boundary |
| --- | --- | --- |
| Web components | Full bounded traversal, cancellation/clear states, keyboard and axe checks above. | Synthetic wire source and jsdom; no browser or live identity claim. |
| Rendered web | First/middle/final pages; recenter from diagram and equivalent lists; dependency direction; exact omissions; back/forward and focus restoration; 200% zoom, 320px viewport, measured contrast; no increasing retained DOM/heap across page replacement. | Previous browser automation returned `ERR_BLOCKED_BY_CLIENT`. Do not retry another browser, proxy or origin to bypass that block. Use an approved browser environment or user-run manual checks. |
| Web reader | Landmarks/headings, source context, current-node state, list/table relationships, predicate/readiness announcements, errors, no focus loss/trap. | Requires the supported real browser/screen reader; axe alone is insufficient. |
| Native simulator | A separate large DEBUG fixture with complete totals and bounded 200/500 neighborhood; paginated node/edge navigation, direction/recenter, accessible list alternative, Dynamic Type and Reduce Motion screenshots. | Existing native graph UI test uses a small graph. A large-fixture simulator result remains separate from real HTTP/device acceptance. |
| Native live/managed phone | Same verified source graph and account, VoiceOver/Voice Control, supported text/contrast/motion settings, background/foreground cancellation, actual VPN loss/recovery and current authorization clearing. | Requires real device, identity/network prerequisites and an approved internal build. |

Use the detailed [accessibility matrix](ACCESSIBILITY.md) and retain exact source
commit, build, OS/device/browser, fixture/receipt digest, command, result and failed
attempts for each layer. Record every unperformed check as open. Do not infer
client acceptance from source query timings or small executed Slurm graphs.
