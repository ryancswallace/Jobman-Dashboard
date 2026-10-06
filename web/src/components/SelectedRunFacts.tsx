import type * as Wire from "../../../contracts/typescript/dashboard.generated";
import { timestamp, title } from "../lib/format";

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
        <h2 id="selected-run-heading">Selected run facts · run {run.number}</h2>
      </div>
      <p className="panel-note">
        These facts describe the selected run. Current job status, timeline and
        scheduler observations below remain separate. Run record timestamps are
        not execution start or completion times.
      </p>
      <dl className="facts">
        {[
          ["Run ID", run.id],
          ["Run phase", title(run.phase)],
          ["Run outcome", title(run.outcome)],
          ["Run desired state", title(run.desiredState)],
          ["Execution ID", run.executionId],
          ["Execution phase", title(run.executionPhase)],
          ["Observation confidence", title(run.confidence)],
          ["Target ID", run.targetId],
          ["Target generation ID", run.targetGenerationId],
          ["Backend", run.backend],
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
