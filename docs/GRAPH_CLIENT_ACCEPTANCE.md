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
  and list controls, source-provided job/dependency states, and exact neighborhood omissions.
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

## Native synthetic profile

The DEBUG-only native profile uses `--dashboard-ui-fixtures` and
`--dashboard-graph-ceiling-fixtures`. `NativeGraphFixtures.swift` generates the
same complete 10,000-node/100,000-edge topology lazily, with bounded 50-child,
100-dependency and 200-node/500-edge neighborhood responses. Its base64 fixture
cursors bind the graph, route, node and direction; they are test protocol values,
not a claim of production cursor authentication. All counts and job/dependency states come from
this explicit synthetic source. No executor, real sign-in, source query or APNs
operation occurs.

The core tests compile that exact DEBUG source through a test-only symlink. They
independently enumerate unique forward edges and incoming counts, decode all
10,000 children and all 100,000 dependencies, validate source-qualified models,
verify maximum neighborhood/layout bounds and exact omissions, and reject
cross-query cursors/oversized limits. They do not retain the graph in the
application; retained uniqueness sets belong only to the test oracle.

The simulator tests use the real URLProtocol transport, workload screens,
detached layout and SwiftUI diagram/list. Pagination appears above long lists so
50-node/100-edge pages can be replaced without scrolling past every row. Selected
nodes expose the selected accessibility trait in both diagram and list. Screenshots
and assertions distinguish complete source totals from loaded page/neighborhood
counts, source job/dependency states and exact dependency predicates.

Run core tests from `ios/`, then the focused simulator class:

```sh
./scripts/test-core.sh
xcodebuild -project JobmanDashboard.xcodeproj -scheme JobmanDashboard \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath /private/tmp/jobman-dashboard-graph-ui \
  -only-testing:DashboardUITests/GraphCeilingUITests \
  -collect-test-diagnostics never CODE_SIGNING_ALLOWED=NO test
python3 scripts/test-graph-accessibility.py --device <booted-iPhone-simulator-UUID>
```

The opt-in display runner records and restores only that simulator's actual text
size and Reduce Motion settings, even after failure. A DEBUG-only label reports
the actual SwiftUI environment; the test requires `accessibility5` and Reduce
Motion enabled before exercising list recentering/dependency navigation. The
standard suite skips this one profile rather than silently pretending to test
large text with default settings. Settings snapshots and result bundles remain
in the runner's printed temporary directory. No production accessibility setting
is overridden. No other process should change the same simulator concurrently.

These are native decoder/simulator observations. Full traversal is a core wire
contract test, not 10,000 UI taps; screenshots do not establish VoiceOver reading
order, Voice Control, physical-device performance, measured contrast or live
source/account behavior. Those gates remain separately open.

### Recorded native result (October 4)

Xcode 27.0 (27A266a), iPhone 18 Pro simulator / iOS 27.0:

- All 70 core tests pass, including five ceiling fixture/contract groups. The
  complete 100,000-edge decoding group takes 1.912 seconds in that run; this is
  test-harness time, not a live API performance result. Log:
  `/private/tmp/jobman-dashboard-native-graph-all-core.log`.
- The three standard UI workflows have passing focused results. Child and
  dependency navigation are in `jobman-dashboard-native-graph-focused-v1.xcresult`;
  maximum diagram/recentering is in `jobman-dashboard-native-graph-focused-v2.xcresult`,
  both under `/private/tmp`. First diagram failure remains in v1: a row-center
  XCTest tap did not turn the switch off. The corrected test targets the switch
  and asserts its state before list scrolling.
- The actual `accessibility5`/Reduce Motion profile passes in 47.579 seconds.
  Evidence is in the temporary directory ending
  `jobman-dashboard-graph-accessibility-3lka1sk5`: before/after values match
  (`large`, Reduce Motion `0`), and `result.json` confirms restoration. Two prior
  bundles (`...-c87agvk8`, `...-x08krtkd`) retain test-scrolling failures and successful
  restoration; assertions and display settings were not relaxed. Recenter resets
  the screen to its top, and virtualized rows must be scrolled into view before
  querying them at the largest text size.
- Standard and enlarged-text screenshots were exported and inspected. Source
  totals/omissions, selected readiness, predicates and wrapped list controls are
  visible. Long IDs wrap in the list; the diagram remains a bounded scrollable
  overview with a complete list alternative. This does not measure contrast or
  establish screen-reader behavior.
- The unsigned Release simulator build passes; its executable contains none of
  the graph fixture/diagnostic markers. Log:
  `/private/tmp/jobman-dashboard-native-graph-release.log`.

