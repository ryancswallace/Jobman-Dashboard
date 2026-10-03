import { Link, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { apiQuery } from "../lib/transport";
import { count, scopeQuery, timestamp } from "../lib/format";
import { useResource } from "../lib/useResource";
import { decodeJobs, decodeOverview } from "../lib/api";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
} from "../components/States";
import { JobTable } from "../components/JobTable";
export function OverviewPage() {
  const { scope, bootstrap, identity } = useSession(),
    [search, setSearch] = useSearchParams();
  const windowHours = search.get("window") ?? "24";
  const query = scopeQuery(scope, bootstrap.sources);
  const summary = useResource(
    apiQuery("overview", { ...query, windowHours }),
    identity,
    bootstrap.preferences.refreshSeconds * 1000,
    decodeOverview,
  );
  const jobs = useResource(
    apiQuery("jobs", { ...query, limit: 8 }),
    identity,
    bootstrap.preferences.refreshSeconds * 1000,
    decodeJobs,
  );
  const data = summary.data?.data;
  const link = (params: Record<string, string>) =>
    `/jobs?${new URLSearchParams({ ...Object.fromEntries(search), ...params })}`;
  const cards = [
    [
      "Active jobs",
      data?.active,
      "phase=active",
      "Across every nonterminal phase",
      "purple",
    ],
    [
      "Awaiting execution",
      data?.awaitingExecution,
      "phase=awaiting",
      "Execution not yet reported running",
      "blue",
    ],
    [
      "Running",
      data?.running,
      "phase=running",
      "With separately reported confidence",
      "green",
    ],
    [
      "Evidence needs attention",
      data?.evidenceAttention,
      "attention=true",
      "Stale, uncertain, or lost observations",
      "amber",
    ],
  ];
  return (
    <>
      <PageHeader
        eyebrow="YOUR WORK, AT A GLANCE"
        title="Overview"
        description="A clear view of activity across your authorized namespaces."
        actions={
          <>
            <label className="inline-field">
              <span className="sr-only">Terminal result window</span>
              <select
                value={windowHours}
                onChange={(e) => {
                  const next = new URLSearchParams(search);
                  next.set("window", e.target.value);
                  setSearch(next);
                }}
              >
                <option value="24">Last 24 hours</option>
                <option value="168">Last 7 days</option>
              </select>
            </label>
            <button
              className="button secondary"
              onClick={() => {
                summary.refresh();
                jobs.refresh();
              }}
            >
              ↻ Refresh
            </button>
          </>
        }
      />
      <div className="page-meta">
        <Freshness
          fetchedAt={summary.fetchedAt}
          loading={summary.loading}
          error={!!summary.error}
        />
        <span>Counts come from complete source summaries</span>
      </div>
      {summary.error && (
        <ErrorNotice error={summary.error} retry={summary.refresh} />
      )}
      <Completeness meta={summary.data?.meta} />
      {!data && summary.loading ? (
        <Spinner label="Loading activity" />
      ) : (
        <div className="metric-grid">
          {cards.map(([label, value, filter, hint, tone]) => (
            <Link
              to={link(Object.fromEntries(new URLSearchParams(filter)))}
              className={`metric-card ${tone}`}
              key={label}
            >
              <span>{label}</span>
              <strong>{count(value)}</strong>
              <small>{hint}</small>
              <span className="metric-arrow" aria-hidden="true">
                ↗
              </span>
            </Link>
          ))}
        </div>
      )}
      {data && (
        <section className="panel">
          <div className="panel-heading">
            <div>
              <h2>Terminal results</h2>
              <p>
                {timestamp(data.window.from, bootstrap.preferences.timezone)} —{" "}
                {timestamp(data.window.to, bootstrap.preferences.timezone)}
              </p>
            </div>
            <span className="badge neutral">By actual completion time</span>
          </div>
          <div className="outcome-grid">
            {Object.entries(data.terminal).map(([outcome, value]) => (
              <Link
                key={outcome}
                to={link({
                  phase: "terminal",
                  outcome,
                  from: data.window.from,
                  to: data.window.to,
                })}
                className="outcome"
              >
                <span
                  className={`outcome-mark ${outcome}`}
                  aria-hidden="true"
                />
                <strong>{count(value)}</strong>
                <span>{outcome.replace(/_/g, " ")}</span>
              </Link>
            ))}
          </div>
          <p className="panel-note">
            Missing completion time: {count(data.missingCompletionTime)}. These
            jobs are excluded from the time window. Attention indicators may
            overlap activity counts.
          </p>
        </section>
      )}
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>Recent jobs</h2>
            <p>Newest submissions in the selected scope</p>
          </div>
          <Link className="text-link" to={link({})}>
            View all jobs →
          </Link>
        </div>
        {jobs.error && <ErrorNotice error={jobs.error} retry={jobs.refresh} />}
        {jobs.loading && !jobs.data ? (
          <Spinner label="Loading jobs" />
        ) : jobs.data?.data.length ? (
          <JobTable jobs={jobs.data.data} />
        ) : (
          <Empty title="No jobs to show">
            Jobs will appear here when they are reported in your authorized
            scope.
          </Empty>
        )}
      </section>
    </>
  );
}
