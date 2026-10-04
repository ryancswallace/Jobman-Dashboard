import { useEffect, useRef, useState } from "react";
import { useSession } from "../lib/session";
import { request, APIError, authorizationErrors } from "../lib/transport";
import { useResource } from "../lib/useResource";
import type { Preferences } from "../lib/models";
import { title, timestamp } from "../lib/format";
import {
  Empty,
  ErrorNotice,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
import {
  decodeDevices,
  deviceSettings,
  deviceDeliveryState,
  updateDevice,
  removeDevice,
  type Device,
  type DeviceSettingsInput,
} from "../lib/devices";
import "./Settings.css";
export function SettingsPage() {
  const { identity } = useSession();
  return <SettingsWorkspace key={identity} />;
}
function SettingsWorkspace() {
  const { bootstrap, refreshBootstrap, logout } = useSession(),
    [draft, setDraft] = useState<Preferences>(bootstrap.preferences),
    [dirty, setDirty] = useState(false),
    [error, setError] = useState<APIError>(),
    [message, setMessage] = useState(""),
    [busy, setBusy] = useState(false);
  const preferencesController = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => preferencesController.current?.abort(), []);
  useEffect(() => {
    if (!dirty) setDraft(bootstrap.preferences);
  }, [bootstrap.preferences, dirty]);
  const save = async () => {
    if (preferencesController.current) return;
    const active = new AbortController();
    preferencesController.current = active;
    setBusy(true);
    setError(undefined);
    try {
      new Intl.DateTimeFormat(undefined, { timeZone: draft.timezone });
      await request("/api/v1/preferences", {
        method: "PUT",
        body: draft,
        revision: draft.revision,
        signal: active.signal,
      });
      if (active.signal.aborted) return;
      setDirty(false);
      setMessage("Preferences saved.");
      refreshBootstrap();
    } catch (e) {
      if (active.signal.aborted) return;
      setError(
        e instanceof APIError
          ? e
          : new APIError(
              "invalid_preferences",
              "Enter a valid IANA timezone, such as America/New_York or UTC.",
            ),
      );
    } finally {
      if (!active.signal.aborted) setBusy(false);
      preferencesController.current = undefined;
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
        <NotificationDevices />
      </div>
    </>
  );
}

function NotificationDevices() {
  const { identity, bootstrap } = useSession();
  const devices = useResource(
    "/api/v1/devices",
    identity,
    10000,
    decodeDevices,
  );
  const [edit, setEdit] = useState<{
    device: Device;
    draft: DeviceSettingsInput;
  }>();
  const [remove, setRemove] = useState<Device>();
  const [busy, setBusy] = useState(false),
    [error, setError] = useState<APIError>(),
    [notice, setNotice] = useState("");
  const [blockedData, setBlockedData] = useState<unknown>();
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    const failure = devices.error;
    if (
      failure &&
      (authorizationErrors.has(failure.code) ||
        failure.status === 401 ||
        failure.status === 403)
    ) {
      controller.current?.abort();
      controller.current = undefined;
      setBusy(false);
      setEdit(undefined);
      setRemove(undefined);
      setNotice("");
    }
  }, [devices.error]);
  const refresh = () => {
    if (controller.current) return;
    setEdit(undefined);
    setRemove(undefined);
    setError(undefined);
    devices.refresh();
  };
  const privateError =
    error &&
    (authorizationErrors.has(error.code) ||
      error.status === 401 ||
      error.status === 403);
  const page =
    devices.error ||
    privateError ||
    (blockedData && blockedData === devices.data)
      ? undefined
      : devices.data;
  const changed =
    !!edit &&
    page?.items.find((d) => d.installationId === edit.device.installationId)
      ?.revision !== edit.device.revision;
  const act = async (device: Device, input?: DeviceSettingsInput) => {
    if (controller.current) return;
    if (input) {
      try {
        input = deviceSettings(input);
      } catch (e) {
        setError(e as APIError);
        return;
      }
    }
    const active = new AbortController();
    controller.current = active;
    setBusy(true);
    setError(undefined);
    setNotice("");
    try {
      if (input) await updateDevice(device, input, active.signal);
      else await removeDevice(device, active.signal);
      if (active.signal.aborted) return;
      setNotice(
        input
          ? "Device preferences saved. Delivery also depends on iPhone permission and setup."
          : "Device removed. Reattaching it requires an explicit action in the iPhone app.",
      );
      setEdit(undefined);
      setRemove(undefined);
      setBlockedData(devices.data);
      devices.refresh();
    } catch (failure) {
      if (active.signal.aborted) return;
      const e =
        failure instanceof APIError
          ? failure
          : new APIError(
              "request_failed",
              "The device change could not be confirmed.",
            );
      setError(e);
      setEdit(undefined);
      setRemove(undefined);
      setBlockedData(devices.data);
      setNotice(
        e.code === "revision_conflict"
          ? "This device changed elsewhere. Refresh devices before editing again."
          : "Refresh devices to check the current state before trying another change.",
      );
    } finally {
      if (!active.signal.aborted) setBusy(false);
      if (controller.current === active) controller.current = undefined;
    }
  };
  return (
    <section className="panel wide notification-devices">
      <div className="panel-heading">
        <div>
          <h2>iPhone notification delivery</h2>
          <p>
            Each device has its own delivery preferences. Alert rules and your
            inbox stay active. Register phones through the native app.
          </p>
        </div>
        <button
          className="button secondary"
          disabled={busy || devices.loading}
          onClick={refresh}
        >
          Refresh devices
        </button>
      </div>
      {(error || devices.error) && (
        <ErrorNotice error={(error || devices.error)!} />
      )}
      {notice && (
        <p className="notice subtle" role="status">
          {notice}
        </p>
      )}
      {devices.loading && !page ? (
        <Spinner label="Loading devices" />
      ) : page?.items.length ? (
        <div className="device-list">
          {page.items.map((device) => (
            <article
              className="device-card"
              key={device.installationId}
              aria-label={device.label}
            >
              <div className="device-card-heading">
                <div>
                  <h3>{device.label}</h3>
                  <p className="small muted">{deviceDeliveryState(device)}</p>
                </div>
                <Status
                  value={
                    !device.enabled
                      ? "disabled"
                      : device.muted
                        ? "muted"
                        : "enabled"
                  }
                />
              </div>
              <dl className="device-facts">
                <div>
                  <dt>iOS permission</dt>
                  <dd>{title(device.permission)}</dd>
                </div>
                <div>
                  <dt>Notification token</dt>
                  <dd>{title(device.tokenStatus)}</dd>
                </div>
                <div>
                  <dt>Last seen</dt>
                  <dd>
                    {timestamp(
                      device.lastSeenAt,
                      bootstrap.preferences.timezone,
                    )}
                  </dd>
                </div>
              </dl>
              <div className="device-actions">
                <button
                  className="button secondary"
                  disabled={busy || devices.loading || !!edit || !!remove}
                  onClick={() =>
                    void act(device, {
                      label: device.label,
                      enabled: !device.enabled,
                      muted: device.muted,
                    })
                  }
                >
                  {device.enabled ? "Stop delivery" : "Enable delivery"}
                </button>
                <button
                  className="button secondary"
                  disabled={busy || devices.loading || !!edit || !!remove}
                  onClick={() =>
                    void act(device, {
                      label: device.label,
                      enabled: device.enabled,
                      muted: !device.muted,
                    })
                  }
                >
                  {device.muted ? "Unmute device" : "Mute device"}
                </button>
                <button
                  className="text-button"
                  disabled={busy || devices.loading || !!edit || !!remove}
                  onClick={() => {
                    setEdit({
                      device,
                      draft: {
                        label: device.label,
                        enabled: device.enabled,
                        muted: device.muted,
                      },
                    });
                    setError(undefined);
                    setNotice("");
                  }}
                >
                  Edit device
                </button>
                <button
                  className="text-button"
                  disabled={busy || devices.loading || !!edit || !!remove}
                  onClick={() => {
                    setRemove(device);
                    setError(undefined);
                    setNotice("");
                  }}
                >
                  Remove device
                </button>
              </div>
              {edit?.device.installationId === device.installationId && (
                <form
                  className="device-editor form-stack"
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (!changed) void act(edit.device, edit.draft);
                  }}
                >
                  {changed && (
                    <p className="notice warning" role="status">
                      This device changed while you were editing. Refresh
                      devices to load its current preferences.
                    </p>
                  )}
                  <label>
                    Device label
                    <input
                      value={edit.draft.label}
                      disabled={busy || changed}
                      onChange={(e) =>
                        setEdit({
                          ...edit,
                          draft: { ...edit.draft, label: e.target.value },
                        })
                      }
                    />
                  </label>
                  <label className="device-checkbox">
                    <input
                      type="checkbox"
                      checked={edit.draft.enabled}
                      disabled={busy || changed}
                      onChange={(e) =>
                        setEdit({
                          ...edit,
                          draft: { ...edit.draft, enabled: e.target.checked },
                        })
                      }
                    />
                    Delivery enabled
                  </label>
                  <label className="device-checkbox">
                    <input
                      type="checkbox"
                      checked={edit.draft.muted}
                      disabled={busy || changed}
                      onChange={(e) =>
                        setEdit({
                          ...edit,
                          draft: { ...edit.draft, muted: e.target.checked },
                        })
                      }
                    />
                    Muted on this device
                  </label>
                  <div className="device-actions">
                    <button className="button" disabled={busy || changed}>
                      {busy ? "Saving…" : "Save device"}
                    </button>
                    <button
                      type="button"
                      className="button secondary"
                      disabled={busy}
                      onClick={() => setEdit(undefined)}
                    >
                      Cancel editing
                    </button>
                  </div>
                </form>
              )}
              {remove?.installationId === device.installationId && (
                <div className="device-removal">
                  <p>
                    Remove <strong>{device.label}</strong> from this account? It
                    will stop receiving new deliveries. The iPhone must
                    explicitly reattach before delivery resumes.
                  </p>
                  <div className="device-actions">
                    <button
                      className="button"
                      disabled={busy || remove.revision !== device.revision}
                      onClick={() => void act(remove)}
                    >
                      Confirm removal
                    </button>
                    <button
                      className="button secondary"
                      disabled={busy}
                      onClick={() => setRemove(undefined)}
                    >
                      Keep device
                    </button>
                  </div>
                  {remove.revision !== device.revision && (
                    <p role="status">
                      This device changed. Refresh before removing it.
                    </p>
                  )}
                </div>
              )}
            </article>
          ))}
        </div>
      ) : page && !page.items.length ? (
        <Empty title="No registered iPhones">
          Sign in through the native iPhone app to register a device and choose
          notification permission.
        </Empty>
      ) : null}
    </section>
  );
}