### Source-contract correction

A follow-up comparison with Control's `GraphNeighborhood` implementation found
that both initial ceiling fixtures incorrectly used whole-graph totals after
recentering. Totals and omissions describe the complete **one-hop induced
neighborhood**, before node/edge limits. They equal 10,000/100,000 at the star
root, but node 1 has 12 nodes/66 edges and the last node has 2 nodes/1 edge.
Independent web/native regressions now enumerate the 12-node forward pairs,
check exact 66-edge membership and verify truncated 3-node/2-edge results retain
12/66 totals and 9/64 omissions. The native label now says “Neighborhood totals”
and omissions say “Not shown in this neighborhood.”

The current source provides four dependency counts (`total`, `satisfied`,
`waiting`, `unsatisfied`) and no separate node `readiness` value. These synthetic
unexecuted jobs are `accepted`, their success-predicate dependencies are
`waiting`, and they have no disposition/current run. Both fixtures now use those
wire facts. The native fixture also accepts the actual empty-direction “Both”
query and the source's 500-edge maximum. The earlier screenshots and recentered
fixture assertions above are preserved but superseded for these semantics;
whole-root bounds and the prior generic UI workflow evidence remain valid.

Before this correction the full simulator regression completed 28 tests: 27
passed, and the separate display profile was explicitly skipped; zero failures
in 620.507 seconds. Evidence is
`/private/tmp/jobman-dashboard-native-graph-all-ui.xcresult` and the adjacent log.
No repeated full generic UI run is required for these fixture/label corrections;
affected core, graph UI and enlarged-text checks are rerun separately.

Two hosted runs of the initial web tests hit their ordinary five-second budgets
for combined navigation/oracle work. Those logs remain at
`/private/tmp/jobman-dashboard-output-bound-ci-failed.log` and
`/private/tmp/jobman-dashboard-output-bound-pr-ci-failed.log`. The corrected wire
fixture uses integer adjacency rather than constructing 100,000 edge objects on
every selected-node request. The full-document populated axe audit is now an
independent test from the keyboard workflow, retaining the same rules and
ordinary five-second budget for each. Product latency targets are unchanged.

The corrected exact-source checks pass:

- All 71 native core tests (six ceiling groups), 1.795 seconds:
  `/private/tmp/jobman-dashboard-native-graph-correction-core-final.log`.
- Three affected standard simulator workflows pass, with one explicit separate
  profile skip, zero failures in 88.648 seconds:
  `/private/tmp/jobman-dashboard-native-graph-correction-ui-final.xcresult`.
  The dependency workflow also selects the actual empty-direction Both option.
- The actual largest-text/Reduce Motion workflow passes in 53.673 seconds;
  `jobman-dashboard-graph-accessibility-9sggavt1/result.json` confirms exact
  restoration with no errors. Corrected standard and enlarged screenshots were
  exported for visual inspection. A preceding corrected profile also passed in
  53.758 seconds; the final run additionally includes the policy/default-limit
  alignment and Both-direction regression.
- Web default suite: 135 passed, two explicitly skipped opt-in checks, 9.85
  seconds. All nine graph checks including the complete 1,200-page walk and
  full SVG axe audit pass together in 61.96 seconds. Logs:
  `/private/tmp/jobman-dashboard-graph-correction-web-full-v2.log` and
  `/private/tmp/jobman-dashboard-graph-correction-web-ceiling.log`.
  The retained local `...-web-full.log` records the combined keyboard/axe budget
  failure before those checks were separated; neither check lost assertions.
- Type checking, production web build, formatting, and the unsigned Release
  simulator build pass. The Release executable contains none of the four
  graph-fixture/diagnostic markers.

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
| Native simulator | A separate large DEBUG fixture with complete totals and bounded 200/500 neighborhood; paginated node/edge navigation, direction/recenter, accessible list alternative, Dynamic Type and Reduce Motion screenshots. | The large DEBUG profile has focused simulator evidence below; real HTTP, physical-device and VoiceOver acceptance remain separate. |
| Native live/managed phone | Same verified source graph and account, VoiceOver/Voice Control, supported text/contrast/motion settings, background/foreground cancellation, actual VPN loss/recovery and current authorization clearing. | Requires real device, identity/network prerequisites and an approved internal build. |

Use the detailed [accessibility matrix](ACCESSIBILITY.md) and retain exact source
commit, build, OS/device/browser, fixture/receipt digest, command, result and failed
attempts for each layer. Record every unperformed check as open. Do not infer
client acceptance from source query timings or small executed Slurm graphs.
