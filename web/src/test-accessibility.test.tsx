import { render } from "@testing-library/react";
import axe, { type AxeResults, type RunOptions } from "axe-core";
import { expect, it, vi } from "vitest";
import { auditAccessibility } from "./test-accessibility";

it("retains every malformed control violation and target with compact passing results", async () => {
  render(
    <main>
      <h1>Accessibility oracle regression</h1>
      <button id="unnamed-first" />
      <button id="unnamed-second" />
      <input id="unlabelled-first" />
      <input id="unlabelled-second" />
    </main>,
  );
  // The spy observes the real axe execution, without replacing rules/results.
  const observed = vi.spyOn(axe, "run");
  await expect(auditAccessibility()).rejects.toThrow();
  const compact = await (observed.mock.results[0].value as Promise<AxeResults>);
  const options = observed.mock.calls[0][1] as RunOptions;
  observed.mockRestore();
  const baselineOptions = { ...options };
  delete baselineOptions.resultTypes;
  const baseline = await axe.run(document.body, baselineOptions);
  const violations = (result: AxeResults) =>
    result.violations.map(({ id, nodes }) => ({
      id,
      targets: nodes.map((node) => node.target),
    }));
  expect(violations(compact)).toEqual(violations(baseline));
  expect(
    compact.violations
      .find(({ id }) => id === "button-name")
      ?.nodes.map((node) => node.target),
  ).toEqual([["#unnamed-first"], ["#unnamed-second"]]);
  expect(
    compact.violations
      .find(({ id }) => id === "label")
      ?.nodes.map((node) => node.target),
  ).toEqual([["#unlabelled-first"], ["#unlabelled-second"]]);
});
