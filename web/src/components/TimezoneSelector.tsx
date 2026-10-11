import { useId, useMemo, useState } from "react";
import fallbackTimezones from "../lib/timezones.json";

const cityNames: Record<string, string> = {
  Calcutta: "Kolkata (Calcutta)",
  Kolkata: "Kolkata (Calcutta)",
  Kiev: "Kyiv (Kiev)",
  Kyiv: "Kyiv (Kiev)",
  Godthab: "Nuuk (Godthab)",
  Nuuk: "Nuuk (Godthab)",
};
function supportsTimezone(zone: string) {
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}
function timezoneLabel(zone: string) {
  if (zone === "UTC") return "UTC — Coordinated Universal Time";
  const parts = zone.replace(/_/g, " ").split("/");
  const rawCity = parts.pop()!;
  const city = cityNames[rawCity] ?? rawCity;
  return parts.length ? `${city} — ${parts.join(" / ")}` : city;
}
function timezoneSearchText(zone: string) {
  let name = "";
  try {
    name =
      new Intl.DateTimeFormat("en", {
        timeZone: zone,
        timeZoneName: "longGeneric",
      })
        .formatToParts()
        .find((part) => part.type === "timeZoneName")?.value ?? "";
  } catch {
    // Keep an existing preference visible even if this browser cannot format it.
  }
  return `${zone} ${timezoneLabel(zone)} ${name}`
    .replace(/[_/]/g, " ")
    .toLowerCase();
}
export function TimezoneSelector({
  value,
  onChange,
}: {
  value: string;
  onChange: (zone: string) => void;
}) {
  const id = useId();
  const [query, setQuery] = useState("");
  const deviceZone = useMemo(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone,
    [],
  );
  const choices = useMemo(() => {
    const zones =
      typeof Intl.supportedValuesOf === "function"
        ? Intl.supportedValuesOf("timeZone")
        : fallbackTimezones.filter(supportsTimezone);
    return [...new Set(["UTC", ...zones, value, deviceZone].filter(Boolean))]
      .map((zone) => ({
        zone,
        label: timezoneLabel(zone),
        search: timezoneSearchText(zone),
      }))
      .sort((a, b) =>
        a.zone === b.zone
          ? 0
          : a.zone === "UTC"
            ? -1
            : b.zone === "UTC"
              ? 1
              : a.label.localeCompare(b.label),
      );
  }, [value, deviceZone]);
  const terms = query
    .trim()
    .toLowerCase()
    .replace(/[_/]/g, " ")
    .split(/\s+/)
    .filter(Boolean);
  const matches = choices.filter((choice) =>
    terms.every((term) => choice.search.includes(term)),
  );
  const selectedOutsideSearch = !matches.some(
    (choice) => choice.zone === value,
  );
  return (
    <div className="timezone-selector">
      <label htmlFor={`${id}-search`}>Find a timezone</label>
      <input
        id={`${id}-search`}
        type="search"
        value={query}
        placeholder="Search city, region or timezone"
        aria-describedby={`${id}-help`}
        onKeyDown={(event) => {
          if (event.key === "Enter") event.preventDefault();
        }}
        onChange={(event) => setQuery(event.target.value)}
      />
      <small id={`${id}-help`}>
        Browse the full list or search, for example, New York, Europe or
        Eastern.
      </small>
      <label htmlFor={`${id}-zone`}>Timezone</label>
      <select
        id={`${id}-zone`}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {selectedOutsideSearch && (
          <optgroup label="Current selection">
            <option value={value}>{timezoneLabel(value)}</option>
          </optgroup>
        )}
        <optgroup label={terms.length ? "Matching timezones" : "All timezones"}>
          {matches.map(({ zone, label }) => (
            <option key={zone} value={zone}>
              {label}
            </option>
          ))}
        </optgroup>
      </select>
      <small role="status">
        {matches.length
          ? `${terms.length ? "Matching timezones" : "Timezones available"}: ${matches.length}.`
          : "No matching timezones. Try another city or region; your selection has not changed."}
      </small>
      {deviceZone && (
        <button
          type="button"
          className="text-button"
          onClick={() => {
            setQuery("");
            onChange(deviceZone);
          }}
        >
          Use this device’s timezone: {timezoneLabel(deviceZone)}
        </button>
      )}
    </div>
  );
}
