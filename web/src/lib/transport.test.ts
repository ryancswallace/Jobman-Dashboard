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
