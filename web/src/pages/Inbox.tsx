import { nativeInboxLink } from "../lib/privateLinks";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import {
  request,
  APIError,
  apiQuery,
  authorizationErrors,
} from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodeInbox, decodeInboxItem, type InboxItem } from "../lib/inbox";
import {
  count,
  jobRoute,
  scopeQuery,
  sourceLabel,
  timestamp,
  title,
} from "../lib/format";
import {
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
import "./Inbox.css";
function useReadState(key: string, refresh: () => void, clear: () => void) {
  const current = useRef(key);
  current.current = key;
  const controller = useRef<AbortController | undefined>(undefined);
  const [state, setState] = useState<{
    key: string;
    busy?: string;
    error?: APIError;
  }>({ key });
  useEffect(
    () => () => {
      controller.current?.abort();
      controller.current = undefined;
    },
    [key],
  );
  const change = async (item: InboxItem) => {
    if (controller.current) return;
    const operation = new AbortController();
    controller.current = operation;
    setState({ key, busy: item.id });
    try {
      decodeInboxItem(
        await request(`/api/v1/inbox/${encodeURIComponent(item.id)}`, {
          method: "PATCH",
          body: { read: !item.read },
          signal: operation.signal,
        }),
        item.id,
      );
      if (current.current === key && !operation.signal.aborted) {
        setState({ key });
        refresh();
      }
    } catch (error) {
      if (current.current !== key || operation.signal.aborted) return;
      const problem =
        error instanceof APIError
          ? error
          : new APIError(
              "request_failed",
              "The notification could not be marked read or unread. Refresh and try again.",
            );
      setState({ key, error: problem });
      if (
        authorizationErrors.has(problem.code) ||
        problem.status === 401 ||
        problem.status === 403
      )
        clear();
    } finally {
      if (controller.current === operation) controller.current = undefined;
    }
  };
  return {
    change,
    dismissError: () =>
      setState((previous) => ({ ...previous, error: undefined })),
    ...(state.key === key ? state : { key }),
  };
}
function InboxEntry({
  item,
  busy,
  onRead,
  detail = false,
}: {
  item: InboxItem;
  busy?: string;
  onRead: (item: InboxItem) => void;
  detail?: boolean;
}) {
  const { bootstrap } = useSession();
  return (
    <article className={`inbox-item ${item.read ? "read" : "unread"}`}>
      <div className="inbox-symbol" aria-hidden="true">
        {item.outcome === "success" ? "✓" : "◇"}
      </div>
      <div className="inbox-body">
        <div className="inbox-heading">
          {detail ? (
            <h2>{item.jobName ?? item.job.jobId}</h2>
          ) : (
            <Link to={`/inbox/${encodeURIComponent(item.id)}`}>
              {item.jobName ?? item.job.jobId}
            </Link>
          )}
          <Status value={item.outcome} />
          {!item.read && <span className="tag">Unread</span>}
        </div>
        <p>
          {sourceLabel(
            bootstrap.sources,
            item.job.deploymentId,
            item.job.namespaceId,
          )}
        </p>
        <dl className="inbox-times">
          <div>
            <dt>Recorded by Control</dt>
            <dd>
              <time dateTime={item.eventAt}>
                {timestamp(item.eventAt, bootstrap.preferences.timezone)}
              </time>
            </dd>
          </div>
          {item.observedCompletedAt && (
            <div>
              <dt>Observed completion</dt>
              <dd>
                {timestamp(
                  item.observedCompletedAt,
                  bootstrap.preferences.timezone,
                )}
              </dd>
            </div>
          )}
          <div>
            <dt>Removed from inbox on</dt>
            <dd>{timestamp(item.expiresAt, bootstrap.preferences.timezone)}</dd>
          </div>
        </dl>
        {item.jobAvailability === "available" ? (
          <Link className="text-button" to={jobRoute(item.job)}>
            Open current job →
          </Link>
        ) : (
          <p className="notice subtle">
            {item.jobAvailability === "missing"
              ? "Current job detail is no longer available. You can still view this earlier notification."
              : "Current job detail is temporarily unavailable. The job name is hidden until its current details can be checked."}
          </p>
        )}
        <details className="inbox-context" open={detail}>
          <summary>
            Matched {item.matchedRules.length === 1 ? "rule" : "rules"} (
            {item.matchedRules.length})
          </summary>
          <ul>
            {item.matchedRules.map((rule) => (
              <li key={rule.ruleId}>
                <strong>{rule.name}</strong> · revision {rule.revision} ·{" "}
                {title(rule.scope)} ·{" "}
                {rule.outcomeMode === "all_terminal"
                  ? "All final results"
                  : rule.outcomes.map(title).join(", ")}
              </li>
            ))}
          </ul>
          <p className="small muted">
            These are the rule settings that matched this event, even if the
            rule was later renamed, stopped, or deleted.
          </p>
        </details>
        <p className="small muted">
          {item.delivery.total === "0"
            ? "No iPhone notification was queued. The update is still available here."
            : `iPhone notification delivery: ${Object.entries(
                item.delivery.byState,
              )
                .map(
                  ([state, n]) => `${count(n)} ${title(state).toLowerCase()}`,
                )
                .join(
                  " · ",
                )}. Acceptance by Apple’s delivery service does not confirm that the notification appeared on your phone.`}
        </p>
        {detail && (
          <p className="small mono">
            Event {item.eventId} · Control {item.controlInstanceId}
          </p>
        )}
      </div>
      <button
        className="text-button"
        disabled={!!busy}
        onClick={() => onRead(item)}
      >
        {busy === item.id ? "Saving…" : item.read ? "Mark unread" : "Mark read"}
      </button>
    </article>
  );
}
export function InboxPage() {
  const { identity, bootstrap, scope } = useSession(),
    [search, setSearch] = useSearchParams(),
    [generation, setGeneration] = useState(0);
  const scoped = scopeQuery(scope, bootstrap.sources),
    key = `${identity}:${scoped.scope}`;
  const cursor = search.get("cursor") ?? undefined;
  const result = useResource(
    apiQuery("inbox", {
      ...scoped,
      limit: 20,
      cursor,
      unread: search.get("unread") === "true" ? "true" : undefined,
    }),
    `${key}:${generation}`,
    cursor ? 0 : bootstrap.preferences.refreshSeconds * 1000,
    decodeInbox,
  );
  const refresh = () => {
    read.dismissError();
    if (cursor) {
      const next = new URLSearchParams(search);
      next.delete("cursor");
      setSearch(next);
    } else result.refresh();
  };
  const read = useReadState(key, result.refresh, () =>
    setGeneration((n) => n + 1),
  );
  const page = result.data;
  return (
    <>
      <PageHeader
        title="Inbox"
        description="Job notifications you can currently access, kept for 30 days after they enter your inbox. Turning off iPhone notifications does not hide this history."
        actions={
          <>
            <button className="button secondary" onClick={refresh}>
              ↻ Refresh
            </button>
            <Link className="button secondary" to="/alerts">
              Manage alert rules
            </Link>
          </>
        }
      />
      <div className="page-meta">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={search.get("unread") === "true"}
            onChange={(event) => {
              const next = new URLSearchParams(search);
              next.delete("cursor");
              event.target.checked
                ? next.set("unread", "true")
                : next.delete("unread");
              setSearch(next);
            }}
          />
          Unread only
        </label>
        <span aria-live="polite">
          {page
            ? `${count(page.unreadCount)} unread${page.completeness === "partial" ? " in available namespaces (partial count)" : ""}`
            : "Unread count unavailable"}
        </span>
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
      </div>
      {(read.error || result.error) && (
        <ErrorNotice error={(read.error ?? result.error)!} retry={refresh} />
      )}
      {page?.completeness === "partial" && (
        <div className="notice" role="status">
          This inbox is incomplete.{" "}
          {page.unavailableSources > 0 &&
            `${page.unavailableSources} deployment(s) could not verify your access. `}
          {page.inaccessibleScopes > 0 &&
            `${page.inaccessibleScopes} selected namespace(s) are no longer accessible. `}
          Counts include only namespaces you can currently access; some job
          details may be unavailable.
        </div>
      )}
      {!page && result.loading ? (
        <Spinner label="Loading your inbox" />
      ) : page?.items.length ? (
        <section className="panel inbox-list" aria-label="Job notifications">
          {page.items.map((item) => (
            <InboxEntry
              key={item.id}
              item={item}
              busy={read.busy}
              onRead={(item) => void read.change(item)}
            />
          ))}
          {page.nextCursor && (
            <div className="pagination">
              <span>Older notifications are available.</span>
              <button
                className="button secondary"
                disabled={!!read.busy}
                onClick={() => {
                  const next = new URLSearchParams(search);
                  next.set("cursor", page.nextCursor!);
                  setSearch(next);
                }}
              >
                Older updates →
              </button>
            </div>
          )}
        </section>
      ) : (
        page &&
        !result.error && (
          <Empty
            title={
              page.completeness === "partial"
                ? "No updates in available namespaces"
                : "You’re caught up"
            }
          >
            {page.completeness === "partial"
              ? "Unavailable deployments may contain more notifications."
              : `No ${search.get("unread") === "true" ? "unread " : ""}updates match the selected namespaces. Notifications appear when a job result matches an active alert rule.`}
          </Empty>
        )
      )}
    </>
  );
}
export function InboxDetailPage() {
  const { inboxId = "" } = useParams(),
    { identity, bootstrap } = useSession(),
    [generation, setGeneration] = useState(0);
  const decode = useCallback(
    (value: unknown) => decodeInboxItem(value, inboxId),
    [inboxId],
  );
  const result = useResource(
    `/api/v1/inbox/${encodeURIComponent(inboxId)}`,
    `${identity}:${inboxId}:${generation}`,
    bootstrap.preferences.refreshSeconds * 1000,
    decode,
  );
  const read = useReadState(`${identity}:${inboxId}`, result.refresh, () =>
    setGeneration((n) => n + 1),
  );
  return (
    <>
      <PageHeader
        title="Job update"
        description="An earlier job notification. Access and links to the job are checked when you open it."
        actions={
          <>
            {result.data && !result.error && (
              <a
                className="button secondary"
                href={nativeInboxLink(result.data.id)}
              >
                Open in app
              </a>
            )}
            <button className="button secondary" onClick={result.refresh}>
              Refresh
            </button>
            <Link className="button secondary" to="/inbox">
              ← Inbox
            </Link>
          </>
        }
      />
      {(read.error || result.error) && (
        <ErrorNotice
          error={(read.error ?? result.error)!}
          retry={() => {
            read.dismissError();
            result.refresh();
          }}
        />
      )}{" "}
      {!result.data && result.loading ? (
        <Spinner label="Opening job notification" />
      ) : (
        result.data && (
          <section className="panel inbox-list">
            <InboxEntry
              item={result.data}
              busy={read.busy}
              onRead={(item) => void read.change(item)}
              detail
            />
          </section>
        )
      )}
    </>
  );
}
