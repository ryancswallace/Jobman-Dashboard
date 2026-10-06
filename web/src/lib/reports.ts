import type {
  Citation,
  Report,
  ReportPage,
} from "../../../contracts/typescript/dashboard.generated";
import type { JobRef } from "./models";
import { APIError } from "./transport";

const invalid = () =>
  new APIError(
    "invalid_response",
    "The report response did not match the requested evidence.",
  );
export const runIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function reportForJob(value: unknown, job: JobRef): Report {
  const report = value as Report;
  if (
    !report ||
    report.deploymentId !== job.deploymentId ||
    report.namespaceId !== job.namespaceId ||
    report.jobId !== job.jobId ||
    typeof report.taskId !== "string" ||
    !report.taskId ||
    typeof report.state !== "string" ||
    typeof report.sourceRevision !== "string"
  )
    throw invalid();
  if (
    report.detail &&
    (!Array.isArray(report.detail.findings) ||
      !Array.isArray(report.detail.citations) ||
      !Array.isArray(report.detail.actions) ||
      !Array.isArray(report.detail.missingEvidence) ||
      !Array.isArray(report.detail.warnings) ||
      !Array.isArray(report.detail.omissions) ||
      !Array.isArray(report.detail.redactionNotices))
  )
    throw invalid();
  return report;
}
export function reportPageForJob(value: unknown, job: JobRef): ReportPage {
  const page = value as ReportPage;
  if (
    !page ||
    !Array.isArray(page.items) ||
    page.items.length > 20 ||
    typeof page.fetchedAt !== "string"
  )
    throw invalid();
  return { ...page, items: page.items.map((item) => reportForJob(item, job)) };
}
export function citationForReport(
  value: unknown,
  report: Report,
  citationId: string,
): Citation {
  const citation = value as Citation;
  if (
    !citation ||
    citation.taskId !== report.taskId ||
    citation.id !== citationId ||
    citation.reportId !== report.reportId ||
    citation.evidenceId !== report.evidenceId ||
    citation.analysisEvidenceId !== report.analysisEvidenceId
  )
    throw invalid();
  if (
    (typeof citation.valueJSON !== "string") ===
    (typeof citation.bytesBase64 !== "string")
  )
    throw invalid();
  return citation;
}

// Keep wide JSON integers and source offsets as original strings. Never treat
// evidence as HTML, terminal escapes, active links, or instructions to execute.
const visibleControls = (text: string) =>
  text.replace(
    /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\u202a-\u202e\u2066-\u2069]/g,
    (char) => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
export function citationPreview(citation: Citation): {
  text: string;
  note: string;
} {
  if (citation.valueJSON !== undefined) {
    if (citation.valueJSON.length > 2 * 1024 * 1024) throw invalid();
    return {
      text: visibleControls(citation.valueJSON.slice(0, 65536)),
      note:
        citation.valueJSON.length > 65536
          ? "Showing the first 65,536 characters of the saved job details in JSON format. Numeric values are preserved exactly."
          : "Saved job details in JSON format. Numeric values are preserved exactly.",
    };
  }
  const encoded = citation.bytesBase64;
  if (
    typeof encoded !== "string" ||
    encoded.length > Math.ceil((2 * 1024 * 1024) / 3) * 4 ||
    encoded.length % 4 !== 0 ||
    !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(
      encoded,
    )
  )
    throw invalid();
  const binary = atob(encoded);
  if (btoa(binary) !== encoded) throw invalid();
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  try {
    const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    return {
      text: visibleControls(text.slice(0, 65536)),
      note: `${bytes.length.toLocaleString()} saved bytes displayed as text.${text.length > 65536 ? " Display limited to the first 65,536 characters." : ""} Hidden control characters are shown as escape sequences.`,
    };
  } catch {
    return {
      text: Array.from(bytes.slice(0, 2048), (byte) =>
        byte.toString(16).padStart(2, "0"),
      ).join(" "),
      note: `${bytes.length.toLocaleString()} saved bytes; not readable as text. Hexadecimal ${bytes.length > 2048 ? "preview limited to the first 2,048 bytes" : "display"}.`,
    };
  }
}
