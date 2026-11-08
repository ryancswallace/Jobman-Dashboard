import { contentScheme } from "./presentation";
import type { JobRef } from "./models";
function component(value: string) {
  if (
    !value ||
    value.length > 256 ||
    /[\\/\s?#%]/.test(value) ||
    value === "." ||
    value === ".."
  )
    throw new Error("Invalid private link identity");
  return encodeURIComponent(value);
}
/** Content links carry IDs only; the native application must authorize the destination. */
export const nativeJobLink = (job: JobRef, scheme = "jobman-dashboard") =>
  `${contentScheme(scheme)}://job/${component(job.deploymentId)}/${component(job.namespaceId)}/${component(job.jobId)}`;
export const nativeInboxLink = (id: string, scheme = "jobman-dashboard") =>
  `${contentScheme(scheme)}://inbox/${component(id)}`;
