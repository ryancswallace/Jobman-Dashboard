import { describe, it, expect } from "vitest";
import {
  decodeJob,
  decodeJobDetail,
  decodeJobs,
  decodeOverview,
  decodeBootstrap,
  type JobDTO,
} from "./api";
import { count, jobRoute, scopeQuery, safeReturnPath } from "./format";
const job: JobDTO = {
  deploymentId: "a",
  namespaceId: "same",
  id: "duplicate",
  targetId: "t",
  revision: "1",
  createdAt: "2026-10-03T00:00:00Z",
  updatedAt: "2026-10-03T01:00:00Z",
  desiredState: "cancel",
  phase: "running",
  confidence: "stale",
  labels: {},
};
describe("contract semantics", () => {
  it("exposes execution only in job detail and preserves source inspection facts", () => {
    const execution = {
      command: { executable: "echo", args: ["", "a b", "\n"] },
      workingDirectory: "/work",
    };
    const enriched = {
      ...job,
      targetName: "Named target",
      partition: "gpu",
      workloadDigest: "sha256:abc",
      confidenceUpdatedAt: "2026-10-06T00:00:00Z",
      execution,
    };
    const listed = decodeJob(enriched);
    expect(listed.execution).toBeUndefined();
    expect(listed).toMatchObject({
      target: { id: "t", name: "Named target" },
      partition: "gpu",
      workloadDigest: "sha256:abc",
      confidenceUpdatedAt: enriched.confidenceUpdatedAt,
    });
    const detail = decodeJobDetail({
      job: enriched,
      execution,
      fetchedAt: job.updatedAt,
    });
    expect(detail.data.execution).toEqual(execution);
    expect(
      decodeJobDetail({ job, executionUnavailableReason: "future_reason" }).data
        .executionUnavailableReason,
    ).toBe("future_reason");
  });
  it("keeps job phase, cancellation intent and stale confidence separate", () => {
    const result = decodeJob(job);
    expect(result.phase).toBe("running");
    expect(result.desiredState).toBe("cancel");
    expect(result.observationConfidence).toBe("stale");
    expect(result.outcome).toBeUndefined();
    expect(result.startedAt).toBeUndefined();
    expect(result.completedAt).toBeUndefined();
  });
  it("keeps unknown source enums rather than guessing failure", () => {
    expect(
      decodeJob({ ...job, phase: "future_phase", outcome: "future_outcome" }),
    ).toMatchObject({ phase: "future_phase", outcome: "future_outcome" });
  });
  it("preserves factual run and lifecycle metadata without inventing event timestamps", () => {
    const facts = {
      imported: true,
      targetGenerationId: "generation-id",
      disposition: "future_disposition",
      lifecycle: {
        startedProvenance: "agent_event",
        startedRecordedAt: "2026-10-03T02:00:00Z",
      },
      currentRun: {
        id: "run-id",
        number: "9007199254740993",
        executionId: "execution-id",
      },
      group: {
        collectionId: "collection-id",
        collectionIndex: 0,
        graphId: "graph-id",
        graphIndex: 0,
      },
      scheduler: {
        reason: "Resources",
        cluster: "lab",
        observedAt: "2026-10-03T03:00:00Z",
      },
    };
    const decoded = decodeJob({ ...job, ...facts });
    const { targetGenerationId, ...preserved } = facts;
    expect(decoded).toMatchObject({
      ...preserved,
      target: { generationId: targetGenerationId },
    });
    expect(decoded.startedAt).toBeUndefined();
    expect(decoded.completedAt).toBeUndefined();
  });
  it("qualifies duplicate IDs and conveys partial source state", () => {
    const result = decodeJobs({
      items: [job, { ...job, deploymentId: "b" }],
      completeness: "partial",
      sources: [
        {
          deploymentId: "b",
          namespaceId: "same",
          status: "unavailable",
          fetchedAt: "now",
        },
      ],
      fetchedAt: "now",
    });
    expect(new Set(result.data.map(jobRoute)).size).toBe(2);
    expect(result.meta.completeness).toBe("partial");
    expect(result.meta.contributions[0].status).toBe("unavailable");
  });
  it("never turns unavailable counts into zeros or loses large decimal precision", () => {
    const result = decodeOverview({
      active: null,
      awaitingExecution: 0,
      terminal: { failure: null, success: 0 },
      window: { from: "a", to: "b" },
      sources: [],
    });
    expect(count(result.data.active)).toBe("Unavailable");
    expect(count(result.data.awaitingExecution)).toBe("0");
    expect(count("9007199254740993").replaceAll(",", "")).toBe(
      "9007199254740993",
    );
  });
  it("expands selected deployment scope only into authorized namespace refs", () => {
    expect(
      JSON.parse(
        scopeQuery({ deployments: ["a"] }, [
          {
            deploymentId: "a",
            displayName: "A",
            status: "available",
            namespaces: [
              { namespaceId: "n", name: "N", roles: [], capabilities: [] },
            ],
          },
          {
            deploymentId: "b",
            displayName: "B",
            status: "available",
            namespaces: [
              { namespaceId: "m", name: "M", roles: [], capabilities: [] },
            ],
          },
        ]).scope,
      ),
    ).toEqual([{ deploymentId: "a", namespaceId: "n" }]);
  });
  it("requires the negotiated Dashboard contract", () => {
    expect(() =>
      decodeBootstrap({ apiVersion: "other", deployments: [] }),
    ).toThrow();
  });
  it("refuses open redirects and backslash normalization tricks", () => {
    for (const path of [
      "https://evil.example",
      "//evil.example",
      "/\\evil.example",
      "/\n",
    ])
      expect(safeReturnPath(path)).toBe("/");
    expect(safeReturnPath("/jobs?phase=running")).toBe("/jobs?phase=running");
  });
});
