import { expect, it } from "vitest";
import type {
  Citation,
  Report,
} from "../../../contracts/typescript/dashboard.generated";
import {
  citationForReport,
  citationPreview,
  reportForJob,
  reportPageForJob,
} from "./reports";
const job = { deploymentId: "east", namespaceId: "research", jobId: "job" };
const report = {
  ...job,
  taskId: "task",
  state: "ready",
  sourceRevision: "9007199254740993",
  reportId: "original-report",
  evidenceId: "evidence",
  analysisEvidenceId: "analysis",
} as Report;
const citation = {
  taskId: report.taskId,
  reportId: report.reportId,
  evidenceId: report.evidenceId,
  analysisEvidenceId: report.analysisEvidenceId,
  id: "item",
} as Citation;
it("requires source-qualified report and sealed citation identities", () => {
  expect(reportForJob(report, job).sourceRevision).toBe("9007199254740993");
  expect(() =>
    reportForJob({ ...report, deploymentId: "west" }, job),
  ).toThrow();
  expect(() =>
    reportPageForJob({ items: Array(21).fill(report), fetchedAt: "now" }, job),
  ).toThrow();
  expect(() =>
    citationForReport(
      { ...citation, reportId: "other", valueJSON: "{}" },
      report,
      "item",
    ),
  ).toThrow();
  expect(() =>
    citationForReport(
      { ...citation, valueJSON: "{}", bytesBase64: "" },
      report,
      "item",
    ),
  ).toThrow();
  expect(
    citationForReport({ ...citation, bytesBase64: "" }, report, "item")
      .bytesBase64,
  ).toBe("");
});
it("preserves wide JSON integers and displays control sequences inertly", () => {
  const result = citationPreview({
    ...citation,
    valueJSON:
      '{"revision":9007199254740993,"text":"<script>alert(1)</script>"}\u202e',
  });
  expect(result.text).toContain("9007199254740993");
  expect(result.text).toContain("<script>alert(1)</script>");
  expect(result.text).toContain("\\u202e");
  expect(result.text).not.toContain("\u202e");
});
it("keeps empty and binary sealed bytes distinct and rejects noncanonical base64", () => {
  expect(citationPreview({ ...citation, bytesBase64: "" }).text).toBe("");
  expect(citationPreview({ ...citation, bytesBase64: "/wA=" })).toMatchObject({
    text: "ff 00",
  });
  for (const bytesBase64 of ["?", "YQ", "YR==", "YQ==\n"])
    expect(() => citationPreview({ ...citation, bytesBase64 })).toThrow();
});
it("bounds large citation displays with an explicit notice", () => {
  const result = citationPreview({
    ...citation,
    bytesBase64: btoa("a".repeat(100000)),
  });
  expect(result.text.length).toBe(65536);
  expect(result.note).toContain("Display limited");
  expect(() =>
    citationPreview({
      ...citation,
      valueJSON: "a".repeat(2 * 1024 * 1024 + 1),
    }),
  ).toThrow();
});
