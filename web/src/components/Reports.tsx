import { useState } from "react";
import type { JobRef, Report, Citation } from "../lib/models";
import { APIError, request, resourcePath } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodePage } from "../lib/api";
import { useSession } from "../lib/session";
import { timestamp, title } from "../lib/format";
import { Empty, ErrorNotice, Spinner, Status } from "./States";
const decodeReports = (value: unknown) => decodePage<Report>(value);
export function Reports({ job }: { job: JobRef }) {
  const { identity, bootstrap } = useSession(),
    path = resourcePath(job, "jobs", job.jobId);
  const results = useResource(`${path}/reports`, identity, 5000, decodeReports);
  const [includeLogTail, setIncludeLogTail] = useState(false),
    [working, setWorking] = useState(false),
    [error, setError] = useState<APIError>(),
    [notice, setNotice] = useState("");
  const generate = async () => {
    setWorking(true);
    setError(undefined);
    try {
      await request(`${path}/reports`, {
        method: "POST",
        body: { includeLogTail },
        idempotencyKey: crypto.randomUUID(),
      });
      setNotice("Deterministic report requested. Status will update here.");
      results.refresh();
    } catch (e) {
      setError(
        e instanceof APIError
          ? e
          : new APIError("request_failed", "Report request failed."),
      );
    } finally {
      setWorking(false);
    }
  };
  return (
    <>
      <section className="panel report-request">
        <div>
          <h2>Explain the available evidence</h2>
          <p>
            Generate a deterministic report from a sealed job snapshot.
            Suggestions are advice and never execute changes.
          </p>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={includeLogTail}
              onChange={(e) => setIncludeLogTail(e.target.checked)}
            />
            Include bounded, redacted log tails (up to 64 KiB per stream)
          </label>
          <p className="muted small">
            Default disclosure: metadata, scheduler and lifecycle facts. No
            model provider is invoked.
          </p>
        </div>
        <button
          className="button"
          disabled={working}
          onClick={() => void generate()}
        >
          {working ? "Requesting…" : "Generate report"}
        </button>
      </section>
      {notice && (
        <p role="status" className="notice subtle">
          {notice}
        </p>
      )}
      {(error || results.error) && (
        <ErrorNotice
          error={(error || results.error)!}
          retry={results.refresh}
        />
      )}
      {results.loading && !results.data ? (
        <Spinner label="Loading reports" />
      ) : results.data?.data.length ? (
        results.data.data.map((report) => (
          <article className="panel report-card" key={report.id}>
            <div className="panel-heading">
              <div>
                <h2>Diagnosis report</h2>
                <p>
                  {timestamp(report.createdAt, bootstrap.preferences.timezone)}
                </p>
              </div>
              <Status value={report.state} />
            </div>
            <div className="panel-body">
              <dl className="facts compact">
                <div>
                  <dt>Report</dt>
                  <dd className="mono">{report.id}</dd>
                </div>
                <div>
                  <dt>Source revision</dt>
                  <dd>{report.sourceRevision ?? "Unavailable"}</dd>
                </div>
                <div>
                  <dt>Evidence</dt>
                  <dd className="mono">{report.evidenceId ?? "Unavailable"}</dd>
                </div>
                <div>
                  <dt>Engine</dt>
                  <dd>{report.engineVersion ?? "Unavailable"}</dd>
                </div>
                <div>
                  <dt>Disclosure</dt>
                  <dd>{report.disclosure ?? "Unavailable"}</dd>
                </div>
              </dl>
              {report.message && <p>{report.message}</p>}
              {report.state === "outdated" && (
                <p className="notice warning">
                  This report describes its recorded snapshot. The source job or
                  evidence has changed.
                </p>
              )}
              {report.findings.map((finding) => (
                <section className="finding" key={finding.id}>
                  <div className="finding-heading">
                    <Status value={finding.severity} />
                    <h3>{finding.title}</h3>
                    {finding.generated && (
                      <span className="badge warn">Generated hypothesis</span>
                    )}
                  </div>
                  <p>{finding.explanation}</p>
                  {finding.confidenceBasis && (
                    <p className="muted">
                      Confidence basis: {finding.confidenceBasis}
                      {finding.confidence
                        ? ` (${finding.confidence}; not a calibrated probability)`
                        : ""}
                    </p>
                  )}
                  <div className="citations">
                    {finding.citations.map((citation) => (
                      <CitationView
                        key={citation.id}
                        citation={citation}
                        path={`${path}/reports/${encodeURIComponent(report.id)}/citations/${encodeURIComponent(citation.id)}`}
                      />
                    ))}
                  </div>
                  {finding.suggestions?.length ? (
                    <>
                      <h4>Suggested actions</h4>
                      <ul>
                        {finding.suggestions.map((s, i) => (
                          <li key={i}>{s}</li>
                        ))}
                      </ul>
                    </>
                  ) : null}
                </section>
              ))}
              {report.missingEvidence.length > 0 && (
                <>
                  <h3>Missing evidence</h3>
                  <ul>
                    {report.missingEvidence.map((item, i) => (
                      <li key={i}>{item}</li>
                    ))}
                  </ul>
                </>
              )}
              {report.retryAdvice && (
                <p>
                  <strong>Retry advice:</strong> {report.retryAdvice}
                </p>
              )}
              {report.warnings?.map((warning, i) => (
                <p key={i} className="notice warning">
                  {warning}
                </p>
              ))}
            </div>
          </article>
        ))
      ) : (
        !results.error && (
          <Empty title="No report yet">
            Request a report to analyze this job’s factual evidence. Missing
            evidence will be disclosed explicitly.
          </Empty>
        )
      )}
    </>
  );
}
function CitationView({
  citation,
  path,
}: {
  citation: Citation;
  path: string;
}) {
  const [open, setOpen] = useState(false),
    { identity } = useSession();
  const detail = useResource<Citation>(open ? path : null, identity, 0);
  return (
    <div>
      <button
        className="text-button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        {open ? "Hide" : "View"} evidence: {citation.label || citation.id}
      </button>
      {open && (
        <div className="citation">
          {detail.loading ? (
            <Spinner label="Validating citation" />
          ) : detail.error ? (
            <ErrorNotice error={detail.error} retry={detail.refresh} />
          ) : detail.data ? (
            <>
              <p>
                {title(detail.data.label)} · bytes{" "}
                {detail.data.startOffset ?? "—"}–{detail.data.endOffset ?? "—"}
              </p>
              <pre>{detail.data.text}</pre>
            </>
          ) : null}
        </div>
      )}
    </div>
  );
}
