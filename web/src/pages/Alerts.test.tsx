import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AlertsPage } from "./Alerts";
import type { Rule, RuleInput } from "../lib/rules";
const session = vi.hoisted(() => ({
  identity: "alice",
  bootstrap: {
    preferences: { timezone: "UTC" },
    sources: [] as {
      deploymentId: string;
      displayName: string;
      namespaces: {
        namespaceId: string;
        name: string;
        capabilities: string[];
      }[];
    }[],
  },
}));
vi.mock("../lib/session", () => ({ useSession: () => session }));
const id = (n: number) =>
  `71000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const ref = { deploymentId: id(1), namespaceId: id(2), jobId: id(3) };
const other = { ...ref, deploymentId: id(5) };
const base: Rule = {
  id: id(4),
  revision: "9007199254740993",
  name: "Team failures",
  enabled: true,
  scope: "namespace_jobs",
  scopes: [{ ...ref, status: "active", activatedAt: "2026-10-04T00:00:00Z" }],
  jobs: [],
  outcomeMode: "selected",
  outcomes: ["failure"],
  inaccessibleScopes: 0,
  unavailableScopes: 0,
  createdAt: "2026-10-04T00:00:00Z",
  updatedAt: "2026-10-04T00:00:00Z",
};
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
type Call = {
  path: string;
  method: string;
  body?: unknown;
  headers: Headers;
  signal?: AbortSignal | null;
};
let calls: Call[];
let current: Rule[];
let handle:
  ((call: Call) => Response | Promise<Response> | undefined) | undefined;
function show(state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: "/alerts", state }]}>
      <AlertsPage />
    </MemoryRouter>,
  );
}
beforeEach(() => {
  session.identity = "alice";
  session.bootstrap.sources = [ref, other].map((value, index) => ({
    deploymentId: value.deploymentId,
    displayName: index ? "West" : "East",
    namespaces: [
      {
        namespaceId: value.namespaceId,
        name: "Research",
        capabilities: ["namespace.read", "jobs.read"],
      },
    ],
  }));
  calls = [];
  current = [];
  handle = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options: RequestInit = {}) => {
      const call = {
        path,
        method: options.method ?? "GET",
        body: options.body ? JSON.parse(String(options.body)) : undefined,
        headers: new Headers(options.headers),
        signal: options.signal,
      };
      calls.push(call);
      const response = handle?.(call);
      if (response) return response;
      if (call.method === "GET" && path.includes("?"))
        return json({ items: current });
      if (call.method === "POST" && path === "/api/v1/rules") {
        const input = call.body as RuleInput;
        const saved = {
          ...base,
          ...input,
          scopes: input.namespaces.map((ns) => ({ ...ns, status: "pending" })),
        };
        current = [saved];
        return json(saved, 201);
      }
      return json(base);
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
it("creates all namespace jobs with explicit multi-source selections and all-terminal intent", async () => {
  const user = userEvent.setup();
  show();
  await user.click(screen.getByRole("button", { name: "+ New alert rule" }));
  await user.type(screen.getByLabelText("Rule name"), "All results");
  await user.click(
    screen.getByRole("radio", { name: /All jobs in selected namespaces/ }),
  );
  await user.click(screen.getByLabelText("East / Research"));
  await user.click(screen.getByLabelText("West / Research"));
  await user.click(screen.getByRole("button", { name: "All terminal" }));
  await user.click(screen.getByRole("button", { name: "Save rule" }));
  await screen.findByText(
    "Rule saved. Check its namespace activation states below.",
  );
  const create = calls.find((call) => call.method === "POST")!;
  expect(create.body).toEqual({
    name: "All results",
    enabled: true,
    scope: "namespace_jobs",
    namespaces: [ref, other].map(({ deploymentId, namespaceId }) => ({
      deploymentId,
      namespaceId,
    })),
    jobs: [],
    outcomeMode: "all_terminal",
    outcomes: [],
  });
  expect(screen.getAllByText("Pending")).toHaveLength(2);
});
it("opens Watch this job as an editable selection without an automatic write", async () => {
  const user = userEvent.setup();
  show({ watchJob: ref });
  expect(await screen.findByLabelText("Rule name")).toHaveValue(
    `Watch ${ref.jobId}`,
  );
  expect(calls.every((call) => call.method === "GET")).toBe(true);
  await user.selectOptions(
    screen.getByLabelText("Job namespace"),
    JSON.stringify([other.deploymentId, other.namespaceId]),
  );
  await user.type(screen.getByLabelText("Job ID"), ref.jobId);
  await user.click(screen.getByRole("button", { name: "Add job" }));
  expect(
    screen.getByText(
      "2 / 100 jobs selected. Each job is checked against current access when you save.",
    ),
  ).toBeVisible();
  await user.click(screen.getByLabelText("East / Research"));
  expect(
    screen.getByText(
      "1 / 100 jobs selected. Each job is checked against current access when you save.",
    ),
  ).toBeVisible();
  await user.click(
    screen.getByRole("button", { name: "Successful completion" }),
  );
  await user.click(screen.getByRole("button", { name: "Save rule" }));
  await waitFor(() =>
    expect(calls.some((call) => call.method === "POST")).toBe(true),
  );
  expect(calls.find((call) => call.method === "POST")?.body).toMatchObject({
    scope: "watched_jobs",
    jobs: [other],
    outcomes: ["success"],
  });
});
it("stops a rule with hidden selections using only enabled and exact revision", async () => {
  current = [
    { ...base, scopes: [], inaccessibleScopes: 1, unavailableScopes: 1 },
  ];
  const user = userEvent.setup();
  show();
  await screen.findByText("Team failures");
  expect(screen.getByRole("button", { name: "Edit rule" })).toBeDisabled();
  expect(screen.queryByText("East / Research")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Stop rule" }));
  await waitFor(() =>
    expect(calls.some((call) => call.method === "PUT")).toBe(true),
  );
  const mutation = calls.find((call) => call.method === "PUT")!;
  expect(mutation.path).toBe(`/api/v1/rules/${base.id}/enabled`);
  expect(mutation.body).toEqual({ enabled: false });
  expect(mutation.headers.get("If-Match")).toBe(base.revision);
});
it("revalidates only after an explicit click, with no invented hidden reference body", async () => {
  current = [{ ...base, scopes: [], unavailableScopes: 1 }];
  const user = userEvent.setup();
  show();
  await screen.findByText("Team failures");
  expect(calls.every((call) => call.method === "GET")).toBe(true);
  await user.click(screen.getByRole("button", { name: "Revalidate access" }));
  await waitFor(() =>
    expect(calls.some((call) => call.path.endsWith("/revalidate"))).toBe(true),
  );
  expect(calls.find((call) => call.path.endsWith("/revalidate"))).toMatchObject(
    { method: "POST", body: undefined },
  );
});
it("requires conflict review and reloads the new wide revision before saving", async () => {
  current = [base];
  let conflict = true;
  handle = (call) =>
    call.method === "PUT" && conflict
      ? json({ code: "revision_conflict", message: "Changed elsewhere" }, 409)
      : call.method === "GET" && call.path === `/api/v1/rules/${base.id}`
        ? json({
            ...base,
            revision: "9007199254740994",
            name: "Updated elsewhere",
          })
        : undefined;
  const user = userEvent.setup();
  show();
  await user.click(await screen.findByRole("button", { name: "Edit rule" }));
  await user.click(screen.getByRole("button", { name: "Save rule" }));
  await screen.findByText("Changed elsewhere");
  expect(screen.getByRole("button", { name: "Save rule" })).toBeDisabled();
  await user.click(
    screen.getByRole("button", { name: "Discard edits and reload rule" }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText("Rule name")).toHaveValue("Updated elsewhere"),
  );
  conflict = false;
  await user.click(screen.getByRole("button", { name: "Save rule" }));
  await waitFor(() =>
    expect(calls.filter((call) => call.method === "PUT")).toHaveLength(2),
  );
  expect(
    calls.filter((call) => call.method === "PUT")[1].headers.get("If-Match"),
  ).toBe("9007199254740994");
});
it("rejects a multibyte name exceeding120 bytes before mutation", async () => {
  const user = userEvent.setup();
  show({ watchJob: ref });
  fireEvent.change(await screen.findByLabelText("Rule name"), {
    target: { value: "界".repeat(41) },
  });
  await user.click(screen.getByRole("button", { name: "Save rule" }));
  expect(
    await screen.findByText(/Use a rule name of 1–120 UTF-8 bytes/),
  ).toBeVisible();
  expect(calls.every((call) => call.method === "GET")).toBe(true);
});
it("does not repeat an uncertain create and requires checking the list", async () => {
  handle = (call) =>
    call.method === "POST"
      ? Promise.reject(new Error("connection lost"))
      : undefined;
  const user = userEvent.setup();
  show({ watchJob: ref });
  await user.click(await screen.findByRole("button", { name: "Save rule" }));
  expect(
    await screen.findByText(/The server may have saved this rule/),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "Save rule" })).toBeDisabled();
  await user.click(
    screen.getByRole("button", { name: "Close and check saved rules" }),
  );
  expect(calls.filter((call) => call.method === "POST")).toHaveLength(1);
});
it("keeps an in-flight create open until the server result can be checked", async () => {
  let complete!: (response: Response) => void;
  handle = (call) =>
    call.method === "POST"
      ? new Promise<Response>((resolve) => {
          complete = resolve;
        })
      : call.method === "GET"
        ? json({ items: [base], nextCursor: "opaque-next" })
        : undefined;
  const user = userEvent.setup();
  show({ watchJob: ref });
  await user.click(await screen.findByRole("button", { name: "Save rule" }));
  const pending = calls.find((call) => call.method === "POST")!;
  expect(screen.getByRole("button", { name: "Close editor" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "+ New alert rule" }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "Edit rule" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Next rules" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Close editor" }));
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  expect(pending.signal?.aborted).toBe(false);
  expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
  const input = pending.body as RuleInput;
  const saved: Rule = {
    ...base,
    ...input,
    scopes: input.namespaces.map((ns) => ({ ...ns, status: "pending" })),
  };
  current = [saved];
  await act(async () => {
    complete(json(saved, 201));
  });
  await screen.findByText(
    "Rule saved. Check its namespace activation states below.",
  );
  expect(calls.filter((call) => call.method === "POST")).toHaveLength(1);
  expect(
    screen.queryByRole("button", { name: "Save rule" }),
  ).not.toBeInTheDocument();
  expect(
    await screen.findByRole("button", { name: "+ New alert rule" }),
  ).toBeEnabled();
});
it("aborts pending mutations and discards old account state on account change", async () => {
  current = [base];
  let complete!: (response: Response) => void;
  handle = (call) =>
    call.method === "PUT"
      ? new Promise<Response>((resolve) => {
          complete = resolve;
        })
      : undefined;
  const user = userEvent.setup();
  const view = show();
  await user.click(await screen.findByRole("button", { name: "Stop rule" }));
  const pending = calls.find((call) => call.method === "PUT")!;
  session.identity = "bob";
  current = [];
  view.rerender(
    <MemoryRouter>
      <AlertsPage />
    </MemoryRouter>,
  );
  expect(pending.signal?.aborted).toBe(true);
  await act(async () => {
    complete(json({ ...base, name: "Alice private rule" }));
  });
  expect(screen.queryByText("Alice private rule")).not.toBeInTheDocument();
  expect(screen.queryByText(/Rule stopped/)).not.toBeInTheDocument();
});
it("paginates20 at a time without retaining prior-page private rows", async () => {
  handle = (call) =>
    call.method === "GET"
      ? json(
          call.path.includes("cursor=")
            ? { items: [{ ...base, id: id(9), name: "Second page" }] }
            : { items: [base], nextCursor: "opaque-rule-cursor" },
        )
      : undefined;
  const user = userEvent.setup();
  show();
  await screen.findByText(base.name);
  await user.click(screen.getByRole("button", { name: "Next rules" }));
  await screen.findByText("Second page");
  expect(screen.queryByText(base.name)).not.toBeInTheDocument();
  expect(calls.at(-1)?.path).toBe(
    "/api/v1/rules?limit=20&cursor=opaque-rule-cursor",
  );
  await user.click(screen.getByRole("button", { name: "Previous rules" }));
  await screen.findByText(base.name);
});

it("clears watched references on authority failure and never restores an old page while retrying", async () => {
  current = [{ ...base, scope: "watched_jobs", jobs: [ref] }];
  let finish!: (response: Response) => void;
  const user = userEvent.setup();
  show();
  await screen.findByText(base.name);
  expect(screen.getByText(ref.jobId)).toBeInTheDocument();
  handle = (call) =>
    call.method === "PUT"
      ? json(
          {
            code: "authorization_unavailable",
            message: "Current access is unavailable",
          },
          503,
        )
      : undefined;
  await user.click(screen.getByRole("button", { name: "Stop rule" }));
  await screen.findByText("Current access is unavailable");
  expect(screen.queryByText(ref.jobId)).not.toBeInTheDocument();
  handle = () =>
    new Promise<Response>((resolve) => {
      finish = resolve;
    });
  await user.click(screen.getByRole("button", { name: "Refresh rules" }));
  expect(screen.queryByText(ref.jobId)).not.toBeInTheDocument();
  await act(async () => {
    finish(
      json({
        items: [{ ...base, scopes: [], jobs: [], unavailableScopes: 1 }],
      }),
    );
  });
  expect(screen.queryByText(ref.jobId)).not.toBeInTheDocument();
  expect(
    await screen.findByRole("button", { name: "Stop rule" }),
  ).toBeEnabled();
});
it("confirms deletion and sends the exact revision without source references", async () => {
  current = [base];
  handle = (call) =>
    call.method === "DELETE" ? new Response(null, { status: 204 }) : undefined;
  const user = userEvent.setup();
  show();
  await user.click(await screen.findByRole("button", { name: "Delete rule" }));
  expect(calls.every((call) => call.method === "GET")).toBe(true);
  await user.click(
    within(
      screen.getByRole("group", { name: `Delete ${base.name}` }),
    ).getByRole("button", { name: "Confirm delete" }),
  );
  await screen.findByText("Rule deleted.");
  const deletion = calls.find((call) => call.method === "DELETE")!;
  expect(deletion.body).toBeUndefined();
  expect(deletion.headers.get("If-Match")).toBe(base.revision);
});
it("keeps unknown rule values inspectable without coercing them into a writable selection", async () => {
  current = [
    {
      ...base,
      scope: "future_scope",
      outcomes: ["future_outcome"],
      scopes: [{ ...ref, status: "future_activation" }],
    },
  ];
  show();
  await screen.findByText(base.name);
  expect(screen.getByText(/Future scope/)).toBeVisible();
  expect(screen.getByText("Future activation")).toBeVisible();
  expect(screen.getByRole("button", { name: "Edit rule" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Stop rule" })).toBeEnabled();
});
