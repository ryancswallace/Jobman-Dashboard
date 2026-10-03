import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { useResource } from "./useResource";
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
const response = (data: unknown, status = 200) =>
  Response.json(data, { status });
describe("account/scope-safe reads", () => {
  it("aborts obsolete work and never publishes an old-account response", async () => {
    let completeOld!: (response: Response) => void;
    const fetcher = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            completeOld = resolve;
          }),
      )
      .mockResolvedValue(response({ name: "account-b" }));
    vi.stubGlobal("fetch", fetcher);
    const hook = renderHook(
      ({ account }) =>
        useResource<{ name: string }>("/api/v1/jobs", account, 0),
      { initialProps: { account: "a" } },
    );
    hook.rerender({ account: "b" });
    expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
    await waitFor(() =>
      expect(hook.result.current.data?.name).toBe("account-b"),
    );
    await act(async () => {
      completeOld(response({ name: "account-a-secret" }));
    });
    expect(hook.result.current.data?.name).toBe("account-b");
  });
  it("clears previously shown content on access revocation", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response({ secret: "namespace-data" }))
      .mockResolvedValue(
        response({ code: "forbidden", message: "Access removed" }, 403),
      );
    vi.stubGlobal("fetch", fetcher);
    const hook = renderHook(() =>
      useResource("/api/v1/jobs", "account:scope", 0),
    );
    await waitFor(() => expect(hook.result.current.data).toBeDefined());
    act(() => hook.result.current.refresh());
    await waitFor(() =>
      expect(hook.result.current.error?.code).toBe("forbidden"),
    );
    expect(hook.result.current.data).toBeUndefined();
  });
  it("retains last known data after a network failure and marks the failure", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response({ phase: "running" }))
      .mockRejectedValue(new TypeError("network"));
    vi.stubGlobal("fetch", fetcher);
    const hook = renderHook(() =>
      useResource<{ phase: string }>("/api/v1/jobs", "a", 0),
    );
    await waitFor(() => expect(hook.result.current.data).toBeDefined());
    act(() => hook.result.current.refresh());
    await waitFor(() =>
      expect(hook.result.current.error?.code).toBe("network_unavailable"),
    );
    expect(hook.result.current.data?.phase).toBe("running");
  });
  it("deduplicates manual refresh while an existing read is pending", () => {
    const fetcher = vi.fn().mockImplementation(() => new Promise(() => {}));
    vi.stubGlobal("fetch", fetcher);
    const hook = renderHook(() => useResource("/api/v1/jobs", "a", 0));
    act(() => {
      hook.result.current.refresh();
      hook.result.current.refresh();
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});
