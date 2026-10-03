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
import { ErrorNotice, Spinner, Status } from "./States";
export function LogViewer({ job }: { job: JobRef }) {
  const { identity } = useSession();
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
    setError(undefined);
  }, [identity, path, stream]);
  useEffect(() => {
    let live = true,
      stopped = false,
      timer: ReturnType<typeof setTimeout> | undefined,
      controller: AbortController | undefined;
    const load = async () => {
      if (!live || controller || document.visibilityState === "hidden") return;
      controller = new AbortController();
      setLoading(true);
      try {
        const params = new URLSearchParams({ stream, limitBytes: "262144" });
        if (cursor.current) params.set("cursor", cursor.current);
        const chunk = await request<LogChunk>(`${path}/logs?${params}`, {
          signal: controller.signal,
        });
        if (live) {
          if (execution.current && chunk.executionId !== execution.current)
            throw new APIError(
              "stream_changed",
              "The execution changed. Reopen Logs to read a fresh tail.",
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
              "A byte range is missing. Reopen Logs to request a fresh tail.",
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
              "Log byte count does not match its declared offsets.",
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
          if (
            chunk.state === "complete" ||
            (!chunk.nextCursor && bytes.length > 0)
          )
            stopped = true;
        }
      } catch (reason) {
        if (
          live &&
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
        controller = undefined;
        if (live) {
          setLoading(false);
          if (following && !stopped)
            timer = setTimeout(() => void load(), 5000);
        }
      }
    };
    const manuallyRefreshed = requestedTick.current !== tick;
    requestedTick.current = tick;
    if (following || manuallyRefreshed) void load();
    const visible = () => {
      clearTimeout(timer);
      if (following && document.visibilityState === "visible" && !stopped)
        void load();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      live = false;
      clearTimeout(timer);
      controller?.abort();
      document.removeEventListener("visibilitychange", visible);
    };
  }, [identity, path, stream, following, tick]);
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
              {s}
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
          {following ? "Pause following" : "Resume following"}
        </button>
        <button className="button secondary" onClick={refresh}>
          Refresh
        </button>
      </div>
      {error && <ErrorNotice error={error} retry={refresh} />}
      {buffer.gap && (
        <p className="notice warning">
          The stream changed or a byte range is missing. Reopen Logs to request
          a new tail.
        </p>
      )}
      {buffer.evicted && (
        <p className="notice subtle">
          Earlier rendered text was removed to keep this view within 2 MiB.
        </p>
      )}
      {loading && !buffer.text ? (
        <Spinner label="Reading authorized log tail" />
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
              ? "This stream is complete and empty."
              : "No log bytes are currently available.")}
        </pre>
      )}
      <div className="log-footer">
        <Status value={state} />
        <span>
          Original byte range {buffer.startOffset ?? "—"}–
          {buffer.endOffset ?? "—"} · {following ? "Following" : "Paused"}
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
