import { useEffect, useRef } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useSession } from "../lib/session";
import { sourceLabel } from "../lib/format";
import type { Scope } from "../lib/models";
const navigation = [
  ["/", "Overview", "◫"],
  ["/jobs", "Jobs", "☷"],
  ["/workloads", "Workloads", "▦"],
  ["/targets", "Targets", "◎"],
  ["/inbox", "Inbox", "▱"],
  ["/alerts", "Alert rules", "♧"],
  ["/settings", "Settings", "⚙"],
];
export function Layout() {
  const { bootstrap, scope, setScope, identity } = useSession(),
    location = useLocation(),
    navigate = useNavigate();
  const main = useRef<HTMLElement>(null),
    previous = useRef(location.pathname);
  useEffect(() => {
    if (previous.current !== location.pathname) {
      main.current?.querySelector<HTMLElement>("h1")?.focus();
      previous.current = location.pathname;
    }
  }, [location.pathname]);
  const scopeTitle = scope.namespace
    ? sourceLabel(
        bootstrap.sources,
        scope.namespace.deploymentId,
        scope.namespace.namespaceId,
      )
    : scope.deployments.length === bootstrap.sources.length
      ? "All authorized namespaces"
      : scope.deployments.length === 1
        ? sourceLabel(bootstrap.sources, scope.deployments[0])
        : `${scope.deployments.length} deployments`;
  const scopeSearch = new URLSearchParams({
    sources: scope.deployments.join(","),
  });
  if (scope.namespace)
    scopeSearch.set("namespace", scope.namespace.namespaceId);
  const select = (next: Scope) => {
    setScope(next);
    document
      .querySelector<HTMLDetailsElement>(".scope-picker")
      ?.removeAttribute("open");
  };
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <NavLink
          to="/"
          className="brand"
          aria-label="Jobman Dashboard overview"
        >
          <img className="brand-light" src="/logo.svg" alt="Jobman Dashboard" />
          <img
            className="brand-dark"
            src="/logo-transparent-dashboard.svg"
            alt="Jobman Dashboard"
          />
        </NavLink>
        <p className="nav-label">WORKSPACE</p>
        <nav aria-label="Main navigation">
          {navigation.map(([path, label, icon]) => (
            <NavLink
              key={path}
              end={path === "/"}
              to={`${path}?${scopeSearch}`}
              className={({ isActive }) =>
                `nav-item ${isActive ? "active" : ""}`
              }
            >
              <span aria-hidden="true" className="nav-icon">
                {icon}
              </span>
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="connection-dot" aria-hidden="true" />
          <span>
            Private workspace
            <br />
            <small>Monitoring only</small>
          </span>
        </div>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <details className="scope-picker">
            <summary>
              <span className="scope-icon" aria-hidden="true">
                ▦
              </span>
              <span>
                <small>MONITORING SCOPE</small>
                <strong>{scopeTitle}</strong>
              </span>
              <span aria-hidden="true">⌄</span>
            </summary>
            <div className="scope-menu">
              <h2>Choose your scope</h2>
              <button
                className="scope-option"
                onClick={() =>
                  select({
                    deployments: bootstrap.sources.map((s) => s.deploymentId),
                  })
                }
              >
                All authorized namespaces
              </button>
              <p className="muted small">
                Combine deployments, or open one namespace.
              </p>
              {bootstrap.sources.map((source) => (
                <div className="scope-source" key={source.deploymentId}>
                  <label>
                    <input
                      type="checkbox"
                      checked={scope.deployments.includes(source.deploymentId)}
                      onChange={(e) => {
                        const ids = e.target.checked
                          ? [...scope.deployments, source.deploymentId]
                          : scope.deployments.filter(
                              (id) => id !== source.deploymentId,
                            );
                        if (ids.length)
                          setScope({ deployments: [...new Set(ids)] });
                      }}
                    />
                    {source.displayName}
                  </label>
                  {source.namespaces.map((ns) => (
                    <button
                      key={ns.namespaceId}
                      className="scope-option namespace-option"
                      onClick={() =>
                        select({
                          deployments: [source.deploymentId],
                          namespace: {
                            deploymentId: source.deploymentId,
                            namespaceId: ns.namespaceId,
                          },
                        })
                      }
                    >
                      {ns.name}
                      <span>{ns.roles.join(" + ")}</span>
                    </button>
                  ))}
                </div>
              ))}
            </div>
          </details>
          <div className="account-menu">
            <button
              className="account-button"
              onClick={() => navigate("/settings")}
              aria-label={`Account settings for ${bootstrap.account.displayName}`}
            >
              <span className="avatar" aria-hidden="true">
                {bootstrap.account.displayName.slice(0, 1).toUpperCase()}
              </span>
              <span>{bootstrap.account.displayName}</span>
            </button>
          </div>
        </header>
        {bootstrap.mode && bootstrap.mode !== "production" && (
          <div className="development-banner" role="status">
            {bootstrap.mode === "fixture"
              ? "Development fixture environment · synthetic data · not a live Control deployment"
              : `Environment: ${bootstrap.mode}`}
          </div>
        )}
        {bootstrap.completeness === "partial" && (
          <div className="development-banner" role="status">
            Some Control connections could not verify current access. Available
            scopes remain usable; aggregate coverage may be incomplete.
          </div>
        )}
        <main ref={main} id="main" className="main-content">
          <Outlet key={identity} />
        </main>
        <footer className="workspace-footer">
          Jobman Dashboard
          <span>Source-reported facts. Current namespace access.</span>
        </footer>
      </div>
    </div>
  );
}
