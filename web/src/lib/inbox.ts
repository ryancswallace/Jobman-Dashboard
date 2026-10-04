import type {
  InboxItem,
  InboxItemPage,
} from "../../../contracts/typescript/dashboard.generated";
import { APIError } from "./transport";
import { canonicalID, nameBytes } from "./rules";
export type { InboxItem, InboxItemPage };
const decimal = (value: unknown): value is string =>
  typeof value === "string" && /^(0|[1-9][0-9]{0,18})$/.test(value);
const instant = (value: unknown): value is string =>
  typeof value === "string" && Number.isFinite(Date.parse(value));
const invalid = (): never => {
  throw new APIError(
    "invalid_response",
    "The inbox response could not be verified. Refresh the view.",
  );
};
export function decodeInboxItem(
  value: unknown,
  expectedID?: string,
): InboxItem {
  const i = value as InboxItem;
  if (
    !i ||
    !canonicalID(i.id) ||
    (expectedID && i.id !== expectedID) ||
    !canonicalID(i.eventId) ||
    !canonicalID(i.controlInstanceId) ||
    !canonicalID(i.job?.deploymentId) ||
    !canonicalID(i.job?.namespaceId) ||
    !canonicalID(i.job?.jobId) ||
    typeof i.outcome !== "string" ||
    !i.outcome ||
    i.outcome.length > 64 ||
    !instant(i.eventAt) ||
    !instant(i.createdAt) ||
    !instant(i.expiresAt) ||
    Date.parse(i.expiresAt) <= Date.parse(i.createdAt) ||
    (i.observedCompletedAt !== undefined && !instant(i.observedCompletedAt)) ||
    typeof i.read !== "boolean" ||
    i.read !== (i.readAt !== undefined) ||
    (i.readAt !== undefined && !instant(i.readAt)) ||
    !["available", "missing", "unavailable"].includes(i.jobAvailability) ||
    (i.jobName !== undefined &&
      (typeof i.jobName !== "string" ||
        new TextEncoder().encode(i.jobName).length > 1024 ||
        i.jobAvailability !== "available")) ||
    !Array.isArray(i.matchedRules) ||
    !i.matchedRules.length ||
    i.matchedRules.length > 100
  )
    invalid();
  const rules = new Set<string>();
  for (const m of i.matchedRules) {
    if (
      !canonicalID(m.ruleId) ||
      rules.has(m.ruleId) ||
      !decimal(m.revision) ||
      m.revision === "0" ||
      typeof m.name !== "string" ||
      nameBytes(m.name) < 1 ||
      nameBytes(m.name) > 120 ||
      /[\u0000-\u001f\u007f-\u009f]/.test(m.name) ||
      !["watched_jobs", "my_jobs", "namespace_jobs"].includes(m.scope) ||
      !["selected", "all_terminal"].includes(m.outcomeMode) ||
      !Array.isArray(m.outcomes) ||
      m.outcomes.length > 6 ||
      m.outcomes.some(
        (v) =>
          ![
            "success",
            "failure",
            "cancelled",
            "timed_out",
            "aborted",
            "lost",
          ].includes(v),
      ) ||
      new Set(m.outcomes).size !== m.outcomes.length ||
      (m.outcomeMode === "selected") !== m.outcomes.length > 0
    )
      invalid();
    rules.add(m.ruleId);
  }
  if (
    !i.delivery ||
    !decimal(i.delivery.total) ||
    BigInt(i.delivery.total) > 50n ||
    !i.delivery.byState ||
    Array.isArray(i.delivery.byState) ||
    typeof i.delivery.byState !== "object"
  )
    invalid();
  let sum = 0n;
  for (const [state, n] of Object.entries(i.delivery.byState)) {
    if (
      !state ||
      state.length > 32 ||
      !decimal(n) ||
      BigInt(n) < 1n ||
      BigInt(n) > 50n
    )
      invalid();
    sum += BigInt(n);
  }
  if (sum !== BigInt(i.delivery.total)) invalid();
  return i;
}
export function decodeInbox(value: unknown): InboxItemPage {
  const p = value as InboxItemPage;
  if (
    !p ||
    !Array.isArray(p.items) ||
    p.items.length > 50 ||
    !decimal(p.unreadCount) ||
    !["complete", "partial"].includes(p.completeness) ||
    !Number.isInteger(p.unavailableSources) ||
    p.unavailableSources < 0 ||
    p.unavailableSources > 32 ||
    !Number.isInteger(p.inaccessibleScopes) ||
    p.inaccessibleScopes < 0 ||
    p.inaccessibleScopes > 320 ||
    !instant(p.fetchedAt) ||
    (p.nextCursor !== undefined &&
      (typeof p.nextCursor !== "string" ||
        !p.nextCursor ||
        p.nextCursor.length > 128)) ||
    ((p.unavailableSources > 0 || p.inaccessibleScopes > 0) &&
      p.completeness !== "partial")
  )
    invalid();
  const seen = new Set<string>();
  for (let n = 0; n < p.items.length; n++) {
    const i = decodeInboxItem(p.items[n]);
    if (
      seen.has(i.id) ||
      (i.jobAvailability === "unavailable" && p.completeness !== "partial") ||
      (n > 0 &&
        (Date.parse(i.createdAt) > Date.parse(p.items[n - 1].createdAt) ||
          (i.createdAt === p.items[n - 1].createdAt &&
            i.id >= p.items[n - 1].id)))
    )
      invalid();
    seen.add(i.id);
  }
  return p;
}
