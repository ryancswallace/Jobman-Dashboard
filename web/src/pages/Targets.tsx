import { useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { apiQuery } from "../lib/transport";
import { scopeQuery, sourceLabel } from "../lib/format";
import { useResource } from "../lib/useResource";
import { decodePage } from "../lib/api";
import type { Target } from "../lib/models";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
const decodeTargets = (value: unknown) => decodePage<Target>(value);
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
    bootstrap.preferences.refreshSeconds * 1000,
    decodeTargets,
  );
  return (
    <>
      <PageHeader
        title="Targets"
        description="Configured execution destinations and advertised capabilities."
        actions={
          <button className="button secondary" onClick={result.refresh}>
            ↻ Refresh
          </button>
        }
      />
      <p className="notice subtle">
        Configured “active” means enabled by policy. It does not establish that
        an agent is connected or healthy. Utilization and capacity monitoring
        come later.
      </p>
      <div className="page-meta">
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
      </div>
      {result.error && (
        <ErrorNotice error={result.error} retry={result.refresh} />
      )}
      <Completeness meta={result.data?.meta} />
      {!result.data && result.loading ? (
        <Spinner label="Loading target configuration" />
      ) : result.data?.data.length ? (
        <section className="panel">
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Target</th>
                  <th>Source / namespace</th>
                  <th>Configured state</th>
                  <th>Backend / provider</th>
                  <th>Generation</th>
                  <th>Capabilities</th>
                </tr>
              </thead>
              <tbody>
                {result.data.data.map((target) => (
                  <tr
                    key={`${target.deploymentId}:${target.namespaceId}:${target.targetId}`}
                  >
                    <td>
                      <strong>{target.name}</strong>
                      <span className="secondary-line mono">
                        {target.targetId}
                      </span>
                    </td>
                    <td>
                      {sourceLabel(
                        bootstrap.sources,
                        target.deploymentId,
                        target.namespaceId,
                      )}
                    </td>
                    <td>
                      <Status value={target.state} tone="neutral" />
                    </td>
                    <td>
                      {target.backend ?? "Unavailable"}
                      <span className="secondary-line">
                        {target.provider ?? "Unavailable"}
                      </span>
                    </td>
                    <td>{target.generation ?? "Unavailable"}</td>
                    <td>
                      {target.capabilities.join(", ") || "None advertised"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {result.data.meta.nextCursor && (
            <div className="pagination">
              <span>Up to 50 targets per page</span>
              <button
                className="button secondary"
                onClick={() =>
                  setSearch({ cursor: result.data!.meta.nextCursor! })
                }
              >
                Next page →
              </button>
            </div>
          )}
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
