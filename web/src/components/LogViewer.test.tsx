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

it("pins selected run provenance and shows the last successful read independently of a later failure", async () => {
  const run = {
    id: "historical-run",
    number: "9007199254740993",
    executionId: "historical-execution",
    phase: "terminal",
    desiredState: "run",
    createdAt: chunk.capturedAt,
    updatedAt: chunk.capturedAt,
  };
  fetcher
    .mockResolvedValueOnce(
      Response.json({
        ...chunk,
        runId: run.id,
        runNumber: run.number,
        executionId: run.executionId,
        state: "open",
        endOffset: "1",
        bytesBase64: "QQ==",
        nextCursor: "historical-cursor",
      }),
    )
    .mockResolvedValueOnce(
      Response.json({
        ...chunk,
        runId: "other-run",
        runNumber: "2",
        executionId: "other-execution",
        state: "complete",
        startOffset: "1",
        endOffset: "2",
        bytesBase64: "Qg==",
      }),
    );
  await act(async () => {
    render(<LogViewer job={job} run={run} />);
  });
  const fetched = screen
    .getByTitle("Last successful authorized log read")
    .getAttribute("datetime");
  expect(fetched).toBeTruthy();
  expect(String(fetcher.mock.calls[0][0])).toContain(
    "runNumber=9007199254740993",
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(String(fetcher.mock.calls[1][0])).toContain(
    "runNumber=9007199254740993",
  );
  expect(String(fetcher.mock.calls[1][0])).toContain(
    "cursor=historical-cursor",
  );
  expect(screen.getByRole("alert")).toHaveTextContent(
    "response no longer matches the selected run",
  );
  expect(screen.getByLabelText("stdout log output")).toHaveTextContent("A");
  expect(screen.getByLabelText("stdout log output")).not.toHaveTextContent("B");
  expect(
    screen.getByTitle("Last successful authorized log read"),
  ).toHaveAttribute("datetime", fetched!);
});

it("accepts factual no-execution runs without inventing an execution", async () => {
  const run = {
    id: "unassigned-run",
    number: "1",
    phase: "pending",
    desiredState: "run",
    createdAt: chunk.capturedAt,
    updatedAt: chunk.capturedAt,
  };
  fetcher.mockResolvedValue(
    Response.json({
      ...chunk,
      runId: run.id,
      runNumber: run.number,
      executionId: "",
      state: "not_captured",
    }),
  );
  await act(async () => {
    render(<LogViewer job={job} run={run} />);
  });
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByText("Not captured")).toBeVisible();
  expect(
    screen.getByTitle("Last successful authorized log read"),
  ).toBeVisible();
});

it.each(["paused", "complete", "error"])(
  "rechecks %s logs on foreground as a paused fresh tail with separate source capture",
  async (mode) => {
    let visibility = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(
      () => visibility as DocumentVisibilityState,
    );
    fetcher.mockResolvedValueOnce(
      mode === "error"
        ? Response.json(
            { code: "source_unavailable", message: "Unavailable" },
            { status: 503 },
          )
        : Response.json({
            ...chunk,
            state: mode === "complete" ? "complete" : "open",
            bytesBase64: "QQ==",
            endOffset: "1",
            nextCursor: "old-cursor",
          }),
    );
    fetcher.mockResolvedValueOnce(
      Response.json({
        ...chunk,
        state: "complete",
        executionId: "new-execution",
        bytesBase64: "Qg==",
        startOffset: "20",
        endOffset: "21",
        capturedAt: "2026-10-03T13:00:00Z",
      }),
    );
    await act(async () => {
      render(<LogViewer job={job} />);
    });
    if (mode === "paused")
      fireEvent.click(screen.getByRole("button", { name: "Pause following" }));
    await act(async () => {
      visibility = "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
    await act(async () => {
      visibility = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(String(fetcher.mock.calls[1][0])).not.toContain("cursor=");
    expect(screen.getByLabelText("stdout log output")).toHaveTextContent("B");
    expect(screen.getByLabelText("stdout log output")).not.toHaveTextContent(
      "A",
    );
    expect(
      screen.getByRole("button", { name: "Resume following" }),
    ).toBeVisible();
    expect(screen.getByText(/Source capture:/)).toHaveTextContent(
      "2026-10-03T13:00:00Z",
    );
    expect(
      screen.getByTitle("Last successful authorized log read"),
    ).not.toHaveAttribute("datetime", "2026-10-03T13:00:00Z");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  },
);

it("discards a pre-hide reply and keeps the selected run on foreground and online recovery", async () => {
  let visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(
    () => visibility as DocumentVisibilityState,
  );
  let late!: (response: Response) => void;
  const run = {
    id: "old-run",
    number: "5",
    executionId: "old-execution",
    phase: "terminal",
    desiredState: "run",
    createdAt: chunk.capturedAt,
    updatedAt: chunk.capturedAt,
  };
  const selected = {
    ...chunk,
    runId: run.id,
    runNumber: run.number,
    executionId: run.executionId,
    endOffset: "1",
    state: "complete",
  };
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        late = resolve;
      }),
  );
  fetcher.mockResolvedValueOnce(
    Response.json({ ...selected, bytesBase64: "Qg==" }),
  );
  fetcher.mockResolvedValueOnce(
    Response.json({ ...selected, bytesBase64: "Qw==" }),
  );
  await act(async () => {
    render(<LogViewer job={job} run={run} />);
  });
  await act(async () => {
    visibility = "hidden";
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
  await act(async () => {
    visibility = "visible";
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await act(async () => {
    late(Response.json({ ...selected, bytesBase64: "QQ==" }));
  });
  expect(screen.getByLabelText("stdout log output")).toHaveTextContent("B");
  expect(screen.getByLabelText("stdout log output")).not.toHaveTextContent("A");
  await act(async () => {
    window.dispatchEvent(new Event("online"));
  });
  expect(screen.getByLabelText("stdout log output")).toHaveTextContent("C");
  for (const call of fetcher.mock.calls)
    expect(String(call[0])).toContain("runNumber=5");
  expect(
    screen.getByRole("button", { name: "Resume following" }),
  ).toBeVisible();
});
