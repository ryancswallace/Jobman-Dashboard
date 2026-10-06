import { Link } from "react-router-dom";
import type { ReactNode } from "react";
import type { Meta } from "../lib/models";
import { errorHeading, relative, safeReturnPath, title } from "../lib/format";
import { useSession } from "../lib/session";
export function Spinner({ label = "Loading" }: { label?: string }) {
  return (
    <div className="loading" role="status">
      <span className="spinner" aria-hidden="true" />
      {label}…
    </div>
  );
}
export function Empty({
  title: heading,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="empty">
      <span className="empty-symbol" aria-hidden="true">
        ◇
      </span>
      <h2>{heading}</h2>
      <p>{children}</p>
    </div>
  );
}
export function ErrorNotice({
  error,
  retry,
}: {
  error: { code: string; message: string; requestId?: string };
  retry?: () => void;
}) {
  const session = error.code === "unauthenticated";
  return (
    <div className="notice error" role="alert">
      <div>
        <strong>{errorHeading(error.code)}</strong>
        <p>{error.message}</p>
        {error.requestId && <small>Reference: {error.requestId}</small>}
      </div>
      {session ? (
        <a
          className="button"
          href={`/auth/login?returnTo=${encodeURIComponent(safeReturnPath(location.pathname + location.search))}`}
        >
          Sign in
        </a>
      ) : (
        retry && (
          <button className="button secondary" onClick={retry}>
            Try again
          </button>
        )
      )}
    </div>
  );
}
export function ConnectionScreen({
  loading,
  error,
  onRetry,
}: {
  loading: boolean;
  error?: { code: string; message: string };
  onRetry: () => void;
}) {
  return (
    <main className="connection">
      <img src="/logo.svg" className="connection-logo" alt="Jobman Dashboard" />
      <div className="connection-card">
        <p className="eyebrow">YOUR PRIVATE MONITORING WORKSPACE</p>
        <h1>
          Know what’s running.
          <br />
          Understand what happened.
        </h1>
        <p>
          Secure access to your organization’s jobs, logs, and results across
          Jobman Control deployments.
        </p>
        {loading ? (
          <Spinner label="Connecting to Dashboard" />
        ) : error ? (
          <ErrorNotice error={error} retry={onRetry} />
        ) : (
          <a className="button" href="/auth/login">
            Sign in with your organization
          </a>
        )}
        <p className="muted small">
          Use your organization’s private network or VPN. Sign in using your
          organization’s account.
        </p>
      </div>
    </main>
  );
}
export function Completeness({ meta }: { meta?: Meta }) {
  const { bootstrap } = useSession();
  if (!meta) return null;
  const issues = (meta.contributions ?? []).filter(
    (c) =>
      c.status !== "available" && c.status !== "complete" && c.status !== "ok",
  );
  return (
    <>
      {(meta.completeness !== "complete" || issues.length > 0) && (
        <div className="notice warning" role="status">
          <div>
            <strong>Partial results</strong>
            <p>
              Some deployments or namespaces could not be loaded. Totals include
              only the available results; missing results do not mean there are
              no jobs.
            </p>
            <ul>
              {issues.map((source, i) => (
                <li key={`${source.deploymentId}:${source.namespaceId}:${i}`}>
                  {bootstrap.sources.find(
                    (s) => s.deploymentId === source.deploymentId,
                  )?.displayName ?? source.deploymentId}
                  : {title(source.status)}
                  {source.message && ` — ${source.message}`}
                </li>
              ))}
            </ul>
          </div>
        </div>
      )}
    </>
  );
}
export function Freshness({
  fetchedAt,
  loading,
  error,
}: {
  fetchedAt?: string;
  loading?: boolean;
  error?: boolean;
}) {
  return (
    <span className={`freshness ${error ? "stale" : ""}`} aria-live="polite">
      {loading
        ? "Refreshing…"
        : error
          ? "Refresh failed · showing last known data"
          : fetchedAt
            ? `Updated ${relative(fetchedAt)}`
            : "Not yet loaded"}
    </span>
  );
}
export function Status({
  value,
  tone,
  label,
}: {
  value?: string;
  tone?: string;
  label?: string;
}) {
  const color =
    tone ??
    (value === "running" || value === "current" || value === "success"
      ? "good"
      : [
            "failure",
            "timed_out",
            "aborted",
            "lost",
            "corrupt",
            "failed",
          ].includes(value ?? "")
        ? "bad"
        : [
              "stale",
              "uncertain",
              "pending",
              "queued",
              "draining",
              "outdated",
            ].includes(value ?? "")
          ? "warn"
          : "neutral");
  return (
    <span className={`badge ${color}`}>
      <span aria-hidden="true" className="status-dot" />
      {label ?? title(value)}
    </span>
  );
}
export function PageHeader({
  eyebrow,
  title: heading,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        {eyebrow && <p className="eyebrow">{eyebrow}</p>}
        <h1 tabIndex={-1}>{heading}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="header-actions">{actions}</div>}
    </header>
  );
}
export function NotFound() {
  return (
    <>
      <PageHeader title="Page not found" />
      <Empty title="This link is unavailable">
        Check the link, or <Link to="/">return to your overview</Link>.
      </Empty>
    </>
  );
}
