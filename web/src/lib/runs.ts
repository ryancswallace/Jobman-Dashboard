import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import type { JobRef } from "./models";
import { APIError } from "./transport";
import { decodeMeta } from "./api";
export const runIdPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export const validRunId = (value: string) =>
  runIdPattern.test(value) && value !== "00000000-0000-0000-0000-000000000000";
const invalid = () =>
  new APIError(
    "invalid_response",
    "Run references could not be verified for this job. Refresh the run list.",
  );
const date = (value: unknown) =>
  typeof value === "string" && Number.isFinite(Date.parse(value));
const decimal = (value: unknown, positive = false): value is string =>
  typeof value === "string" &&
  /^(0|[1-9][0-9]*)$/.test(value) &&
  value.length <= 19 &&
  BigInt(value) <= 9223372036854775807n &&
  (!positive || value !== "0");
export function decodeRun(value: unknown): Wire.JobRun {
  const r = value as Wire.JobRun;
  if (
    !r ||
    typeof r.id !== "string" ||
    !validRunId(r.id) ||
    !decimal(r.number, true) ||
    !date(r.createdAt) ||
    !date(r.updatedAt)
  )
    throw invalid();
  for (const key of [
    "phase",
    "desiredState",
    "outcome",
    "executionPhase",
    "backend",
    "confidence",
  ] as const) {
    const text = r[key];
    if ((key === "phase" || key === "desiredState") && !text) throw invalid();
    if (
      text !== undefined &&
      (typeof text !== "string" || !text || text.length > 64)
    )
      throw invalid();
  }
  if (r.executionId !== undefined) {
    if (
      ![r.executionId, r.targetId, r.targetGenerationId].every(
        (id) => typeof id === "string" && validRunId(id),
      ) ||
      !r.executionPhase ||
      !r.backend
    )
      throw invalid();
  } else if (
    [
      r.executionPhase,
      r.targetId,
      r.targetGenerationId,
      r.backend,
      r.confidence,
    ].some((x) => x !== undefined)
  )
    throw invalid();
  return r;
}
function authority(dto: Wire.RunPage | Wire.RunDetail, job: JobRef) {
  if (
    dto.completeness !== "complete" ||
    !date(dto.fetchedAt) ||
    !Array.isArray(dto.sources) ||
    dto.sources.length !== 1
  )
    throw invalid();
  const source = dto.sources[0];
  if (
    source.deploymentId !== job.deploymentId ||
    source.namespaceId !== job.namespaceId ||
    source.status !== "available" ||
    !date(source.asOf) ||
    !date(source.fetchedAt)
  )
    throw invalid();
}
export function runPageForJob(value: unknown, job: JobRef) {
  const dto = value as Wire.RunPage;
  if (!dto) throw invalid();
  authority(dto, job);
  if (
    !Array.isArray(dto.items) ||
    dto.items.length > 100 ||
    !decimal(dto.total) ||
    BigInt(dto.total) < BigInt(dto.items.length) ||
    (dto.nextCursor !== undefined &&
      (typeof dto.nextCursor !== "string" ||
        !dto.nextCursor ||
        dto.nextCursor.length > 512 ||
        !dto.items.length ||
        BigInt(dto.total) <= BigInt(dto.items.length)))
  )
    throw invalid();
  let previous: bigint | undefined;
  const ids = new Set<string>();
  const items = dto.items.map((value) => {
    const r = decodeRun(value),
      n = BigInt(r.number);
    if (ids.has(r.id) || (previous !== undefined && n >= previous))
      throw invalid();
    ids.add(r.id);
    previous = n;
    return r;
  });
  return { data: { ...dto, items }, meta: decodeMeta(dto) };
}
export function runDetailForJob(value: unknown, job: JobRef, runID: string) {
  const dto = value as Wire.RunDetail;
  if (!dto) throw invalid();
  authority(dto, job);
  const run = decodeRun(dto.run);
  if (run.id !== runID) throw invalid();
  return { data: run, meta: decodeMeta(dto) };
}
