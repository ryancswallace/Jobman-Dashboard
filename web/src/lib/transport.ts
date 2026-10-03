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
      "The requested route is not a Dashboard API route.",
    );
  const headers = new Headers({ Accept: "application/json" });
  if (options.body !== undefined)
    headers.set("Content-Type", "application/json");
  if (options.method && options.method !== "GET")
    headers.set("X-CSRF-Token", csrfToken);
  if (options.revision) headers.set("If-Match", options.revision);
  if (options.idempotencyKey)
    headers.set("Idempotency-Key", options.idempotencyKey);
  let response: Response;
  try {
    response = await fetch(path, {
      method: options.method ?? "GET",
      body:
        options.body === undefined ? undefined : JSON.stringify(options.body),
      headers,
      signal: options.signal,
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError")
      throw error;
    throw new APIError(
      "network_unavailable",
      "Dashboard could not be reached. Check your private-network or VPN connection.",
    );
  }
  const contentType = response.headers.get("Content-Type") ?? "";
  if (response.status === 204) return undefined as T;
  if (!contentType.includes("application/json"))
    throw new APIError(
      "invalid_response",
      "Dashboard returned an unexpected response. Try again or contact your operator.",
      response.status,
    );
  let data: unknown;
  try {
    data = await response.json();
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
