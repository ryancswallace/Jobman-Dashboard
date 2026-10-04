import axe from "axe-core";
import { expect } from "vitest";

// DOM accessibility regressions, not a browser, visual contrast, keyboard/AT
// device or live-source acceptance claim. axe documents that color-contrast
// cannot run in jsdom; the rendered browser matrix remains a separate gate.
export async function auditAccessibility() {
  const results = await axe.run(document.body, {
    runOnly: {
      type: "tag",
      values: [
        "wcag2a",
        "wcag2aa",
        "wcag21a",
        "wcag21aa",
        "wcag22a",
        "wcag22aa",
        "best-practice",
      ],
    },
    rules: { "color-contrast": { enabled: false } },
  });
  expect(
    results.violations.map(({ id, nodes }) => ({
      id,
      targets: nodes.map((node) => node.target),
    })),
  ).toEqual([]);
}
