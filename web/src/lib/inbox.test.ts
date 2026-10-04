import { expect, it } from "vitest";
import {
  decodeInbox,
  decodeInboxItem,
  type InboxItem,
  type InboxItemPage,
} from "./inbox";
const id = (n: number) =>
  `71000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
export const inboxItem: InboxItem = {
  id: id(1),
  job: { deploymentId: id(2), namespaceId: id(3), jobId: id(4) },
  controlInstanceId: id(5),
  eventId: id(6),
  outcome: "future_outcome",
  eventAt: "2026-10-04T00:00:00Z",
  createdAt: "2026-10-04T01:00:00Z",
  expiresAt: "2026-11-03T01:00:00Z",
  read: false,
  matchedRules: [
    {
      ruleId: id(7),
      revision: "9007199254740993",
      name: "Failures",
      scope: "namespace_jobs",
      outcomeMode: "all_terminal",
      outcomes: [],
    },
  ],
  jobAvailability: "missing",
  delivery: { total: "2", byState: { accepted: "1", pending: "1" } },
};
export const inboxPage: InboxItemPage = {
  items: [inboxItem],
  unreadCount: "9007199254740993",
  completeness: "complete",
  unavailableSources: 0,
  inaccessibleScopes: 0,
  fetchedAt: "2026-10-04T01:00:01Z",
};
it("preserves exact counts, unknown outcomes, original matching revision and missing current job", () => {
  const p = decodeInbox(inboxPage);
  expect(p.unreadCount).toBe("9007199254740993");
  expect(p.items[0].matchedRules[0].revision).toBe("9007199254740993");
  expect(p.items[0].outcome).toBe("future_outcome");
});
it.each([
  { unreadCount: 9007199254740993 },
  { items: [inboxItem, inboxItem] },
  { unavailableSources: 1 },
  { nextCursor: "x".repeat(129) },
  { items: [{ ...inboxItem, jobName: "Cached secret name" }] },
  { items: [{ ...inboxItem, read: true }] },
  {
    items: [
      { ...inboxItem, delivery: { total: "1", byState: { accepted: "2" } } },
    ],
  },
])("rejects inconsistent inbox fields %j", (patch) => {
  expect(() => decodeInbox({ ...inboxPage, ...patch })).toThrow();
});
it("binds opaque detail response identity", () =>
  expect(() => decodeInboxItem(inboxItem, id(8))).toThrow());
