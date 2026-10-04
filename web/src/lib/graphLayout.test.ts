import { expect, it } from "vitest";
import { layoutGraph } from "./graphLayout";

it("bounds the actual worker layout independently of full graph size", () => {
  const nodes = Array.from({ length: 10000 }, (_, i) => ({
    id: String(i),
    parents: i ? [String(i - 1)] : [],
  }));
  const before = JSON.stringify(nodes);
  const layout = layoutGraph(nodes);
  expect(layout.positions).toHaveLength(200);
  expect(layout.positions[199]).toEqual({
    id: "199",
    x: 199 * 250 + 30,
    y: 30,
  });
  expect(new Set(layout.positions.map((n) => n.id)).size).toBe(200);
  expect(JSON.stringify(nodes)).toBe(before);
});
it("terminates cycles and ignores outside-page edges without inventing readiness", () => {
  const nodes = [
    { id: "a", parents: ["b", "outside"] },
    { id: "b", parents: ["a"] },
  ];
  const result = layoutGraph(nodes);
  expect(result.positions).toEqual([
    { id: "a", x: 30, y: 30 },
    { id: "b", x: 30, y: 135 },
  ]);
  expect(Object.keys(result).sort()).toEqual(["height", "positions", "width"]);
});
