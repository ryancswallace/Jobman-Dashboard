import { jobStatus, statusConfidence } from "../lib/format";
import { useMemo } from "react";
import { RunSelector } from "../components/RunSelector";
import { runDetailForJob, validRunId } from "../lib/runs";
import { nativeJobLink } from "../lib/privateLinks";
import {
  Link,
  useLocation,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { useSession } from "../lib/session";
import { resourcePath } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodeJobDetail } from "../lib/api";
import { ArtifactList } from "../components/ArtifactList";
import { sourceLabel, timestamp, title, workloadRoute } from "../lib/format";
import {
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
import { LogViewer } from "../components/LogViewer";
import { Reports } from "../components/Reports";
import { JobExecution } from "../components/JobExecution";
import { SelectedRunFacts } from "../components/SelectedRunFacts";
export function JobDetailPage() {
  const { deploymentId = "", namespaceId = "", jobId = "" } = useParams(),
    { bootstrap, identity } = useSession(),
    [search, setSearch] = useSearchParams(),
    location = useLocation();
  const ref = { deploymentId, namespaceId, jobId },
    path = resourcePath(ref, "jobs", jobId),
    tab = search.get("tab") ?? "details";
  const result = useResource(
      path,
      identity,
      bootstrap.preferences.refreshSeconds * 1000,
      decodeJobDetail,
    ),
    job = result.data?.data;
  const selectedRunId = search.get("runId") ?? "";
  const runDecode = useMemo(
    () => (value: unknown) => runDetailForJob(value, ref, selectedRunId),
    [deploymentId, namespaceId, jobId, selectedRunId],
  );
  const runResult = useResource(
    selectedRunId && validRunId(selectedRunId)
      ? `${path}/runs/${selectedRunId}`
      : null,
    identity,
    bootstrap.preferences.refreshSeconds * 1000,
    runDecode,
  );
  const selectedRun = runResult.error ? undefined : runResult.data?.data;
  const selectRun = (id?: string) => {
    const next = new URLSearchParams(search);
    if (id) next.set("runId", id);
    else next.delete("runId");
    next.delete("artifactRun");
    next.delete("artifactCursor");
    setSearch(next);
  };
  const runReady = !selectedRunId || !!selectedRun;

  return (
    <>
      <Link
        className="back-link"
        to={location.state?.returnTo ?? "/jobs"}
        state={{ scrollY: location.state?.scrollY }}
      >
        ← Back to jobs
      </Link>
      <PageHeader
        eyebrow={sourceLabel(bootstrap.sources, deploymentId, namespaceId)}
        title={job?.name || jobId}
        description={job?.name ? jobId : undefined}
        actions={
          job && (
            <>
              <a className="button secondary" href={nativeJobLink(ref)}>
                Open in app
              </a>
              <Link
                className="button secondary"
                to="/alerts"
                state={{ watchJob: ref }}
              >
                ☆ Watch this job
              </Link>
            </>
          )
        }
      />
      <div className="page-meta">
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
        <button className="text-button" onClick={result.refresh}>
          Refresh
        </button>
      </div>
      {result.error && (
        <ErrorNotice error={result.error} retry={result.refresh} />
      )}
      {!job && result.loading ? (
        <Spinner label="Loading job" />
      ) : (
        job && (
          <>
            <div className="status-strip">
              <div>
                <span>Job status</span>
                <Status value={job.phase} label={jobStatus(job.phase)} />
              </div>
              <div>
                <span>Final result</span>
                <Status value={job.outcome} />
              </div>
              <div>
                <span>Requested state</span>
                <Status value={job.desiredState} />
              </div>
              <div>
                <span>Status confidence</span>
                <Status
                  value={job.observationConfidence}
                  label={statusConfidence(job.observationConfidence)}
                />
              </div>
            </div>
            <p className="panel-note">
              Requested state shows whether Control has been asked to run or
              cancel the job. Status confidence describes how current and
              reliable its latest execution observations are. Outdated means
              Control has not heard recently from the execution agent. It does
              not prove the job has stopped.
            </p>
            {job.desiredState === "cancel" && job.outcome !== "cancelled" && (
              <p className="notice warning">
                Cancellation is requested. Control has not yet confirmed that
                the job was cancelled.
              </p>
            )}
            {job.imported && (
              <p className="notice subtle">
                This job was imported from earlier records. Submitter, start,
                and completion details are shown only when Control provides
                them.
              </p>
            )}
            <RunSelector
              key={`${identity}:${path}`}
              job={ref}
              selected={selectedRun}
              select={selectRun}
            />
            {selectedRunId && !validRunId(selectedRunId) && (
              <p role="alert">
                The selected run ID is invalid.{" "}
                <button onClick={() => selectRun()}>Clear run selection</button>
              </p>
            )}
            {runResult.error && (
              <>
                <ErrorNotice
                  error={runResult.error}
                  retry={runResult.refresh}
                />
                <button
                  className="button secondary"
                  onClick={() => selectRun()}
                >
                  Clear run selection
                </button>
              </>
            )}
            {selectedRunId && !runResult.error && runResult.loading && (
              <Spinner label="Verifying selected run" />
            )}
            <nav className="tabs" aria-label="Job sections">
              {["details", "logs", "artifacts", "diagnosis"].map((t) => (
                <button
                  key={t}
                  className={tab === t ? "selected" : ""}
                  aria-current={tab === t ? "page" : undefined}
                  onClick={() => {
                    const next = new URLSearchParams(search);
                    next.set("tab", t);
                    setSearch(next);
                  }}
                >
                  {t === "artifacts"
                    ? "Output files"
                    : t[0].toUpperCase() + t.slice(1)}
                </button>
              ))}
            </nav>
            {tab === "details" && (
              <div className="detail-grid">
                <JobExecution key={`${identity}:${path}`} job={job} />
                {selectedRun && (
                  <SelectedRunFacts
                    run={selectedRun}
                    timezone={bootstrap.preferences.timezone}
                  />
                )}
                <section className="panel">
                  <div className="panel-heading">
                    <h2>Current job details</h2>
                  </div>
                  <dl className="facts">
                    <div>
                      <dt>Submitted by</dt>
                      <dd>
                        {job.owner?.displayName ||
                          job.owner?.id ||
                          "Original submitter unavailable"}
                      </dd>
                    </div>
                    <div>
                      <dt>Target</dt>
                      <dd>{job.target?.name ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Target ID</dt>
                      <dd>{job.target?.id ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Partition</dt>
                      <dd>{job.partition ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Workload checksum</dt>
                      <dd className="mono">
                        {job.workloadDigest ?? "Unavailable"}
                      </dd>
                    </div>
                    <div>
                      <dt>Target configuration version</dt>
                      <dd>{job.target?.generation ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Execution system</dt>
                      <dd>{job.target?.backend ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Target configuration ID</dt>
                      <dd>{job.target?.generationId ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Dependency decision</dt>
                      <dd>{title(job.disposition)}</dd>
                    </div>
                    <div>
                      <dt>Record version</dt>
                      <dd>{job.revision}</dd>
                    </div>
                    <div>
                      <dt>Current run</dt>
                      <dd>
                        {job.currentRun?.id ?? "Unavailable"}
                        {job.currentRun && ` (run ${job.currentRun.number})`}
                      </dd>
                    </div>
                    <div>
                      <dt>Execution ID</dt>
                      <dd>{job.currentRun?.executionId ?? "Unavailable"}</dd>
                    </div>
                    {job.group?.collectionId && (
                      <div>
                        <dt>Collection</dt>
                        <dd>
                          <Link
                            to={workloadRoute({
                              ...job,
                              kind: "collection",
                              id: job.group.collectionId,
                            })}
                          >
                            {job.group.collectionId}
                          </Link>
                          {job.group.collectionIndex !== undefined &&
                            ` · item ${job.group.collectionIndex}`}
                        </dd>
                      </div>
                    )}
                    {job.group?.graphId && (
                      <div>
                        <dt>Dependency graph</dt>
                        <dd>
                          <Link
                            to={workloadRoute({
                              ...job,
                              kind: "graph",
                              id: job.group.graphId,
                            })}
                          >
                            {job.group.graphId}
                          </Link>
                          {job.group.graphIndex !== undefined &&
                            ` · node ${job.group.graphIndex}`}
                        </dd>
                      </div>
                    )}
                    {Object.entries(job.labels ?? {}).map(([label, value]) => (
                      <div key={label}>
                        <dt>Label: {label}</dt>
                        <dd>{value}</dd>
                      </div>
                    ))}
                  </dl>
                  <p className="panel-note">
                    The execution system is the software that runs the job, such
                    as Slurm. The target configuration version identifies the
                    settings selected for this job. The workload checksum
                    identifies its submitted workload definition.
                  </p>
                </section>
                <section className="panel">
                  <div className="panel-heading">
                    <h2>Current job timeline</h2>
                  </div>
                  <dl className="facts">
                    {[
                      ["Created", job.createdAt],
                      ["Started", job.startedAt],
                      ["Completed", job.completedAt],
                      ["Record updated", job.updatedAt],
                      ["Status confidence updated", job.confidenceUpdatedAt],
                      ["Start recorded", job.lifecycle?.startedRecordedAt],
                      [
                        "Completion recorded",
                        job.lifecycle?.completedRecordedAt,
                      ],
                    ].map(([label, value]) => (
                      <div key={label}>
                        <dt>{label}</dt>
                        <dd>
                          {timestamp(value, bootstrap.preferences.timezone)}
                        </dd>
                      </div>
                    ))}
                    <div>
                      <dt>Start time source</dt>
                      <dd>{title(job.lifecycle?.startedProvenance)}</dd>
                    </div>
                    <div>
                      <dt>Completion time source</dt>
                      <dd>{title(job.lifecycle?.completedProvenance)}</dd>
                    </div>
                  </dl>
                  <p className="panel-note">
                    Started and completed times describe the job’s execution.
                    Recorded times show when Control saved those observations. A
                    record update does not confirm that the job is still
                    running. Missing times remain unavailable.
                  </p>
                </section>
                <section className="panel wide">
                  <div className="panel-heading">
                    <h2>Current job scheduler details</h2>
                  </div>
                  {job.scheduler ? (
                    <dl className="facts compact">
                      {[
                        ["Scheduler job ID", job.scheduler.nativeId],
                        ["Execution system", job.scheduler.backend],
                        ["State", job.scheduler.state],
                        ["Reason", job.scheduler.reason],
                        ["Cluster", job.scheduler.cluster],
                        [
                          "Observed at",
                          timestamp(
                            job.scheduler.observedAt,
                            bootstrap.preferences.timezone,
                          ),
                        ],
                      ].map(([label, value]) => (
                        <div key={label}>
                          <dt>{label}</dt>
                          <dd>{value ?? "Unavailable"}</dd>
                        </div>
                      ))}
                    </dl>
                  ) : (
                    <p className="panel-body muted">
                      No scheduler details have been reported for this job.
                    </p>
                  )}
                </section>
              </div>
            )}
            {tab === "logs" && runReady && (
              <LogViewer
                key={`${identity}:${path}:${selectedRunId}`}
                job={ref}
                run={selectedRun}
              />
            )}
            {tab === "diagnosis" && runReady && (
              <Reports
                key={`${identity}:${path}:${selectedRunId}`}
                job={ref}
                run={selectedRun}
              />
            )}
            {tab === "artifacts" && runReady && (
              <ArtifactList
                key={`${identity}:${path}:${selectedRunId}`}
                path={path}
                run={selectedRun}
              />
            )}
          </>
        )
      )}
    </>
  );
}
