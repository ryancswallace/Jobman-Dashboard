import { useMemo, useState } from "react";
import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import type { JobRef } from "../lib/models";
import { useSession } from "../lib/session";
import { useResource } from "../lib/useResource";
import { resourcePath } from "../lib/transport";
import { runPageForJob } from "../lib/runs";
import { timestamp } from "../lib/format";
import { ErrorNotice, Freshness, Spinner, Status } from "./States";
export function RunSelector({
  job,
  selected,
  select,
}: {
  job: JobRef;
  selected?: Wire.JobRun;
  select: (id?: string) => void;
}) {
  const { identity, bootstrap } = useSession();
  const [open, setOpen] = useState(false),
    [pages, setPages] = useState<string[]>([]);
  const cursor = pages.at(-1),
    path = resourcePath(job, "jobs", job.jobId);
  const decode = useMemo(
    () => (v: unknown) => runPageForJob(v, job),
    [job.deploymentId, job.namespaceId, job.jobId],
  );
  const result = useResource(
    open
      ? `${path}/runs?${new URLSearchParams({ limit: "50", ...(cursor ? { cursor } : {}) })}`
      : null,
    identity,
    0,
    decode,
  );
  return (
    <section className="panel run-selector" aria-label="Run selection">
      <div className="panel-heading">
        <div>
          <h2>Run selection</h2>
          <p>
            {selected
              ? `Run ${selected.number} · ${selected.id}`
              : "Current logs · all artifact runs · recent diagnosis evidence"}
          </p>
        </div>
        <button
          className="button secondary"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          {open ? "Close run list" : "Choose a run"}
        </button>
        {selected && (
          <button className="button secondary" onClick={() => select()}>
            Use current defaults
          </button>
        )}
      </div>
      {selected && (
        <div className="panel-body">
          <Status value={selected.phase} />
          <Status value={selected.outcome} />
          <p>
            Run metadata updated{" "}
            {timestamp(selected.updatedAt, bootstrap.preferences.timezone)}.
            This is not an execution lifecycle time.
          </p>
          {selected.executionId ? (
            <p>
              Execution <span className="mono">{selected.executionId}</span> ·{" "}
              {selected.executionPhase}
            </p>
          ) : (
            <p>No execution has been assigned to this run.</p>
          )}
        </div>
      )}
      {open && (
        <div className="panel-body">
          <p>
            Run references are newest first. This selection applies to logs,
            artifact metadata and new diagnosis requests.
          </p>
          <Freshness
            fetchedAt={result.fetchedAt}
            loading={result.loading}
            error={!!result.error}
          />
          <button
            className="text-button"
            onClick={() => {
              if (pages.length) setPages([]);
              else result.refresh();
            }}
          >
            Refresh runs
          </button>
          {result.error && (
            <ErrorNotice
              error={result.error}
              retry={() => {
                setPages([]);
                result.refresh();
              }}
            />
          )}
          {!result.data && result.loading ? (
            <Spinner label="Loading run references" />
          ) : (
            result.data && (
              <>
                <p>
                  {result.data.data.total} recorded runs at the start of this
                  traversal.
                </p>
                {!result.data.data.items.length && (
                  <p>
                    No run has been recorded. An unassigned job or imported
                    history may have no execution runs.
                  </p>
                )}
                <ul className="run-list">
                  {result.data.data.items.map((run) => (
                    <li key={run.id}>
                      <button
                        className="text-button"
                        aria-current={
                          selected?.id === run.id ? "true" : undefined
                        }
                        onClick={() => select(run.id)}
                      >
                        Select run {run.number}
                      </button>{" "}
                      · {run.phase}
                      {run.outcome ? ` / ${run.outcome}` : ""}
                      <span className="secondary-line mono">{run.id}</span>
                    </li>
                  ))}
                </ul>
                <div className="pagination">
                  {!!pages.length && (
                    <button
                      className="button secondary"
                      disabled={result.loading}
                      onClick={() => setPages((p) => p.slice(0, -1))}
                    >
                      Previous run page
                    </button>
                  )}
                  {result.data.data.nextCursor && (
                    <button
                      className="button secondary"
                      disabled={result.loading || pages.length >= 63}
                      onClick={() =>
                        setPages((p) => [...p, result.data!.data.nextCursor!])
                      }
                    >
                      Next run page
                    </button>
                  )}
                </div>
                {pages.length >= 63 && (
                  <p>
                    Navigation history reached its 64-page limit. Refresh to
                    restart.
                  </p>
                )}
              </>
            )
          )}
        </div>
      )}
    </section>
  );
}
