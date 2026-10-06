import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { useEffect, useRef, useState } from "react";
import type { JobRef, LogChunk } from "../lib/models";
import {
  APIError,
  authorizationErrors,
  request,
  resourcePath,
} from "../lib/transport";
import { appendLog, type LogBuffer } from "../lib/logs";
import { useSession } from "../lib/session";
import { ErrorNotice, Freshness, Spinner, Status } from "./States";
export function LogViewer({ job, run }: { job: JobRef; run?: Wire.JobRun }) {
  const { identity } = useSession();
  const [fetchedAt, setFetchedAt] = useState<string>();
  const [capturedAt, setCapturedAt] = useState<string>();
  const [stream, setStream] = useState("stdout"),
    [following, setFollowing] = useState(true),
    [search, setSearch] = useState(""),
    [tick, setTick] = useState(0);
  const [buffer, setBuffer] = useState<LogBuffer>({
      text: "",
      evicted: false,
      gap: false,
    }),
    [state, setState] = useState("loading"),
    [error, setError] = useState<APIError>(),
    [loading, setLoading] = useState(true);
  const cursor = useRef<string | undefined>(undefined),
    view = useRef<HTMLPreElement>(null),
    decoder = useRef(new TextDecoder()),
    requestedTick = useRef<number | undefined>(undefined),
    end = useRef<string | undefined>(undefined),
    execution = useRef<string | undefined>(undefined);
  const path = resourcePath(job, "jobs", job.jobId);
  const refresh = () => {
    if (
      error &&
      ["cursor_expired", "stream_changed", "log_gap"].includes(error.code)
    ) {
      cursor.current = undefined;
      end.current = undefined;
      execution.current = undefined;
      decoder.current = new TextDecoder();
      setBuffer({ text: "", evicted: false, gap: false });
    }
    setTick((value) => value + 1);
  };
  useEffect(() => {
    setBuffer({ text: "", evicted: false, gap: false });
    cursor.current = undefined;
    end.current = undefined;
    execution.current = undefined;
    decoder.current = new TextDecoder();
    requestedTick.current = undefined;
    setState("loading");
    setFetchedAt(undefined);
    setCapturedAt(undefined);
    setError(undefined);
  }, [identity, path, stream, run?.id]);
  useEffect(() => {
    let live = true,
      stopped = false,
      scene = 0,
      timer: ReturnType<typeof setTimeout> | undefined,
      controller: AbortController | undefined;
    const load = async () => {
      if (!live || controller || document.visibilityState === "hidden") return;
      controller = new AbortController();
      const readController = controller,
        readScene = scene;
      setLoading(true);
      try {
        const params = new URLSearchParams({ stream, limitBytes: "262144" });
        if (cursor.current) params.set("cursor", cursor.current);
        if (run) params.set("runNumber", run.number);
        const chunk = await request<LogChunk>(`${path}/logs?${params}`, {
          signal: readController.signal,
        });
        if (live && readScene === scene) {
          if (
            run &&
            (chunk.runId !== run.id ||
              chunk.runNumber !== run.number ||
              chunk.executionId !== (run.executionId ?? ""))
          )
            throw new APIError(
              "stream_changed",
              "The response no longer matches the selected run. Refresh the run list and select the run again.",
            );
          if (execution.current && chunk.executionId !== execution.current)
            throw new APIError(
              "stream_changed",
              "The job’s execution changed. Refresh to load its latest log output.",
            );
          if (
            end.current &&
            BigInt(chunk.endOffset) <= BigInt(end.current) &&
            !(
              chunk.startOffset === end.current &&
              chunk.endOffset === end.current
            )
          ) {
            cursor.current = chunk.nextCursor;
            return;
          }
          if (end.current && chunk.startOffset !== end.current)
            throw new APIError(
              "log_gap",
              "Part of the log is missing. Refresh to load the latest output.",
            );
          const raw = atob(chunk.bytesBase64),
            bytes = Uint8Array.from(raw, (c) => c.charCodeAt(0));
          if (
            bytes.length > 262144 ||
            BigInt(chunk.endOffset) - BigInt(chunk.startOffset) !==
              BigInt(bytes.length)
          )
            throw new APIError(
              "invalid_response",
              "The log response is inconsistent and cannot be displayed. Try refreshing.",
            );
          const text = decoder.current.decode(bytes, {
            stream: chunk.state !== "complete",
          });
          setBuffer((previous) => appendLog(previous, { ...chunk, text }));
          cursor.current = chunk.nextCursor;
          end.current = chunk.endOffset;
          execution.current = chunk.executionId;
          setState(chunk.truncated ? "truncated" : chunk.state);
          setError(undefined);
          setFetchedAt(new Date().toISOString());
          setCapturedAt(
            chunk.capturedAt && Number.isFinite(Date.parse(chunk.capturedAt))
              ? chunk.capturedAt
              : undefined,
          );
          if (
            chunk.state === "complete" ||
            (!chunk.nextCursor && bytes.length > 0)
          )
            stopped = true;
        }
      } catch (reason) {
        if (
          live &&
          readScene === scene &&
          !(reason instanceof DOMException && reason.name === "AbortError")
        ) {
          const e =
            reason instanceof APIError
              ? reason
              : new APIError(
                  "invalid_response",
                  "Log content could not be read safely.",
                );
          setError(e);
          stopped = true;
          if (authorizationErrors.has(e.code))
            setBuffer({ text: "", evicted: false, gap: false });
        }
      } finally {
        if (controller === readController) controller = undefined;
        if (live && readScene === scene) {
          setLoading(false);
          if (following && !stopped)
            timer = setTimeout(() => void load(), 5000);
        }
      }
    };
    const manuallyRefreshed = requestedTick.current !== tick;
    requestedTick.current = tick;
    if (following || manuallyRefreshed) void load();
    const reconnect = () => {
      scene++;
      clearTimeout(timer);
      controller?.abort();
      controller = undefined;
      setFollowing(false);
      setLoading(false);
      if (document.visibilityState !== "visible") return;
      // A resumed scene obtains a fresh authorized tail even when following
      // was paused or the old stream completed. Never append a pre-hide reply.
      cursor.current = undefined;
      end.current = undefined;
      execution.current = undefined;
      decoder.current = new TextDecoder();
      setBuffer({ text: "", evicted: false, gap: false });
      setCapturedAt(undefined);
      setError(undefined);
      setState("loading");
      setTick((value) => value + 1);
    };
    document.addEventListener("visibilitychange", reconnect);
    window.addEventListener("online", reconnect);
    return () => {
      live = false;
      clearTimeout(timer);
      controller?.abort();
      document.removeEventListener("visibilitychange", reconnect);
      window.removeEventListener("online", reconnect);
    };
  }, [
    identity,
    path,
    stream,
    following,
    tick,
    run?.id,
    run?.number,
    run?.executionId,
  ]);
  useEffect(() => {
    if (following && view.current)
      view.current.scrollTop = view.current.scrollHeight;
  }, [buffer.text, following]);
  const matches = search
    ? buffer.text.toLocaleLowerCase().split(search.toLocaleLowerCase()).length -
      1
    : 0;
  return (
    <section className="panel log-panel">
      <div className="log-toolbar">
        <div className="segmented" aria-label="Log stream">
          {["stdout", "stderr"].map((s) => (
            <button
              key={s}
              aria-pressed={stream === s}
              className={stream === s ? "selected" : ""}
              onClick={() => setStream(s)}
            >
              {s === "stdout" ? "Output (stdout)" : "Errors (stderr)"}
            </button>
          ))}
        </div>
        <label className="log-search">
          <span className="sr-only">Search loaded log text</span>
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search loaded text"
          />
        </label>
        <button
          className="button secondary"
          onClick={() => setFollowing((v) => !v)}
        >
          {following ? "Pause live updates" : "Resume live updates"}
        </button>
        <button className="button secondary" onClick={refresh}>
          Refresh
        </button>
      </div>
      {error && <ErrorNotice error={error} retry={refresh} />}
      {buffer.gap && (
        <p className="notice warning">
          The log changed or part of it is missing. Reopen Logs to load the
          latest output.
        </p>
      )}
      {buffer.evicted && (
        <p className="notice subtle">
          Earlier lines have been removed from this view to keep it responsive.
          This view shows up to 2 MiB of text.
        </p>
      )}
      {loading && !buffer.text ? (
        <Spinner label="Loading recent log output" />
      ) : (
        <pre
          ref={view}
          className="log-output"
          tabIndex={0}
          aria-label={`${stream} log output`}
          onScroll={() => {
            const element = view.current;
            if (
              element &&
              element.scrollHeight - element.scrollTop - element.clientHeight >
                40
            )
              setFollowing(false);
          }}
        >
          {buffer.text ||
            (state === "complete"
              ? "This log is complete and contains no output."
              : "No log text is currently loaded.")}
        </pre>
      )}
      <div className="log-footer">
        <Status value={state} />
        <Freshness fetchedAt={fetchedAt} loading={loading} error={!!error} />
        {fetchedAt && (
          <time dateTime={fetchedAt} title="Last successful log refresh">
            {fetchedAt}
          </time>
        )}
        <span>
          Log captured:{" "}
          {capturedAt ? (
            <time dateTime={capturedAt}>{capturedAt}</time>
          ) : (
            "Unavailable"
          )}
        </span>
        <span>
          Original byte range {buffer.startOffset ?? "—"}–
          {buffer.endOffset ?? "—"} · {following ? "Live updates on" : "Paused"}
        </span>
        <span aria-live="polite">
          {search
            ? `${matches} matches in loaded text`
            : "Search covers loaded text only"}
        </span>
      </div>
    </section>
  );
}
