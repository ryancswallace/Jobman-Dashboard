import { useCallback, useEffect, useRef, useState } from "react";
import { APIError, authorizationErrors, request } from "./transport";

const identityDecode = <T>(value: unknown) => value as T;
interface State<T> {
  key: string;
  data?: T;
  error?: APIError;
  loading: boolean;
  fetchedAt?: string;
}
/** One active read per view; identity changes hide old data before the cleanup effect runs. */
export function useResource<T>(
  path: string | null,
  identity: string,
  interval = 5000,
  decode: (value: unknown) => T = identityDecode,
) {
  const key = `${identity}:${path}`;
  const [state, setState] = useState<State<T>>({ key, loading: !!path });
  const retryRef = useRef<() => void>(() => {});
  useEffect(() => {
    let live = true,
      controller: AbortController | undefined,
      timer: ReturnType<typeof setTimeout> | undefined,
      failures = 0,
      stopped = false;
    const update = (partial: Partial<State<T>>) => {
      if (live)
        setState((previous) => ({
          ...(previous.key === key ? previous : { key, loading: true }),
          ...partial,
          key,
        }));
    };
    const schedule = () => {
      if (
        !live ||
        stopped ||
        interval === 0 ||
        document.visibilityState === "hidden"
      )
        return;
      timer = setTimeout(
        () => void load(),
        failures
          ? Math.min(60000, interval * 2 ** Math.min(failures, 5)) *
              (0.8 + Math.random() * 0.4)
          : interval,
      );
    };
    const load = async () => {
      if (!path || !live || controller) return;
      clearTimeout(timer);
      controller = new AbortController();
      update({ loading: true });
      try {
        const data = decode(
          await request<unknown>(path, { signal: controller.signal }),
        );
        if (live) {
          failures = 0;
          stopped = false;
          update({
            data,
            error: undefined,
            loading: false,
            fetchedAt: new Date().toISOString(),
          });
        }
      } catch (error) {
        if (
          !live ||
          (error instanceof DOMException && error.name === "AbortError")
        )
          return;
        const apiError =
          error instanceof APIError
            ? error
            : new APIError("request_failed", "The request failed.");
        failures++;
        if (
          authorizationErrors.has(apiError.code) ||
          apiError.status === 401 ||
          apiError.status === 403
        ) {
          stopped = true;
          update({ data: undefined, error: apiError, loading: false });
        } else update({ error: apiError, loading: false });
      } finally {
        controller = undefined;
        schedule();
      }
    };
    const onVisibility = () => {
      clearTimeout(timer);
      if (document.visibilityState === "visible") void load();
    };
    const onOnline = () => void load();
    retryRef.current = () => void load();
    setState({ key, loading: !!path });
    if (path) void load();
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("online", onOnline);
    return () => {
      live = false;
      controller?.abort();
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("online", onOnline);
    };
  }, [path, key, interval, decode]);
  const refresh = useCallback(() => retryRef.current(), []);
  return { ...(state.key === key ? state : { key, loading: !!path }), refresh };
}
