import { useState } from "react";
import { useSession } from "../lib/session";
import { request, APIError } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodePage } from "../lib/api";
import { sourceLabel, title } from "../lib/format";
import type { AlertRule, NamespaceRef } from "../lib/models";
import { outcomes, unsuccessful } from "../lib/models";
import {
  Empty,
  ErrorNotice,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
const decodeRules = (value: unknown) => decodePage<AlertRule>(value);
export function AlertsPage() {
  const { identity } = useSession(),
    rules = useResource("/api/v1/rules?limit=50", identity, 0, decodeRules),
    [edit, setEdit] = useState<AlertRule | "new">(),
    [error, setError] = useState<APIError>();
  const toggle = async (rule: AlertRule) => {
    try {
      await request(`/api/v1/rules/${encodeURIComponent(rule.id)}`, {
        method: "PUT",
        body: ruleInput({ ...rule, enabled: !rule.enabled }),
        revision: rule.revision,
      });
      rules.refresh();
    } catch (e) {
      setError(
        e instanceof APIError
          ? e
          : new APIError("request_failed", "The rule could not be updated."),
      );
    }
  };
  return (
    <>
      <PageHeader
        title="Alert rules"
        description="Choose the jobs and outcomes you want to follow. Rules are opt-in and continue while the apps are closed."
        actions={
          <button className="button" onClick={() => setEdit("new")}>
            + New alert rule
          </button>
        }
      />
      {(error || rules.error) && (
        <ErrorNotice error={(error || rules.error)!} retry={rules.refresh} />
      )}
      <p className="notice subtle">
        Matching rules produce one inbox item per event, even when scopes
        overlap. Push delivery is controlled separately for each iPhone.
      </p>
      {edit && (
        <RuleEditor
          key={typeof edit === "string" ? "new" : edit.id}
          rule={edit === "new" ? undefined : edit}
          onClose={() => setEdit(undefined)}
          onSaved={() => {
            setEdit(undefined);
            rules.refresh();
          }}
        />
      )}
      {!rules.data && rules.loading ? (
        <Spinner label="Loading alert rules" />
      ) : rules.data?.data.length ? (
        <div className="card-list">
          {rules.data.data.map((rule) => (
            <article className="panel rule-card" key={rule.id}>
              <div className="rule-heading">
                <div>
                  <h2>{rule.name}</h2>
                  <p>
                    {title(rule.scope)} ·{" "}
                    {rule.outcomeMode === "all_terminal"
                      ? "All terminal outcomes"
                      : rule.outcomes.map(title).join(", ")}
                  </p>
                </div>
                <Status
                  value={rule.enabled ? "enabled" : "disabled"}
                  tone={rule.enabled ? "good" : "neutral"}
                />
              </div>
              <div className="rule-scopes">
                {rule.namespaces.length} explicitly selected namespace
                {rule.namespaces.length === 1 ? "" : "s"}
                {rule.scope === "watched_jobs" &&
                  ` · ${rule.jobs.length} watched jobs`}
              </div>
              {rule.activation?.map((a) => (
                <p key={a.deploymentId} className="muted small">
                  {a.deploymentId}: {title(a.status)}
                </p>
              ))}
              <div className="rule-actions">
                <button
                  className="button secondary"
                  onClick={() => setEdit(rule)}
                >
                  Edit rule
                </button>
                <button
                  className="text-button"
                  onClick={() => void toggle(rule)}
                >
                  {rule.enabled ? "Disable" : "Enable"}
                </button>
              </div>
            </article>
          ))}
        </div>
      ) : (
        !rules.error && (
          <Empty title="No alert rules yet">
            Watch a job from its detail page, or add a rule for your own jobs or
            entire namespaces.
          </Empty>
        )
      )}
    </>
  );
}
function RuleEditor({
  rule,
  onClose,
  onSaved,
}: {
  rule?: AlertRule;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { bootstrap } = useSession(),
    [draft, setDraft] = useState<Omit<AlertRule, "id" | "revision">>(
      () =>
        rule ?? {
          name: "",
          enabled: true,
          scope: "my_jobs",
          namespaces: [],
          jobs: [],
          outcomeMode: "selected",
          outcomes: [...unsuccessful],
        },
    ),
    [error, setError] = useState<APIError>(),
    [busy, setBusy] = useState(false),
    [jobNamespace, setJobNamespace] = useState(""),
    [jobId, setJobId] = useState("");
  const options = bootstrap.sources.flatMap((s) =>
    s.namespaces.map((n) => ({
      ref: { deploymentId: s.deploymentId, namespaceId: n.namespaceId },
      label: `${s.displayName} / ${n.name}`,
    })),
  );
  const nsKey = (ns: NamespaceRef) =>
    JSON.stringify([ns.deploymentId, ns.namespaceId]);
  const toggleNamespace = (ref: NamespaceRef, checked: boolean) =>
    setDraft({
      ...draft,
      namespaces: checked
        ? [...draft.namespaces, ref]
        : draft.namespaces.filter((n) => nsKey(n) !== nsKey(ref)),
    });
  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      if (!draft.name.trim())
        throw new APIError("invalid_rule", "Give this rule a name.");
      if (!draft.namespaces.length)
        throw new APIError(
          "invalid_rule",
          "Select at least one authorized namespace.",
        );
      if (draft.scope === "watched_jobs" && !draft.jobs.length)
        throw new APIError("invalid_rule", "Add at least one job to watch.");
      if (draft.outcomeMode === "selected" && !draft.outcomes.length)
        throw new APIError(
          "invalid_rule",
          "Select at least one terminal outcome.",
        );
      await request(
        rule ? `/api/v1/rules/${encodeURIComponent(rule.id)}` : "/api/v1/rules",
        {
          method: rule ? "PUT" : "POST",
          body: ruleInput(draft),
          revision: rule?.revision,
          idempotencyKey: rule ? undefined : crypto.randomUUID(),
        },
      );
      onSaved();
    } catch (e) {
      setError(
        e instanceof APIError
          ? e
          : new APIError("request_failed", "The rule could not be saved."),
      );
    } finally {
      setBusy(false);
    }
  };
  const addJob = () => {
    const option = options.find((o) => nsKey(o.ref) === jobNamespace);
    if (!option || !jobId.trim()) return;
    const ref = { ...option.ref, jobId: jobId.trim() };
    if (!draft.jobs.some((j) => JSON.stringify(j) === JSON.stringify(ref)))
      setDraft({
        ...draft,
        jobs: [...draft.jobs, ref],
        namespaces: draft.namespaces.some((n) => nsKey(n) === jobNamespace)
          ? draft.namespaces
          : [...draft.namespaces, option.ref],
      });
    setJobId("");
  };
  return (
    <section className="panel editor">
      <div className="panel-heading">
        <h2>{rule ? "Edit alert rule" : "Create an alert rule"}</h2>
        <button className="text-button" onClick={onClose}>
          Close
        </button>
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        className="panel-body form-stack"
      >
        {error && <ErrorNotice error={error} />}
        <label>
          Rule name
          <input
            value={draft.name}
            maxLength={120}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
            required
            placeholder="e.g. Team training failures"
          />
        </label>
        <fieldset>
          <legend>Which jobs?</legend>
          {[
            [
              "watched_jobs",
              "Specific watched jobs",
              "Only the job references you select.",
            ],
            [
              "my_jobs",
              "All my jobs",
              "Existing and future jobs with your verified submitting identity.",
            ],
            [
              "namespace_jobs",
              "All jobs in selected namespaces",
              "Includes everyone’s existing and future jobs.",
            ],
          ].map(([value, label, hint]) => (
            <label className="choice" key={value}>
              <input
                type="radio"
                name="scope"
                checked={draft.scope === value}
                onChange={() =>
                  setDraft({ ...draft, scope: value as AlertRule["scope"] })
                }
              />
              <span>
                <strong>{label}</strong>
                <small>{hint}</small>
              </span>
            </label>
          ))}
        </fieldset>
        <fieldset>
          <legend>Authorized namespaces</legend>
          <p className="muted small">
            Selections may span deployments. New namespaces are never added to a
            rule automatically.
          </p>
          <div className="namespace-choices">
            {options.map((o) => (
              <label className="checkbox-label" key={nsKey(o.ref)}>
                <input
                  type="checkbox"
                  checked={draft.namespaces.some(
                    (n) => nsKey(n) === nsKey(o.ref),
                  )}
                  onChange={(e) => toggleNamespace(o.ref, e.target.checked)}
                />
                {o.label}
              </label>
            ))}
          </div>
        </fieldset>
        {draft.scope === "watched_jobs" && (
          <fieldset>
            <legend>Watched jobs</legend>
            <div className="input-action">
              <select
                aria-label="Watched job namespace"
                value={jobNamespace}
                onChange={(e) => setJobNamespace(e.target.value)}
              >
                <option value="">Select namespace</option>
                {options.map((o) => (
                  <option key={nsKey(o.ref)} value={nsKey(o.ref)}>
                    {o.label}
                  </option>
                ))}
              </select>
              <input
                aria-label="Watched job ID"
                value={jobId}
                onChange={(e) => setJobId(e.target.value)}
                placeholder="Exact job ID"
              />
              <button
                type="button"
                className="button secondary"
                onClick={addJob}
              >
                Add job
              </button>
            </div>
            <ul>
              {draft.jobs.map((job, i) => (
                <li key={i}>
                  {sourceLabel(
                    bootstrap.sources,
                    job.deploymentId,
                    job.namespaceId,
                  )}{" "}
                  / <span className="mono">{job.jobId}</span>{" "}
                  <button
                    type="button"
                    className="text-button"
                    onClick={() =>
                      setDraft({
                        ...draft,
                        jobs: draft.jobs.filter((_, index) => index !== i),
                      })
                    }
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          </fieldset>
        )}
        <fieldset>
          <legend>Terminal outcomes</legend>
          <div className="preset-buttons">
            <button
              type="button"
              className="button secondary"
              onClick={() =>
                setDraft({
                  ...draft,
                  outcomeMode: "selected",
                  outcomes: [...unsuccessful],
                })
              }
            >
              Unsuccessful
            </button>
            <button
              type="button"
              className="button secondary"
              onClick={() =>
                setDraft({
                  ...draft,
                  outcomeMode: "selected",
                  outcomes: ["success"],
                })
              }
            >
              Successful completion
            </button>
            <button
              type="button"
              className="button secondary"
              onClick={() =>
                setDraft({
                  ...draft,
                  outcomeMode: "all_terminal",
                  outcomes: [],
                })
              }
            >
              All terminal
            </button>
          </div>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={draft.outcomeMode === "all_terminal"}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  outcomeMode: e.target.checked ? "all_terminal" : "selected",
                })
              }
            />
            All terminal outcomes, including future unknown outcomes
          </label>
          <div className="namespace-choices">
            {outcomes.map((outcome) => (
              <label className="checkbox-label" key={outcome}>
                <input
                  type="checkbox"
                  disabled={draft.outcomeMode === "all_terminal"}
                  checked={
                    draft.outcomeMode === "all_terminal" ||
                    draft.outcomes.includes(outcome)
                  }
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      outcomes: e.target.checked
                        ? [...draft.outcomes, outcome]
                        : draft.outcomes.filter((o) => o !== outcome),
                    })
                  }
                />
                {title(outcome)}
              </label>
            ))}
          </div>
        </fieldset>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
          />
          Enable this rule
        </label>
        <p className="muted small">
          New or changed scopes activate from the source’s current event
          checkpoint. Existing history is not sent as new alerts. A source
          outage may leave activation pending.
        </p>
        <div className="form-actions">
          <button className="button" disabled={busy}>
            {busy ? "Saving…" : "Save rule"}
          </button>
          <button type="button" className="button secondary" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}

function ruleInput(rule: Omit<AlertRule, "id" | "revision">) {
  return {
    name: rule.name,
    enabled: rule.enabled,
    scope: rule.scope,
    namespaces: rule.namespaces,
    jobs: rule.jobs,
    outcomeMode: rule.outcomeMode,
    outcomes: rule.outcomes,
  };
}
