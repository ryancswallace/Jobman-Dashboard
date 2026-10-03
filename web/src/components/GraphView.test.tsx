import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { GraphView } from "./GraphView";
import type { GraphNode } from "../lib/models";
class ControlledWorker {
  static instances: ControlledWorker[] = [];
  onmessage?: (event: { data: unknown }) => void;
  postMessage = vi.fn();
  terminate = vi.fn();
  constructor() {
    ControlledWorker.instances.push(this);
  }
  finish(id: string) {
    this.onmessage?.({
      data: { positions: [{ id, x: 30, y: 30 }], width: 550, height: 180 },
    });
  }
}
const node = (id: string): GraphNode => ({
  id,
  job: {
    deploymentId: "east",
    namespaceId: "research",
    jobId: id,
    phase: "running",
    revision: "1",
    createdAt: "2026-10-03T12:00:00Z",
  },
});
beforeEach(() => {
  ControlledWorker.instances = [];
  vi.stubGlobal("Worker", ControlledWorker);
});
afterEach(() => vi.unstubAllGlobals());
it("hides removed-node layouts and ignores obsolete replies after a page replacement", () => {
  const select = vi.fn();
  const view = render(<GraphView nodes={[node("old")]} onSelect={select} />);
  const old = ControlledWorker.instances[0];
  act(() => old.finish("old"));
  expect(screen.getByRole("button", { name: /old, running/ })).toBeVisible();
  view.rerender(<GraphView nodes={[node("new")]} onSelect={select} />);
  expect(old.terminate).toHaveBeenCalledOnce();
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  expect(screen.getByText("Laying out this node page…")).toBeVisible();
  act(() => ControlledWorker.instances[1].finish("new"));
  expect(screen.getByRole("button", { name: /new, running/ })).toBeVisible();
  act(() => old.finish("old"));
  expect(screen.getByRole("button", { name: /new, running/ })).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /old, running/ }),
  ).not.toBeInTheDocument();
});
it("invalidates a layout when dependency edges change with the same nodes", () => {
  const before = node("same");
  const view = render(<GraphView nodes={[before]} onSelect={() => {}} />);
  act(() => ControlledWorker.instances[0].finish("same"));
  view.rerender(
    <GraphView
      nodes={[
        {
          ...before,
          dependencies: [
            {
              upstreamNodeId: "parent",
              predicate: "afterok",
              status: "waiting",
            },
          ],
        },
      ]}
      onSelect={() => {}}
    />,
  );
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  act(() => ControlledWorker.instances[1].finish("same"));
  expect(screen.getByRole("img")).toBeVisible();
});
