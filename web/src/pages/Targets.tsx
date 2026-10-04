import "./Targets.css";
import { useCallback, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { apiQuery } from "../lib/transport";
import { count, scopeQuery, sourceLabel, timestamp } from "../lib/format";
import { useResource } from "../lib/useResource";
import {
  decodeTargets,
  decodeTargetDetail,
  decodeTargetPartitions,
  targetPath,
  targetRoute,
  type Target,
} from "../lib/targets";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
const configurationNotice =
  "Configured state and advertised capabilities describe policy. They do not establish agent connectivity or health. Utilization and capacity monitoring come later.";
export function TargetsPage() {
  const { bootstrap, scope, identity } = useSession(),
    [search, setSearch] = useSearchParams();
  const result = useResource(
    apiQuery("targets", {
      ...scopeQuery(scope, bootstrap.sources),
      limit: 50,
      cursor: search.get("cursor") ?? undefined,
    }),
    identity,
    search.get("cursor") ? 0 : bootstrap.preferences.refreshSeconds * 1000,
    decodeTargets,
  );
  const refresh = () => {
    if (search.has("cursor")) setSearch({});
    else result.refresh();
  };
  return (
    <>
      <PageHeader
        title="Targets"
        description="Configured execution destinations and generation metadata."
        actions={
          <button className="button secondary" onClick={refresh}>
            ↻ Refresh
          </button>
        }
      />
      <p className="notice subtle">{configurationNotice}</p>
      <div className="page-meta">
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
        <span>
          {result.data
            ? `${count(result.data.data.total)} targets ${result.data.meta.completeness === "partial" ? "in available sources (subtotal)" : ""}`
            : "Source totals unavailable"}
        </span>
      </div>
      {result.error && <ErrorNotice error={result.error} retry={refresh} />}
      <Completeness meta={result.data?.meta} />
      {!result.data && result.loading ? (
        <Spinner label="Loading target configuration" />
      ) : result.data?.data.items.length ? (
        <section className="panel target-catalog">
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Target</th>
                  <th>Source / namespace</th>
                  <th>Configured state</th>
                  <th>Backend / provider</th>
                  <th>Generation</th>
                  <th>Partitions</th>
                </tr>
              </thead>
              <tbody>
                {result.data.data.items.map((t) => (
                  <tr key={targetPath(t)}>
                    <td>
                      <Link className="job-name" to={targetRoute(t)}>
                        {t.name}
                      </Link>
                      <span className="secondary-line mono">{t.targetId}</span>
                    </td>
                    <td>
                      {sourceLabel(
                        bootstrap.sources,
                        t.deploymentId,
                        t.namespaceId,
                      )}
                    </td>
                    <td>
                      <Status value={t.state} tone="neutral" />
                    </td>
                    <td>
                      {t.generation.executionBackend}
                      <span className="secondary-line">
                        {t.generation.provider.kind}
                      </span>
                    </td>
                    <td>{t.generation.number}</td>
                    <td>{count(t.generation.partitionCount)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="pagination">
            <span>
              Grouped by source; newest targets first within each source.
              Bounded pages.
            </span>
            {result.data.meta.nextCursor && (
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
          <details>
            <summary>Source totals and observation times</summary>
            <ul>
              {result.data.data.totals.map((t) => (
                <li key={`${t.deploymentId}:${t.namespaceId}`}>
                  {sourceLabel(
                    bootstrap.sources,
                    t.deploymentId,
                    t.namespaceId,
                  )}
                  : {count(t.total)} · observed {timestamp(t.asOf)}
                </li>
              ))}
            </ul>
          </details>
        </section>
      ) : (
        !result.error && (
          <Empty title="No targets available">
            No configured targets were returned for this scope.
          </Empty>
        )
      )}
    </>
  );
}
export function TargetDetailPage() {
  const { deploymentId = "", namespaceId = "", targetId = "" } = useParams(),
    { identity, bootstrap } = useSession();
  const [revision, setRevision] = useState(0);
  const decode = useCallback(
    (value: unknown) =>
      decodeTargetDetail(value, { deploymentId, namespaceId, targetId }),
    [deploymentId, namespaceId, targetId],
  );
  const result = useResource(
    targetPath({ deploymentId, namespaceId, targetId }),
    identity,
    0,
    decode,
  );
  const refresh = () => {
    setRevision((v) => v + 1);
    result.refresh();
  };
  const t = result.data?.data;
  return (
    <div className="target-details">
      <Link className="back-link" to="/targets">
        ← Back to targets
      </Link>
      <PageHeader
        title={t?.name ?? "Target"}
        description={sourceLabel(bootstrap.sources, deploymentId, namespaceId)}
        actions={
          <button className="button secondary" onClick={refresh}>
            ↻ Refresh target
          </button>
        }
      />
      <p className="notice subtle">{configurationNotice}</p>
      {result.error && <ErrorNotice error={result.error} retry={refresh} />}
      <Completeness meta={result.data?.meta} />
      {result.loading && !t && <Spinner label="Loading target details" />}
      {t && (
        <>
          <section className="panel">
            <h2>Configuration</h2>
            <dl className="facts-grid">
              <dt>Target ID</dt>
              <dd className="mono">{t.targetId}</dd>
              <dt>Kind / configured state</dt>
              <dd>
                {t.kind} / {t.state}
              </dd>
              <dt>Revision</dt>
              <dd>{t.revision}</dd>
              <dt>Generation</dt>
              <dd>
                {t.generation.number} ·{" "}
                <span className="mono">{t.generation.id}</span>
              </dd>
              <dt>Backend / transport</dt>
              <dd>
                {t.generation.executionBackend} / {t.generation.transport}
              </dd>
              <dt>Provider</dt>
              <dd>
                <BoundedTargetText
                  value={[
                    t.generation.provider.kind,
                    t.generation.provider.region,
                    t.generation.provider.clusterName,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                />
              </dd>
              <dt>Runtimes</dt>
              <dd>{t.generation.runtimes.join(", ") || "None advertised"}</dd>
              <dt>Operating systems</dt>
              <dd>
                {t.generation.operatingSystems.join(", ") || "None advertised"}
              </dd>
              <dt>Architectures</dt>
              <dd>
                {t.generation.architectures.join(", ") || "None advertised"}
              </dd>
              <dt>Capabilities</dt>
              <dd>
                {t.generation.capabilities.join(", ") || "None advertised"}
              </dd>
              <dt>Log store</dt>
              <dd>
                {t.generation.logStore
                  ? `${t.generation.logStore.name} · version ${t.generation.logStore.version}`
                  : "None configured"}
              </dd>
              <dt>Artifact stores</dt>
              <dd>
                {t.generation.artifactStores
                  .map((s) => `${s.name} · version ${s.version}`)
                  .join(", ") || "None configured"}
              </dd>
              <dt>Created / updated</dt>
              <dd>
                {timestamp(t.createdAt)} / {timestamp(t.updatedAt)}
              </dd>
              <dt>Source observed</dt>
              <dd>{timestamp(t.asOf)}</dd>
            </dl>
          </section>
          <section className="panel">
            <h2>Partition preview</h2>
            <p>
              {t.generation.partitions.length} of{" "}
              {count(t.generation.partitionCount)} configured partitions
              {t.generation.partitionsTruncated ? " · preview truncated" : ""}.
              The complete catalog is paged below.
            </p>
            <details>
              <summary>Show partition preview</summary>
              <ul className="partition-list">
                {t.generation.partitions.map((p) => (
                  <li key={p.name}>
                    {p.name}
                    {p.isDefault ? " (default)" : ""}
                  </li>
                ))}
              </ul>
            </details>
          </section>
          <PartitionBrowser
            key={`${targetPath(t)}:${t.generation.id}:${revision}`}
            target={t}
            refreshTarget={refresh}
          />
        </>
      )}
    </div>
  );
}
function PartitionBrowser({
  target,
  refreshTarget,
}: {
  target: Target;
  refreshTarget: () => void;
}) {
  const { identity } = useSession();
  const [history, setHistory] = useState<(string | undefined)[]>([undefined]);
  const cursor = history[history.length - 1];
  const decode = useCallback(
    (value: unknown) => decodeTargetPartitions(value, target),
    [target],
  );
  const result = useResource(
    apiQuery(`${targetPath(target).replace("/api/v1/", "")}/partitions`, {
      generationId: target.generation.id,
      limit: 50,
      cursor,
    }),
    identity,
    0,
    decode,
  );
  const resetRequired =
    result.error?.code === "target_changed" ||
    result.error?.code === "cursor_expired";
  return (
    <section className="panel">
      <h2>All partitions</h2>
      <p>
        Generation {target.generation.number}; each page rechecks current access
        and generation.
      </p>
      {result.error && (
        <ErrorNotice
          error={result.error}
          retry={resetRequired ? refreshTarget : result.refresh}
        />
      )}
      {resetRequired ? (
        <button className="button" onClick={refreshTarget}>
          Refresh target and restart partitions
        </button>
      ) : (
        <>
          {result.loading && !result.data && (
            <Spinner label="Loading partition page" />
          )}
          {result.data && (
            <>
              <p>
                {result.data.data.items.length} partitions on this page ·{" "}
                {count(result.data.data.total)} total
              </p>
              <ul className="partition-list">
                {result.data.data.items.map((p) => (
                  <li key={p.name}>
                    {p.name}
                    {p.isDefault ? " (default)" : ""}
                  </li>
                ))}
              </ul>
              <Completeness meta={result.data.meta} />
              <div className="pagination">
                <button
                  className="button secondary"
                  disabled={history.length === 1 || result.loading}
                  onClick={() => setHistory((h) => h.slice(0, -1))}
                >
                  Previous partition page
                </button>
                {result.data.meta.nextCursor && (
                  <button
                    className="button secondary"
                    disabled={result.loading}
                    onClick={() =>
                      setHistory((h) => [...h, result.data!.meta.nextCursor!])
                    }
                  >
                    Next partition page
                  </button>
                )}
              </div>
            </>
          )}
        </>
      )}
    </section>
  );
}

export function BoundedTargetText({ value }: { value: string }) {
  const truncated = value.length > 512;
  return (
    <span>
      {truncated ? value.slice(0, 512) + "…" : value}
      {truncated && (
        <span className="secondary-line">
          Display truncated after 512 characters; the source value is preserved.
        </span>
      )}
    </span>
  );
}
