# Client accessibility acceptance

Accessibility is part of T06 and T10 release acceptance. Automated DOM checks,
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

Automated DOM evidence is complete for the listed cases. The current browser
automation environment rejected the live preview with `ERR_BLOCKED_BY_CLIENT`;
no alternate-origin/browser bypass was attempted. Manual rendered-browser and
assistive-technology checks remain open. Existing simulator workflow passes
are recorded separately in `IMPLEMENTATION_STATUS.md`; they do not satisfy
physical or company-managed iPhone accessibility acceptance.
