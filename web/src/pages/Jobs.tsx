import { useEffect } from "react";
import { useLocation, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { apiQuery } from "../lib/transport";
import { scopeQuery, localDateValue } from "../lib/format";
import { useResource } from "../lib/useResource";
import { decodeJobs } from "../lib/api";
import { outcomes } from "../lib/models";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
} from "../components/States";
import { JobTable } from "../components/JobTable";
export function JobsPage() {
  const { scope, bootstrap, identity } = useSession(),
    [search, setSearch] = useSearchParams(),
    location = useLocation();
  const values = Object.fromEntries(search),
    query = scopeQuery(scope, bootstrap.sources);
  const result = useResource(
    apiQuery("jobs", {
      ...query,
      limit: 50,
      cursor: values.cursor,
      phase: values.phase,
      outcome: values.outcome,
      owner: values.owner,
      jobId: values.jobId,
      from: values.from,
      to: values.to,
      attention: values.attention,
    }),
    identity,
    values.cursor ? 0 : bootstrap.preferences.refreshSeconds * 1000,
    decodeJobs,
  );
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(search);
    value ? next.set(key, value) : next.delete(key);
    next.delete("cursor");
    setSearch(next);
  };
  useEffect(() => {
    if (result.data && location.state?.scrollY)
      window.scrollTo(0, location.state.scrollY);
  }, [!!result.data]);
  const filtered = [
    "phase",
    "outcome",
    "owner",
    "jobId",
    "from",
    "to",
    "attention",
  ].some((key) => values[key]);
  return (
    <>
      <PageHeader
        title="Jobs"
        description="Follow every job in your namespace, including work submitted by teammates."
        actions={
          <button
            className="button secondary"
            onClick={() =>
              values.cursor ? set("cursor", "") : result.refresh()
            }
          >
            ↻ Refresh
          </button>
        }
      />
      <form
        className="filters"
        onSubmit={(e) => {
          e.preventDefault();
          const form = new FormData(e.currentTarget);
          set("jobId", String(form.get("jobId") ?? ""));
        }}
      >
        <label>
          Phase
          <select
            value={values.phase ?? ""}
            onChange={(e) => set("phase", e.target.value)}
          >
            <option value="">All phases</option>
            <option value="active">All active</option>
            <option value="awaiting">Awaiting execution</option>
            {[
              "accepted",
              "assigning",
              "accepted_execution",
              "running",
              "terminal",
            ].map((p) => (
              <option key={p} value={p}>
                {p.replace(/_/g, " ")}
              </option>
            ))}
          </select>
        </label>
        <label>
          Outcome
          <select
            value={values.outcome ?? ""}
            onChange={(e) => set("outcome", e.target.value)}
          >
            <option value="">All outcomes</option>
            {outcomes.map((o) => (
              <option key={o} value={o}>
                {o.replace(/_/g, " ")}
              </option>
            ))}
          </select>
        </label>
        <label>
          Submitted by
          <select
            value={values.owner ?? ""}
            onChange={(e) => set("owner", e.target.value)}
          >
            <option value="">Everyone</option>
            <option value="me">Me</option>
          </select>
        </label>
        <label>
          Completed from (UTC)
          <input
            type="datetime-local"
            value={localDateValue(values.from)}
            onChange={(e) =>
              set(
                "from",
                e.target.value
                  ? new Date(e.target.value + "Z").toISOString()
                  : "",
              )
            }
          />
        </label>
        <label>
          Completed before (UTC)
          <input
            type="datetime-local"
            value={localDateValue(values.to)}
            onChange={(e) =>
              set(
                "to",
                e.target.value
                  ? new Date(e.target.value + "Z").toISOString()
                  : "",
              )
            }
          />
        </label>
        <label className="grow">
          Exact job ID
          <div className="input-action">
            <input
              name="jobId"
              defaultValue={values.jobId}
              key={values.jobId}
              placeholder="Enter a complete job ID"
              autoComplete="off"
            />
            <button className="button secondary" type="submit">
              Find
            </button>
          </div>
        </label>
        {filtered && (
          <button
            className="text-button"
            type="button"
            onClick={() => {
              const next = new URLSearchParams(search);
              [
                "phase",
                "outcome",
                "owner",
                "jobId",
                "from",
                "to",
                "attention",
                "cursor",
              ].forEach((k) => next.delete(k));
              setSearch(next);
            }}
          >
            Clear filters
          </button>
        )}
      </form>
      {(values.from || values.to) && (
        <p className="notice subtle">
          Completion window: {values.from ?? "Any start"} to{" "}
          {values.to ?? "Any end"} (UTC, end exclusive).
        </p>
      )}
      {values.attention && (
        <p className="notice subtle">
          Showing nonterminal jobs with stale, uncertain, or lost observation
          confidence.
        </p>
      )}
      <div className="page-meta">
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
        <span>Newest first · up to 50 per page</span>
      </div>
      {result.error && (
        <ErrorNotice
          error={result.error}
          retry={() => (values.cursor ? set("cursor", "") : result.refresh())}
        />
      )}
      <Completeness meta={result.data?.meta} />
      <section className="panel">
        {!result.data && result.loading ? (
          <Spinner label="Loading jobs" />
        ) : result.data?.data.length ? (
          <JobTable jobs={result.data.data} />
        ) : (
          !result.error && (
            <Empty
              title={filtered ? "No jobs match these filters" : "No jobs yet"}
            >
              {filtered
                ? "Try another phase, outcome, or exact ID."
                : "Submitted jobs in this scope will appear here."}
            </Empty>
          )
        )}
        {result.data && (
          <div className="pagination">
            <span>
              {result.data.data.length} jobs on this page · results reflect
              current source state
            </span>
            <div>
              {values.cursor && (
                <button
                  className="button secondary"
                  onClick={() => set("cursor", "")}
                >
                  First page
                </button>
              )}
              {result.data.meta.nextCursor && (
                <button
                  className="button secondary"
                  onClick={() => {
                    const next = new URLSearchParams(search);
                    next.set("cursor", result.data!.meta.nextCursor!);
                    setSearch(next);
                  }}
                >
                  Next page →
                </button>
              )}
            </div>
          </div>
        )}
      </section>
    </>
  );
}
