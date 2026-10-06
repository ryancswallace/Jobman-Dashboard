import { useEffect, useRef, useState } from "react";
import { useSession } from "../lib/session";
import { APIError, authorizationErrors, request } from "../lib/transport";
import { outcomes, unsuccessful } from "../lib/models";
import type { JobRef, NamespaceRef } from "../lib/models";
import { sourceLabel, title } from "../lib/format";
import {
  canonicalID,
  decodeRule,
  editableRule,
  jobKey,
  nameBytes,
  namespaceKey,
  newRule,
  ruleDraft,
  validateRule,
  type Rule,
  type RuleInput,
} from "../lib/rules";
import { ErrorNotice } from "./States";

export function RuleEditor({
  rule: initial,
  job,
  onClose,
  onSaved,
}: {
  rule?: Rule;
  job?: JobRef;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { bootstrap } = useSession();
  const [rule, setRule] = useState(initial);
  const [draft, setDraft] = useState<RuleInput>(() =>
    initial ? ruleDraft(initial) : newRule(job),
  );
  const [error, setError] = useState<APIError>();
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [inaccessible, setInaccessible] = useState(false);
  const [filter, setFilter] = useState("");
  const [jobNamespace, setJobNamespace] = useState(
    job ? namespaceKey(job) : "",
  );
  const [jobId, setJobId] = useState("");
  const requestRef = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => requestRef.current?.abort(), []);
  const options = bootstrap.sources.flatMap((source) =>
    source.namespaces
      .filter(
        (ns) =>
          ns.capabilities.includes("namespace.read") &&
          ns.capabilities.includes("jobs.read"),
      )
      .map((ns) => ({
        ref: { deploymentId: source.deploymentId, namespaceId: ns.namespaceId },
        label: `${source.displayName} / ${ns.name}`,
      })),
  );
  const known = new Set(options.map((option) => namespaceKey(option.ref)));
  const missing = draft.namespaces.some((ref) => !known.has(namespaceKey(ref)));
  const change = (patch: Partial<RuleInput>) => {
    setDraft((old) => ({ ...old, ...patch }));
    setError(undefined);
  };
  const toggleNamespace = (ref: NamespaceRef, selected: boolean) => {
    const key = namespaceKey(ref);
    change({
      namespaces: selected
        ? [...draft.namespaces, ref]
        : draft.namespaces.filter((item) => namespaceKey(item) !== key),
      jobs: selected
        ? draft.jobs
        : draft.jobs.filter((item) => namespaceKey(item) !== key),
    });
  };
  const addJob = () => {
    const option = options.find(
      (entry) => namespaceKey(entry.ref) === jobNamespace,
    );
    const id = jobId.trim();
    if (!option || !canonicalID(id)) {
      setError(
        new APIError(
          "invalid_rule",
          "Choose a namespace you can access and paste the full job ID, using lowercase letters, from the job details page.",
        ),
      );
      return;
    }
    const ref = { ...option.ref, jobId: id };
    if (draft.jobs.some((item) => jobKey(item) === jobKey(ref))) {
      setError(
        new APIError(
          "invalid_rule",
          "This job is already selected in that deployment and namespace.",
        ),
      );
      return;
    }
    if (draft.jobs.length >= 100) {
      setError(
        new APIError("invalid_rule", "A rule can watch at most 100 jobs."),
      );
      return;
    }
    change({
      jobs: [...draft.jobs, ref],
      namespaces: draft.namespaces.some(
        (item) => namespaceKey(item) === jobNamespace,
      )
        ? draft.namespaces
        : [...draft.namespaces, option.ref],
    });
    setJobId("");
  };
  const save = async () => {
    if (requestRef.current || uncertain || inaccessible) return;
    setError(undefined);
    let input: RuleInput;
    try {
      if (missing)
        throw new APIError(
          "forbidden",
          "A selected namespace is no longer available. Close the editor and refresh your access before editing this rule.",
        );
      input = validateRule(draft);
    } catch (failure) {
      setError(failure as APIError);
      return;
    }
    const controller = new AbortController();
    requestRef.current = controller;
    setBusy(true);
    try {
      const value = await request<unknown>(
        rule ? `/api/v1/rules/${encodeURIComponent(rule.id)}` : "/api/v1/rules",
        {
          method: rule ? "PUT" : "POST",
          body: input,
          revision: rule?.revision,
          signal: controller.signal,
        },
      );
      const saved = decodeRule(value);
      if (rule && saved.id !== rule.id)
        throw new APIError(
          "invalid_response",
          "Dashboard returned a different rule than the one being edited. Close the editor and refresh the list.",
        );
      if (!controller.signal.aborted) onSaved();
    } catch (failure) {
      if (controller.signal.aborted) return;
      const apiError =
        failure instanceof APIError
          ? failure
          : new APIError("request_failed", "The rule could not be saved.");
      setError(apiError);
      if (
        authorizationErrors.has(apiError.code) ||
        apiError.status === 401 ||
        apiError.status === 403
      )
        setInaccessible(true);
      // Creation has no idempotency contract. Never blindly repeat a request
      // whose response was lost after the server may have admitted the rule.
      if (!rule && ![400, 422, 429].includes(apiError.status))
        setUncertain(true);
    } finally {
      if (!controller.signal.aborted) setBusy(false);
      requestRef.current = undefined;
    }
  };
  const reload = async () => {
    if (!rule || requestRef.current) return;
    const controller = new AbortController();
    requestRef.current = controller;
    setBusy(true);
    try {
      const current = decodeRule(
        await request(`/api/v1/rules/${encodeURIComponent(rule.id)}`, {
          signal: controller.signal,
        }),
      );
      if (current.id !== rule.id || !editableRule(current))
        throw new APIError(
          "forbidden",
          "This rule contains unavailable or unsupported selections. Close the editor, then refresh the list or choose Check access and resume.",
        );
      if (!controller.signal.aborted) {
        setRule(current);
        setDraft(ruleDraft(current));
        setError(undefined);
        setInaccessible(false);
      }
    } catch (failure) {
      if (!controller.signal.aborted)
        setError(
          failure instanceof APIError
            ? failure
            : new APIError(
                "request_failed",
                "The current rule could not be loaded.",
              ),
        );
    } finally {
      if (!controller.signal.aborted) setBusy(false);
      requestRef.current = undefined;
    }
  };
  if (inaccessible || missing)
    return (
      <section className="panel editor">
        <div className="panel-body">
          <ErrorNotice
            error={
              error ??
              new APIError(
                "authorization_unavailable",
                "A selected namespace is no longer available. Close the editor and refresh your access.",
              )
            }
          />
          <button
            className="button secondary"
            disabled={busy}
            onClick={onClose}
          >
            Close and refresh rules
          </button>
        </div>
      </section>
    );
  return (
    <section
      className="panel editor rule-editor"
      aria-labelledby="rule-editor-title"
    >
      <div className="panel-heading">
        <h2 id="rule-editor-title">
          {rule ? "Edit alert rule" : "Create an alert rule"}
        </h2>
        <button className="text-button" disabled={busy} onClick={onClose}>
          Close editor
        </button>
      </div>
      <form
        className="panel-body form-stack"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        {error && <ErrorNotice error={error} />}
        {error?.code === "revision_conflict" && (
          <button
            type="button"
            className="button secondary"
            disabled={busy}
            onClick={() => void reload()}
          >
            Discard edits and reload rule
          </button>
        )}
        {uncertain && (
          <p className="notice warning" role="status">
            The server may have saved this rule. Close the editor and check the
            refreshed rule list before creating another.
          </p>
        )}
        {missing && (
          <p className="notice warning" role="status">
            A selected namespace is no longer authorized or available. Saving is
            paused; close the editor and refresh your access.
          </p>
        )}
        <fieldset
          disabled={busy || uncertain || inaccessible || missing}
          className="rule-editor-fields"
        >
          <label>
            Rule name
            <input
              value={draft.name}
              maxLength={120}
              onChange={(event) => change({ name: event.target.value })}
              required
              placeholder="e.g. Team training failures"
              aria-describedby="rule-name-budget"
            />
          </label>
          <p
            id="rule-name-budget"
            className={
              nameBytes(draft.name) > 120 ? "rule-validation" : "muted small"
            }
          >
            Name size: {nameBytes(draft.name)} of 120 bytes. Some characters use
            more than one byte.
          </p>
          <fieldset>
            <legend>Which jobs?</legend>
            {[
              [
                "watched_jobs",
                "Specific watched jobs",
                "Only the exact jobs you select.",
              ],
              [
                "my_jobs",
                "All my jobs",
                "Current and future jobs submitted by your account.",
              ],
              [
                "namespace_jobs",
                "All jobs in selected namespaces",
                "Everyone’s current and future jobs in the namespaces you select.",
              ],
            ].map(([value, label, hint]) => (
              <label className="choice" key={value}>
                <input
                  type="radio"
                  name="job-scope"
                  checked={draft.scope === value}
                  onChange={() => change({ scope: value })}
                />
                <span>
                  <strong>{label}</strong>
                  <small>{hint}</small>
                </span>
              </label>
            ))}
          </fieldset>
          <fieldset>
            <legend>Namespaces you can access</legend>
            <p className="muted small">
              Selections may span deployments. New namespaces are never added
              automatically. Removing a namespace also removes its watched jobs.
            </p>
            <label>
              Filter namespaces
              <input
                type="search"
                value={filter}
                onChange={(event) => setFilter(event.target.value)}
              />
            </label>
            <p className="muted small">
              {draft.namespaces.length} / 320 namespaces selected
            </p>
            <div className="namespace-choices rule-namespace-options">
              {options
                .filter((option) =>
                  option.label
                    .toLocaleLowerCase()
                    .includes(filter.toLocaleLowerCase()),
                )
                .map((option) => {
                  const selected = draft.namespaces.some(
                    (ref) => namespaceKey(ref) === namespaceKey(option.ref),
                  );
                  return (
                    <label
                      className="checkbox-label"
                      key={namespaceKey(option.ref)}
                    >
                      <input
                        type="checkbox"
                        checked={selected}
                        disabled={!selected && draft.namespaces.length >= 320}
                        onChange={(event) =>
                          toggleNamespace(option.ref, event.target.checked)
                        }
                      />
                      {option.label}
                    </label>
                  );
                })}
            </div>
            {!options.length && (
              <p>
                No authorized namespaces are available. Refresh your connection
                to try again.
              </p>
            )}
          </fieldset>
          {draft.scope === "watched_jobs" && (
            <fieldset>
              <legend>Watched jobs</legend>
              <p className="muted small">
                {draft.jobs.length} / 100 jobs selected. Each job is checked
                against current access when you save.
              </p>
              <div className="rule-job-input">
                <label>
                  Job namespace
                  <select
                    value={jobNamespace}
                    onChange={(event) => setJobNamespace(event.target.value)}
                  >
                    <option value="">Select namespace</option>
                    {options.map((option) => (
                      <option
                        key={namespaceKey(option.ref)}
                        value={namespaceKey(option.ref)}
                      >
                        {option.label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Job ID
                  <input
                    value={jobId}
                    maxLength={36}
                    onChange={(event) => setJobId(event.target.value)}
                    placeholder="Paste the full job ID"
                  />
                </label>
                <button
                  type="button"
                  className="button secondary"
                  disabled={draft.jobs.length >= 100}
                  onClick={addJob}
                >
                  Add job
                </button>
              </div>
              <ul className="rule-watched-jobs">
                {draft.jobs.map((ref) => (
                  <li key={jobKey(ref)}>
                    <span>
                      {sourceLabel(
                        bootstrap.sources,
                        ref.deploymentId,
                        ref.namespaceId,
                      )}
                      <code>{ref.jobId}</code>
                    </span>
                    <button
                      type="button"
                      className="text-button"
                      aria-label={`Remove job ${ref.jobId} from ${sourceLabel(bootstrap.sources, ref.deploymentId, ref.namespaceId)}`}
                      onClick={() =>
                        change({
                          jobs: draft.jobs.filter(
                            (item) => jobKey(item) !== jobKey(ref),
                          ),
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
            <legend>Final results</legend>
            <div className="preset-buttons">
              {[
                {
                  label: "Unsuccessful",
                  mode: "selected",
                  values: [...unsuccessful],
                },
                {
                  label: "Successful completion",
                  mode: "selected",
                  values: ["success"],
                },
                {
                  label: "All final results",
                  mode: "all_terminal",
                  values: [],
                },
              ].map((preset) => (
                <button
                  key={preset.label}
                  type="button"
                  className="button secondary"
                  onClick={() =>
                    change({
                      outcomeMode: preset.mode,
                      outcomes: preset.values,
                    })
                  }
                >
                  {preset.label}
                </button>
              ))}
            </div>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.outcomeMode === "all_terminal"}
                onChange={(event) =>
                  change({
                    outcomeMode: event.target.checked
                      ? "all_terminal"
                      : "selected",
                    outcomes: event.target.checked ? [] : [...unsuccessful],
                  })
                }
              />
              All final results, including any added in future versions
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
                    onChange={(event) =>
                      change({
                        outcomes: event.target.checked
                          ? [...draft.outcomes, outcome]
                          : draft.outcomes.filter((item) => item !== outcome),
                      })
                    }
                  />
                  {title(outcome)}
                </label>
              ))}
            </div>
            <p className="muted small">
              The unsuccessful preset includes failure, timed out, aborted and
              lost. Cancellation is a separate choice.
            </p>
          </fieldset>
          {!rule && (
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(event) => change({ enabled: event.target.checked })}
              />
              Enable this rule
            </label>
          )}
        </fieldset>
        <p className="muted small">
          New or changed rules notify you about events recorded after monitoring
          starts. Earlier job results do not create new alerts. Monitoring may
          stay Pending while a Control server is unavailable.{" "}
          {rule &&
            "Use the rule’s Stop or Enable control to change whether it is enabled."}
        </p>
        <div className="form-actions">
          <button
            className="button"
            disabled={
              busy ||
              uncertain ||
              inaccessible ||
              missing ||
              error?.code === "revision_conflict"
            }
          >
            {busy ? "Saving…" : "Save rule"}
          </button>
          <button
            type="button"
            className="button secondary"
            disabled={busy}
            onClick={onClose}
          >
            {uncertain ? "Close and check saved rules" : "Cancel"}
          </button>
        </div>
      </form>
    </section>
  );
}
