import { jobStatus } from "../lib/format";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { apiQuery, resourcePath } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodeWorkloads, decodeWorkloadDetail } from "../lib/workloads";
import { GraphInspector } from "../components/GraphInspector";
import {
  count,
  jobRoute,
  scopeQuery,
  sourceLabel,
  timestamp,
  title,
  workloadRoute,
} from "../lib/format";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
export function WorkloadsPage() {
  const { bootstrap, identity, scope } = useSession(),
    [search, setSearch] = useSearchParams(),
    kind = search.get("kind") ?? "collection";
  const result = useResource(
    apiQuery(`workloads/${kind}`, {
      ...scopeQuery(scope, bootstrap.sources),
      limit: 50,
      cursor: search.get("cursor") ?? undefined,
    }),
    identity,
    search.get("cursor") ? 0 : bootstrap.preferences.refreshSeconds * 1000,
    decodeWorkloads,
  );
  return (
    <>
      <PageHeader
        title="Job groups"
        description="Browse related jobs in collections, Slurm arrays, and dependency graphs."
        actions={
          <button
            className="button secondary"
            onClick={() => {
              if (search.get("cursor")) {
                const q = new URLSearchParams(search);
                q.delete("cursor");
                setSearch(q);
              } else result.refresh();
            }}
          >
            ↻ Refresh
          </button>
        }
      />
      <nav className="tabs" aria-label="Job group type">
        {[
          ["collection", "Collections"],
          ["array", "Slurm arrays"],
          ["graph", "Dependency graphs"],
        ].map(([value, label]) => (
          <button
            key={value}
            className={kind === value ? "selected" : ""}
            aria-current={kind === value ? "page" : undefined}
            onClick={() => {
              const q = new URLSearchParams(search);
              q.set("kind", value);
              q.delete("cursor");
              setSearch(q);
            }}
          >
            {label}
          </button>
        ))}
      </nav>
      <p className="panel-note">
        {kind === "graph"
          ? "A dependency graph connects jobs with conditions that must be met before dependent jobs can run."
          : kind === "array"
            ? "A Slurm array groups related tasks. Open an array to inspect each task and its job details."
            : "A collection groups related jobs. Open a collection to see its jobs, results, and execution limits."}
      </p>
      <div className="page-meta">
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
        <span>
          {result.data
            ? `Job groups: ${count(result.data.data.total)}${result.data.meta.completeness === "partial" ? " in available deployments (partial count)" : ""}`
            : "Group totals and individual jobs"}
        </span>
      </div>
      {result.error && (
        <ErrorNotice error={result.error} retry={result.refresh} />
      )}
      <Completeness meta={result.data?.meta} />
      {!result.data && result.loading ? (
        <Spinner label="Loading job groups" />
      ) : result.data?.data.items.length ? (
        <section className="panel">
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Job group</th>
                  <th>Deployment / namespace</th>
                  <th>Jobs</th>
                  <th>Job counts</th>
                  <th>Policy</th>
                </tr>
              </thead>
              <tbody>
                {result.data.data.items.map((w) => (
                  <tr key={`${w.deploymentId}:${w.namespaceId}:${w.id}`}>
                    <td>
                      <Link className="job-name" to={workloadRoute(w)}>
                        {w.name || w.id}
                      </Link>
                      <span className="secondary-line mono">{w.id}</span>
                    </td>
                    <td>
                      {sourceLabel(
                        bootstrap.sources,
                        w.deploymentId,
                        w.namespaceId,
                      )}
                    </td>
                    <td>{count(w.totalChildren)}</td>
                    <td>
                      <div className="count-chips">
                        {Object.entries(w.counts).map(([key, value]) => (
                          <span className="count-chip" key={key}>
                            {jobStatus(key)} {count(value)}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td>
                      {w.failurePolicy || w.unsatisfiedPolicy || "Unavailable"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="pagination">
            <span>Group records are not counted as additional jobs.</span>
            {result.data.meta.nextCursor && (
              <button
                className="button secondary"
                onClick={() => {
                  const q = new URLSearchParams(search);
                  q.set("cursor", result.data!.meta.nextCursor!);
                  setSearch(q);
                }}
              >
                Next page →
              </button>
            )}
          </div>
        </section>
      ) : (
        !result.error && (
          <Empty
            title={`No ${kind === "array" ? "Slurm arrays" : kind === "graph" ? "dependency graphs" : "collections"} returned for this selection`}
          >
            Job groups appear when reported by the selected Control deployments.
          </Empty>
        )
      )}
    </>
  );
}
export function WorkloadDetailPage() {
  const {
      deploymentId = "",
      namespaceId = "",
      kind = "",
      id = "",
    } = useParams(),
    { identity, bootstrap } = useSession(),
    [search, setSearch] = useSearchParams(),
    selected = search.get("node") ?? undefined;
  const setSelected = (node?: string) => {
    const q = new URLSearchParams(search);
    if (node) q.set("node", node);
    else q.delete("node");
    setSearch(q);
  };
  const path = resourcePath(
    { deploymentId, namespaceId },
    `workloads/${encodeURIComponent(kind)}`,
    id,
  );
  const result = useResource(
      `${path}?${new URLSearchParams({ limit: "50", ...(search.get("cursor") ? { cursor: search.get("cursor")! } : {}) })}`,
      identity,
      search.get("cursor") ? 0 : bootstrap.preferences.refreshSeconds * 1000,
      decodeWorkloadDetail,
    ),
    detail = result.data?.data,
    w = detail?.workload;
  return (
    <>
      <Link
        className="back-link"
        to={`/workloads?kind=${encodeURIComponent(kind)}`}
      >
        ← Back to job groups
      </Link>
      <PageHeader
        eyebrow={sourceLabel(bootstrap.sources, deploymentId, namespaceId)}
        title={w?.name || id}
        description={`${title(kind)} · ${id}`}
        actions={
          <button
            className="button secondary"
            onClick={() => {
              if (search.get("cursor")) {
                const q = new URLSearchParams(search);
                q.delete("cursor");
                setSearch(q);
              } else result.refresh();
            }}
          >
            ↻ Refresh
          </button>
        }
      />
      {result.error && (
        <ErrorNotice error={result.error} retry={result.refresh} />
      )}
      <Completeness meta={result.data?.meta} />
      {!detail && result.loading ? (
        <Spinner label="Loading job group" />
      ) : (
        detail &&
        w && (
          <>
            <div className="metric-grid workload-metrics">
              <div className="metric-card purple">
                <span>Total jobs in group</span>
                <strong>{count(w.totalChildren)}</strong>
                <small>
                  Reported by Control at{" "}
                  {timestamp(w.asOf, bootstrap.preferences.timezone)}
                </small>
              </div>
              {Object.entries(w.counts).map(([key, value]) => (
                <div className="metric-card" key={key}>
                  <span>{jobStatus(key)}</span>
                  <strong>{count(value)}</strong>
                </div>
              ))}
            </div>
            <p className="panel-note">
              In these group totals, Active excludes jobs that are only
              submitted and jobs that have finished.
            </p>
            <section className="panel">
              <dl className="facts compact">
                <div>
                  <dt>Concurrency limit</dt>
                  <dd>{w.concurrency ?? "Unavailable"}</dd>
                </div>
                <div>
                  <dt>Failure policy</dt>
                  <dd>{w.failurePolicy ?? "Unavailable"}</dd>
                </div>
                {w.arrayPolicy && (
                  <div>
                    <dt>Array policy</dt>
                    <dd>{w.arrayPolicy}</dd>
                  </div>
                )}
                {w.arrayMode && (
                  <div>
                    <dt>Array mode</dt>
                    <dd>{w.arrayMode}</dd>
                  </div>
                )}
                {w.unsatisfiedPolicy && (
                  <div>
                    <dt>When dependencies cannot be met</dt>
                    <dd>{w.unsatisfiedPolicy}</dd>
                  </div>
                )}
                <div>
                  <dt>Record version</dt>
                  <dd>{w.revision}</dd>
                </div>
                {kind === "array" && (
                  <div>
                    <dt>Scheduler array ID</dt>
                    <dd>{w.arrayId ?? "Not observed"}</dd>
                  </div>
                )}
              </dl>
            </section>
            {kind === "graph" && (
              <GraphInspector
                path={path}
                scope={{ deploymentId, namespaceId }}
                onSelect={setSelected}
                onClear={() => setSelected(undefined)}
                selected={selected}
              />
            )}
            <section className="panel">
              <div className="panel-heading">
                <div>
                  <h2>
                    {kind === "graph"
                      ? "Graph nodes"
                      : kind === "array"
                        ? "Array tasks"
                        : "Jobs in this collection"}
                  </h2>
                  <p>
                    {kind === "graph"
                      ? "Each node is a job. Control reports whether its dependencies allow it to run."
                      : "Open a job to view its details and logs."}
                  </p>
                </div>
              </div>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>{kind === "array" ? "Task index" : "Node / job"}</th>
                      <th>Job</th>
                      <th>Status / result</th>
                      <th>Ready to run / dependency decision</th>
                      {kind === "graph" && <th>Dependencies</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {detail.children.map((node) => (
                      <tr
                        key={node.id}
                        className={selected === node.id ? "selected-row" : ""}
                      >
                        <td>
                          {kind === "array" ? (
                            (node.taskIndex ?? "Unavailable")
                          ) : kind === "graph" ? (
                            <button
                              className="text-button"
                              aria-current={
                                selected === node.id ? "true" : undefined
                              }
                              onClick={() => setSelected(node.id)}
                            >
                              {node.name ?? node.id}
                            </button>
                          ) : (
                            (node.name ?? `Child ${node.index ?? node.id}`)
                          )}
                        </td>
                        <td>
                          <Link to={jobRoute(node.job)}>
                            {node.job.name || node.job.jobId}
                          </Link>
                        </td>
                        <td>
                          <Status
                            value={node.job.phase}
                            label={jobStatus(node.job.phase)}
                          />
                          {node.job.outcome && (
                            <span className="secondary-line">
                              {title(node.job.outcome)}
                            </span>
                          )}
                        </td>
                        <td>
                          {title(node.readiness ?? node.disposition)}
                          {node.disposition && node.readiness && (
                            <span className="secondary-line">
                              {title(node.disposition)}
                            </span>
                          )}
                        </td>
                        {kind === "graph" && (
                          <td>
                            {node.dependencyCounts ? (
                              <div className="count-chips">
                                {Object.entries(node.dependencyCounts).map(
                                  ([state, value]) => (
                                    <span key={state} className="count-chip">
                                      {title(state)} {count(value)}
                                    </span>
                                  ),
                                )}
                              </div>
                            ) : (
                              "Dependency counts unavailable"
                            )}
                            <button
                              className="text-button"
                              aria-current={
                                selected === node.id ? "true" : undefined
                              }
                              onClick={() => setSelected(node.id)}
                            >
                              Inspect dependencies
                            </button>
                          </td>
                        )}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="pagination">
                <span>
                  {detail.children.length} children loaded of{" "}
                  {count(detail.total)} at this child-page observation
                </span>
                <div>
                  {search.get("cursor") && (
                    <button
                      className="button secondary"
                      onClick={() => setSearch({})}
                    >
                      First page
                    </button>
                  )}
                  {result.data?.meta.nextCursor && (
                    <button
                      className="button secondary"
                      onClick={() =>
                        setSearch({ cursor: result.data!.meta.nextCursor! })
                      }
                    >
                      Next page →
                    </button>
                  )}
                </div>
              </div>
            </section>
          </>
        )
      )}
    </>
  );
}
