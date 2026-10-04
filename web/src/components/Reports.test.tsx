import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { Reports } from "./Reports";
import { auditAccessibility } from "../test-accessibility";
const session = vi.hoisted(() => ({
  identity: "alice:east:research",
  bootstrap: { preferences: { timezone: "UTC", refreshSeconds: 5 } },
}));
vi.mock("../lib/session", () => ({ useSession: () => session }));
const job = { deploymentId: "east", namespaceId: "research", jobId: "job" };
const confidence = { score: 80, band: "high", basis: "Observed exit status" };
const ready: Wire.Report = {
  ...job,
  taskId: "task",
  state: "ready",
  profile: "metadata",
  sourceRevision: "9007199254740993",
  createdAt: "2026-10-03T12:00:00Z",
  expiresAt: "2026-11-02T12:00:00Z",
  outdated: true,
  reportId: "original-report",
  evidenceId: "evidence",
  analysisEvidenceId: "analysis",
  detail: {
    capturedAt: "2026-10-03T12:00:00Z",
    generatedAt: "2026-10-03T12:00:01Z",
    controlInstanceId: "instance",
    controlVersion: "control-version",
    contractVersion: "contract-version",
    platform: "linux/arm64",
    phase: "terminal",
    outcome: "failure",
    runs: [{ id: "run", number: "9007199254740993", executionId: "execution" }],
    versions: {
      companion: "companion-version",
      engine: "engine-version",
      jobman: "jobman-version",
      collector: "collector-version",
      evidenceSchema: 1,
      reportSchema: 1,
      generationSchema: 1,
      proposalSchema: 1,
    },
    mode: "deterministic",
    primaryFindingId: "finding",
    analyzers: [{ name: "execution", version: "1" }],
    generators: [],
    findings: [
      {
        id: "finding",
        code: "exit_nonzero",
        category: "execution",
        severity: "warning",
        title: "Recorded nonzero exit",
        explanation: "Execution reported exit code 123.",
        confidence,
        supportingEvidence: ["citation"],
        contradictingEvidence: [],
        contradictingFindings: [],
        analyzer: "execution",
      },
    ],
    actions: [
      {
        id: "action",
        code: "inspect",
        kind: "inspect",
        summary: "Review workload inputs",
        description:
          "Read the retained evidence before deciding what to change.",
        supportingEvidence: ["citation"],
        requiresConfirmation: true,
      },
    ],
    retry: {
      verdict: "unknown",
      existingPolicy: "No retry configured",
      confidence,
      rationale: "The cause is unresolved.",
      reasons: ["Incomplete context"],
      supportingEvidence: [],
    },
    citations: [
      {
        id: "citation",
        code: "stdout",
        label: "Captured stdout",
        kind: "artifact",
        sourceEvidenceId: "evidence",
        startOffset: "0",
        endOffset: "6",
      },
    ],
    missingEvidence: [
      {
        code: "scheduler_absent",
        description: "No scheduler observation was captured.",
      },
    ],
    warnings: [{ code: "partial", message: "Some evidence was unavailable." }],
    disclosure: {
      providerInvoked: false,
      generatedContentUsed: false,
      locality: "local",
      classes: ["metadata"],
      itemIds: ["citation"],
      artifactIds: [],
      enrichmentIds: [],
      itemCount: "1",
      artifactCount: "0",
      enrichmentCount: "0",
      artifactBytes: "0",
      enrichmentBytes: "0",
      requestBytes: "0",
      redactionNoticeCount: "0",
    },
    omissions: [{ code: "history", affects: ["older runs"] }],
    redactionNotices: [],
  },
};
const citation: Wire.Citation = {
  ...ready.detail!.citations[0],
  taskId: "task",
  reportId: "original-report",
  evidenceId: "evidence",
  analysisEvidenceId: "analysis",
  bytesBase64: btoa("proof\n"),
  originalOffsetsExact: false,
  originalStartOffset: "9007199254740993",
  originalEndOffset: "9007199254740999",
  rangeBasis: "sanitized",
  sourceRevision: "9007199254740993",
  quality: "observed",
  disclosure: "redacted",
};
const pending: Wire.Report = {
  ...ready,
  detail: undefined,
  state: "queued",
  reportId: undefined,
  evidenceId: undefined,
  analysisEvidenceId: undefined,
  outdated: false,
};
const page = (items: Wire.Report[], nextCursor?: string) => ({
  items,
  nextCursor,
  fetchedAt: "2026-10-03T12:00:00Z",
});
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  session.identity = "alice:east:research";
  session.bootstrap.preferences.refreshSeconds = 5;
  fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("provides accessible findings, disclosure and sealed citation controls", async () => {
  fetcher.mockImplementation(async (path: string) =>
    Response.json(
      path.includes("/citations/")
        ? citation
        : path.endsWith("/task")
          ? ready
          : page([ready]),
    ),
  );
  render(
    <main>
      <h1>Job diagnosis</h1>
      <Reports job={job} />
    </main>,
  );
  await userEvent.click(
    await screen.findByRole("button", { name: "Open report" }),
  );
  await screen.findByText("Recorded nonzero exit");
  await auditAccessibility();
  await userEvent.click(
    screen.getByRole("button", { name: "Captured stdout" }),
  );
  await screen.findByText("proof");
  await auditAccessibility();
});

