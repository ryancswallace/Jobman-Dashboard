import type {
  Device,
  DevicePage,
  DeviceSettingsInput,
} from "../../../contracts/typescript/dashboard.generated";
import { APIError, dashboardClient } from "./transport";
import { canonicalID } from "./rules";
export type { Device, DevicePage, DeviceSettingsInput };

const invalid = () =>
  new APIError(
    "invalid_response",
    "Dashboard returned an invalid device response.",
  );
const revision = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[1-9][0-9]{0,18}$/.test(value) &&
  BigInt(value) <= 9223372036854775807n;
const date = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 40 &&
  Number.isFinite(Date.parse(value));
const labelValid = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length > 0 &&
  value === value.trim() &&
  new TextEncoder().encode(value).length <= 120 &&
  !/[\u0000-\u001f\u007f-\u009f]/.test(value);
const fields = [
  "installationId",
  "revision",
  "label",
  "topic",
  "environment",
  "state",
  "enabled",
  "muted",
  "permission",
  "tokenStatus",
  "revocationReady",
  "createdAt",
  "updatedAt",
  "lastSeenAt",
];
export function decodeDevice(value: unknown): Device {
  const d = value as Device;
  if (
    !d ||
    Object.keys(d).length !== fields.length ||
    fields.some((key) => !Object.hasOwn(d, key)) ||
    !canonicalID(d.installationId) ||
    !revision(d.revision) ||
    !labelValid(d.label) ||
    typeof d.topic !== "string" ||
    d.topic.length > 255 ||
    !/^[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$/.test(d.topic) ||
    !["sandbox", "production"].includes(d.environment) ||
    d.state !== "bound" ||
    typeof d.enabled !== "boolean" ||
    typeof d.muted !== "boolean" ||
    typeof d.revocationReady !== "boolean" ||
    ![
      "not_determined",
      "denied",
      "authorized",
      "provisional",
      "ephemeral",
    ].includes(d.permission) ||
    !["current", "invalid", "absent"].includes(d.tokenStatus) ||
    !date(d.createdAt) ||
    !date(d.updatedAt) ||
    !date(d.lastSeenAt) ||
    Date.parse(d.updatedAt) < Date.parse(d.createdAt) ||
    Date.parse(d.lastSeenAt) < Date.parse(d.createdAt) ||
    Date.parse(d.lastSeenAt) > Date.parse(d.updatedAt)
  )
    throw invalid();
  return d;
}
export function decodeDevices(value: unknown): DevicePage {
  const page = value as DevicePage;
  if (
    !page ||
    Object.keys(page).length !== 1 ||
    !Array.isArray(page.items) ||
    page.items.length > 50
  )
    throw invalid();
  const items = page.items.map(decodeDevice);
  if (
    items.some(
      (item, index) =>
        index > 0 && item.installationId <= items[index - 1].installationId,
    )
  )
    throw invalid();
  return { items };
}
export function deviceSettings(
  input: DeviceSettingsInput,
): DeviceSettingsInput {
  const result = {
    label: input.label.trim(),
    enabled: input.enabled,
    muted: input.muted,
  };
  if (
    !labelValid(result.label) ||
    typeof result.enabled !== "boolean" ||
    typeof result.muted !== "boolean"
  )
    throw new APIError(
      "invalid_device",
      "Enter a short device name without line breaks or control characters (up to 120 bytes; some characters use more than one byte).",
    );
  return result;
}
export async function updateDevice(
  device: Device,
  input: DeviceSettingsInput,
  signal: AbortSignal,
): Promise<Device> {
  decodeDevice(device);
  const updated = decodeDevice(
    await dashboardClient.updateDeviceSettings({
      path: { installationId: device.installationId },
      headers: { "If-Match": device.revision },
      body: deviceSettings(input),
      signal,
    }),
  );
  if (
    updated.installationId !== device.installationId ||
    BigInt(updated.revision) < BigInt(device.revision)
  )
    throw invalid();
  return updated;
}
export async function removeDevice(
  device: Device,
  signal: AbortSignal,
): Promise<void> {
  decodeDevice(device);
  await dashboardClient.removeDevice({
    path: { installationId: device.installationId },
    headers: { "If-Match": device.revision },
    signal,
  });
}
export function deviceDeliveryState(d: Device): string {
  if (!d.enabled) return "Delivery stopped";
  if (d.muted) return "Device muted";
  if (!d.revocationReady) return "Finish notification setup in the iPhone app";
  if (d.permission === "denied" || d.permission === "not_determined")
    return "Allow notifications in iOS";
  if (d.tokenStatus !== "current")
    return "Open the iPhone app to refresh notification delivery";
  return "Device ready for notification delivery";
}
