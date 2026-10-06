import type { JobRef, NamespaceRef } from "./models";
import { outcomes, unsuccessful } from "./models";
import { APIError } from "./transport";

import type {
  Rule,
  RuleInput,
  RulePage,
} from "../../../contracts/typescript/dashboard.generated";
export type { Rule, RuleInput, RulePage };

export const namespaceKey = (ref: NamespaceRef) =>
  JSON.stringify([ref.deploymentId, ref.namespaceId]);
export const jobKey = (ref: JobRef) =>
  JSON.stringify([ref.deploymentId, ref.namespaceId, ref.jobId]);
export const canonicalID = (id: unknown): id is string =>
  typeof id === "string" &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id) &&
  id !== "00000000-0000-0000-0000-000000000000";
const namespace = (value: NamespaceRef) =>
  value && canonicalID(value.deploymentId) && canonicalID(value.namespaceId);
export const nameBytes = (name: string) =>
  new TextEncoder().encode(name.trim()).length;
const invalid = (message: string) => new APIError("invalid_rule", message);
export function validateRule(input: RuleInput): RuleInput {
  const name = input.name.trim();
  if (
    !name ||
    nameBytes(name) > 120 ||
    /[\u0000-\u001f\u007f-\u009f]/.test(name)
  )
    throw invalid(
      "Enter a short rule name without line breaks or control characters (up to 120 bytes; some characters use more than one byte).",
    );
  if (!["watched_jobs", "my_jobs", "namespace_jobs"].includes(input.scope))
    throw invalid("Choose which jobs this rule should watch.");
  if (
    !input.namespaces.length ||
    input.namespaces.length > 320 ||
    input.namespaces.some((ref) => !namespace(ref)) ||
    new Set(input.namespaces.map((ref) => ref.deploymentId)).size > 32
  )
    throw invalid(
      "Select 1–320 authorized namespaces across no more than 32 deployments.",
    );
  const namespaces = [
    ...new Map(
      input.namespaces.map((ref) => [
        namespaceKey(ref),
        { deploymentId: ref.deploymentId, namespaceId: ref.namespaceId },
      ]),
    ).values(),
  ];
  const selected = new Set(namespaces.map(namespaceKey));
  let jobs: JobRef[] = [];
  if (input.scope === "watched_jobs") {
    if (
      !input.jobs.length ||
      input.jobs.length > 100 ||
      input.jobs.some(
        (ref) =>
          !namespace(ref) ||
          !canonicalID(ref.jobId) ||
          !selected.has(namespaceKey(ref)),
      )
    )
      throw invalid(
        "Select 1–100 jobs using their full IDs from the chosen namespaces.",
      );
    jobs = [
      ...new Map(
        input.jobs.map((ref) => [
          jobKey(ref),
          {
            deploymentId: ref.deploymentId,
            namespaceId: ref.namespaceId,
            jobId: ref.jobId,
          },
        ]),
      ).values(),
    ];
  }
  if (!["selected", "all_terminal"].includes(input.outcomeMode))
    throw invalid("Choose specific final results or all final results.");
  const selectedOutcomes =
    input.outcomeMode === "all_terminal" ? [] : [...new Set(input.outcomes)];
  if (
    input.outcomeMode === "selected" &&
    (!selectedOutcomes.length ||
      selectedOutcomes.some(
        (outcome) => !(outcomes as readonly string[]).includes(outcome),
      ))
  )
    throw invalid("Select at least one final result.");
  return {
    name,
    enabled: input.enabled,
    scope: input.scope,
    namespaces,
    jobs,
    outcomeMode: input.outcomeMode,
    outcomes: selectedOutcomes,
  };
}
export function newRule(job?: JobRef): RuleInput {
  return {
    name: job ? `Watch ${job.jobId}` : "",
    enabled: true,
    scope: job ? "watched_jobs" : "my_jobs",
    namespaces: job
      ? [{ deploymentId: job.deploymentId, namespaceId: job.namespaceId }]
      : [],
    jobs: job ? [job] : [],
    outcomeMode: "selected",
    outcomes: [...unsuccessful],
  };
}
export const hiddenReferences = (rule: Rule) =>
  rule.inaccessibleScopes + rule.unavailableScopes > 0;