it("uses the strict profile/run contract and retries an uncertain request with the same idempotency key", async () => {
  let posts = 0;
  fetcher.mockImplementation(async (_path: string, options: RequestInit) => {
    if (options.method === "POST") {
      if (++posts === 1) throw new TypeError("Synthetic network failure");
      return Response.json(pending, { status: 202 });
    }
    return Response.json(_path.endsWith("/task") ? pending : page([]));
  });
  render(<Reports job={job} />);
  await screen.findByText("No reports on this page");
  await userEvent.click(screen.getByRole("checkbox"));
  await userEvent.type(
    screen.getByLabelText("Report run UUID"),
    "79000000-0000-4000-8000-000000000001",
  );
  await userEvent.click(
    screen.getByRole("button", { name: "Generate report" }),
  );
  await screen.findByRole("alert");
  await userEvent.click(
    screen.getByRole("button", { name: "Generate report" }),
  );
  await screen.findByText(/Report request accepted/);
  const calls = fetcher.mock.calls.filter(
    ([, options]) => options.method === "POST",
  );
  expect(JSON.parse(calls[0][1].body)).toEqual({
    profile: "include_log_tail",
    runId: "79000000-0000-4000-8000-000000000001",
  });
  expect(calls[0][1].headers.get("Idempotency-Key")).toBe(
    calls[1][1].headers.get("Idempotency-Key"),
  );
  expect(calls[0][1].credentials).toBe("same-origin");
  expect(await screen.findByText(/report is in progress/)).toBeVisible();
});

it("uses a new request key after a definitive source snapshot rejection", async () => {
  let posts = 0;
  fetcher.mockImplementation(async (path: string, options: RequestInit) => {
    if (options.method === "POST") {
      if (++posts === 1)
        return Response.json(
          { code: "snapshot_changed", message: "The source was restored." },
          { status: 409 },
        );
      return Response.json(pending, { status: 202 });
    }
    return Response.json(path.endsWith("/task") ? pending : page([]));
  });
  render(<Reports job={job} />);
  await screen.findByText("No reports on this page");
  await userEvent.click(
    screen.getByRole("button", { name: "Generate report" }),
  );
  await screen.findByText("The source was restored.");
  await userEvent.click(
    screen.getByRole("button", { name: "Generate report" }),
  );
  await screen.findByText(/Report request accepted/);
  const calls = fetcher.mock.calls.filter(
    ([, options]) => options.method === "POST",
  );
  expect(calls[0][1].body).toBe(calls[1][1].body);
  expect(calls[0][1].headers.get("Idempotency-Key")).not.toBe(
    calls[1][1].headers.get("Idempotency-Key"),
  );
});

it("polls a pending task into its original sealed findings and renders advice without execution controls", async () => {
  vi.useFakeTimers();
  let current = pending;
  fetcher.mockImplementation(async (path: string) =>
    Response.json(path.endsWith("/task") ? current : page([pending])),
  );
  await act(async () => {
    render(<Reports job={job} />);
  });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Open report" }));
  });
  expect(screen.getByText(/report is in progress/)).toBeVisible();
  current = ready;
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(screen.getByText("Recorded nonzero exit")).toBeVisible();
  expect(screen.getByText("original-report")).toBeVisible();
  expect(screen.getAllByText(/9007199254740993/).length).toBeGreaterThan(0);
  expect(screen.getAllByText(/not a calibrated probability/).length).toBe(2);
  expect(screen.getByText(/source evidence has changed/)).toBeVisible();
  expect(screen.getByText(/No scheduler observation/)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /retry job|cancel job|run command/i }),
  ).not.toBeInTheDocument();
});

