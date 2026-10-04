import { useEffect, useMemo, useRef, useState } from "react";
import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import type { JobRef } from "../lib/models";
import { APIError, dashboardClient, resourcePath } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { useSession } from "../lib/session";
import { count, timestamp, title } from "../lib/format";
import {
  citationForReport,
  citationPreview,
  reportForJob,
  reportPageForJob,
  runIDPattern,
} from "../lib/reports";
import { Empty, ErrorNotice, Spinner, Status } from "./States";
import "./Reports.css";

export function Reports({ job, run }: { job: JobRef; run?: Wire.JobRun }) {
  const { identity } = useSession();
  return (
    <ReportBrowser
      key={`${identity}:${resourcePath(job, "jobs", job.jobId)}:${run?.id ?? "default"}`}
      job={job}
      run={run}
    />
  );
}
function ReportBrowser({ job, run }: { job: JobRef; run?: Wire.JobRun }) {
  const { identity, bootstrap } = useSession();
  const path = resourcePath(job, "jobs", job.jobId);
  const [readGeneration, setReadGeneration] = useState(0);
  const [cursors, setCursors] = useState<string[]>([]),
    [taskId, setTaskId] = useState<string>();
  const [profile, setProfile] = useState("metadata"),
    [runId, setRunId] = useState("");
  const [working, setWorking] = useState(false),
    [error, setError] = useState<APIError>(),
    [notice, setNotice] = useState("");
  const pending = useRef<AbortController | null>(null);
  const intent = useRef<{ body: string; key: string } | null>(null);
  const decode = useMemo(
    () => (value: unknown) => reportPageForJob(value, job),
    [job.deploymentId, job.namespaceId, job.jobId],
  );
  const cursor = cursors.at(-1);
  const results = useResource(
    `${path}/reports?${new URLSearchParams({ limit: "20", ...(cursor ? { cursor } : {}) })}`,
    `${identity}:${readGeneration}`,
    5000,
    decode,
  );
  useEffect(() => () => pending.current?.abort(), []);
  const restart = () => {
    setCursors([]);
    setError(undefined);
    setReadGeneration((previous) => previous + 1);
  };
  const generate = async () => {
    if (pending.current) return;
    const selectedRun = run?.id ?? runId.trim();
    if (
      selectedRun &&
      (!runIDPattern.test(selectedRun) ||
        selectedRun === "00000000-0000-0000-0000-000000000000")
    ) {
      setError(
        new APIError(
          "invalid_request",
          "Enter an actual run UUID, or leave it blank to select recent runs.",
        ),
      );
      return;
    }
    const body: Wire.ReportRequest = {
      profile,
      ...(selectedRun ? { runId: selectedRun } : {}),
    };
    const serialized = JSON.stringify(body);
    if (intent.current?.body !== serialized)
      intent.current = { body: serialized, key: crypto.randomUUID() };
    const controller = new AbortController();
    pending.current = controller;
    setWorking(true);
    setError(undefined);
    setNotice("");
    try {
      const report = reportForJob(
        await dashboardClient.requestReport({
          path: job,
          body,
          headers: { "Idempotency-Key": intent.current.key },
          signal: controller.signal,
        }),
        job,
      );
      if (controller.signal.aborted) return;
      intent.current = null;
      setTaskId(report.taskId);
      setCursors([]);
      setNotice(
        "Report request accepted. Its status and sealed findings appear below.",
      );
      results.refresh();
    } catch (failure) {
      if (
        !controller.signal.aborted &&
        failure instanceof APIError &&
        [
          "snapshot_changed",
          "revision_conflict",
          "invalid_request",
          "forbidden",
          "not_found_or_inaccessible",
        ].includes(failure.code)
      ) {
        // A definitive rejection must not pin later explicit requests to an
        // old task. Ambiguous network/source failures retain their retry key.
        intent.current = null;
      }
      if (!controller.signal.aborted)
        setError(
          failure instanceof APIError
            ? failure
            : new APIError("request_failed", "The report request failed."),
        );
    } finally {
      if (!controller.signal.aborted) {
        pending.current = null;
        setWorking(false);
      }
    }
  };
  // Any failed authority/source read hides retained content immediately. A
  // subsequent successful fetch is required before showing it again.
  const accessible = !results.error && !error;
  const page = accessible ? results.data : undefined;
  return (
    <>
      <section className="panel report-request">
        <div>
          <h2>Explain the available evidence</h2>
          <p>
            Generate a deterministic report from a sealed job snapshot.
            Suggested actions are advice; they never execute changes.
          </p>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={profile === "include_log_tail"}
              disabled={working}
              onChange={(event) =>
                setProfile(
                  event.target.checked ? "include_log_tail" : "metadata",
                )
              }
            />
            Include redacted log tails (up to 64 KiB per stream)
          </label>
          <p>
            Metadata is included by default. Log tails require your current log
            access and an operator-configured redaction policy. No model
            provider is invoked.
          </p>
          <label className="report-run">
            Actual run UUID (optional)
            <input
              aria-label="Report run UUID"
              value={run?.id ?? runId}
              readOnly={!!run}
              disabled={working}
              placeholder="Recent runs"
              onChange={(event) => setRunId(event.target.value)}
              spellCheck={false}
              maxLength={36}
            />
          </label>
        </div>
        <button
          className="button"
          disabled={working || !!results.error}
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
        <ErrorNotice error={(error || results.error)!} retry={restart} />
      )}
      <section className="panel report-history">
        <div className="panel-heading">
          <div>
            <h2>Report history</h2>
            <p>
              Reports available to your account for this job, across all runs.
              Each report retains its original run references.
            </p>
          </div>
          <button className="button secondary" onClick={restart}>
            Refresh reports
          </button>
        </div>
        {results.loading && !page ? (
          <Spinner label="Loading reports" />
        ) : (
          page && (
            <>
              {page.items.length ? (
                <ul className="report-list">
                  {page.items.map((report) => (
                    <li key={report.taskId}>
                      <div>
                        <strong>
                          {report.profile === "include_log_tail"
                            ? "Metadata and log tails"
                            : "Metadata report"}
                        </strong>
                        <span className="secondary-line">
                          {timestamp(
                            report.createdAt,
                            bootstrap.preferences.timezone,
                          )}{" "}
                          · revision {report.sourceRevision}
                        </span>
                        <span className="mono secondary-line">
                          Task {report.taskId}
                        </span>
                      </div>
                      <div>
                        <Status value={report.state} />
                        {report.outdated && <Status value="outdated" />}
                        <button
                          className="text-button"
                          aria-pressed={taskId === report.taskId}
                          onClick={() => setTaskId(report.taskId)}
                        >
                          Open report
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              ) : (
                <Empty title="No reports on this page">
                  Request a report to analyze available evidence. Missing
                  evidence is disclosed explicitly.
                </Empty>
              )}
              {cursors.length >= 100 && (
                <p className="panel-note">
                  Back navigation retains the previous 100 pages. Refresh
                  reports to return to the beginning.
                </p>
              )}
              <div className="pagination">
                <span>
                  Fetched{" "}
                  {timestamp(page.fetchedAt, bootstrap.preferences.timezone)}
                </span>
                <div>
                  <button
                    className="button secondary"
                    disabled={!cursors.length}
                    onClick={() => {
                      setTaskId(undefined);
                      setCursors((old) => old.slice(0, -1));
                    }}
                  >
                    Previous reports
                  </button>
                  <button
                    className="button secondary"
                    disabled={!page.nextCursor}
                    onClick={() => {
                      setTaskId(undefined);
                      setCursors((old) => [
                        ...old.slice(-99),
                        page.nextCursor!,
                      ]);
                    }}
                  >
                    Next reports
                  </button>
                </div>
              </div>
            </>
          )
        )}
      </section>
      {taskId && accessible && (
        <ReportView
          key={`${taskId}:${readGeneration}`}
          path={`${path}/reports/${encodeURIComponent(taskId)}`}
          taskId={taskId}
          job={job}
          onReadError={setError}
        />
      )}
    </>
  );
}
function ReportView({
  path,
  taskId,
  job,
  onReadError,
}: {
  path: string;
  taskId: string;
  job: JobRef;
  onReadError: (error: APIError) => void;
}) {
  const { identity, bootstrap } = useSession();
  const decode = useMemo(
    () => (value: unknown) => {
      const report = reportForJob(value, job);
      if (
        report.taskId !== taskId ||
        (report.state === "ready" && !report.detail)
      )
        throw new APIError(
          "invalid_response",
          "The report detail is incomplete or has a different identity.",
        );
      return report;
    },
    [job.deploymentId, job.namespaceId, job.jobId, taskId],
  );
  const result = useResource(path, identity, 5000, decode);
  useEffect(() => {
    if (result.error) onReadError(result.error);
  }, [result.error, onReadError]);
  const report = result.error ? undefined : result.data;
  return (
    <article
      className="panel report-card"
      aria-label="Selected diagnosis report"
    >
      <div className="panel-heading">
        <h2>Diagnosis report</h2>
        <button className="text-button" onClick={result.refresh}>
          Refresh selected report
        </button>
      </div>
      {result.error ? (
        <ErrorNotice error={result.error} retry={result.refresh} />
      ) : !report ? (
        <Spinner label="Loading diagnosis" />
      ) : (
        <div className="panel-body">
          <div className="finding-heading">
            <Status value={report.state} />
            {report.outdated && <Status value="outdated" />}
          </div>
          {report.outdated && (
            <p className="notice warning">
              The source evidence has changed. This report describes its
              original sealed snapshot.
            </p>
          )}
          <dl className="facts compact">
            <Fact label="Task" value={report.taskId} />
            <Fact label="Original report" value={report.reportId} />
            <Fact label="Original evidence" value={report.evidenceId} />
            <Fact label="Analysis evidence" value={report.analysisEvidenceId} />
            <Fact label="Source revision" value={report.sourceRevision} />
            <Fact label="Selected run" value={report.runId || "Recent runs"} />
            <Fact label="Profile" value={title(report.profile)} />
            <Fact
              label="Expires"
              value={timestamp(
                report.expiresAt,
                bootstrap.preferences.timezone,
              )}
            />
          </dl>
          {report.failureCode && (
            <p className="notice warning">
              Report could not finish: {title(report.failureCode)}. You can
              request a new report after the condition is resolved.
            </p>
          )}
          {!report.detail && !report.failureCode && (
            <p role="status">
              {["queued", "collecting", "analyzing"].includes(report.state)
                ? "The report is in progress. Status updates automatically while this view is visible."
                : "Sealed findings are not available for this task."}
            </p>
          )}
          {report.detail && (
            <SealedReport
              key={`${report.reportId}:${report.evidenceId}:${report.analysisEvidenceId}`}
              report={report}
              detail={report.detail}
              path={path}
              onReadError={onReadError}
            />
          )}
        </div>
      )}
    </article>
  );
}
function Fact({ label, value }: { label: string; value?: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value || "Unavailable"}</dd>
    </div>
  );
}
function Confidence({ confidence }: { confidence: Wire.ReportConfidence }) {
  return (
    <p className="muted">
      Confidence: {confidence.score}/100 · {title(confidence.band)}.{" "}
      {confidence.basis} This score is not a calibrated probability.
    </p>
  );
}
function SealedReport({
  report,
  detail,
  path,
  onReadError,
}: {
  report: Wire.Report;
  detail: Wire.ReportDetail;
  path: string;
  onReadError: (error: APIError) => void;
}) {
  const { bootstrap } = useSession();
  const [citationId, setCitationId] = useState<string>();
  const refs = (ids: string[], label: string) =>
    ids.length > 0 && (
      <div className="report-evidence-links">
        <strong>{label}:</strong>
        {ids.map((id) => {
          const citation = detail.citations.find((entry) => entry.id === id);
          return citation ? (
            <button
              key={id}
              className="text-button"
              onClick={() => setCitationId(id)}
            >
              View evidence: {citation.label || id}
            </button>
          ) : (
            <span key={id} className="mono">
              {id} (citation unavailable)
            </span>
          );
        })}
      </div>
    );
  const d = detail.disclosure;
  return (
    <>
      <p>
        Snapshot captured{" "}
        {timestamp(detail.capturedAt, bootstrap.preferences.timezone)} · report
        generated{" "}
        {timestamp(detail.generatedAt, bootstrap.preferences.timezone)}
      </p>
      {detail.findings.length === 0 && (
        <p>No finding was established from the available evidence.</p>
      )}
      {detail.findings.map((finding) => (
        <section className="finding" key={finding.id}>
          <div className="finding-heading">
            <Status value={finding.severity} />
            <h3>{finding.title}</h3>
            {finding.id === detail.primaryFindingId && (
              <span className="badge neutral">Primary finding</span>
            )}
          </div>
          <p>{finding.explanation}</p>
          <p className="muted">
            {finding.code} · {title(finding.category)} · analyzer{" "}
            {finding.analyzer}
          </p>
          <Confidence confidence={finding.confidence} />
          {refs(finding.supportingEvidence, "Supporting evidence")}
          {refs(finding.contradictingEvidence, "Contradicting evidence")}
          {finding.contradictingFindings.length > 0 && (
            <p>
              Contradicting findings:{" "}
              {finding.contradictingFindings
                .map(
                  (id) =>
                    detail.findings.find((entry) => entry.id === id)?.title ||
                    id,
                )
                .join("; ")}
            </p>
          )}
        </section>
      ))}
      <section className="finding">
        <h3>Suggested actions</h3>
        <p>
          Advice only. Dashboard does not run commands, retry, cancel, or change
          a job.
        </p>
        {detail.actions.length ? (
          detail.actions.map((action) => (
            <div key={action.id}>
              <h4>{action.summary}</h4>
              <p>{action.description}</p>
              <p className="muted">
                {title(action.kind)} · {action.code}
                {action.requiresConfirmation
                  ? " · Requires review and confirmation before any action outside Dashboard."
                  : ""}
              </p>
              {refs(action.supportingEvidence, "Supporting evidence")}
            </div>
          ))
        ) : (
          <p>No action is suggested.</p>
        )}
      </section>
      <section className="finding">
        <h3>Retry advice: {title(detail.retry.verdict)}</h3>
        <p>{detail.retry.rationale}</p>
        <p>Existing policy: {detail.retry.existingPolicy || "Unavailable"}</p>
        <Confidence confidence={detail.retry.confidence} />
        <ul>
          {detail.retry.reasons.map((reason, i) => (
            <li key={i}>{reason}</li>
          ))}
        </ul>
        {detail.retry.earliestAt && (
          <p>
            Earliest suggested time:{" "}
            {timestamp(detail.retry.earliestAt, bootstrap.preferences.timezone)}
          </p>
        )}
        {refs(detail.retry.supportingEvidence, "Supporting evidence")}
      </section>
      <section className="finding">
        <h3>Missing evidence and warnings</h3>
        {!detail.missingEvidence.length && !detail.warnings.length && (
          <p>No missing evidence or warnings were reported.</p>
        )}
        <ul>
          {detail.missingEvidence.map((missing, i) => (
            <li key={`missing${i}`}>
              <strong>{missing.code}:</strong> {missing.description}
            </li>
          ))}
          {detail.warnings.map((warning, i) => (
            <li key={`warning${i}`}>
              <strong>{warning.code}:</strong> {warning.message}
            </li>
          ))}
        </ul>
      </section>
      <section className="finding">
        <h3>Sealed evidence</h3>
        <p>
          Citations open the original sealed evidence for this report. They do
          not reread current logs.
        </p>
        <ul>
          {detail.citations.map((citation) => (
            <li key={citation.id}>
              <button
                className="text-button"
                aria-pressed={citationId === citation.id}
                onClick={() => setCitationId(citation.id)}
              >
                {citation.label || citation.id}
              </button>{" "}
              · {title(citation.kind)}{" "}
              <span className="mono">{citation.id}</span>
            </li>
          ))}
        </ul>
        {citationId && (
          <CitationView
            key={citationId}
            report={report}
            citationId={citationId}
            path={`${path}/citations/${encodeURIComponent(citationId)}`}
            onReadError={onReadError}
          />
        )}
      </section>
      <details className="finding">
        <summary>Provenance and disclosure</summary>
        <dl className="facts compact">
          <Fact label="Control instance" value={detail.controlInstanceId} />
          <Fact label="Control version" value={detail.controlVersion} />
          <Fact label="Contract" value={detail.contractVersion} />
          <Fact label="Platform" value={detail.platform} />
          <Fact label="Observed phase" value={title(detail.phase)} />
          <Fact label="Observed outcome" value={title(detail.outcome)} />
          <Fact label="Mode" value={title(detail.mode)} />
          {Object.entries(detail.versions).map(([name, value]) => (
            <Fact key={name} label={title(name)} value={String(value)} />
          ))}
        </dl>
        <p>
          Actual runs:{" "}
          {detail.runs.length
            ? detail.runs
                .map(
                  (run) =>
                    `${run.number} (${run.id})${run.executionId ? ` · execution ${run.executionId}` : ""}`,
                )
                .join("; ")
            : "Unavailable"}
        </p>
        <p>
          Analyzers:{" "}
          {detail.analyzers
            .map((analyzer) => `${analyzer.name} ${analyzer.version}`)
            .join("; ") || "None recorded"}
        </p>
        <p>
          Provider invoked: {d.providerInvoked ? "Yes" : "No"}. Generated
          content used: {d.generatedContentUsed ? "Yes" : "No"}. Locality:{" "}
          {d.locality}.
        </p>
        {detail.generators.map((generator, i) => (
          <p key={i}>
            Generator: {generator.provider} · {generator.model} ·{" "}
            {generator.profile} · {generator.locality}
          </p>
        ))}
        <dl className="facts compact">
          <Fact label="Disclosure profile" value={d.profile} />
          <Fact
            label="Provider / model"
            value={[d.provider, d.model].filter(Boolean).join(" / ") || "None"}
          />
          <Fact label="Request reference" value={d.requestId} />
          <Fact label="Data classes" value={d.classes.join(", ") || "None"} />
          <Fact label="Items" value={count(d.itemCount)} />
          <Fact
            label="Artifacts"
            value={`${count(d.artifactCount)} · ${count(d.artifactBytes)} bytes`}
          />
          <Fact
            label="Enrichments"
            value={`${count(d.enrichmentCount)} · ${count(d.enrichmentBytes)} bytes`}
          />
          <Fact label="Request bytes" value={count(d.requestBytes)} />
          <Fact
            label="Redaction notices"
            value={count(d.redactionNoticeCount)}
          />
        </dl>
        <p>Disclosed item IDs: {d.itemIds.join(", ") || "None"}</p>
        <p>Disclosed artifact IDs: {d.artifactIds.join(", ") || "None"}</p>
        <p>Disclosed enrichment IDs: {d.enrichmentIds.join(", ") || "None"}</p>
        <h4>Omissions</h4>
        <ul>
          {detail.omissions.map((omission, i) => (
            <li key={i}>
              {omission.code}: {omission.affects.join(", ")}
            </li>
          ))}
        </ul>
        <h4>Redaction notices</h4>
        <ul>
          {detail.redactionNotices.map((redaction, i) => (
            <li key={i}>
              {redaction.code} · {count(redaction.count)} ·{" "}
              {redaction.affects.join(", ")}
            </li>
          ))}
        </ul>
      </details>
    </>
  );
}
function CitationView({
  report,
  citationId,
  path,
  onReadError,
}: {
  report: Wire.Report;
  citationId: string;
  path: string;
  onReadError: (error: APIError) => void;
}) {
  const { identity, bootstrap } = useSession();
  const decode = useMemo(
    () => (value: unknown) => {
      const citation = citationForReport(value, report, citationId);
      return { citation, preview: citationPreview(citation) };
    },
    [
      report.taskId,
      report.reportId,
      report.evidenceId,
      report.analysisEvidenceId,
      citationId,
    ],
  );
  const result = useResource(path, identity, 5000, decode);
  useEffect(() => {
    if (result.error) onReadError(result.error);
  }, [result.error, onReadError]);
  if (result.error)
    return <ErrorNotice error={result.error} retry={result.refresh} />;
  if (!result.data) return <Spinner label="Validating sealed citation" />;
  const { citation: c, preview } = result.data;
  return (
    <div className="citation" aria-label="Sealed citation">
      <h4>{c.label}</h4>
      <p>{preview.note}</p>
      <pre>{preview.text || "(Empty sealed content)"}</pre>
      <dl className="facts compact">
        <Fact label="Range basis" value={c.rangeBasis} />
        <Fact
          label="Sanitized byte range"
          value={
            c.startOffset !== undefined && c.endOffset !== undefined
              ? `[${c.startOffset}, ${c.endOffset})`
              : undefined
          }
        />
        <Fact
          label="Original byte mapping"
          value={
            c.originalOffsetsExact &&
            c.originalStartOffset !== undefined &&
            c.originalEndOffset !== undefined
              ? `[${c.originalStartOffset}, ${c.originalEndOffset}) — exact`
              : "No exact original byte mapping for this selection"
          }
        />
        <Fact label="Source entity" value={c.sourceEntityId} />
        <Fact label="Source revision" value={c.sourceRevision} />
        <Fact label="Run" value={c.runId} />
        <Fact label="Run number" value={c.runNumber} />
        <Fact label="Execution" value={c.executionId} />
        <Fact label="Stream" value={c.stream} />
        <Fact label="Quality" value={c.quality} />
        <Fact label="Disclosure" value={c.disclosure} />
        <Fact
          label="Captured"
          value={timestamp(c.capturedAt, bootstrap.preferences.timezone)}
        />
        <Fact
          label="Observed"
          value={timestamp(c.observedAt, bootstrap.preferences.timezone)}
        />
      </dl>
    </div>
  );
}
