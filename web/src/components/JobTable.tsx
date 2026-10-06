import { jobStatus, statusConfidence } from "../lib/format";
import { Link, useLocation } from "react-router-dom";
import type { Job } from "../lib/models";
import {
  jobRoute,
  relative,
  sourceLabel,
  timestamp,
  title,
} from "../lib/format";
import { Status } from "./States";
import { useSession } from "../lib/session";
export function JobTable({ jobs }: { jobs: Job[] }) {
  const { bootstrap } = useSession(),
    location = useLocation();
  return (
    <div className="table-wrap">
      <table>
        <caption className="sr-only">
          Jobs in your selected deployments and namespaces
        </caption>
        <thead>
          <tr>
            <th>Job</th>
            <th>Deployment / namespace</th>
            <th>Status / result</th>
            <th>Status confidence</th>
            <th>Target</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((job) => (
            <tr key={`${job.deploymentId}:${job.namespaceId}:${job.jobId}`}>
              <td>
                <Link
                  className="job-name"
                  to={jobRoute(job)}
                  state={{
                    returnTo: location.pathname + location.search,
                    scrollY: window.scrollY,
                  }}
                >
                  {job.name || job.jobId}
                </Link>
                {job.name && (
                  <span className="secondary-line mono">{job.jobId}</span>
                )}
              </td>
              <td>
                {sourceLabel(
                  bootstrap.sources,
                  job.deploymentId,
                  job.namespaceId,
                )}
              </td>
              <td>
                <Status value={job.phase} label={jobStatus(job.phase)} />
                {job.outcome && (
                  <span className="secondary-line">{title(job.outcome)}</span>
                )}
              </td>
              <td>
                <Status
                  value={job.observationConfidence}
                  label={statusConfidence(job.observationConfidence)}
                />
              </td>
              <td>{job.target?.name ?? "Unavailable"}</td>
              <td>
                <time
                  dateTime={job.createdAt}
                  title={timestamp(
                    job.createdAt,
                    bootstrap.preferences.timezone,
                  )}
                >
                  {relative(job.createdAt)}
                </time>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
