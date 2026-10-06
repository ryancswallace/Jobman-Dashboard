import type { JobRef, Scope, Source } from "./models";
export function title(value?: string) {
  return value
    ? value.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase())
    : "Unavailable";
}
export function jobStatus(value?: string) {
  switch (value) {
    case "accepted":
      return "Submitted";
    case "assigning":
      return "Assigning execution";
    case "accepted_execution":
      return "Execution accepted";
    case "terminal":
      return "Finished";
    default:
      return title(value);
  }
}

export function statusConfidence(value?: string) {
  if (value === "stale") return "Outdated";
  if (value === "lost") return "Reported lost";
  return title(value);
}

export function errorHeading(code: string) {
  const headings: Record<string, string> = {
    unauthenticated: "Sign in again",
    forbidden: "Access unavailable",
    authorization_unavailable: "Unable to check your access",
    not_found_or_inaccessible: "Item unavailable",
    source_unavailable: "Service unavailable",
    unsupported_contract: "Feature unavailable",
    not_implemented: "Feature unavailable",
    invalid_request: "Check your request",
    invalid_rule: "Check the alert rule",
    invalid_device: "Check device settings",
    invalid_preferences: "Check your preferences",
    invalid_route: "Link unavailable",
    invalid_response: "Unable to read the response",
    response_too_large: "Too much data to display",
    network_unavailable: "Unable to connect",
    request_timeout: "Request took too long",
    request_failed: "Request unsuccessful",
    revision_conflict: "Changes need to be refreshed",
    cursor_expired: "Refresh this list",
    target_changed: "Target configuration changed",
    stream_changed: "Log changed",
    log_gap: "Part of the log is missing",
    logout_pending: "Sign-out not yet confirmed",
    rate_limited: "Too many requests",
  };
  return Object.hasOwn(headings, code)
    ? headings[code]
    : "Unable to complete this request";
}
export function count(value?: string) {
  if (value === undefined) return "Unavailable";
  try {
    return BigInt(value).toLocaleString();
  } catch {
    return "Unavailable";
  }
}
export function timestamp(value?: string, timezone = "UTC") {
  if (!value || Number.isNaN(Date.parse(value))) return "Unavailable";
  try {
    return (
      new Intl.DateTimeFormat(undefined, {
        dateStyle: "medium",
        timeStyle: "medium",
        timeZone: timezone,
      }).format(new Date(value)) + ` (${timezone})`
    );
  } catch {
    return value;
  }
}
export function relative(value?: string) {
  if (!value || Number.isNaN(Date.parse(value))) return "Time unavailable";
  const seconds = Math.max(
    0,
    Math.round((Date.now() - Date.parse(value)) / 1000),
  );
  return seconds < 60
    ? `${seconds}s ago`
    : seconds < 3600
      ? `${Math.floor(seconds / 60)}m ago`
      : seconds < 86400
        ? `${Math.floor(seconds / 3600)}h ago`
        : `${Math.floor(seconds / 86400)}d ago`;
}
export function jobRoute(ref: JobRef) {
  return `/deployments/${encodeURIComponent(ref.deploymentId)}/namespaces/${encodeURIComponent(ref.namespaceId)}/jobs/${encodeURIComponent(ref.jobId)}`;
}
export function workloadRoute(ref: {
  deploymentId: string;
  namespaceId: string;
  id: string;
  kind: string;
}) {
  return `/deployments/${encodeURIComponent(ref.deploymentId)}/namespaces/${encodeURIComponent(ref.namespaceId)}/workloads/${encodeURIComponent(ref.kind)}/${encodeURIComponent(ref.id)}`;
}
export function sourceLabel(
  sources: Source[],
  deploymentId: string,
  namespaceId?: string,
) {
  const source = sources.find((s) => s.deploymentId === deploymentId);
  return `${source?.displayName ?? deploymentId}${namespaceId ? ` / ${source?.namespaces.find((n) => n.namespaceId === namespaceId)?.name ?? namespaceId}` : ""}`;
}
export function scopeQuery(scope: Scope, sources: Source[]) {
  const namespaces = scope.namespace
    ? [scope.namespace]
    : sources
        .filter((s) => scope.deployments.includes(s.deploymentId))
        .flatMap((s) =>
          s.namespaces.map((n) => ({
            deploymentId: s.deploymentId,
            namespaceId: n.namespaceId,
          })),
        );
  return { scope: JSON.stringify(namespaces) };
}
export function scopeKey(scope: Scope) {
  return JSON.stringify([scope.deployments.slice().sort(), scope.namespace]);
}
export function safeReturnPath(path: string) {
  return path.startsWith("/") &&
    !path.startsWith("//") &&
    !/[\\\r\n]/.test(path)
    ? path
    : "/";
}

export function localDateValue(value?: string) {
  return value && Number.isFinite(Date.parse(value))
    ? new Date(value).toISOString().slice(0, 16)
    : "";
}
