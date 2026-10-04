import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SettingsPage } from "./Settings";
import { setCSRFToken } from "../lib/transport";
import type { Device } from "../lib/devices";
import { auditAccessibility } from "../test-accessibility";
const session = vi.hoisted(() => ({
  identity: "alice",
  bootstrap: {
    account: { id: "account-a", displayName: "Alice" },
    preferences: {
      revision: "1",
      timezone: "UTC",
      appearance: "system",
      refreshSeconds: 10,
    },
    sources: [],
  },
  refreshBootstrap: vi.fn(),
  logout: vi.fn(),
}));
vi.mock("../lib/session", () => ({ useSession: () => session }));
const base: Device = {
  installationId: "81000000-0000-4000-8000-000000000001",
  revision: "9007199254740993",
  label: "Alice phone",
  topic: "test.jobman.dashboard",
  environment: "sandbox",
  state: "bound",
  enabled: true,
  muted: false,
  permission: "authorized",
  tokenStatus: "current",
  revocationReady: true,
  createdAt: "2026-10-04T00:00:00Z",
  updatedAt: "2026-10-04T00:00:00Z",
  lastSeenAt: "2026-10-04T00:00:00Z",
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
let current: Device[],
  calls: Call[],
  handle: ((c: Call) => Response | Promise<Response> | undefined) | undefined;
beforeEach(() => {
  session.identity = "alice";
  calls = [];
  current = [{ ...base }];
  handle = undefined;
  setCSRFToken("csrf-current");
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
      const override = handle?.(call);
      if (override) return override;
      if (call.method === "GET") return json({ items: current });
      if (call.method === "DELETE") {
        current = [];
        return new Response(null, { status: 204 });
      }
      if (call.method === "PUT" && path.endsWith("/settings")) {
        current = [
          {
            ...current[0],
            ...(call.body as object),
            revision: (BigInt(current[0].revision) + 1n).toString(),
          },
        ];
        return json(current[0]);
      }
      return json({});
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  setCSRFToken();
});
const phone = () => screen.getByRole("article", { name: "Alice phone" });
it("keeps owned device details, editing and removal confirmation accessible", async () => {
  render(
    <main>
      <SettingsPage />
    </main>,
  );
  await screen.findByRole("article", { name: "Alice phone" });
  await auditAccessibility();
  await userEvent.click(
    within(phone()).getByRole("button", { name: "Edit device" }),
  );
  await screen.findByLabelText("Device label");
  await auditAccessibility();
  await userEvent.click(screen.getByRole("button", { name: "Cancel editing" }));
  await userEvent.click(
    within(phone()).getByRole("button", { name: "Remove device" }),
  );
  await screen.findByRole("button", { name: "Confirm removal" });
  await auditAccessibility();
});
it("lists owned devices with setup/permission state and no unsupported paging query", async () => {
  current = [{ ...base, revocationReady: false }];
  render(<SettingsPage />);
  expect(
    await screen.findByText("Finish notification setup in the iPhone app"),
  ).toBeVisible();
  expect(screen.getByText("Authorized")).toBeVisible();
  expect(calls[0].path).toBe("/api/v1/devices");
  expect(
    screen.queryByRole("button", { name: /Bind|Register/ }),
  ).not.toBeInTheDocument();
});
it("edits labels and remote preferences with exact wide CAS and browser CSRF", async () => {
  const user = userEvent.setup();
  render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Edit device" }),
  );
  const label = screen.getByLabelText("Device label");
  await user.clear(label);
  await user.type(label, "Travel phone");
  await user.click(screen.getByLabelText("Muted on this device"));
  await user.click(screen.getByRole("button", { name: "Save device" }));
  await screen.findByRole("article", { name: "Travel phone" });
  const save = calls.find((c) => c.method === "PUT")!;
  expect(save.body).toEqual({
    label: "Travel phone",
    enabled: true,
    muted: true,
  });
  expect(save.headers.get("If-Match")).toBe("9007199254740993");
  expect(save.headers.get("X-CSRF-Token")).toBe("csrf-current");
  expect(save.path).toBe(`/api/v1/devices/${base.installationId}/settings`);
});
it("keeps stopped and muted preferences independent and requires explicit removal confirmation", async () => {
  const user = userEvent.setup();
  render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Mute device" }),
  );
  await screen.findByText("Device muted");
  await user.click(
    within(phone()).getByRole("button", { name: "Stop delivery" }),
  );
  await screen.findByText("Delivery stopped");
  expect(calls.filter((c) => c.method === "PUT")[1].body).toEqual({
    label: "Alice phone",
    enabled: false,
    muted: true,
  });
  await user.click(
    within(phone()).getByRole("button", { name: "Remove device" }),
  );
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  await user.click(screen.getByRole("button", { name: "Keep device" }));
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  await user.click(
    within(phone()).getByRole("button", { name: "Remove device" }),
  );
  await user.click(screen.getByRole("button", { name: "Confirm removal" }));
  await screen.findByText("No registered iPhones");
  const deletion = calls.find((c) => c.method === "DELETE")!;
  expect(deletion.headers.get("If-Match")).toBe("9007199254740995");
  expect(deletion.body).toBeUndefined();
});
it("does not retry an uncertain write until the user obtains a fresh list", async () => {
  const user = userEvent.setup();
  handle = (c) =>
    c.method === "PUT"
      ? Promise.reject(new TypeError("connection lost"))
      : undefined;
  render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Stop delivery" }),
  );
  await screen.findByText(/Refresh devices to check/);
  expect(screen.queryByRole("article")).not.toBeInTheDocument();
  expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
  await user.click(screen.getByRole("button", { name: "Refresh devices" }));
  await screen.findByRole("article", { name: "Alice phone" });
  expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
});
it("clears inaccessible content and requires reload after a revision conflict", async () => {
  const user = userEvent.setup();
  handle = (c) =>
    c.method === "PUT"
      ? json(
          {
            code: "revision_conflict",
            message: "Synthetic current revision changed",
          },
          409,
        )
      : undefined;
  render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Mute device" }),
  );
  await screen.findByText(/This device changed elsewhere/);
  expect(screen.queryByRole("article")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Refresh devices" }));
  await screen.findByRole("article", { name: "Alice phone" });
  handle = (c) =>
    c.method === "PUT"
      ? json({ code: "forbidden", message: "Current account disabled" }, 403)
      : undefined;
  await user.click(
    within(phone()).getByRole("button", { name: "Mute device" }),
  );
  await screen.findByText("Current account disabled");
  expect(screen.queryByText("Alice phone")).not.toBeInTheDocument();
});
it("locks editing and removal controls while a change is in flight", async () => {
  let complete!: (r: Response) => void;
  handle = (c) =>
    c.method === "PUT"
      ? new Promise((resolve) => {
          complete = resolve;
        })
      : undefined;
  const user = userEvent.setup();
  render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Edit device" }),
  );
  await user.click(screen.getByRole("button", { name: "Save device" }));
  expect(screen.getByRole("button", { name: "Cancel editing" })).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Refresh devices" }),
  ).toBeDisabled();
  expect(
    within(phone()).getByRole("button", { name: "Remove device" }),
  ).toBeDisabled();
  await act(async () => complete(json(base)));
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Cancel editing" }),
    ).not.toBeInTheDocument(),
  );
});
it("aborts and ignores a late mutation result after account switch", async () => {
  let complete!: (r: Response) => void;
  handle = (c) =>
    c.method === "PUT"
      ? new Promise((resolve) => {
          complete = resolve;
        })
      : undefined;
  const user = userEvent.setup();
  const view = render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Mute device" }),
  );
  const mutation = calls.find((c) => c.method === "PUT")!;
  session.identity = "bob";
  current = [];
  view.rerender(<SettingsPage />);
  expect(mutation.signal?.aborted).toBe(true);
  expect(screen.queryByText("Alice phone")).not.toBeInTheDocument();
  await act(async () => complete(json({ ...base, muted: true })));
  await screen.findByText("No registered iPhones");
  expect(
    screen.queryByText(/Device preferences saved/),
  ).not.toBeInTheDocument();
});
it("invalidates an editor when a fresh list shows a newer revision", async () => {
  const user = userEvent.setup();
  render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Edit device" }),
  );
  current = [{ ...base, revision: "9007199254740994" }];
  fireEvent(window, new Event("online"));
  await screen.findByText(/This device changed while you were editing/);
  expect(screen.getByRole("button", { name: "Save device" })).toBeDisabled();
  expect(calls.some((c) => c.method === "PUT")).toBe(false);
});
it("keeps the new mutation abort fence after a revoked request settles late", async () => {
  let first!: (response: Response) => void,
    second!: (response: Response) => void;
  let denied = false,
    writes = 0;
  handle = (call) => {
    if (call.method === "GET" && denied)
      return json({ code: "forbidden", message: "Access changed" }, 403);
    if (call.method === "PUT")
      return new Promise((resolve) => {
        if (++writes === 1) first = resolve;
        else second = resolve;
      });
    return undefined;
  };
  const user = userEvent.setup(),
    view = render(<SettingsPage />);
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Mute device" }),
  );
  denied = true;
  fireEvent(window, new Event("online"));
  await screen.findByText("Access changed");
  expect(calls.find((call) => call.method === "PUT")!.signal?.aborted).toBe(
    true,
  );
  denied = false;
  await user.click(screen.getByRole("button", { name: "Refresh devices" }));
  await screen.findByRole("article", { name: "Alice phone" });
  await user.click(
    within(phone()).getByRole("button", { name: "Stop delivery" }),
  );
  const latest = calls.filter((call) => call.method === "PUT")[1];
  await act(async () => first(json({ ...base, muted: true })));
  session.identity = "bob";
  current = [];
  view.rerender(<SettingsPage />);
  expect(latest.signal?.aborted).toBe(true);
  await act(async () => second(json({ ...base, enabled: false })));
  await screen.findByText("No registered iPhones");
});
