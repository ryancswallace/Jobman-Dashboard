# Client accessibility acceptance

Accessibility is part of the T06 and T10 validation roadmap. Additional manual
acceptance is scheduled after the first production release under the
[approved scope](FIRST_RELEASE_SCOPE.md). Automated DOM checks,
browser rendering and assistive-technology tests are separate evidence. Record
the exact build, OS, browser/device, display settings and observed result for
each manual check below; an unperformed check remains open.

## Automated web regression checks

Run from `web/`:

```sh
npm ci
npm test
npm run typecheck
npm run build
npm run format:check
```

The pinned development dependency `axe-core` runs against the real React
components in jsdom with synthetic API responses. `App.a11y.test.tsx` covers
fourteen routes, open scope and rule editors, unavailable-source announcements,
skip-link entry and route focus. Additional component tests cover populated
inbox list/detail, owned-device settings/edit/removal, deterministic findings
and sealed citations, and keyboard activation of graph nodes. The full web
suite has 126 passing tests at this checkpoint, including 22 added cases.

The shared test helper enables WCAG 2 A/AA, 2.1 AA, 2.2 AA and best-practice
rules. Only `color-contrast` is disabled because jsdom cannot evaluate it;
the [axe maintainer documentation](https://github.com/dequelabs/axe-core)
describes this limitation. Passing these checks does not establish WCAG
conformance, a complete accessibility tree, visual contrast or actual
screen-reader behavior. The dependency is test-only and is absent from the
production JavaScript bundle.

The new graph regression found an SVG marked as a single image containing
focusable buttons. The container now exposes a labeled group so its node
controls retain their semantics. WAI documents that the `img` role makes
descendants presentational in its
[ARIA authoring guidance](https://www.w3.org/WAI/ARIA/apg/practices/hiding-semantics/).
Paginated node and dependency lists remain the complete navigation alternative.

## Manual acceptance matrix

Run each relevant workflow with the deployed candidate and current authorized
synthetic accounts. Use only synthetic content in retained screenshots or
screen-reader transcripts. Do not retain tokens, private configuration or
workload content from another environment.

| Surface | Procedure | Required result |
| --- | --- | --- |
| Web keyboard | Enter via Tab, use the skip link, visit every navigation item and detail/back route, edit scope/rules/device settings, inspect logs, reports and graphs. | Visible focus; meaningful traversal; no trap; new route has a meaningful focus target; Enter/Space operate controls; every operation has a keyboard path. |
| Web screen reader | With the supported browser/OS reader, traverse landmarks/headings and read tables, counts, source context, labels, validation, unavailable/partial states and asynchronously loaded results. | Names and roles match the operation; errors and status updates are discoverable; visual-only distinctions have textual equivalents; refresh does not repeatedly steal focus. |
| Web graph | Select a node by keyboard and reader; use the node, incoming/outgoing dependency and neighborhood lists, including more than one page and omitted counts. | Same source identities, predicates and selection are available without interpreting a diagram or color. |
| Web display | Inspect light/dark/system modes, browser zoom to 200%, a 320 CSS-pixel viewport and text enlargement. Measure text/control/focus contrast in the rendered browser. | Content reflows without loss; controls remain usable; necessary two-dimensional views have usable scrolling and list alternatives; applicable WCAG AA contrast criteria pass. |
| iPhone VoiceOver | Sign in and visit overview, jobs, workload groups, target detail, logs, diagnosis/citations, rules, inbox and device settings; open a private notification route. | Correct labels, traits, reading order and selected state; complete graph/list alternative; meaningful loading/errors; no inaccessible operation. |
| iPhone display and input | Use largest supported Dynamic Type, increased contrast, Reduce Motion, light/dark mode and Voice Control on a representative company-managed phone. | Content and actions remain reachable, labels do not obscure values, motion is not required to understand status, named controls can be activated. |
| Access changes | Remove an actual direct role grant while a private screen is open, then restore it; exercise network/VPN loss and recovery. | Cleared private content cannot be read from retained UI; current errors/status are announced without presenting stale data as authorized. |

The earlier deployed preview attempt returned `ERR_BLOCKED_BY_CLIENT`. The
loopback check below uses the explicitly synthetic fixture. Existing simulator
workflow passes are recorded separately in `IMPLEMENTATION_STATUS.md`.

### Rendered synthetic web check, 2026-11-09 UTC

Chrome rendered the loopback fixture with two synthetic deployments. Keyboard
activation of the skip link initially changed the fragment without moving
focus. At 320 CSS pixels, the wide job table could overflow inside a container
that lacked a keyboard focus target. After the changes, Enter focused the main
landmark, and ArrowRight moved the labeled job results region 40 CSS pixels.
The 320-pixel document had no page-level horizontal overflow. The 830-pixel
table scrolled inside a 282-pixel region.

Keyboard activation moved focus to the heading for all seven workspace
routes and a source-qualified job detail. The detail and list both displayed
the stale confidence as “Outdated” with explanatory text. Fixture settings,
rules and inbox showed the unavailable-service state in an alert region.
Sampled rendered dark-theme text contrast was 14.53:1 for the main heading,
6.01:1 for active navigation, 6.69:1 for inactive navigation and 6.88:1 for the
stale-status badge. The focused main landmark and table displayed a
three-pixel outline.

A separate Chrome 156 profile rendered the Jobs page with the system light
scheme. Six sampled text pairs ranged from 5.11:1 for the warning badge to
14.55:1 for the heading. Setting Chrome's page zoom to 200% reduced the
1280 CSS pixel viewport to 640 CSS pixels without page-level horizontal
overflow. Tab and Enter activated the skip link and focused the main
landmark with its three-pixel outline.

The native core ran 91 tests and the unsigned iOS 27 simulator build passed.
At the largest Dynamic Type setting, the initial graph journey failed to reach
the group type selector. Job groups now places the type choices above scope
and presents separate selected-state buttons at accessibility sizes. The
opt-in iPhone 18 Pro simulator test passed with maximum accessibility text and
Reduce Motion in 87 seconds. It reached the graph's node list and dependency
controls and restored both simulator settings.

Live screen-reader speech, a full contrast matrix across screens and states,
deployed preference and access changes, and managed iPhone VoiceOver remain
open. The Chrome accessibility tree supplied roles and names for inspection.
Physical device behavior needs its own acceptance record.