it("reads only sealed citations, discloses unknown original offsets and clears all report content after citation denial", async () => {
  let denied = false;
  fetcher.mockImplementation(async (path: string) => {
    if (path.includes("/citations/"))
      return denied
        ? Response.json(
            { code: "forbidden", message: "Access changed." },
            { status: 403 },
          )
        : Response.json(citation);
    return Response.json(path.endsWith("/task") ? ready : page([ready]));
  });
  render(<Reports job={job} />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Open report" }),
  );
  await userEvent.click(
    await screen.findByRole("button", { name: "Captured stdout" }),
  );
  expect(await screen.findByText("proof")).toBeVisible();
  expect(
    screen.getByText("No exact original byte mapping for this selection"),
  ).toBeVisible();
  expect(screen.queryByText(/9007199254740999/)).not.toBeInTheDocument();
  expect(
    fetcher.mock.calls.some(([path]) => String(path).includes("/logs")),
  ).toBe(false);
  denied = true;
  await act(async () => {
    window.dispatchEvent(new Event("online"));
  });
  await screen.findByText("Access changed.");
  expect(screen.queryByText("proof")).not.toBeInTheDocument();
  expect(screen.queryByText("Recorded nonzero exit")).not.toBeInTheDocument();
  expect(screen.queryByText("original-report")).not.toBeInTheDocument();
});

it("hides retained findings when source authority becomes unavailable", async () => {
  let unavailable = false;
  fetcher.mockImplementation(async (path: string) =>
    path.endsWith("/task") && unavailable
      ? Response.json(
          {
            code: "authorization_unavailable",
            message: "Authority unavailable.",
          },
          { status: 503 },
        )
      : Response.json(path.endsWith("/task") ? ready : page([ready])),
  );
  render(<Reports job={job} />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Open report" }),
  );
  await screen.findByText("Recorded nonzero exit");
  unavailable = true;
  await userEvent.click(
    screen.getByRole("button", { name: "Refresh selected report" }),
  );
  await screen.findByText("Authority unavailable.");
  expect(screen.queryByText("Recorded nonzero exit")).not.toBeInTheDocument();
  fetcher.mockImplementation(() => new Promise<Response>(() => {}));
  await userEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(screen.queryByText("Task task")).not.toBeInTheDocument();
  expect(screen.queryByText("Recorded nonzero exit")).not.toBeInTheDocument();
});

it("aborts report creation and ignores a late response when the account changes", async () => {
  let complete: (value: Response) => void = () => {};
  let signal: AbortSignal | undefined;
  fetcher.mockImplementation((_path: string, options: RequestInit) => {
    if (options.method === "POST") {
      signal = options.signal as AbortSignal;
      return new Promise<Response>((resolve) => {
        complete = resolve;
      });
    }
    return Promise.resolve(Response.json(page([])));
  });
  const view = render(<Reports job={job} />);
  await screen.findByText("No reports on this page");
  await userEvent.click(
    screen.getByRole("button", { name: "Generate report" }),
  );
  session.identity = "bob:east:research";
  view.rerender(<Reports job={job} />);
  expect(signal?.aborted).toBe(true);
  await act(async () => {
    complete(Response.json(pending, { status: 202 }));
  });
  expect(screen.queryByText(/Report request accepted/)).not.toBeInTheDocument();
  expect(
    screen.queryByLabelText("Selected diagnosis report"),
  ).not.toBeInTheDocument();
});

