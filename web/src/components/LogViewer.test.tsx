import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LogViewer } from "./LogViewer";
vi.mock("../lib/session", () => ({
  useSession: () => ({ identity: "account:east:research" }),
}));
const job = { deploymentId: "east", namespaceId: "research", jobId: "job" };
const chunk = {
  executionId: "execution",
  stream: "stdout",
  runId: "run",
  startOffset: "0",
  endOffset: "0",
  state: "awaiting_capture",
  bytesBase64: "",
  truncated: false,
  capturedAt: "2026-10-03T12:00:00Z",
};
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  vi.useFakeTimers();
  fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it("continues following while an empty stream awaits capture", async () => {
  fetcher.mockResolvedValueOnce(Response.json(chunk)).mockResolvedValueOnce(
    Response.json({
      ...chunk,
      endOffset: "1",
      state: "complete",
      bytesBase64: "QQ==",
    }),
  );
  await act(async () => {
    render(<LogViewer job={job} />);
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(screen.getByLabelText("stdout log output")).toHaveTextContent("A");
  expect(screen.getByText("Complete")).toBeVisible();
});
it("does not issue another read when following is paused", async () => {
  fetcher.mockResolvedValue(Response.json({ ...chunk, nextCursor: "cursor" }));
  await act(async () => {
    render(<LogViewer job={job} />);
  });
  fireEvent.click(screen.getByRole("button", { name: "Pause following" }));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  });
  expect(fetcher).toHaveBeenCalledTimes(2);
});
it("flushes an incomplete UTF-8 suffix on a zero-byte final chunk", async () => {
  fetcher
    .mockResolvedValueOnce(
      Response.json({
        ...chunk,
        endOffset: "2",
        bytesBase64: "4oI=",
        state: "streaming",
        nextCursor: "cursor",
      }),
    )
    .mockResolvedValueOnce(
      Response.json({
        ...chunk,
        startOffset: "2",
        endOffset: "2",
        state: "complete",
      }),
    );
  await act(async () => {
    render(<LogViewer job={job} />);
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(screen.getByLabelText("stdout log output")).toHaveTextContent("�");
  expect(screen.getByText("Complete")).toBeVisible();
});

it("restarts an expired continuation from a fresh tail on explicit refresh", async () => {
  fetcher
    .mockResolvedValueOnce(
      Response.json({
        ...chunk,
        endOffset: "1",
        bytesBase64: "QQ==",
        nextCursor: "old-cursor",
        state: "open",
      }),
    )
    .mockResolvedValueOnce(
      Response.json(
        { code: "cursor_expired", message: "Refresh logs." },
        { status: 409 },
      ),
    )
    .mockResolvedValueOnce(
      Response.json({
        ...chunk,
        executionId: "new-execution",
        startOffset: "20",
        endOffset: "21",
        bytesBase64: "Qg==",
        state: "complete",
      }),
    );
  await act(async () => {
    render(<LogViewer job={job} />);
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(String(fetcher.mock.calls[1][0])).toContain("cursor=old-cursor");
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  });
  expect(String(fetcher.mock.calls[2][0])).not.toContain("cursor=");
  expect(screen.getByLabelText("stdout log output")).toHaveTextContent("B");
  expect(screen.getByLabelText("stdout log output")).not.toHaveTextContent("A");
});