export function editableRule(rule: Rule): boolean {
  if (hiddenReferences(rule)) return false;
  try {
    validateRule({ ...rule, namespaces: rule.scopes });
    return true;
  } catch {
    return false;
  }
}
export function ruleDraft(rule: Rule): RuleInput {
  if (!editableRule(rule))
    throw invalid(
      "Refresh the rule list or choose Check access and resume before editing this rule.",
    );
  return validateRule({ ...rule, namespaces: rule.scopes });
}
export function decodeRule(value: unknown): Rule {
  const rule = value as Rule;
  const bad = () =>
    new APIError(
      "invalid_response",
      "Dashboard returned an invalid or oversized alert rule.",
    );
  if (
    !rule ||
    !canonicalID(rule.id) ||
    typeof rule.revision !== "string" ||
    !/^[1-9][0-9]{0,18}$/.test(rule.revision) ||
    BigInt(rule.revision) > 9223372036854775807n ||
    typeof rule.name !== "string" ||
    nameBytes(rule.name) > 120 ||
    typeof rule.enabled !== "boolean" ||
    typeof rule.scope !== "string" ||
    typeof rule.outcomeMode !== "string" ||
    !Array.isArray(rule.outcomes) ||
    rule.outcomes.length > 6 ||
    rule.outcomes.some((item) => typeof item !== "string") ||
    !Array.isArray(rule.scopes) ||
    rule.scopes.length > 320 ||
    !Array.isArray(rule.jobs) ||
    rule.jobs.length > 100 ||
    ![rule.inaccessibleScopes, rule.unavailableScopes].every(
      (n) => Number.isInteger(n) && n >= 0 && n <= 320,
    ) ||
    rule.scopes.length + rule.inaccessibleScopes + rule.unavailableScopes >
      320 ||
    typeof rule.createdAt !== "string" ||
    !Number.isFinite(Date.parse(rule.createdAt)) ||
    typeof rule.updatedAt !== "string" ||
    !Number.isFinite(Date.parse(rule.updatedAt))
  )
    throw bad();
  if (
    rule.scopes.some(
      (scope) =>
        !namespace(scope) ||
        typeof scope.status !== "string" ||
        (scope.activatedAt !== undefined &&
          (typeof scope.activatedAt !== "string" ||
            !Number.isFinite(Date.parse(scope.activatedAt)))),
    ) ||
    new Set(rule.scopes.map(namespaceKey)).size !== rule.scopes.length
  )
    throw bad();
  const visible = new Set(rule.scopes.map(namespaceKey));
  if (
    rule.jobs.some(
      (job) =>
        !namespace(job) ||
        !canonicalID(job.jobId) ||
        !visible.has(namespaceKey(job)),
    ) ||
    new Set(rule.jobs.map(jobKey)).size !== rule.jobs.length
  )
    throw bad();
  return rule;
}
export function decodeRulePage(value: unknown): RulePage {
  const page = value as RulePage;
  if (
    !page ||
    !Array.isArray(page.items) ||
    page.items.length > 20 ||
    (page.nextCursor !== undefined &&
      (typeof page.nextCursor !== "string" ||
        !page.nextCursor ||
        page.nextCursor.length > 128))
  )
    throw new APIError(
      "invalid_response",
      "Dashboard returned an invalid alert-rule page.",
    );
  const items = page.items.map(decodeRule);
  if (new Set(items.map((item) => item.id)).size !== items.length)
    throw new APIError(
      "invalid_response",
      "Dashboard returned duplicate alert-rule identities.",
    );
  return { ...page, items };
}
