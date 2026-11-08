import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import type { Presentation } from "../../../contracts/typescript/dashboard.generated";
import { applyTheme, validateTheme } from "./presentationTheme";
export const defaultPresentation: Presentation = {
  schemaVersion: 1,
  productName: "Jobman Dashboard",
  organizationName: "",
  authLabel: "Sign in with your organization",
  headerText: "",
  footerText: "Private workspace",
  logoPath: "/logo.svg",
  logoDarkPath: "/logo-transparent-dashboard.svg",
  helpLinks: [],
  notices: [],
  contentLinkScheme: "jobman-dashboard",
};
export function contentScheme(value: string): string {
  if (
    !/^[a-z][a-z0-9.-]{2,79}$/.test(value) ||
    value.includes("..") ||
    value.endsWith(".") ||
    !(
      value === "jobman-dashboard" ||
      value === "jobman-dashboard-auth" ||
      value.includes(".")
    )
  )
    throw new Error("Invalid content link scheme");
  return value;
}
const text = (v: unknown, max: number) =>
  typeof v === "string" &&
  new TextEncoder().encode(v).length <= max &&
  v.trim() === v &&
  !/[\u0000-\u001f\u007f-\u009f]/.test(v);
const asset = (v: unknown) =>
  typeof v === "string" &&
  v.length <= 256 &&
  /^\/(?!\/)[a-zA-Z0-9_./-]+\.(svg|png|webp)$/.test(v) &&
  !v.split("/").some((p) => p === "." || p === "..");
export function decodePresentation(raw: unknown): Presentation {
  const v = raw as Presentation;
  if (
    !v ||
    v.schemaVersion !== 1 ||
    !text(v.productName, 120) ||
    !v.productName ||
    !text(v.authLabel, 120) ||
    !v.authLabel ||
    !text(v.organizationName, 200) ||
    !text(v.headerText, 1000) ||
    !text(v.footerText, 1000) ||
    !asset(v.logoPath) ||
    !asset(v.logoDarkPath) ||
    !Array.isArray(v.helpLinks) ||
    v.helpLinks.length > 8 ||
    !Array.isArray(v.notices) ||
    v.notices.length > 8
  )
    throw new Error("Invalid presentation");
  contentScheme(v.contentLinkScheme);
  validateTheme(v.theme);
  if (new Set(v.notices.map((n) => n.id)).size !== v.notices.length)
    throw new Error("Duplicate notice");
  for (const n of v.notices)
    if (
      !text(n.id, 64) ||
      !n.id ||
      !text(n.text, 4000) ||
      !n.text ||
      !["info", "warning"].includes(n.severity)
    )
      throw new Error("Invalid notice");
  for (const l of v.helpLinks) {
    if (!text(l.label, 120) || !l.label || !text(l.url, 2048))
      throw new Error("Invalid help link");
    const u = new URL(l.url);
    if (u.protocol !== "https:" || u.username || u.password)
      throw new Error("Invalid help link");
  }
  return v;
}
const Context = createContext(defaultPresentation);
async function readPresentation(response: Response): Promise<Presentation> {
  if (
    !response.ok ||
    !response.headers.get("Content-Type")?.includes("application/json") ||
    !response.body
  )
    throw new Error("Unavailable presentation");
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  const parts: string[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > 512 * 1024) throw new Error("Presentation too large");
      parts.push(decoder.decode(value, { stream: true }));
    }
    parts.push(decoder.decode());
    return decodePresentation(JSON.parse(parts.join("")));
  } finally {
    void reader.cancel().catch(() => {});
  }
}
export function PresentationProvider({ children }: { children: ReactNode }) {
  const [value, setValue] = useState(defaultPresentation);
  useEffect(() => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 10000);
    void fetch("/presentation", {
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      signal: controller.signal,
    })
      .then(readPresentation)
      .then((v) => {
        if (!controller.signal.aborted) setValue(v);
      })
      .catch(() => {})
      .finally(() => clearTimeout(timer));
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, []);
  useEffect(() => {
    document.title = value.productName;
  }, [value.productName]);
  useEffect(() => applyTheme(value.theme), [value.theme]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export const usePresentation = () => useContext(Context);
export function DeploymentNotices() {
  const p = usePresentation();
  return (
    <>
      {p.headerText && <p className="deployment-heading">{p.headerText}</p>}
      {p.notices.map((n) => (
        <div
          key={n.id}
          className={`notice ${n.severity === "warning" ? "warning" : "deployment-notice"}`}
          role="note"
        >
          {n.text}
        </div>
      ))}
    </>
  );
}
export function DeploymentHelp() {
  const p = usePresentation();
  return (
    <nav aria-label="Organization help">
      {p.helpLinks.map((l) => (
        <a key={l.url + l.label} href={l.url} rel="noreferrer">
          {l.label}
        </a>
      ))}
    </nav>
  );
}
