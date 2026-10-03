import { useEffect, useState } from "react";
import { useSession } from "../lib/session";
import { request, APIError } from "../lib/transport";
import { useResource } from "../lib/useResource";
import { decodePage } from "../lib/api";
import type { Device, Preferences } from "../lib/models";
import { title } from "../lib/format";
import {
  Empty,
  ErrorNotice,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
const decodeDevices = (value: unknown) => decodePage<Device>(value);
export function SettingsPage() {
  const { identity, bootstrap, refreshBootstrap, logout } = useSession(),
    [draft, setDraft] = useState<Preferences>(bootstrap.preferences),
    [dirty, setDirty] = useState(false),
    [error, setError] = useState<APIError>(),
    [message, setMessage] = useState(""),
    [busy, setBusy] = useState(false);
  const devices = useResource(
    "/api/v1/devices?limit=50",
    identity,
    0,
    decodeDevices,
  );
  useEffect(() => {
    if (!dirty) setDraft(bootstrap.preferences);
  }, [bootstrap.preferences, dirty]);
  const save = async () => {
    setBusy(true);
    setError(undefined);
    try {
      new Intl.DateTimeFormat(undefined, { timeZone: draft.timezone });
      await request("/api/v1/preferences", {
        method: "PUT",
        body: draft,
        revision: draft.revision,
      });
      setDirty(false);
      setMessage("Preferences saved.");
      refreshBootstrap();
    } catch (e) {
      setError(
        e instanceof APIError
          ? e
          : new APIError(
              "invalid_preferences",
              "Enter a valid IANA timezone, such as America/New_York or UTC.",
            ),
      );
    } finally {
      setBusy(false);
    }
  };
  const toggleDevice = async (device: Device) => {
    try {
      await request(`/api/v1/devices/${encodeURIComponent(device.id)}`, {
        method: "PATCH",
        body: { enabled: !device.enabled },
      });
      devices.refresh();
    } catch (e) {
      setError(
        e instanceof APIError
          ? e
          : new APIError(
              "request_failed",
              "Device delivery could not be updated.",
            ),
      );
    }
  };
  return (
    <>
      <PageHeader
        title="Settings"
        description="Personal preferences, current access, and notification delivery."
      />
      {error && <ErrorNotice error={error} />}
      <div className="settings-grid">
        <section className="panel">
          <div className="panel-heading">
            <h2>Display and refresh</h2>
          </div>
          <form
            className="panel-body form-stack"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <label>
              Timezone
              <input
                value={draft.timezone}
                onChange={(e) => {
                  setDraft({ ...draft, timezone: e.target.value });
                  setDirty(true);
                }}
                placeholder="America/New_York"
              />
              <small>Use an IANA timezone. UTC is always available.</small>
            </label>
            <label>
              Appearance
              <select
                value={draft.appearance}
                onChange={(e) => {
                  setDraft({
                    ...draft,
                    appearance: e.target.value as Preferences["appearance"],
                  });
                  setDirty(true);
                }}
              >
                {["system", "light", "dark"].map((v) => (
                  <option key={v} value={v}>
                    {title(v)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Foreground refresh
              <select
                value={draft.refreshSeconds}
                onChange={(e) => {
                  setDraft({
                    ...draft,
                    refreshSeconds: Number(e.target.value),
                  });
                  setDirty(true);
                }}
              >
                {[
                  [0, "Manual only"],
                  [5, "Every 5 seconds"],
                  [10, "Every 10 seconds"],
                  [30, "Every 30 seconds"],
                ].map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
              <small>
                Polling pauses in hidden tabs. Access is still revalidated while
                signed in.
              </small>
            </label>
            <button className="button" disabled={busy || !dirty}>
              {busy ? "Saving…" : "Save preferences"}
            </button>
            {message && (
              <p role="status" className="success-text">
                {message}
              </p>
            )}
          </form>
        </section>
        <section className="panel">
          <div className="panel-heading">
            <h2>Account</h2>
          </div>
          <div className="panel-body">
            <div className="profile">
              <span className="avatar large" aria-hidden="true">
                {bootstrap.account.displayName.slice(0, 1)}
              </span>
              <div>
                <strong>{bootstrap.account.displayName}</strong>
                <p className="mono small">{bootstrap.account.id}</p>
              </div>
            </div>
            <p>
              Active Directory sign-in through AD FS. Namespace access comes
              from current direct group membership.
            </p>
            <p className="muted">
              Browser job and log content stays in memory. Sign-out clears this
              view.
            </p>
            <button className="button secondary" onClick={() => void logout()}>
              Sign out
            </button>
          </div>
        </section>
        <section className="panel wide">
          <div className="panel-heading">
            <div>
              <h2>Connections and current access</h2>
              <p>
                Capabilities are the exact union of all roles within each
                deployment and namespace.
              </p>
            </div>
          </div>
          <div className="panel-body">
            {bootstrap.sources.map((source) => (
              <section className="connection-row" key={source.deploymentId}>
                <div className="connection-heading">
                  <h3>{source.displayName}</h3>
                  <Status value={source.status} />
                </div>
                {source.namespaces.map((ns) => (
                  <details className="permission-row" key={ns.namespaceId}>
                    <summary>
                      <strong>{ns.name}</strong>
                      <span>{ns.roles.join(" + ")}</span>
                    </summary>
                    <p className="muted small">Effective capabilities</p>
                    <div className="count-chips">
                      {ns.capabilities.map((cap) => (
                        <span className="count-chip mono" key={cap}>
                          {cap}
                        </span>
                      ))}
                    </div>
                    <p className="muted small">
                      Authorization revision{" "}
                      {ns.authorizationVersion ?? "Unavailable"}. Dashboard
                      remains monitoring only.
                    </p>
                  </details>
                ))}
              </section>
            ))}
          </div>
        </section>
        <section className="panel wide">
          <div className="panel-heading">
            <div>
              <h2>iPhone notification delivery</h2>
              <p>
                Muting a device leaves your rules and inbox active. APNs
                registration is managed by the native app.
              </p>
            </div>
          </div>
          {devices.error && (
            <ErrorNotice error={devices.error} retry={devices.refresh} />
          )}{" "}
          {!devices.data && devices.loading ? (
            <Spinner label="Loading devices" />
          ) : devices.data?.data.length ? (
            <div className="panel-body">
              {devices.data.data.map((device) => (
                <div className="device-row" key={device.id}>
                  <div>
                    <strong>{device.name}</strong>
                    <p className="muted small">
                      {device.platform ?? "iPhone"} · OS permission:{" "}
                      {title(device.permission)}
                    </p>
                  </div>
                  <Status value={device.enabled ? "enabled" : "muted"} />
                  <button
                    className="button secondary"
                    onClick={() => void toggleDevice(device)}
                  >
                    {device.enabled ? "Mute this device" : "Enable delivery"}
                  </button>
                </div>
              ))}
            </div>
          ) : (
            !devices.error && (
              <Empty title="No registered iPhones">
                Sign in through the native iPhone app to register a device and
                choose notification permission.
              </Empty>
            )
          )}
        </section>
      </div>
    </>
  );
}
