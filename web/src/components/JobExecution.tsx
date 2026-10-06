import { useState } from "react";
import type { Job } from "../lib/models";

export function JobExecution({ job }: { job: Job }) {
  const [copyStatus, setCopyStatus] = useState("");
  const execution = job.execution;
  const command = execution
    ? JSON.stringify(
        [execution.command.executable, ...execution.command.args],
        null,
        2,
      )
    : undefined;
  // JSON already escapes C0 controls. Escape remaining invisible controls in
  // the presentation, including UTF-16 surrogate pairs for supplementary text.
  // JSON parsing still reconstructs the original argument vector exactly.
  const displayedCommand = command?.replace(
    /[\p{Cf}\p{Zl}\p{Zp}\u007f-\u009f]/gu,
    (character) =>
      character
        .split("")
        .map((unit) => `\\u${unit.charCodeAt(0).toString(16).padStart(4, "0")}`)
        .join(""),
  );
  const unavailable =
    job.executionUnavailableReason === "unsupported"
      ? "This Control deployment does not provide the submitted command."
      : job.executionUnavailableReason === "too_large"
        ? "The submitted command exceeds the display limit. No partial command is shown."
        : job.executionUnavailableReason === "missing"
          ? "Control has no submitted command for this job."
          : "The submitted command is unavailable.";
  return (
    <section className="panel wide" aria-labelledby="execution-heading">
      <div className="panel-heading">
        <h2 id="execution-heading">Submitted command</h2>
        {command !== undefined && (
          <button
            className="button secondary"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(command);
                setCopyStatus("Command arguments copied as JSON.");
              } catch {
                setCopyStatus(
                  "Copy is unavailable. Select and copy the command below.",
                );
              }
            }}
          >
            Copy command as JSON
          </button>
        )}
      </div>
      {execution ? (
        <>
          <p className="panel-note">
            This is the command saved when the job was submitted. The first item
            is the program; the remaining items are its arguments, in order.
            JSON keeps spaces, empty arguments, and line breaks intact. This is
            not a command to paste directly into a terminal. Hidden
            text-formatting characters are shown as Unicode escapes; copying
            preserves the original values.
          </p>
          <div className="panel-body">
            <pre
              className="execution-command"
              tabIndex={0}
              aria-label="Submitted command and arguments"
            >
              <code>{displayedCommand}</code>
            </pre>
            {copyStatus && <p role="status">{copyStatus}</p>}
          </div>
          <dl className="facts">
            <div>
              <dt>Submitted working directory</dt>
              <dd>
                <code>{execution.workingDirectory || "Not specified"}</code>
              </dd>
            </div>
          </dl>
          <p className="panel-note">
            The folder requested at submission. A path such as workspace:/
            refers to the job’s workspace; the actual folder on the execution
            host is not shown.
          </p>
        </>
      ) : (
        <p className="panel-body muted">{unavailable}</p>
      )}
    </section>
  );
}
