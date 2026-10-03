import { useState } from "react";
import {
  Link,
  useLocation,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { useSession } from "../lib/session";
import { APIError, request, resourcePath } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodeJobDetail, decodePage } from "../lib/api";
import type { Artifact } from "../lib/models";
import { count, sourceLabel, timestamp, workloadRoute } from "../lib/format";
import {
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
  Empty,
} from "../components/States";
import { LogViewer } from "../components/LogViewer";
import { Reports } from "../components/Reports";
const decodeArtifacts = (value: unknown) => decodePage<Artifact>(value);
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
  const artifacts = useResource(
    tab === "artifacts" ? `${path}/artifacts?limit=50` : null,
    identity,
    0,
    decodeArtifacts,
  );
  const [watchState, setWatchState] = useState(""),
    [watchError, setWatchError] = useState<APIError>();
  const watch = async () => {
    setWatchState("Saving…");
    try {
      await request("/api/v1/rules", {
        method: "POST",
        body: {
          name: `Watch ${job?.name || jobId}`,
          enabled: true,
          scope: "watched_jobs",
          namespaces: [{ deploymentId, namespaceId }],
          jobs: [ref],
          outcomeMode: "selected",
          outcomes: ["failure", "timed_out", "aborted", "lost"],
        },
        idempotencyKey: crypto.randomUUID(),
      });
      setWatchState("Watching unsuccessful results");
    } catch (e) {
      setWatchError(
        e instanceof APIError
          ? e
          : new APIError("request_failed", "The alert could not be saved."),
      );
      setWatchState("");
    }
  };
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
            <button
              className="button secondary"
              disabled={!!watchState}
              onClick={() => void watch()}
            >
              {watchState || "☆ Watch this job"}
            </button>
          )
        }
      />
      {watchError && <ErrorNotice error={watchError} />}
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
                <span>Execution phase</span>
                <Status value={job.phase} />
              </div>
              <div>
                <span>Terminal outcome</span>
                <Status value={job.outcome} />
              </div>
              <div>
                <span>Desired state</span>
                <Status value={job.desiredState} />
              </div>
              <div>
                <span>Observation confidence</span>
                <Status value={job.observationConfidence} />
              </div>
            </div>
            {job.desiredState === "cancel" && job.outcome !== "cancelled" && (
              <p className="notice warning">
                Cancellation is requested. A cancelled terminal outcome has not
                been reported.
              </p>
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
                  {t[0].toUpperCase() + t.slice(1)}
                </button>
              ))}
            </nav>
            {tab === "details" && (
              <div className="detail-grid">
                <section className="panel">
                  <div className="panel-heading">
                    <h2>Job facts</h2>
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
                      <dt>Target generation</dt>
                      <dd>{job.target?.generation ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Backend</dt>
                      <dd>{job.target?.backend ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Revision</dt>
                      <dd>{job.revision}</dd>
                    </div>
                    <div>
                      <dt>Run</dt>
                      <dd>
                        {job.runId ?? "Unavailable"}
                        {job.runNumber && ` (run ${job.runNumber})`}
                      </dd>
                    </div>
                    {job.group && (
                      <div>
                        <dt>Workload</dt>
                        <dd>
                          <Link to={workloadRoute({ ...job, ...job.group })}>
                            {job.group.kind} / {job.group.id}
                          </Link>
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
                </section>
                <section className="panel">
                  <div className="panel-heading">
                    <h2>Reported timeline</h2>
                  </div>
                  <dl className="facts">
                    {[
                      ["Created", job.createdAt],
                      ["Started", job.startedAt],
                      ["Completed", job.completedAt],
                      ["Record updated", job.updatedAt],
                      ["Confidence updated", job.confidenceUpdatedAt],
                    ].map(([label, value]) => (
                      <div key={label}>
                        <dt>{label}</dt>
                        <dd>
                          {timestamp(value, bootstrap.preferences.timezone)}
                        </dd>
                      </div>
                    ))}
                  </dl>
                  <p className="panel-note">
                    Record updates are not execution heartbeats. Missing
                    lifecycle observations stay unavailable.
                  </p>
                </section>
                <section className="panel wide">
                  <div className="panel-heading">
                    <h2>Scheduler observations</h2>
                  </div>
                  {job.scheduler ? (
                    <dl className="facts compact">
                      {[
                        ["Native job ID", job.scheduler.nativeId],
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
                      No scheduler evidence is available for this job.
                    </p>
                  )}
                </section>
              </div>
            )}
            {tab === "logs" && (
              <LogViewer key={`${identity}:${path}`} job={ref} />
            )}
            {tab === "diagnosis" && (
              <Reports key={`${identity}:${path}`} job={ref} />
            )}
            {tab === "artifacts" && (
              <section className="panel">
                <div className="panel-heading">
                  <div>
                    <h2>Published artifacts</h2>
                    <p>
                      Metadata only. Downloads are not part of this release.
                    </p>
                  </div>
                </div>
                {artifacts.error && (
                  <ErrorNotice
                    error={artifacts.error}
                    retry={artifacts.refresh}
                  />
                )}{" "}
                {!artifacts.data && artifacts.loading ? (
                  <Spinner label="Loading artifact metadata" />
                ) : artifacts.data?.data.length ? (
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Name</th>
                          <th>Size</th>
                          <th>Checksum</th>
                          <th>Published</th>
                          <th>Availability</th>
                        </tr>
                      </thead>
                      <tbody>
                        {artifacts.data.data.map((a) => (
                          <tr key={a.id}>
                            <td>{a.name}</td>
                            <td>{count(a.sizeBytes)} bytes</td>
                            <td className="mono">
                              {a.checksum ?? "Unavailable"}
                            </td>
                            <td>
                              {timestamp(
                                a.publishedAt,
                                bootstrap.preferences.timezone,
                              )}
                            </td>
                            <td>
                              <Status value={a.availability} />
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  !artifacts.error && (
                    <Empty title="No artifact metadata">
                      No artifacts have been published for this job.
                    </Empty>
                  )
                )}
              </section>
            )}
          </>
        )
      )}
    </>
  );
}
