import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import type { Result } from "./models";
import { decodeMeta } from "./api";
import { APIError, resourcePath } from "./transport";
export type Target = Wire.Target;
export type TargetCatalog = Pick<Wire.TargetPage, "items" | "total" | "totals">;
const decimal = (v: string) => /^(0|[1-9][0-9]{0,39})$/.test(v);
function invalid(): never {
  throw new APIError(
    "invalid_response",
    "Target metadata did not match the requested source or generation.",
  );
}
export function targetPath(
  t: Pick<Target, "deploymentId" | "namespaceId" | "targetId">,
) {
  return resourcePath(t, "targets", t.targetId);
}
export function targetRoute(t: Target) {
  return targetPath(t).replace("/api/v1", "");
}
function validate(t: Target) {
  const g = t.generation;
  if (
    !t.targetId ||
    !t.deploymentId ||
    !t.namespaceId ||
    !t.asOf ||
    !g?.id ||
    !decimal(g.number) ||
    !decimal(g.partitionCount) ||
    !Array.isArray(g.partitions) ||
    g.partitions.length > 200 ||
    !Array.isArray(g.capabilities) ||
    !Array.isArray(g.artifactStores)
  )
    invalid();
  if (
    BigInt(g.partitionCount) < BigInt(g.partitions.length) ||
    g.partitionsTruncated !==
      BigInt(g.partitionCount) > BigInt(g.partitions.length)
  )
    invalid();
}
export function decodeTargets(value: unknown): Result<TargetCatalog> {
  const dto = value as Wire.TargetPage;
  if (
    !Array.isArray(dto.items) ||
    dto.items.length > 200 ||
    !decimal(dto.total) ||
    !Array.isArray(dto.totals)
  )
    invalid();
  dto.items.forEach(validate);
  if (new Set(dto.items.map((t) => targetPath(t))).size !== dto.items.length)
    invalid();
  return {
    data: { items: dto.items, total: dto.total, totals: dto.totals },
    meta: decodeMeta(dto),
  };
}
export function decodeTargetDetail(
  value: unknown,
  expected: Pick<Target, "deploymentId" | "namespaceId" | "targetId">,
): Result<Target> {
  const dto = value as Wire.TargetDetail;
  validate(dto.target);
  if (
    targetPath(dto.target) !== targetPath(expected) ||
    dto.sources.some(
      (s) =>
        s.deploymentId !== expected.deploymentId ||
        s.namespaceId !== expected.namespaceId,
    )
  )
    invalid();
  return { data: dto.target, meta: decodeMeta(dto) };
}
export function decodeTargetPartitions(
  value: unknown,
  target: Target,
): Result<Pick<Wire.TargetPartitionPage, "items" | "total">> {
  const dto = value as Wire.TargetPartitionPage;
  if (
    dto.targetId !== target.targetId ||
    dto.generationId !== target.generation.id ||
    !decimal(dto.total) ||
    !Array.isArray(dto.items) ||
    dto.items.length > 200 ||
    dto.sources.some(
      (s) =>
        s.deploymentId !== target.deploymentId ||
        s.namespaceId !== target.namespaceId,
    )
  )
    invalid();
  let last = "";
  for (const p of dto.items) {
    if (!p.name || p.name <= last) invalid();
    last = p.name;
  }
  return {
    data: { items: dto.items, total: dto.total },
    meta: decodeMeta(dto),
  };
}
