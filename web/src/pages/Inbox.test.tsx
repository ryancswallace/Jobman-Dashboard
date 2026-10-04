import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { InboxPage, InboxDetailPage } from "./Inbox";
import type { InboxItem, InboxItemPage } from "../lib/inbox";
import { auditAccessibility } from "../test-accessibility";
const session = vi.hoisted(() => ({
  identity: "alice",
  scope: { deployments: [] as string[] },
  bootstrap: {
    preferences: { timezone: "UTC", refreshSeconds: 60 },
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
const base: InboxItem = {
  id: id(1),
  job: { deploymentId: id(2), namespaceId: id(3), jobId: id(4) },
  controlInstanceId: id(5),
  eventId: id(6),
  outcome: "failure",
  eventAt: "2026-10-04T00:00:00Z",
  createdAt: "2026-10-04T01:00:00Z",
  expiresAt: "2026-11-03T01:00:00Z",
  read: false,
  matchedRules: [
    {
      ruleId: id(7),
      revision: "9007199254740993",
      name: "Failures",
      scope: "namespace_jobs",
      outcomeMode: "selected",
      outcomes: ["failure"],
    },
  ],
  jobAvailability: "available",
  jobName: "Synthetic job",
  delivery: { total: "1", byState: { accepted: "1" } },
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
type Call = {
  path: string;
  method: string;
  body?: unknown;
  signal?: AbortSignal | null;
};
let calls: Call[],
  page: InboxItemPage,
  handle: ((c: Call) => Response | Promise<Response> | undefined) | undefined;
function view(path = "/inbox") {
  return (
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/inbox" element={<InboxPage />} />
        <Route path="/inbox/:inboxId" element={<InboxDetailPage />} />
      </Routes>
    </MemoryRouter>
  );
}
function show(path = "/inbox") {
  return render(view(path));
}
beforeEach(() => {
  session.identity = "alice";
  session.bootstrap.preferences.refreshSeconds = 60;
  session.scope = { deployments: [id(2)] };
  session.bootstrap.sources = [
    {
      deploymentId: id(2),
      displayName: "East",
      namespaces: [
        {
          namespaceId: id(3),
          name: "Research",
          capabilities: ["namespace.read", "jobs.read"],
        },
      ],
    },
  ];
  page = {
    items: [structuredClone(base)],
    unreadCount: "1",
    completeness: "complete",
    unavailableSources: 0,
    inaccessibleScopes: 0,
    fetchedAt: "2026-10-04T01:00:01Z",
  };
  calls = [];
  handle = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, options: RequestInit = {}) => {
      const c = {
        path,
        method: options.method ?? "GET",
        body: options.body ? JSON.parse(String(options.body)) : undefined,
        signal: options.signal,
      };
      calls.push(c);
      const response = handle?.(c);
      if (response) return response;
      if (c.method === "PATCH") {
        page.items[0].read = true;
        page.items[0].readAt = "2026-10-04T01:00:02Z";
        page.unreadCount = "0";
        return json(page.items[0]);
      }
      if (
        new URL(path, "https://dashboard.test").pathname ===
        `/api/v1/inbox/${id(1)}`
      )
        return json(page.items[0]);
      return json(page);
    }),
  );
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it.each(["/inbox", `/inbox/${id(1)}`])(
  "provides accessible populated notification content at %s",
  async (path) => {
    render(<main>{view(path)}</main>);
    await screen.findByText("Synthetic job");
    await auditAccessibility();
  },
);
it("renders immutable context, exact counts and source-qualified refreshed destination", async () => {
  page.unreadCount = "9007199254740993";
  show();
  await screen.findByText("Synthetic job");
  expect(screen.getByText(/9,007,199,254,740,993 unread/)).toBeInTheDocument();
  expect(screen.getByText(/revision 9007199254740993/)).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: /Open current job/ }),
  ).toHaveAttribute(
    "href",
    `/deployments/${id(2)}/namespaces/${id(3)}/jobs/${id(4)}`,
  );
  expect(
    screen.getByText(/Provider acceptance does not confirm phone presentation/),
  ).toBeInTheDocument();
});
it("shows partial authorized subtotal and missing current job without a stale name", async () => {
  page.items[0] = { ...base, jobName: undefined, jobAvailability: "missing" };
  page.completeness = "partial";
  page.unavailableSources = 1;
  show();
  await screen.findByText(/Current job detail is no longer available/);
  expect(screen.queryByText("Synthetic job")).not.toBeInTheDocument();
  expect(
    screen.getByText(/1 unread in verified scopes \(subtotal\)/),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: /Open current job/ }),
  ).not.toBeInTheDocument();
});
it("marks read through bounded API and refreshes unread count", async () => {
  show();
  await screen.findByText("Synthetic job");
  fireEvent.click(screen.getByRole("button", { name: "Mark read" }));
  await screen.findByRole("button", { name: "Mark unread" });
  expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ read: true });
  expect(screen.getByText("0 unread")).toBeInTheDocument();
});
it("preserves unread and scope filters on older-page navigation", async () => {
  page.nextCursor = "opaque-next";
  show("/inbox?unread=true");
  await screen.findByText("Synthetic job");
  fireEvent.click(screen.getByRole("button", { name: /Older updates/ }));
  await waitFor(() =>
    expect(
      calls.some(
        (c) =>
          new URL(c.path, "https://test").searchParams.get("cursor") ===
          "opaque-next",
      ),
    ).toBe(true),
  );
  const query = new URL(calls.at(-1)!.path, "https://test").searchParams;
  expect(query.get("unread")).toBe("true");
  expect(JSON.parse(query.get("scope")!)).toEqual([
    { deploymentId: id(2), namespaceId: id(3) },
  ]);
});
it("clears history immediately when a mutation finds revoked authority", async () => {
  handle = (c) =>
    c.method === "PATCH"
      ? json({ code: "forbidden", message: "Access changed" }, 403)
      : calls.some((v) => v.method === "PATCH")
        ? json(
            {
              code: "authorization_unavailable",
              message: "Current proof unavailable",
            },
            503,
          )
        : undefined;
  show();
  await screen.findByText("Synthetic job");
  fireEvent.click(screen.getByRole("button", { name: "Mark read" }));
  await screen.findByText("Access changed");
  expect(screen.queryByText("Synthetic job")).not.toBeInTheDocument();
});
it("aborts old-account mutation and ignores a late response", async () => {
  let finish: (v: Response) => void = () => {};
  handle = (c) =>
    c.method === "PATCH"
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : session.identity === "bob"
        ? json({ ...page, items: [], unreadCount: "0" })
        : undefined;
  const ui = show();
  await screen.findByText("Synthetic job");
  fireEvent.click(screen.getByRole("button", { name: "Mark read" }));
  session.identity = "bob";
  ui.rerender(view());
  await screen.findByText("You’re caught up");
  expect(calls.find((c) => c.method === "PATCH")?.signal?.aborted).toBe(true);
  await act(async () =>
    finish(json({ ...base, read: true, readAt: "2026-10-04T01:00:02Z" })),
  );
  expect(screen.queryByText("Synthetic job")).not.toBeInTheDocument();
});
it("resolves opaque inbox detail and rejects a substituted record", async () => {
  handle = (c) =>
    c.path === `/api/v1/inbox/${id(8)}` ? json(base) : undefined;
  show(`/inbox/${id(8)}`);
  await screen.findByRole("alert");
  expect(screen.queryByText("Synthetic job")).not.toBeInTheDocument();
});

it("offers a credential-free native link only for the authorized opaque inbox item", async () => {
  handle = (c) =>
    c.path === `/api/v1/inbox/${base.id}` ? json(base) : undefined;
  show(`/inbox/${base.id}`);
  expect(
    await screen.findByRole("link", { name: "Open in app" }),
  ).toHaveAttribute("href", `jobman-dashboard://inbox/${base.id}`);
});

it.each([0, 5, 10, 30])(
  "honors %is inbox-detail refresh and retains explicit and foreground reads",
  async (seconds) => {
    vi.useFakeTimers();
    session.bootstrap.preferences.refreshSeconds = seconds;
    await act(async () => {
      show(`/inbox/${id(1)}`);
    });
    expect(screen.getByText("Synthetic job")).toBeVisible();
    expect(calls).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(seconds ? seconds * 1000 - 1 : 60000);
    });
    expect(calls).toHaveLength(1);
    if (seconds) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1);
      });
      expect(calls).toHaveLength(2);
    }
    const before = calls.length;
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    });
    expect(calls).toHaveLength(before + 1);
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(calls).toHaveLength(before + 2);
    expect(calls.every((call) => call.method === "GET")).toBe(true);
  },
);
