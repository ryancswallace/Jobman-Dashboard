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
  const city = Object.hasOwn(cityNames, rawCity) ? cityNames[rawCity] : rawCity;
  return parts.length ? `${city} — ${parts.join(" / ")}` : city;
}
function normalize(value: string) {
  return value
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .trim();
}
function timezoneChoice(zone: string) {
  const names: string[] = [];
  const abbreviations: string[] = [];
  let generic = "";
  try {
    const year = new Date().getUTCFullYear();
    const dates = [
      new Date(Date.UTC(year, 0, 15)),
      new Date(Date.UTC(year, 6, 15)),
    ];
    for (const style of ["longGeneric", "long", "short"] as const) {
      const formatter = new Intl.DateTimeFormat("en-US", {
        timeZone: zone,
        timeZoneName: style,
      });
      for (const date of dates) {
        const name = formatter
          .formatToParts(date)
          .find((part) => part.type === "timeZoneName")?.value;
        if (name) {
          names.push(name);
          if (style === "short" && /^[a-z]{2,5}$/i.test(name))
            abbreviations.push(normalize(name));
          if (style === "longGeneric") generic = name;
        }
      }
    }
  } catch {
    // Keep an existing preference visible even if this browser cannot format it.
  }
  const city = timezoneLabel(zone);
  const label = zone === "UTC" || !generic ? city : `${generic} — ${city}`;
  const normalizedNames = names.map(normalize);
  return {
    zone,
    label,
    abbreviations,
    search: normalize(`${zone} ${city} ${names.join(" ")}`),
    names: [...normalizedNames, normalize(generic.replace(/\bTime\b/g, ""))],
  };
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
  const catalog = useMemo(() => {
    const zones =
      typeof Intl.supportedValuesOf === "function"
        ? Intl.supportedValuesOf("timeZone")
        : fallbackTimezones.filter(supportsTimezone);
    return [...new Set(["UTC", ...zones])].map(timezoneChoice);
  }, []);
  const choices = useMemo(() => {
    const extras = [...new Set([value, deviceZone].filter(Boolean))].filter(
      (zone) => !catalog.some((choice) => choice.zone === zone),
    );
    return [...catalog, ...extras.map(timezoneChoice)].sort((a, b) =>
      a.zone === b.zone
        ? 0
        : a.zone === "UTC"
          ? -1
          : b.zone === "UTC"
            ? 1
            : a.label.localeCompare(b.label),
    );
  }, [catalog, value, deviceZone]);
  const abbreviations = new Set(
    choices.flatMap((choice) => choice.abbreviations),
  );
  const normalizedQuery = normalize(query);
  const terms = normalizedQuery.split(/\s+/).filter(Boolean);
  const matches = choices.filter((choice) =>
    terms.every((term) =>
      abbreviations.has(term)
        ? choice.search.split(" ").includes(term)
        : choice.search.includes(term),
    ),
  );
  if (terms.length) {
    const rank = (choice: (typeof choices)[number]) =>
      choice.names.includes(normalizedQuery) ? 0 : 1;
    matches.sort((a, b) => rank(a) - rank(b));
  }
  const selected = choices.find((choice) => choice.zone === value)!;
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
        Search by city, region or name, such as New York, Eastern Time or EST.
        Then choose a timezone below.
      </small>
      <label htmlFor={`${id}-zone`}>Timezone</label>
      <select
        id={`${id}-zone`}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {selectedOutsideSearch && (
          <optgroup label="Current selection">
            <option value={value}>{selected.label}</option>
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
      <small>
        Selected: {selected.label}. Daylight-saving changes apply automatically
        where observed.
      </small>
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
