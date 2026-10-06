import type {
  Bootstrap,
  Job,
  Overview,
  Result,
  Meta,
  Contribution,
} from "./models";
import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { APIError } from "./transport";
export type JobDTO = Wire.Job;
export type SourceDTO = Wire.SourceStatus;
export type BootstrapDTO = Wire.Bootstrap;
export type PageDTO<T> = Omit<Wire.JobPage, "items"> & { items: T[] };
export function decodeBootstrap(value: unknown): Bootstrap {
  const dto = value as BootstrapDTO;
  if (
    dto.apiVersion !== "jobman.dashboard/v1" ||
    !Array.isArray(dto.deployments)
  )
    throw new APIError(
      "unsupported_contract",
      "This Dashboard API version is not supported by this client.",
    );
  const namespaces = dto.deployments.flatMap((d) => d.namespaces);
  return {
    account: dto.account,
    completeness: dto.completeness,
    sources: dto.deployments.map((d) => ({
      deploymentId: d.id,
      displayName: d.name,
      status: d.status,
      namespaces: d.namespaces.map((n) => ({
        namespaceId: n.id,
        name: n.name,
        roles: n.roles,
        capabilities: n.capabilities,
        authorizationVersion: n.authorizationVersion,
        authorizationExpiresAt: n.authorizationExpiresAt,
      })),
    })),
    preferences: {
      ...dto.preferences,
      appearance:
        dto.preferences.appearance === "dark" ||
        dto.preferences.appearance === "light"
          ? dto.preferences.appearance
          : "system",
    },
    csrfToken: dto.csrfToken,
    authorizationCheckedAt:
      namespaces.map((n) => n.authorizationCheckedAt).sort()[0] ??
      new Date().toISOString(),
    authorizationValidUntil:
      namespaces.map((n) => n.authorizationExpiresAt).sort()[0] ??
      new Date(Date.now() + 120000).toISOString(),
    mode: dto.fixtureMode ? "fixture" : undefined,
  };
}
export function decodeJob(dto: JobDTO): Job {
  return {
    deploymentId: dto.deploymentId,
    namespaceId: dto.namespaceId,
    jobId: dto.id,
    name: dto.name,
    phase: dto.phase,
    outcome: dto.outcome,
    desiredState: dto.desiredState,
    observationConfidence: dto.confidence,
    revision: dto.revision,
    imported: dto.imported,
    disposition: dto.disposition,
    lifecycle: dto.lifecycle,
    currentRun: dto.currentRun,
    group: dto.group,
    owner: dto.owner ? { ...dto.owner, verified: true } : undefined,
    createdAt: dto.createdAt,
    updatedAt: dto.updatedAt,
    startedAt: dto.startedAt,
    completedAt: dto.completedAt,
    confidenceUpdatedAt: dto.confidenceUpdatedAt,
    partition: dto.partition,
    workloadDigest: dto.workloadDigest,
    labels: dto.labels,
    target: {
      id: dto.targetId,
      name: dto.targetName ?? dto.targetId,
      generation: dto.targetGeneration,
      generationId: dto.targetGenerationId,
      backend: dto.backend,
    },
    scheduler: dto.scheduler
      ? { ...dto.scheduler, nativeId: dto.scheduler.jobId }
      : undefined,
  };
}
function contribution(dto: SourceDTO): Contribution {
  return {
    deploymentId: dto.deploymentId,
    namespaceId: dto.namespaceId,
    status: dto.status,
    observedAt: dto.asOf,
    message: dto.message,
  };
}
export function decodeMeta(dto: {
  completeness?: string;
  sources?: SourceDTO[];
  fetchedAt?: string;
  nextCursor?: string;
}): Meta {
  return {
    completeness: dto.completeness ?? "complete",
    contributions: (dto.sources ?? []).map(contribution),
    lastFetchedAt: dto.fetchedAt,
    nextCursor: dto.nextCursor,
  };
}
export function decodeJobs(value: unknown): Result<Job[]> {
  const dto = value as PageDTO<JobDTO>;
  return { data: dto.items.map(decodeJob), meta: decodeMeta(dto) };
}
export function decodeJobDetail(value: unknown): Result<Job> {
  const dto = value as Wire.JobDetail;
  return {
    data: {
      ...decodeJob(dto.job),
      execution: dto.execution,
      executionUnavailableReason: dto.executionUnavailableReason,
    },
    meta: decodeMeta(dto),
  };
}
export function decodeOverview(value: unknown): Result<Overview> {
  const dto = value as Wire.Overview;
  const numeric = (n?: number | null) => (n == null ? undefined : String(n));
  return {
    data: {
      active: numeric(dto.active),
      awaitingExecution: numeric(dto.awaitingExecution),
      running: numeric(dto.running),
      evidenceAttention: numeric(dto.evidenceAttention),
      missingCompletionTime: numeric(dto.missingCompletionTime),
      terminal: Object.fromEntries(
        Object.entries(dto.terminal).map(([key, n]) => [key, numeric(n)]),
      ),
      window: dto.window,
    } as Overview,
    meta: decodeMeta(dto),
  };
}
export function decodePage<T>(value: unknown): Result<T[]> {
  const dto = value as PageDTO<T>;
  return { data: dto.items, meta: decodeMeta(dto) };
}
