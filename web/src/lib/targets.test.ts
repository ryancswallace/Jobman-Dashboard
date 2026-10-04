import { describe, expect, it } from "vitest";
import {
  decodeTargetDetail,
  decodeTargetPartitions,
  decodeTargets,
  targetRoute,
  type Target,
} from "./targets";
const target: Target = {
  deploymentId: "east",
  namespaceId: "ns",
  targetId: "target/id",
  name: "Synthetic target",
  kind: "future-kind",
  state: "future-state",
  revision: "9007199254740993",
  createdAt: "2026-10-03T12:00:00Z",
  updatedAt: "2026-10-03T12:00:00Z",
  asOf: "2026-10-03T12:00:00Z",
  generation: {
    id: "generation-1",
    number: "9007199254740993",
    executionBackend: "slurm",
    transport: "agent-api",
    runtimes: ["native"],
    operatingSystems: ["linux"],
    architectures: ["x86_64"],
    capabilities: [],
    partitions: [{ name: "batch", isDefault: true }],
    partitionCount: "201",
    partitionsTruncated: true,
    artifactStores: [],
    provider: { kind: "on-prem" },
  },
};
const meta = {
  sources: [
    {
      deploymentId: "east",
      namespaceId: "ns",
      status: "available",
      fetchedAt: target.asOf,
    },
  ],
  completeness: "complete",
  fetchedAt: target.asOf,
};
describe("target facts", () => {
  it("keeps wide generation numbers, unknown kinds and source-qualified routes", () => {
    const out = decodeTargets({
      ...meta,
      items: [target, { ...target, deploymentId: "west" }],
      total: "2",
      totals: [],
    });
    expect(out.data.items[0].generation.number).toBe("9007199254740993");
    expect(out.data.items[0].kind).toBe("future-kind");
    expect(targetRoute(target)).toBe(
      "/deployments/east/namespaces/ns/targets/target%2Fid",
    );
  });
  it("rejects cross-source target details and inconsistent preview totals", () => {
    expect(() =>
      decodeTargetDetail(
        { ...meta, target: { ...target, deploymentId: "west" } },
        target,
      ),
    ).toThrow();
    expect(() =>
      decodeTargets({
        ...meta,
        items: [
          {
            ...target,
            generation: { ...target.generation, partitionsTruncated: false },
          },
        ],
        total: "1",
        totals: [],
      }),
    ).toThrow();
  });
  it("rejects partitions from another generation or source and duplicate rows", () => {
    const page = {
      ...meta,
      targetId: target.targetId,
      generationId: target.generation.id,
      total: "201",
      items: target.generation.partitions,
    };
    expect(decodeTargetPartitions(page, target).data.total).toBe("201");
    expect(() =>
      decodeTargetPartitions({ ...page, generationId: "new" }, target),
    ).toThrow();
    expect(() =>
      decodeTargetPartitions(
        { ...page, sources: [{ ...meta.sources[0], namespaceId: "other" }] },
        target,
      ),
    ).toThrow();
    expect(() =>
      decodeTargetPartitions(
        { ...page, items: [...page.items, ...page.items] },
        target,
      ),
    ).toThrow();
  });
});
