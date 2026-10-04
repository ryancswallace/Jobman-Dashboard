import { afterEach, expect, it, vi } from "vitest";
import {
  decodeDevice,
  decodeDevices,
  deviceSettings,
  deviceDeliveryState,
  updateDevice,
  removeDevice,
  type Device,
} from "./devices";
import { setCSRFToken } from "./transport";
const device: Device = {
  installationId: "81000000-0000-4000-8000-000000000001",
  revision: "9007199254740993",
  label: "Synthetic iPhone",
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
afterEach(() => {
  vi.unstubAllGlobals();
  setCSRFToken();
});
it("bounds device responses and preserves exact decimal revisions", () => {
  expect(decodeDevice(device).revision).toBe("9007199254740993");
  expect(decodeDevices({ items: [device] }).items).toEqual([device]);
  for (const bad of [
    { ...device, revision: 9007199254740993 },
    { ...device, revision: "9223372036854775808" },
    { ...device, revision: "01" },
    { ...device, revocationReady: undefined },
    { ...device, token: "secret" },
    { ...device, state: "removed" },
    { ...device, label: "a\n" },
    { ...device, label: "é".repeat(61) },
    { ...device, lastSeenAt: "2027-01-01T00:00:00Z" },
  ])
    expect(() => decodeDevice(bad)).toThrow();
  expect(() => decodeDevices({ items: [device, device] })).toThrow();
  expect(() => decodeDevices({ items: Array(51).fill(device) })).toThrow();
  expect(() => decodeDevices({ items: [], nextCursor: "hidden" })).toThrow();
});
it("uses generated settings and removal operations with body-only settings and current CAS", async () => {
  const fetch = vi.fn(async (_path: string, init: RequestInit) =>
    init.method === "DELETE"
      ? new Response(null, { status: 204 })
      : new Response(
          JSON.stringify({
            ...device,
            label: "Renamed",
            revision: "9007199254740994",
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
  );
  vi.stubGlobal("fetch", fetch);
  setCSRFToken("current-browser-csrf");
  const signal = new AbortController().signal;
  await updateDevice(
    device,
    { label: " Renamed ", enabled: true, muted: false },
    signal,
  );
  await removeDevice(device, signal);
  expect(fetch.mock.calls[0][0]).toBe(
    `/api/v1/devices/${device.installationId}/settings`,
  );
  const request = fetch.mock.calls[0][1];
  expect(request.method).toBe("PUT");
  expect(new Headers(request.headers).get("If-Match")).toBe(device.revision);
  expect(new Headers(request.headers).get("X-CSRF-Token")).toBe(
    "current-browser-csrf",
  );
  expect(JSON.parse(String(request.body))).toEqual({
    label: "Renamed",
    enabled: true,
    muted: false,
  });
  expect(request.signal?.aborted).toBe(false);
  expect(fetch.mock.calls[1][0]).toBe(
    `/api/v1/devices/${device.installationId}`,
  );
  expect(fetch.mock.calls[1][1].method).toBe("DELETE");
  expect(fetch.mock.calls[1][1].body).toBeUndefined();
});
it("refuses mismatched successful identities and unsupported setting labels", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            ...device,
            installationId: "81000000-0000-4000-8000-000000000002",
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  await expect(
    updateDevice(
      device,
      { label: "Other", enabled: true, muted: false },
      new AbortController().signal,
    ),
  ).rejects.toThrow();
  expect(() =>
    deviceSettings({ label: "é".repeat(61), enabled: true, muted: false }),
  ).toThrow();
  expect(deviceDeliveryState({ ...device, revocationReady: false })).toContain(
    "setup",
  );
  expect(deviceDeliveryState({ ...device, permission: "denied" })).toContain(
    "iOS",
  );
  expect(deviceDeliveryState({ ...device, muted: true })).toContain("muted");
});

it("propagates caller cancellation through the deadline signal during a device mutation", async () => {
  const fetcher = vi.fn<typeof fetch>(() => new Promise<Response>(() => {}));
  vi.stubGlobal("fetch", fetcher);
  const caller = new AbortController();
  const pending = updateDevice(
    device,
    { label: "Test", enabled: true, muted: false },
    caller.signal,
  );
  const checked = expect(pending).rejects.toMatchObject({ name: "AbortError" });
  const signal = fetcher.mock.calls[0][1]?.signal;
  expect(signal).toBeDefined();
  expect(signal?.aborted).toBe(false);
  caller.abort();
  await checked;
  expect(signal?.aborted).toBe(true);
  expect(fetcher).toHaveBeenCalledTimes(1);
});
