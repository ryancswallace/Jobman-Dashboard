import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { decodeJob, decodeMeta } from "./api";
import type {
  GraphNode,
  WorkloadDetail,
  WorkloadCatalog,
  GraphNeighborhood,
  GraphDependencies,
  Result,
} from "./models";
const decodeChild = (child: Wire.WorkloadChild): GraphNode => ({
  ...child,
  job: decodeJob(child.job),
});
export function decodeWorkloads(value: unknown): Result<WorkloadCatalog> {
  const dto = value as Wire.WorkloadPage;
  return {
    data: { items: dto.items, total: dto.total, totals: dto.totals },
    meta: decodeMeta(dto),
  };
}
export function decodeWorkloadDetail(value: unknown): Result<WorkloadDetail> {
  const dto = value as Wire.WorkloadDetail;
  return {
    data: {
      workload: dto.workload,
      total: dto.total,
      children: dto.children.map(decodeChild),
    },
    meta: decodeMeta(dto),
  };
}
export function decodeGraphDependencies(
  value: unknown,
): Result<GraphDependencies> {
  const dto = value as Wire.GraphEdgePage;
  return {
    data: { items: dto.items, total: dto.total },
    meta: decodeMeta(dto),
  };
}
export function decodeGraphNeighborhood(
  value: unknown,
): Result<GraphNeighborhood> {
  const dto = value as Wire.GraphNeighborhood;
  return {
    data: {
      ...dto,
      nodes: dto.nodes.map((child) => ({
        ...decodeChild(child),
        dependencies: dto.edges
          .filter((edge) => edge.toJobId === child.id)
          .map((edge) => ({
            upstreamNodeId: edge.fromJobId,
            predicate: edge.predicate,
            status: edge.state,
          })),
      })),
    },
    meta: decodeMeta(dto),
  };
}
