import {
  DashboardClient,
  type DashboardRequest,
} from "../../../contracts/typescript/dashboard.generated";
export class APIError extends Error {
  constructor(
    public code: string,
    message: string,
    public status = 0,
    public requestId?: string,
  ) {
    super(message);
    this.name = "APIError";
  }
}
let csrfToken = "";
export function setCSRFToken(token?: string) {
  csrfToken = token ?? "";
}
export async function request<T>(
  path: string,
  options: {
    signal?: AbortSignal;
    method?: string;
    body?: unknown;
    revision?: string;
    idempotencyKey?: string;
  } = {},
): Promise<T> {
  if (!path.startsWith("/api/v1/") || path.includes("://"))
    throw new APIError(
      "invalid_route",
      "This link does not point to a supported Dashboard operation.",
    );
  const headers = new Headers({ Accept: "application/json" });
  if (options.body !== undefined)
    headers.set("Content-Type", "application/json");
  if (options.method && options.method !== "GET")
    headers.set("X-CSRF-Token", csrfToken);
  if (options.revision) headers.set("If-Match", options.revision);
  if (options.idempotencyKey)
    headers.set("Idempotency-Key", options.idempotencyKey);
  const controller = new AbortController();
  let timedOut = false;
  const parentAbort = () => controller.abort();
  if (options.signal?.aborted)
    throw new DOMException("The request was cancelled.", "AbortError");
  options.signal?.addEventListener("abort", parentAbort, { once: true });
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 30000);
  let interrupt!: () => void;
  const interrupted = new Promise<never>((_, reject) => {
    interrupt = () =>
      reject(new DOMException("The request was cancelled.", "AbortError"));
    controller.signal.addEventListener("abort", interrupt, { once: true });
  });
  const bounded = <Value>(work: Promise<Value>) =>
    Promise.race([work, interrupted]);
  let response: Response | undefined;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  let consumed = false;
  try {
    response = await bounded(
      fetch(path, {
        method: options.method ?? "GET",
        body:
          options.body === undefined ? undefined : JSON.stringify(options.body),
        headers,
        signal: controller.signal,
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
      }),
    );
    const contentType = response.headers.get("Content-Type") ?? "";
    if (response.status === 204) return undefined as T;
    if (!contentType.includes("application/json"))
      throw new APIError(
        "invalid_response",
        "Dashboard returned an unexpected response. Try again or contact your Dashboard administrator.",
        response.status,
      );
    const maximumBytes = 4 * 1024 * 1024;
    const declared = response.headers.get("Content-Length");
    if (
      declared &&
      /^\d+$/.test(declared) &&
      BigInt(declared) > BigInt(maximumBytes)
    )
      throw new APIError(
        "response_too_large",
        "Dashboard returned more data than this view can safely load.",
        response.status,
      );
    reader = response.body?.getReader();
    const parts: string[] = [];
    const decoder = new TextDecoder("utf-8", { fatal: true });
    let bytes = 0;
    while (reader) {
      const part = await bounded(reader.read());
      if (part.done) {
        consumed = true;
        break;
      }
      bytes += part.value.byteLength;
      if (bytes > maximumBytes)
        throw new APIError(
          "response_too_large",
          "Dashboard returned more data than this view can safely load.",
          response.status,
        );
      parts.push(decoder.decode(part.value, { stream: true }));
    }
    parts.push(decoder.decode());
    let data: unknown;
    try {
      data = JSON.parse(parts.join(""));
    } catch {
      throw new APIError(
        "invalid_response",
        "Dashboard returned an unreadable response.",
        response.status,
      );
    }
    if (!response.ok) {
      const envelope = data as {
        error?: { code?: string; message?: string; requestId?: string };
        code?: string;
        message?: string;
        requestId?: string;
      };
      const error = envelope.error ?? envelope;
      throw new APIError(
        error.code ??
          (response.status === 401 ? "unauthenticated" : "request_failed"),
        error.message ?? "Dashboard could not complete this request.",
        response.status,
        error.requestId,
      );
    }
    return data as T;
  } catch (error) {
    if (timedOut)
      throw new APIError(
        "request_timeout",
        options.method && options.method !== "GET"
          ? "Dashboard did not finish within 30 seconds. Your change may have been saved; check the current state before retrying."
          : "Dashboard did not finish within 30 seconds. Check your private-network or VPN connection and try again.",
      );
    if (
      controller.signal.aborted ||
      (error instanceof DOMException && error.name === "AbortError")
    )
      throw new DOMException("The request was cancelled.", "AbortError");
    if (error instanceof APIError) throw error;
    throw new APIError(
      response ? "invalid_response" : "network_unavailable",
      response
        ? "Dashboard returned an unreadable response."
        : "Dashboard could not be reached. Check your private-network or VPN connection.",
    );
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener("abort", parentAbort);
    controller.signal.removeEventListener("abort", interrupt);
    if (!consumed) {
      controller.abort();
      // Cancellation must not extend the overall deadline if a stream stalls.
      if (reader) void reader.cancel().catch(() => {});
      else if (response?.body) void response.body.cancel().catch(() => {});
    }
  }
}

export function resourcePath(
  ref: { deploymentId: string; namespaceId: string },
  kind: string,
  id: string,
): string {
  return `/api/v1/deployments/${encodeURIComponent(ref.deploymentId)}/namespaces/${encodeURIComponent(ref.namespaceId)}/${kind}/${encodeURIComponent(id)}`;
}
export function apiQuery(
  path: string,
  values: Record<string, string | number | undefined>,
): string {
  const query = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") query.set(key, String(value));
  });
  return `/api/v1/${path}${query.size ? `?${query}` : ""}`;
}
export const authorizationErrors = new Set([
  "unauthenticated",
  "forbidden",
  "authorization_unavailable",
  "not_found_or_inaccessible",
]);

// The generated client delegates all browser session/security behavior to this adapter.
export const dashboardClient = new DashboardClient(
  <T>(operation: DashboardRequest) => {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(operation.query ?? {})) {
      if (value !== undefined) query.set(key, String(value));
    }
    return request<T>(operation.path + (query.size ? `?${query}` : ""), {
      method: operation.method,
      signal: operation.signal,
      body: operation.body,
      revision: operation.headers?.["If-Match"],
      idempotencyKey: operation.headers?.["Idempotency-Key"],
    });
  },
);
