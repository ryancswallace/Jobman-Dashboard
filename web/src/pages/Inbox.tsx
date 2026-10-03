import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { request, APIError, apiQuery } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodePage } from "../lib/api";
import type { InboxItem } from "../lib/models";
import { jobRoute, sourceLabel, timestamp, title } from "../lib/format";
import {
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
const decodeInbox = (value: unknown) => decodePage<InboxItem>(value);
export function InboxPage() {
  const { identity, bootstrap } = useSession(),
    [search, setSearch] = useSearchParams(),
    [error, setError] = useState<APIError>();
  const result = useResource(
    apiQuery("inbox", {
      limit: 50,
      cursor: search.get("cursor") ?? undefined,
      unread: search.get("unread") ?? undefined,
    }),
    identity,
    bootstrap.preferences.refreshSeconds * 1000,
    decodeInbox,
  );
  const read = async (item: InboxItem) => {
    try {
      await request(`/api/v1/inbox/${encodeURIComponent(item.id)}`, {
        method: "PATCH",
        body: { read: !item.read },
      });
      result.refresh();
    } catch (e) {
      setError(
        e instanceof APIError
          ? e
          : new APIError("request_failed", "Read state could not be updated."),
      );
    }
  };
  return (
    <>
      <PageHeader
        title="Inbox"
        description="Your authorized job updates, retained for 30 days. Available even when push is muted or missed."
        actions={
          <Link className="button secondary" to="/alerts">
            Manage alert rules
          </Link>
        }
      />
      <div className="page-meta">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={search.get("unread") === "true"}
            onChange={(e) =>
              setSearch(e.target.checked ? { unread: "true" } : {})
            }
          />
          Unread only
        </label>
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
      </div>
      {(error || result.error) && (
        <ErrorNotice error={(error || result.error)!} retry={result.refresh} />
      )}{" "}
      {!result.data && result.loading ? (
        <Spinner label="Loading your inbox" />
      ) : result.data?.data.length ? (
        <section className="panel inbox-list">
          {result.data.data.map((item) => (
            <article
              className={`inbox-item ${item.read ? "read" : "unread"}`}
              key={item.id}
            >
              <div className="inbox-symbol" aria-hidden="true">
                {item.outcome === "success" ? "✓" : "◇"}
              </div>
              <div className="inbox-body">
                <div className="inbox-heading">
                  <Link to={jobRoute(item.job)}>
                    {title(item.outcome)} · {item.job.jobId}
                  </Link>
                  <Status value={item.outcome} />
                </div>
                <p>
                  {sourceLabel(
                    bootstrap.sources,
                    item.job.deploymentId,
                    item.job.namespaceId,
                  )}
                </p>
                <time dateTime={item.eventAt}>
                  {timestamp(item.eventAt, bootstrap.preferences.timezone)}
                </time>
                <p className="muted small">
                  Matched rules: {item.matchedRules.join(", ") || "Unavailable"}
                  {item.deliveryStatus &&
                    ` · Push: ${title(item.deliveryStatus)}`}
                </p>
              </div>
              <button className="text-button" onClick={() => void read(item)}>
                {item.read ? "Mark unread" : "Mark read"}
              </button>
            </article>
          ))}
          {result.data.meta.nextCursor && (
            <div className="pagination">
              <span>More authorized history is available.</span>
              <button
                className="button secondary"
                onClick={() =>
                  setSearch({ cursor: result.data!.meta.nextCursor! })
                }
              >
                Older updates →
              </button>
            </div>
          )}
        </section>
      ) : (
        !result.error && (
          <Empty title="You’re caught up">
            No {search.get("unread") ? "unread " : ""}updates are available.
            Alerts appear after your rules become active.
          </Empty>
        )
      )}
    </>
  );
}
