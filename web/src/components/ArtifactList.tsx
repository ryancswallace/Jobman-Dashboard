import { useMemo } from "react";
import { APIError } from "../lib/transport";
import { useSearchParams } from "react-router-dom";
import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { decodeMeta } from "../lib/api";
import { useResource } from "../lib/useResource";
import { useSession } from "../lib/session";
import { count, timestamp } from "../lib/format";
import { Completeness, Empty, ErrorNotice, Freshness, Spinner } from "./States";

const decodeArtifacts = (value: unknown) => {
  const dto = value as Wire.ArtifactPage;
  return {
    data: { items: dto.items, total: dto.total },
    meta: decodeMeta(dto),
  };
};
export function ArtifactList({
  path,
  run: selectedRun,
}: {
  path: string;
  run?: Wire.JobRun;
}) {
  const { identity, bootstrap } = useSession();
  const [search, setSearch] = useSearchParams();
  const cursor = search.get("artifactCursor");
  const run = selectedRun?.number ?? search.get("artifactRun");
  const decode = useMemo(
    () => (value: unknown) => {
      const out = decodeArtifacts(value);
      if (
        selectedRun &&
        out.data.items.some(
          (item) =>
            item.runId !== selectedRun.id ||
            item.runNumber !== selectedRun.number ||
            item.executionId !== selectedRun.executionId,
        )
      )
        throw new APIError(
          "invalid_response",
          "Artifact metadata does not match the selected run.",
        );
      return out;
    },
    [selectedRun?.id, selectedRun?.number, selectedRun?.executionId],
  );
  const result = useResource(
    `${path}/artifacts?${new URLSearchParams({ limit: "50", ...(cursor ? { cursor } : {}), ...(run ? { runNumber: run } : {}) })}`,
    identity,
    0,
    decode,
  );
  const firstPage = () => {
    if (cursor) {
      const q = new URLSearchParams(search);
      q.delete("artifactCursor");
      setSearch(q);
    } else result.refresh();
  };
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>Published artifacts</h2>
          <p>Metadata only. File availability has not been checked.</p>
        </div>
        <button className="button secondary" onClick={firstPage}>
          Refresh artifacts
        </button>
      </div>
      <div className="panel-body">
        <label>
          Actual run number{" "}
          <input
            aria-label="Artifact run number"
            type="text"
            inputMode="numeric"
            pattern="[1-9][0-9]*"
            placeholder="All runs"
            value={run ?? ""}
            readOnly={!!selectedRun}
            onChange={(event) => {
              const q = new URLSearchParams(search);
              q.delete("artifactCursor");
              if (event.target.value) q.set("artifactRun", event.target.value);
              else q.delete("artifactRun");
              setSearch(q, { replace: true });
            }}
          />
        </label>
      </div>
      <Freshness
        fetchedAt={result.fetchedAt}
        loading={result.loading}
        error={!!result.error}
      />
      <Completeness meta={result.data?.meta} />
      {result.error && <ErrorNotice error={result.error} retry={firstPage} />}
      {!result.data && result.loading ? (
        <Spinner label="Loading artifact metadata" />
      ) : (
        result.data && (
          <>
            {result.data.data.items.length ? (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Name / execution</th>
                      <th>Run</th>
                      <th>Size</th>
                      <th>Checksum</th>
                      <th>Published</th>
                      <th>Availability</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.data.data.items.map((a) => (
                      <tr key={a.id}>
                        <td>
                          {a.name}
                          <span className="secondary-line mono">
                            {a.executionId}
                          </span>
                        </td>
                        <td>{a.runNumber}</td>
                        <td>{count(a.sizeBytes)} bytes</td>
                        <td className="mono">{a.checksum}</td>
                        <td>
                          {timestamp(
                            a.publishedAt,
                            bootstrap.preferences.timezone,
                          )}
                        </td>
                        <td>
                          {a.availability === "metadata_only"
                            ? "Published metadata; bytes unverified"
                            : a.availability}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <Empty title="No artifact metadata">
                No artifacts have been published for this job
                {run ? ` in run ${run}` : ""}.
              </Empty>
            )}
            <div className="pagination">
              <span>
                {result.data.data.items.length} shown of{" "}
                {count(result.data.data.total)} published artifacts
              </span>
              <div>
                {cursor && (
                  <button className="button secondary" onClick={firstPage}>
                    First artifact page
                  </button>
                )}
                {result.data.meta.nextCursor && (
                  <button
                    className="button secondary"
                    onClick={() => {
                      const q = new URLSearchParams(search);
                      q.set("artifactCursor", result.data!.meta.nextCursor!);
                      setSearch(q);
                    }}
                  >
                    Next artifact page →
                  </button>
                )}
              </div>
            </div>
          </>
        )
      )}
    </section>
  );
}
