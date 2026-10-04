import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useSession } from "../lib/session";
import { request, APIError, authorizationErrors } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { sourceLabel, timestamp, title } from "../lib/format";
import type { JobRef } from "../lib/models";
import {
  canonicalID,
  decodeRule,
  decodeRulePage,
  editableRule,
  hiddenReferences,
  namespaceKey,
  type Rule,
} from "../lib/rules";
import {
  Empty,
  ErrorNotice,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
import { RuleEditor } from "../components/RuleEditor";
import "./Alerts.css";

export function AlertsPage() {
  const { identity } = useSession();
  return <AlertWorkspace key={identity} />;
}
function AlertWorkspace() {
  const { identity, bootstrap } = useSession();
  const location = useLocation(),
    navigate = useNavigate();
  const initial = location.state?.watchJob as JobRef | undefined;
  const initialJob =
    initial &&
    canonicalID(initial.deploymentId) &&
    canonicalID(initial.namespaceId) &&
    canonicalID(initial.jobId) &&
    bootstrap.sources.some(
      (source) =>
        source.deploymentId === initial.deploymentId &&
        source.namespaces.some((ns) => ns.namespaceId === initial.namespaceId),
    )
      ? initial
      : undefined;
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.at(-1);
  const rules = useResource(
    `/api/v1/rules?${new URLSearchParams({ limit: "20", ...(cursor ? { cursor } : {}) })}`,
    identity,
    bootstrap.preferences.refreshSeconds * 1000,
    decodeRulePage,
  );
  const [edit, setEdit] = useState<{ rule?: Rule; job?: JobRef } | undefined>(
    () => (initialJob ? { job: initialJob } : undefined),
  );
  const [error, setError] = useState<APIError>();
  const [clearedData, setClearedData] = useState<unknown>();
  const [busy, setBusy] = useState<string>();
  const [notice, setNotice] = useState("");
  const [deleteID, setDeleteID] = useState<string>();
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    if (rules.error) {
      setEdit(undefined);
      setDeleteID(undefined);
    }
  }, [rules.error]);
  const refresh = () => {
    setError(undefined);
    setDeleteID(undefined);
    if (cursors.length) setCursors([]);
    else rules.refresh();
  };
  const closeEditor = () => {
    setEdit(undefined);
    navigate(location.pathname + location.search, {
      replace: true,
      state: null,
    });
    refresh();
  };
  const action = async (
    rule: Rule,
    mode: "enabled" | "revalidate" | "delete",
  ) => {
    if (controller.current) return;
    const active = new AbortController();
    controller.current = active;
    setBusy(rule.id);
    setError(undefined);
    setNotice("");
    try {
      const value = await request<unknown>(
        `/api/v1/rules/${encodeURIComponent(rule.id)}${mode === "delete" ? "" : `/${mode}`}`,
        {
          method:
            mode === "enabled" ? "PUT" : mode === "delete" ? "DELETE" : "POST",
          body: mode === "enabled" ? { enabled: !rule.enabled } : undefined,
          revision: rule.revision,
          signal: active.signal,
        },
      );
      if (mode !== "delete" && decodeRule(value).id !== rule.id)
        throw new APIError(
          "invalid_response",
          "The updated rule identity did not match the selected rule.",
        );
      if (!active.signal.aborted) {
        setNotice(
          mode === "delete"
            ? "Rule deleted."
            : mode === "enabled"
              ? rule.enabled
                ? "Rule stopped. Pending matches for this rule are no longer eligible for delivery."
                : "Rule enabled. Check each namespace’s activation state below."
              : "Revalidation completed. Check each namespace’s current activation state below.",
        );
        refresh();
      }
    } catch (failure) {
      if (!active.signal.aborted) {
        const apiError =
          failure instanceof APIError
            ? failure
            : new APIError(
                "request_failed",
                "The rule could not be updated. Refresh the list to check its current state.",
              );
        setError(apiError);
        if (
          authorizationErrors.has(apiError.code) ||
          apiError.status === 401 ||
          apiError.status === 403
        ) {
          setClearedData(rules.data);
          setEdit(undefined);
        }
      }
    } finally {
      if (!active.signal.aborted) setBusy(undefined);
      controller.current = undefined;
    }
  };
  const privateFailure =
    error &&
    (authorizationErrors.has(error.code) ||
      error.status === 401 ||
      error.status === 403);
  const page =
    rules.error || privateFailure || (clearedData && clearedData === rules.data)
      ? undefined
      : rules.data;
  const currentEditing =
    edit?.rule && page?.items.find((rule) => rule.id === edit.rule!.id);
  const editStillVisible =
    !edit?.rule || !page || (!!currentEditing && editableRule(currentEditing));
  return (
    <>
      <PageHeader
        title="Alert rules"
        description="Choose which job results to follow. Each rule has explicit namespace selections and its own activation state."
        actions={
          <button
            className="button"
            disabled={!!edit || !!busy}
            onClick={() => {
              setEdit({});
              setNotice("");
            }}
          >
            + New alert rule
          </button>
        }
      />
      {(error || rules.error) && (
        <ErrorNotice error={(error || rules.error)!} retry={refresh} />
      )}
      {notice && (
        <p className="notice subtle" role="status">
          {notice}
        </p>
      )}
      <p className="notice subtle">
        Rules are personal and do not grant access. Monitoring begins when a
        namespace’s activation is active; pending or unavailable sources may
        delay it. iPhone delivery is configured separately.
      </p>
      {edit && !rules.error && !privateFailure && editStillVisible && (
        <RuleEditor
          key={edit.rule?.id ?? JSON.stringify(edit.job) ?? "new"}
          rule={edit.rule}
          job={edit.job}
          onClose={closeEditor}
          onSaved={() => {
            setNotice(
              "Rule saved. Check its namespace activation states below.",
            );
            closeEditor();
          }}
        />
      )}
      {edit && !editStillVisible && (
        <p className="notice warning" role="status">
          This rule’s selections changed or are no longer visible. Close the
          editor and refresh before editing.
          <button className="button secondary" onClick={closeEditor}>
            Close and refresh rules
          </button>
        </p>
      )}
      <div className="page-meta">
        <span>
          {page
            ? `${page.items.length} rules on this page`
            : "Personal alert rules"}
        </span>
        <button className="text-button" onClick={refresh}>
          Refresh rules
        </button>
      </div>
      {rules.loading && !page ? (
        <Spinner label="Loading alert rules" />
      ) : page?.items.length ? (
        <div className="card-list">
          {page.items.map((rule) => (
            <article
              className="panel rule-card"
              key={rule.id}
              aria-labelledby={`rule-${rule.id}`}
            >
              <div className="rule-heading">
                <div>
                  <h2 id={`rule-${rule.id}`}>{rule.name}</h2>
                  <p>
                    {rule.scope === "watched_jobs"
                      ? "Specific watched jobs"
                      : rule.scope === "my_jobs"
                        ? "All my jobs"
                        : rule.scope === "namespace_jobs"
                          ? "All jobs in selected namespaces"
                          : title(rule.scope)}{" "}
                    ·{" "}
                    {rule.outcomeMode === "all_terminal"
                      ? "All terminal outcomes, including future outcomes"
                      : rule.outcomes.map(title).join(", ") ||
                        title(rule.outcomeMode)}
                  </p>
                </div>
                <Status
                  value={rule.enabled ? "enabled" : "disabled"}
                  tone={rule.enabled ? "good" : "neutral"}
                />
              </div>
              <dl className="rule-state-summary">
                <div>
                  <dt>Visible namespaces</dt>
                  <dd>{rule.scopes.length}</dd>
                </div>
                {rule.scope === "watched_jobs" && (
                  <div>
                    <dt>Visible watched jobs</dt>
                    <dd>{rule.jobs.length}</dd>
                  </div>
                )}
                <div>
                  <dt>Updated</dt>
                  <dd>
                    {timestamp(rule.updatedAt, bootstrap.preferences.timezone)}
                  </dd>
                </div>
              </dl>
              {hiddenReferences(rule) && (
                <p className="notice warning">
                  {rule.inaccessibleScopes > 0 && (
                    <span>
                      {rule.inaccessibleScopes} inaccessible namespace
                      {rule.inaccessibleScopes === 1 ? "" : "s"}.{" "}
                    </span>
                  )}
                  {rule.unavailableScopes > 0 && (
                    <span>
                      {rule.unavailableScopes} unavailable namespace
                      {rule.unavailableScopes === 1 ? "" : "s"}.{" "}
                    </span>
                  )}
                  References are hidden until current access can be verified.
                  Stopping the rule remains available.
                </p>
              )}
              <ul className="rule-activation-list">
                {rule.scopes.map((scope) => (
                  <li key={namespaceKey(scope)}>
                    <span>
                      {sourceLabel(
                        bootstrap.sources,
                        scope.deploymentId,
                        scope.namespaceId,
                      )}
                      {scope.activatedAt && (
                        <small>
                          Monitoring since{" "}
                          {timestamp(
                            scope.activatedAt,
                            bootstrap.preferences.timezone,
                          )}
                        </small>
                      )}
                    </span>
                    <Status value={scope.status} />
                  </li>
                ))}
              </ul>
              {rule.jobs.length > 0 && (
                <details className="rule-visible-jobs">
                  <summary>Selected watched jobs ({rule.jobs.length})</summary>
                  <ul>
                    {rule.jobs.map((job) => (
                      <li key={JSON.stringify(job)}>
                        {sourceLabel(
                          bootstrap.sources,
                          job.deploymentId,
                          job.namespaceId,
                        )}
                        <code>{job.jobId}</code>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
              {!editableRule(rule) && !hiddenReferences(rule) && (
                <p className="muted small">
                  This rule contains values this client cannot edit. You can
                  still stop or delete it.
                </p>
              )}
              <div className="rule-actions">
                <button
                  className="button secondary"
                  disabled={!!edit || !!busy || !editableRule(rule)}
                  onClick={() => setEdit({ rule })}
                >
                  Edit rule
                </button>
                <button
                  className="button secondary"
                  disabled={!!busy}
                  onClick={() => void action(rule, "enabled")}
                >
                  {busy === rule.id
                    ? "Updating…"
                    : rule.enabled
                      ? "Stop rule"
                      : "Enable rule"}
                </button>
                <button
                  className="text-button"
                  disabled={!!busy || !rule.enabled}
                  onClick={() => void action(rule, "revalidate")}
                >
                  Revalidate access
                </button>
                <button
                  className="text-button"
                  disabled={!!busy}
                  onClick={() => setDeleteID(rule.id)}
                >
                  Delete rule
                </button>
              </div>
              {rule.enabled &&
                (hiddenReferences(rule) ||
                  rule.scopes.some(
                    (scope) => scope.status === "inaccessible",
                  )) && (
                  <p className="panel-note">
                    Revalidate access explicitly resumes eligible scopes from a
                    new source checkpoint. Restored group membership alone does
                    not resume revoked monitoring.
                  </p>
                )}
              {deleteID === rule.id && (
                <div
                  className="notice warning"
                  role="group"
                  aria-label={`Delete ${rule.name}`}
                >
                  <p>
                    Delete this personal rule? Future matches from it will stop.
                    Existing inbox history is retained.
                  </p>
                  <div>
                    <button
                      className="button secondary"
                      disabled={!!busy}
                      onClick={() => void action(rule, "delete")}
                    >
                      Confirm delete
                    </button>
                    <button
                      className="text-button"
                      onClick={() => setDeleteID(undefined)}
                    >
                      Keep rule
                    </button>
                  </div>
                </div>
              )}
            </article>
          ))}
        </div>
      ) : (
        page && (
          <Empty
            title={cursor ? "No rules on this page" : "No alert rules yet"}
          >
            Watch individual jobs, your own jobs, or everyone’s jobs in selected
            namespaces. Select the outcomes you want to follow.
          </Empty>
        )
      )}
      {page && (
        <div className="pagination">
          <span>Up to 20 rules per page</span>
          <div>
            <button
              className="button secondary"
              disabled={!!edit || !cursors.length || !!busy}
              onClick={() => {
                setEdit(undefined);
                setCursors((prior) => prior.slice(0, -1));
              }}
            >
              Previous rules
            </button>
            <button
              className="button secondary"
              disabled={
                !!edit || !page.nextCursor || !!busy || cursors.length >= 5
              }
              onClick={() => {
                setEdit(undefined);
                setCursors((prior) => [...prior, page.nextCursor!]);
              }}
            >
              Next rules
            </button>
          </div>
        </div>
      )}
    </>
  );
}