it("uses bounded opaque history continuations and can return to the first page", async () => {
  fetcher.mockImplementation(async (path: string) =>
    Response.json(
      new URL(path, "https://dashboard.test").searchParams.has("cursor")
        ? page([{ ...pending, taskId: "older-task" }])
        : page([pending], "opaque-cursor"),
    ),
  );
  render(<Reports job={job} />);
  await screen.findByText("Task task");
  await userEvent.click(screen.getByRole("button", { name: "Next reports" }));
  await screen.findByText("Task older-task");
  expect(
    fetcher.mock.calls.some(([path]) =>
      String(path).includes("limit=20&cursor=opaque-cursor"),
    ),
  ).toBe(true);
  await userEvent.click(
    screen.getByRole("button", { name: "Previous reports" }),
  );
  await waitFor(() => expect(screen.getByText("Task task")).toBeVisible());
  expect(
    screen.getByRole("button", { name: "Previous reports" }),
  ).toBeDisabled();
});

it.each([0, 5, 10, 30])(
  "honors %is ordinary report/history/citation refresh without disabling explicit or online reads",
  async (seconds) => {
    vi.useFakeTimers();
    session.bootstrap.preferences.refreshSeconds = seconds;
    fetcher.mockImplementation(async (path: string) =>
      Response.json(
        path.includes("/citations/")
          ? citation
          : path.endsWith("/task")
            ? ready
            : page([ready]),
      ),
    );
    await act(async () => {
      render(<Reports job={job} />);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Open report" }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Captured stdout" }));
    });
    expect(screen.getByText("proof")).toBeVisible();
    expect(fetcher).toHaveBeenCalledTimes(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(seconds ? seconds * 1000 - 1 : 60000);
    });
    expect(fetcher).toHaveBeenCalledTimes(3);
    if (seconds) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1);
      });
      expect(fetcher).toHaveBeenCalledTimes(6);
    }
    const previous = fetcher.mock.calls.length;
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Refresh selected report" }),
      );
    });
    expect(fetcher).toHaveBeenCalledTimes(previous + 1);
    await act(async () => {
      window.dispatchEvent(new Event("online"));
    });
    expect(fetcher).toHaveBeenCalledTimes(previous + 4);
    expect(screen.getByText("proof")).toBeVisible();
  },
);

it.each([0, 30])(
  "follows each pending report state every 5s at preference %is and returns to ordinary cadence when ready",
  async (seconds) => {
    vi.useFakeTimers();
    session.bootstrap.preferences.refreshSeconds = seconds;
    let current = pending;
    fetcher.mockImplementation(async (path: string) =>
      Response.json(path.endsWith("/task") ? current : page([pending])),
    );
    await act(async () => {
      render(<Reports job={job} />);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Open report" }));
    });
    for (const next of [
      { ...pending, state: "collecting" },
      { ...pending, state: "analyzing" },
      ready,
    ]) {
      current = next;
      const before = fetcher.mock.calls.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4999);
      });
      expect(fetcher).toHaveBeenCalledTimes(before);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1);
      });
      expect(fetcher).toHaveBeenCalledTimes(before + 1);
    }
    expect(screen.getByText("Recorded nonzero exit")).toBeVisible();
    const details = () =>
      fetcher.mock.calls.filter(([path]) => String(path).endsWith("/task"))
        .length;
    expect(details()).toBe(4);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(seconds ? 29999 : 60000);
    });
    expect(details()).toBe(4);
    if (seconds) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1);
      });
      expect(details()).toBe(5);
    }
  },
);

it("pauses pending follow while hidden and reauthorizes on foreground without overlapping a pending read", async () => {
  vi.useFakeTimers();
  session.bootstrap.preferences.refreshSeconds = 0;
  let visible = true;
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() =>
    visible ? "visible" : "hidden",
  );
  let finish: ((value: Response) => void) | undefined;
  fetcher.mockImplementation(async (path: string) =>
    Response.json(path.endsWith("/task") ? pending : page([pending])),
  );
  await act(async () => {
    render(<Reports job={job} />);
  });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Open report" }));
  });
  visible = false;
  await act(async () => {
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(fetcher).toHaveBeenCalledTimes(2);
  fetcher.mockImplementation((path: string) =>
    path.endsWith("/task")
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : Promise.resolve(Response.json(page([pending]))),
  );
  visible = true;
  await act(async () => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(fetcher).toHaveBeenCalledTimes(4);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(fetcher).toHaveBeenCalledTimes(4);
  await act(async () => {
    finish!(Response.json(ready));
  });
  expect(screen.getByText("Recorded nonzero exit")).toBeVisible();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(fetcher).toHaveBeenCalledTimes(4);
});
