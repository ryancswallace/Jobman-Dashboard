import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  useRef,
  type ReactNode,
} from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { Bootstrap, Scope } from "./models";
import { decodeBootstrap } from "./api";
import { useResource } from "./useResource";
import { setCSRFToken, request, APIError } from "./transport";
import { scopeKey } from "./format";
import { ConnectionScreen } from "../components/States";
interface Session {
  bootstrap: Bootstrap;
  scope: Scope;
  setScope: (scope: Scope) => void;
  identity: string;
  refreshBootstrap: () => void;
  logout: () => Promise<void>;
}
const Context = createContext<Session | null>(null);
export function SessionProvider({ children }: { children: ReactNode }) {
  const [signedOut, setSignedOut] = useState(() => {
    try {
      return window.localStorage.getItem("jobman.signoutPending") === "1";
    } catch {
      return false;
    }
  });
  const [logoutError, setLogoutError] = useState<{
    code: string;
    message: string;
  }>(() => ({
    code: "logout_pending",
    message:
      "Sign-out has not yet been confirmed by the server. Private views remain cleared. Reconnect and try again.",
  }));
  const logoutCSRF = useRef("");
  const bootstrap = useResource<Bootstrap>(
    signedOut ? null : "/api/v1/bootstrap",
    "session",
    5000,
    decodeBootstrap,
  );
  const location = useLocation(),
    navigate = useNavigate();
  const [clock, setClock] = useState(Date.now());
  const [selectedScope, setSelectedScope] = useState<Scope | null>(null);
  const data = bootstrap.data;
  useEffect(() => {
    setCSRFToken(data?.csrfToken);
    return () => setCSRFToken();
  }, [data?.csrfToken]);
  useEffect(() => {
    const update = () => setClock(Date.now()),
      timer = setInterval(update, 1000);
    document.addEventListener("visibilitychange", update);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
    };
  }, []);
  useEffect(() => {
    document.documentElement.dataset.appearance =
      data?.preferences.appearance ?? "system";
  }, [data?.preferences.appearance]);
  // Expiry removes only stale-authority scopes; a failed source cannot hide healthy sources.
  const available = (data?.sources ?? [])
    .map((source) => ({
      ...source,
      namespaces: source.namespaces.filter((namespace) => {
        const until = namespace.authorizationExpiresAt
          ? Date.parse(namespace.authorizationExpiresAt)
          : Date.parse(data!.authorizationCheckedAt) + 120000;
        return Number.isFinite(until) && until > clock;
      }),
    }))
    .filter((source) => source.namespaces.length > 0);
  const hadNamespaces = !!data?.sources.some((s) => s.namespaces.length > 0);
  const expired = hadNamespaces && available.length === 0;
  const availableKey = JSON.stringify(
    available.map((s) => [
      s.deploymentId,
      s.namespaces.map((n) => [n.namespaceId, n.authorizationVersion]),
    ]),
  );
  const scope = useMemo(() => {
    const params = new URLSearchParams(location.search);
    const requestedSources = params.get("sources")?.split(",").filter(Boolean);
    const route = location.pathname.match(
      /^\/deployments\/([^/]+)\/namespaces\/([^/]+)\//,
    );
    const resourceScope = route
      ? {
          deployments: [decodeRouteSegment(route[1])],
          namespace: {
            deploymentId: decodeRouteSegment(route[1]),
            namespaceId: decodeRouteSegment(route[2]),
          },
        }
      : null;
    const selected = requestedSources
      ? {
          deployments: requestedSources,
          namespace:
            params.get("namespace") && requestedSources.length === 1
              ? {
                  deploymentId: requestedSources[0],
                  namespaceId: params.get("namespace")!,
                }
              : undefined,
        }
      : (resourceScope ?? selectedScope);
    // Keep an explicit removed/invalid scope; never broaden it to remaining grants.
    const deployments =
      selected?.deployments ?? available.map((s) => s.deploymentId);
    const namespace = selected?.namespace;
    return { deployments, namespace };
  }, [location.search, location.pathname, selectedScope, availableKey]);
  const logout = async () => {
    logoutCSRF.current ||= data?.csrfToken ?? "";
    setSignedOut(true);
    setCSRFToken();
    try {
      window.localStorage.setItem("jobman.signoutPending", "1");
    } catch {
      /* The in-memory sign-out hold still applies. */
    }
    setLogoutError({
      code: "logout_pending",
      message: "Signing out. Your private views have been cleared.",
    });
    try {
      if (!logoutCSRF.current) {
        try {
          const current = await request<{ csrfToken?: string }>(
            "/api/v1/bootstrap",
          );
          logoutCSRF.current = current.csrfToken ?? "";
        } catch (error) {
          if (!(error instanceof APIError && error.status === 401)) throw error;
        }
      }
      const response = await fetch("/auth/logout", {
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        redirect: "error",
        headers: { "X-CSRF-Token": logoutCSRF.current },
      });
      if (!response.ok && response.status !== 401)
        throw new Error("Logout failed");
      logoutCSRF.current = "";
      try {
        window.localStorage.removeItem("jobman.signoutPending");
      } catch {
        /* No persisted state available. */
      }
      setLogoutError({
        code: "unauthenticated",
        message: "Signed out. Your private views have been cleared.",
      });
    } catch {
      setLogoutError({
        code: "logout_pending",
        message:
          "The server could not confirm sign-out. Private views remain cleared. Reconnect and try again; the server session remains active until sign-out succeeds.",
      });
    }
  };
  if (signedOut)
    return (
      <ConnectionScreen
        loading={false}
        error={logoutError}
        onRetry={() => void logout()}
      />
    );
  if (!data || expired)
    return (
      <ConnectionScreen
        loading={bootstrap.loading && !expired}
        error={
          expired
            ? {
                code: "authorization_unavailable",
                message:
                  "Current access could not be verified. Sensitive views have been cleared until Dashboard can verify your access again.",
              }
            : bootstrap.error
        }
        onRetry={bootstrap.refresh}
      />
    );
  const identity = `${data.account.id}:${availableKey}:${scopeKey(scope)}`;
  const setScope = (next: Scope) => {
    setSelectedScope(next);
    const query = new URLSearchParams();
    query.set("sources", next.deployments.join(","));
    if (next.namespace) query.set("namespace", next.namespace.namespaceId);
    navigate(`/?${query}`);
  };
  return (
    <Context.Provider
      value={{
        bootstrap: { ...data, sources: available },
        scope,
        setScope,
        identity,
        refreshBootstrap: bootstrap.refresh,
        logout,
      }}
    >
      {children}
    </Context.Provider>
  );
}
export function useSession() {
  const value = useContext(Context);
  if (!value) throw new Error("SessionProvider is required");
  return value;
}

function decodeRouteSegment(value: string) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}
