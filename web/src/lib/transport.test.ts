import { describe, it, expect, vi, afterEach } from "vitest";
import {
  APIError,
  apiQuery,
  request,
  resourcePath,
  setCSRFToken,
  dashboardClient,
} from "./transport";
afterEach(() => {
  vi.unstubAllGlobals();
  setCSRFToken();
});
describe("same-origin transport", () => {
  it("uses no-store and session cookies without putting tokens in URLs", async () => {
    const fetcher = vi.fn().mockResolvedValue(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetcher);
    await request("/api/v1/jobs");
    expect(fetcher).toHaveBeenCalledWith(
      "/api/v1/jobs",
      expect.objectContaining({
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
      }),
    );
  });
  it("sends CSRF, revisions and idempotency on supported personal mutations", async () => {
    const fetcher = vi.fn().mockResolvedValue(Response.json({ revision: "3" }));
    vi.stubGlobal("fetch", fetcher);
    setCSRFToken("test-csrf");
    await request("/api/v1/preferences", {
      method: "PUT",
      body: { timezone: "UTC" },
      revision: "2",
      idempotencyKey: "request-1",
    });
    const options = fetcher.mock.calls[0][1];
    expect(options.headers.get("X-CSRF-Token")).toBe("test-csrf");
    expect(options.headers.get("If-Match")).toBe("2");
    expect(options.headers.get("Idempotency-Key")).toBe("request-1");
  });
  it("rejects arbitrary origins before fetch", async () => {
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    await expect(
      request("https://other.example/api/v1/jobs"),
    ).rejects.toMatchObject({ code: "invalid_route" });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it("preserves API error classification and request ID", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        Response.json(
          {
            code: "authorization_unavailable",
            message: "Verification unavailable",
            requestId: "r1",
          },
          { status: 503 },
        ),
      ),
    );
    await expect(request("/api/v1/jobs")).rejects.toMatchObject({
      code: "authorization_unavailable",
      status: 503,
      requestId: "r1",
    });
  });
  it("does not mistake an HTML login page for data", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("<html>sign in</html>", {
          headers: { "Content-Type": "text/html" },
        }),
      ),
    );
    await expect(request("/api/v1/jobs")).rejects.toBeInstanceOf(APIError);
  });
  it("keeps abort distinct from disconnection", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new DOMException("cancelled", "AbortError")),
    );
    await expect(request("/api/v1/jobs")).rejects.toMatchObject({
      name: "AbortError",
    });
  });
  it("qualifies resource IDs and treats scope as opaque query data", () => {
    expect(
      resourcePath(
        { deploymentId: "control/a", namespaceId: "ns b" },
        "jobs",
        "job?x",
      ),
    ).toBe("/api/v1/deployments/control%2Fa/namespaces/ns%20b/jobs/job%3Fx");
    expect(
      new URL(
        apiQuery("jobs", {
          scope: JSON.stringify([{ deploymentId: "a", namespaceId: "b" }]),
        }),
        "https://dashboard.example",
      ).searchParams.get("scope"),
    ).toBe('[{"deploymentId":"a","namespaceId":"b"}]');
  });
});

it("generated callable client preserves source paths and secure transport", async () => {
  const fetcher = vi.fn().mockResolvedValue(Response.json({ job: {} }));
  vi.stubGlobal("fetch", fetcher);
  await dashboardClient.job({
    path: {
      deploymentId: "control/a",
      namespaceId: "namespace",
      jobId: "job?1",
    },
  });
  expect(fetcher.mock.calls[0][0]).toBe(
    "/api/v1/deployments/control%2Fa/namespaces/namespace/jobs/job%3F1",
  );
  expect(fetcher.mock.calls[0][1]).toMatchObject({
    cache: "no-store",
    credentials: "same-origin",
  });
});

describe("bounded browser reads", () => {
  afterEach(() => vi.useRealTimers());
  it("times out stalled headers, aborts fetch, and never retries an uncertain mutation", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn<typeof fetch>(() => new Promise<Response>(() => {}));
    vi.stubGlobal("fetch", fetcher);
    const checked = expect(
      request("/api/v1/preferences", { method: "PUT", body: {} }),
    ).rejects.toMatchObject({
      code: "request_timeout",
      message: expect.stringContaining("may have been saved"),
    });
    await vi.advanceTimersByTimeAsync(30000);
    await checked;
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });
  it("shares one deadline across headers and a stalled body without waiting for cancel", async () => {
    vi.useFakeTimers();
    const cancel = vi.fn(() => new Promise<void>(() => {}));
    const body = new ReadableStream<Uint8Array>({ cancel });
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((resolve) =>
            setTimeout(
              () =>
                resolve(
                  new Response(body, {
                    headers: { "Content-Type": "application/json" },
                  }),
                ),
              20000,
            ),
          ),
      ),
    );
    const checked = expect(request("/api/v1/jobs")).rejects.toMatchObject({
      code: "request_timeout",
    });
    await vi.advanceTimersByTimeAsync(29999);
    expect(cancel).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await checked;
    expect(cancel).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });
  it("keeps caller cancellation distinct even while the body is pending", async () => {
    const cancel = vi.fn();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(new ReadableStream({ cancel }), {
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    const controller = new AbortController();
    const checked = expect(
      request("/api/v1/jobs", { signal: controller.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
    await Promise.resolve();
    await Promise.resolve();
    controller.abort();
    await checked;
    expect(cancel).toHaveBeenCalledOnce();
  });
  it("rejects both declared and chunked oversized JSON before decoding", async () => {
    for (const declared of [true, false]) {
      const cancel = vi.fn();
      const body = new ReadableStream<Uint8Array>({
        start(stream) {
          if (!declared) stream.enqueue(new Uint8Array(4 * 1024 * 1024 + 1));
        },
        cancel,
      });
      const headers = new Headers({ "Content-Type": "application/json" });
      if (declared) headers.set("Content-Length", String(4 * 1024 * 1024 + 1));
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(new Response(body, { headers })),
      );
      await expect(request("/api/v1/jobs")).rejects.toMatchObject({
        code: "response_too_large",
      });
      expect(cancel).toHaveBeenCalledOnce();
    }
  });
  it("accepts the exact byte ceiling and split UTF-8 without changing JSON values", async () => {
    const bytes = new TextEncoder().encode(
      JSON.stringify("€" + "x".repeat(4 * 1024 * 1024 - 5)),
    );
    expect(bytes.length).toBe(4 * 1024 * 1024);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          new ReadableStream({
            start(stream) {
              stream.enqueue(bytes.subarray(0, 2));
              stream.enqueue(bytes.subarray(2));
              stream.close();
            },
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    const value = await request<string>("/api/v1/jobs");
    expect(value.startsWith("€")).toBe(true);
    expect(value.length).toBe(4 * 1024 * 1024 - 4);
  });
});
