import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { jobStatus, statusConfidence, timestamp, title } from "../lib/format";

export function SelectedRunFacts({
  run,
  timezone,
}: {
  run: Wire.JobRun;
  timezone: string;
}) {
  return (
    <section className="panel wide" aria-labelledby="selected-run-heading">
      <div className="panel-heading">
        <h2 id="selected-run-heading">
          Selected run details · run {run.number}
        </h2>
      </div>
      <p className="panel-note">
        These facts describe the selected run. The status, timeline, and
        scheduler details below describe the current job, which may have moved
        on to a later run. Record dates show when this run’s information was
        saved, not when it started or finished.
      </p>
      <dl className="facts">
        {[
          ["Run ID", run.id],
          ["Run status", jobStatus(run.phase)],
          ["Run final result", title(run.outcome)],
          ["Run requested state", title(run.desiredState)],
          ["Execution ID", run.executionId],
          ["Execution phase", title(run.executionPhase)],
          ["Status confidence", statusConfidence(run.confidence)],
          ["Target ID", run.targetId],
          ["Target configuration ID", run.targetGenerationId],
          ["Execution system", run.backend],
          ["Run record created", timestamp(run.createdAt, timezone)],
          ["Run record updated", timestamp(run.updatedAt, timezone)],
        ].map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value ?? "Unavailable"}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
