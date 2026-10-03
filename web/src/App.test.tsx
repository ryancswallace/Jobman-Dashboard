import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import { App } from "./App";
const ns = {
  id: "ns",
  name: "Research",
  roles: ["viewer", "operator"],
  capabilities: ["jobs.read", "logs.read"],
  authorizationVersion: "1",
  authorizationCheckedAt: new Date().toISOString(),
  authorizationExpiresAt: new Date(Date.now() + 120000).toISOString(),
};
const bootstrap = {
  apiVersion: "jobman.dashboard/v1",
  fixtureMode: true,
  completeness: "complete",
  account: { id: "alice", displayName: "Lab Alice" },
  deployments: [
    { id: "east", name: "Lab East", status: "available", namespaces: [ns] },
    { id: "west", name: "Lab West", status: "available", namespaces: [ns] },
  ],
  preferences: {
    revision: "1",
    timezone: "UTC",
    appearance: "light",
    refreshSeconds: 0,
  },
  csrfToken: "fixture-csrf",
};
const job = {
  deploymentId: "east",
  namespaceId: "ns",
  id: "duplicate-id",
  name: "Synthetic test",
  targetId: "slurm",
  revision: "1",
  phase: "running",
  desiredState: "cancel",
  confidence: "stale",
  createdAt: "2026-10-03T12:00:00Z",
  updatedAt: "2026-10-03T12:05:00Z",
  labels: {},
};
const page = (items: unknown[]) => ({
  items,
  completeness: "complete",
  sources: [],
  fetchedAt: new Date().toISOString(),
});
let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  // Node's native experimental Web Storage can shadow jsdom's implementation.
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, String(value)),
    removeItem: (key: string) => storage.delete(key),
    clear: () => storage.clear(),
    key: (index: number) => [...storage.keys()][index] ?? null,
    get length() {
      return storage.size;
    },
  } satisfies Storage);
  history.replaceState(null, "", "/");
  fetcher = vi
    .fn()
    .mockImplementation(async (input: string, options?: RequestInit) => {
      const url = new URL(input, "http://localhost");
      if (url.pathname === "/api/v1/bootstrap") return Response.json(bootstrap);
      if (url.pathname === "/api/v1/jobs")
        return Response.json(page([job, { ...job, deploymentId: "west" }]));
      if (url.pathname === "/api/v1/overview")
        return Response.json({
          active: 2,
          awaitingExecution: 0,
          running: 2,
          evidenceAttention: 2,
          missingCompletionTime: 0,
          terminal: { success: 0, failure: 0 },
          window: { from: "2026-10-02T12:00:00Z", to: "2026-10-03T12:00:00Z" },
          sources: [],
          completeness: "complete",
        });
      if (url.pathname.endsWith("/jobs/duplicate-id"))
        return Response.json({ job, fetchedAt: new Date().toISOString() });
      if (options?.method === "POST")
        return Response.json({ id: "new", revision: "1" }, { status: 201 });
      return Response.json(page([]));
    });
  vi.stubGlobal("fetch", fetcher);
  vi.stubGlobal("scrollTo", vi.fn());
});
afterEach(() => {
  vi.unstubAllGlobals();
});
describe("web monitoring workflows", () => {
  it("labels fixture mode and preserves two equal job IDs in separate source links", async () => {
    render(<App />);
    expect(
      await screen.findByText(/Development fixture environment/),
    ).toBeVisible();
    const links = await screen.findAllByRole("link", {
      name: "Synthetic test",
    });
    expect(links).toHaveLength(2);
    expect(links.map((a) => a.getAttribute("href"))).toEqual(
      expect.arrayContaining([
        "/deployments/east/namespaces/ns/jobs/duplicate-id",
        "/deployments/west/namespaces/ns/jobs/duplicate-id",
      ]),
    );
    expect(
      screen.queryByRole("button", { name: /cancel job/i }),
    ).not.toBeInTheDocument();
  });
  it("shows cancellation intent without inventing a cancelled outcome or start time", async () => {
    history.replaceState(
      null,
      "",
      "/deployments/east/namespaces/ns/jobs/duplicate-id",
    );
    render(<App />);
    expect(await screen.findByText(/Cancellation is requested/)).toBeVisible();
    expect(screen.getByText("Stale")).toBeVisible();
    expect(screen.queryByText("Cancelled")).not.toBeInTheDocument();
    expect(screen.getByText("Started").nextElementSibling).toHaveTextContent(
      "Unavailable",
    );
  });
  it("sends filters to the API and keeps them in the shareable URL", async () => {
    history.replaceState(null, "", "/jobs");
    render(<App />);
    await screen.findAllByRole("link", { name: "Synthetic test" });
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Phase" }),
      "running",
    );
    await waitFor(() => expect(location.search).toContain("phase=running"));
    expect(
      fetcher.mock.calls.some(([path]) => path.includes("phase=running")),
    ).toBe(true);
  });
  it("preserves a removed explicit namespace instead of broadening to other grants", async () => {
    history.replaceState(null, "", "/jobs?sources=east&namespace=removed");
    render(<App />);
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(([path]) => {
          const url = new URL(path, "http://localhost");
          return (
            url.pathname === "/api/v1/jobs" &&
            url.searchParams.get("scope") ===
              JSON.stringify([{ deploymentId: "east", namespaceId: "removed" }])
          );
        }),
      ).toBe(true),
    );
  });
  it("creates an opt-in namespace rule across two sources with all terminal outcomes", async () => {
    history.replaceState(null, "", "/alerts");
    render(<App />);
    await screen.findByRole("button", { name: "+ New alert rule" });
    await userEvent.click(
      screen.getByRole("button", { name: "+ New alert rule" }),
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Rule name" }),
      "Synthetic team updates",
    );
    await userEvent.click(
      screen.getByRole("radio", { name: /All jobs in selected namespaces/ }),
    );
    const fieldset = screen.getByRole("group", {
      name: "Authorized namespaces",
    });
    await userEvent.click(
      within(fieldset).getByRole("checkbox", { name: "Lab East / Research" }),
    );
    await userEvent.click(
      within(fieldset).getByRole("checkbox", { name: "Lab West / Research" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "All terminal" }));
    await userEvent.click(screen.getByRole("button", { name: "Save rule" }));
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(([, options]) => options.method === "POST"),
      ).toBe(true),
    );
    const mutation = fetcher.mock.calls.find(
      ([, options]) => options.method === "POST",
    )!;
    expect(JSON.parse(mutation[1].body)).toMatchObject({
      scope: "namespace_jobs",
      outcomeMode: "all_terminal",
      namespaces: [
        { deploymentId: "east", namespaceId: "ns" },
        { deploymentId: "west", namespaceId: "ns" },
      ],
    });
    expect(mutation[1].headers.get("X-CSRF-Token")).toBe("fixture-csrf");
  });
  it("preserves denied and network failures rather than pretending lists are empty", async () => {
    history.replaceState(null, "", "/targets");
    fetcher.mockImplementation(async (input: string) =>
      input === "/api/v1/bootstrap"
        ? Response.json(bootstrap)
        : Response.json(
            {
              code: "authorization_unavailable",
              message: "Directory verification is unavailable",
            },
            { status: 503 },
          ),
    );
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Directory verification is unavailable",
    );
    expect(screen.queryByText("No targets available")).not.toBeInTheDocument();
  });
});

it("offline sign-out clears sensitive content and does not silently restore the session", async () => {
  history.replaceState(null, "", "/settings");
  render(<App />);
  await screen.findByRole("button", { name: "Sign out" });
  const previous = fetcher.getMockImplementation()!;
  fetcher.mockImplementation((input: string, options?: RequestInit) =>
    input === "/auth/logout"
      ? Promise.reject(new TypeError("offline"))
      : previous(input, options),
  );
  await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
  expect(
    await screen.findByText(/The server could not confirm sign-out/),
  ).toBeVisible();
  expect(screen.queryByText("Lab Alice")).not.toBeInTheDocument();
  expect(window.localStorage.getItem("jobman.signoutPending")).toBe("1");
});
