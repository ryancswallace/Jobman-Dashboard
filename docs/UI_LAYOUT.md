# Web layout review

Reviewed 2026-10-11 using synthetic data in the private Dashboard Lab.

## Changes

- Shared page, card, metadata and footer insets align at each breakpoint.
- Page headings, panel actions and pagination wrap without clipping. Long job,
  namespace and rule names stay within their containers.
- Overview metric labels reserve room for their indicators and two text lines,
  aligning value baselines across cards.
- Date filters use full rows on phones; tabular records retain horizontal
  scrolling instead of squeezing short job names into single-character columns.
- Namespace/device checkboxes align beside labels. Inbox actions move below
  notification content on phones; alert cards align actions and warning insets.
- Run lists have row spacing, separated status badges and no empty pagination
  strip. Graph and output-file freshness text uses the surrounding card inset.
- Dark-mode badges and inputs follow theme tokens. The light-mode white
  logo has a dark rounded background for contrast; the connection screen
  uses the same theme-specific logos as navigation.

## Verification

Chrome review covered Overview, Jobs, collections, Slurm arrays, dependency
graphs, Targets, Inbox, Alert rules/editor, Settings, the connection screen,
and job details including run selection, logs, output files and diagnosis.
Primary viewport coverage was desktop (1728px), phone (390 × 844), and selected
light-theme overview/settings checks at 1024 × 900. Long synthetic names,
empty states, grouped actions and overflowing tables were inspected. Checked
phone pages kept document width at 390px; wide tables and diagrams scroll
within their containers. Appearance preferences and viewport overrides were
restored after review.

The production web build, formatting and existing web suite passed: 184 tests,
with two existing skips. Authenticated Lab preview and web-session smoke checks
also passed, and all seven served HTTPS assets matched the build. Independent source review found no blockers; its
alert-card double-inset observation was corrected. This is visual engineering
verification in Chrome, not comprehensive assistive-technology, Safari or
physical-device acceptance.

## Lab preview

The preview updates static assets only. API, worker and log-broker executables
remain RC10. Seven assets are deployed in an immutable directory, with a private
configuration/override rollback receipt and API readiness verification. The
final asset manifest SHA-256 is
`63e17e21abcb72575615733be1f954cf69ecda4c8c8094788608186481eee8e0`.
No migrations or changes to monitored jobs were required.

## Dropdown arrow follow-up

Single-value dropdowns share a centered arrow inset 12px from the right padding
edge and 36px of right text padding. This applies to Overview, Jobs, Settings,
watched-job selection and graph dependency direction. The controls remain native
selects for keyboard and screen-reader interaction. Forced-color mode restores
the system indicator. Multi-selects and list boxes retain native presentation.

## Timezone selection

Settings offers a native timezone dropdown plus a city, region and timezone-name
search (for example, New York, Europe or Eastern). UTC is included explicitly,
and the device timezone can be selected with one button. Search does not alter
the current selection or submit preferences; Save preferences remains explicit.
Existing timezone identifiers and aliases are preserved.

The catalog uses the browser's `Intl.supportedValuesOf("timeZone")` and its
timezone database for formatting. Older browsers use the bundled
`web/src/lib/timezones.json` fallback, generated from the pinned Node runtime's
`Intl.supportedValuesOf("timeZone")` list. Refresh this fallback when updating the
Node toolchain. The list describes timezones, not every city within each zone.
