import { expect, it } from "vitest";
import { decodeRun, runDetailForJob, runPageForJob } from "./runs";
import { nativeInboxLink, nativeJobLink } from "./privateLinks";
const id = (n: number) =>
  `71000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const job = { deploymentId: id(1), namespaceId: id(2), jobId: id(3) };
const run = {
  id: id(4),
  number: "9007199254740993",
  phase: "future_phase",
  desiredState: "run",
  createdAt: "2026-10-04T12:00:00Z",
  updatedAt: "2026-10-04T12:00:00Z",
};
const authority = {
  completeness: "complete",
  fetchedAt: run.updatedAt,
  sources: [
    {
      deploymentId: job.deploymentId,
      namespaceId: job.namespaceId,
      status: "available",
      asOf: run.updatedAt,
      fetchedAt: run.updatedAt,
    },
  ],
};
it("keeps exact run numbers and future run states without inventing an execution", () => {
  expect(decodeRun(run)).toEqual(run);
  expect(
    runPageForJob({ ...authority, items: [run], total: "1" }, job).data.items[0]
      .number,
  ).toBe("9007199254740993");
  expect(
    runDetailForJob({ ...authority, run }, job, run.id).data.executionId,
  ).toBeUndefined();
});
it("rejects wrong source/detail identity and unavailable publication", () => {
  for (const bad of [
    { ...authority, completeness: "partial" },
    { ...authority, sources: [] },
    {
      ...authority,
      sources: [{ ...authority.sources[0], deploymentId: id(9) }],
    },
    {
      ...authority,
      sources: [{ ...authority.sources[0], status: "unavailable" }],
    },
  ])
    expect(() => runDetailForJob({ ...bad, run }, job, run.id)).toThrow();
  expect(() => runDetailForJob({ ...authority, run }, job, id(8))).toThrow();
});
it("rejects imprecise, overflowing and incomplete execution references", () => {
  for (const patch of [
    { number: 9007199254740993 },
    { number: "9223372036854775808" },
    { number: "00" },
    { number: "0" },
    { id: "invalid" },
    { createdAt: "bad" },
    { executionId: id(9) },
    { targetId: id(8) },
    { phase: "" },
  ])
    expect(() => decodeRun({ ...run, ...patch })).toThrow();
  expect(
    decodeRun({
      ...run,
      executionId: id(6),
      targetId: id(7),
      targetGenerationId: id(8),
      executionPhase: "running",
      backend: "future_backend",
    }).executionId,
  ).toBe(id(6));
});
it("requires decreasing distinct bounded pages with truthful totals", () => {
  const lower = { ...run, id: id(5), number: "2" };
  expect(
    runPageForJob(
      { ...authority, items: [run, lower], total: "3", nextCursor: "opaque" },
      job,
    ).data.items,
  ).toHaveLength(2);
  for (const patch of [
    { items: [lower, run], total: "2" },
    { items: [run, run], total: "2" },
    { items: [run], total: "0" },
    { items: [], total: "0", nextCursor: "opaque" },
    { items: [run], total: "1", nextCursor: "opaque" },
    { items: [run], total: "1", nextCursor: "" },
  ])
    expect(() => runPageForJob({ ...authority, ...patch }, job)).toThrow();
});
it("builds credential-free source-qualified private links and rejects ambiguous components", () => {
  expect(nativeJobLink(job)).toBe(
    `jobman-dashboard://job/${job.deploymentId}/${job.namespaceId}/${job.jobId}`,
  );
  expect(nativeInboxLink(id(8))).toBe(`jobman-dashboard://inbox/${id(8)}`);
  for (const value of [
    "",
    "..",
    "x/y",
    "x\\y",
    "x?token=secret",
    "x#y",
    "x%2fy",
    "\nx",
  ])
    expect(() => nativeJobLink({ ...job, namespaceId: value })).toThrow();
});
