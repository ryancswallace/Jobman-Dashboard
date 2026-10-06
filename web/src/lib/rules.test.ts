import { expect, it } from "vitest";
import {
  decodeRule,
  decodeRulePage,
  editableRule,
  newRule,
  ruleDraft,
  validateRule,
  type Rule,
} from "./rules";
const id = (n: number) =>
  `71000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const ref = { deploymentId: id(1), namespaceId: id(2), jobId: id(3) };
const rule: Rule = {
  id: id(4),
  revision: "9007199254740993",
  name: "Watch",
  enabled: true,
  scope: "watched_jobs",
  scopes: [{ ...ref, status: "active" }],
  jobs: [ref],
  outcomeMode: "selected",
  outcomes: ["failure"],
  inaccessibleScopes: 0,
  unavailableScopes: 0,
  createdAt: "2026-10-04T00:00:00Z",
  updatedAt: "2026-10-04T00:00:00Z",
};
it("keeps source-qualified duplicate job IDs and clears inactive form choices", () => {
  const second = { ...ref, deploymentId: id(5) };
  const value = validateRule({
    ...newRule(ref),
    namespaces: [ref, second],
    jobs: [ref, second],
  });
  expect(value.jobs).toHaveLength(2);
  expect(
    validateRule({
      ...value,
      scope: "my_jobs",
      outcomeMode: "all_terminal",
      outcomes: ["future_value"],
    }),
  ).toMatchObject({ jobs: [], outcomes: [] });
  expect(() => validateRule({ ...value, namespaces: [ref] })).toThrow(
    "chosen namespaces",
  );
});
it("enforces UTF-8 byte, canonical UUID, job and source bounds", () => {
  expect(
    validateRule({ ...newRule(ref), name: "界".repeat(40) }).name,
  ).toHaveLength(40);
  expect(() =>
    validateRule({ ...newRule(ref), name: "界".repeat(41) }),
  ).toThrow("120 bytes");
  expect(() =>
    validateRule({ ...newRule(ref), name: "watch\u0085job" }),
  ).toThrow("control");
  expect(() =>
    validateRule({ ...newRule(ref), jobs: [{ ...ref, jobId: "bad-id" }] }),
  ).toThrow("full IDs");
  expect(() =>
    validateRule({ ...newRule(ref), jobs: Array(101).fill(ref) }),
  ).toThrow("100");
  expect(() =>
    validateRule({
      ...newRule(ref),
      namespaces: Array.from({ length: 33 }, (_, n) => ({
        ...ref,
        deploymentId: id(n + 1),
      })),
    }),
  ).toThrow("32 deployments");
});
it("does not turn redacted views into writable partial rules", () => {
  expect(ruleDraft(rule).namespaces).toEqual([
    { deploymentId: ref.deploymentId, namespaceId: ref.namespaceId },
  ]);
  const hidden = { ...rule, scopes: [], jobs: [], unavailableScopes: 1 };
  expect(decodeRule(hidden)).toEqual(hidden);
  expect(editableRule(hidden)).toBe(false);
  expect(() => ruleDraft(hidden)).toThrow("Check access and resume");
  expect(() => decodeRule({ ...hidden, jobs: [ref] })).toThrow("invalid");
});
it("preserves wide revisions and future values while rejecting invalid pages", () => {
  expect(
    decodeRule({
      ...rule,
      scope: "future_scope",
      scopes: [{ ...ref, status: "future_state" }],
    }).revision,
  ).toBe("9007199254740993");
  expect(editableRule({ ...rule, scope: "future_scope" })).toBe(false);
  expect(() => decodeRulePage({ items: [rule, rule] })).toThrow("duplicate");
  expect(() => decodeRulePage({ items: Array(21).fill(rule) })).toThrow(
    "invalid",
  );
  expect(() =>
    decodeRule({ ...rule, revision: "9223372036854775808" }),
  ).toThrow("invalid");
});
