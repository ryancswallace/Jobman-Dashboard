# Dashboard wording and terminology

The web and iPhone apps use the same terms for monitoring jobs. Labels explain
what the user is looking at; help text supplies context and error messages say
what happened and what to try next. Internal API field names remain unchanged.

## Shared terms

| Term | Meaning in Dashboard |
| --- | --- |
| Deployment | A Jobman Control installation connected to Dashboard. |
| Namespace | A group of jobs and related resources with shared access rules within one deployment. A namespace is not necessarily the same as a team. |
| Job status | The job's current stage, such as waiting, running, or finished. |
| Final result | How a finished job ended. A request to cancel is not proof that cancellation completed. |
| Requested state | What Jobman was asked to do; the observed job may not have reached that state yet. |
| Status confidence | How certain the source is about the reported status. It is separate from connectivity and the job's final result. |
| Run | One recorded execution attempt of a job. Historical run details are separate from the current job state. |
| Submitted command | The executable, arguments, and working directory recorded when the job was submitted. |
| Job groups | Collections, Slurm arrays, and dependency graphs that organize related jobs. |
| Target | An execution environment configured in Jobman Control. Target settings do not establish current connectivity or available capacity. |
| Target configuration version | The particular recorded target configuration associated with a job or displayed target. |
| Alert rule | Conditions that determine which future job events create alerts for the user. |
| Inbox | Previously recorded alerts the user is currently allowed to read. |

Keep Slurm, deployment, namespace, run, and target where they identify distinct
concepts; explain them near the relevant controls. Keep IDs, paths, commands,
unknown source values, and technical evidence available for investigation.
Do not rewrite job names or log content supplied by users or upstream systems.

## Copy conventions

- Use readable labels in navigation, headings, filters, accessibility labels,
  and errors. Avoid exposing codes such as `unsupported_contract` as a heading.
- Replace implementation terms such as bounded pages, source contributions,
  revision conflicts, and binding intent with the user-visible effect.
- Distinguish unavailable data from empty results and from zero counts. Explain
  partial results and how to retry without implying that missing jobs succeeded.
- Say whether a view describes the current job, a selected historical run, or
  a saved report. Avoid implying that a saved report updates with the job.
- Explain when logs are omitted or shortened, and when an output file listing
  describes metadata rather than downloadable file contents.
- Give a useful next step: refresh a list, reopen settings, check private network
  or VPN access, or contact the administrator with the request ID.
- Keep access errors ambiguous about whether an inaccessible item exists. Never
  put command contents, log text, credentials, or internal exception details into
  error messages.
- Use administrator for the person managing the Dashboard installation. Preserve
  explicit recovery choices when disabling alerts or removing devices is still
  possible after a capacity or rate limit.

## Review coverage

The wording review covers web and iPhone connection/sign-in, navigation and scope
selection, overview, job lists/details, run selection, submitted commands, logs,
output files, targets/partitions, job groups/dependencies, diagnosis reports,
alert rules, inbox/device settings, loading/empty/partial states, and errors.
Shared backend error messages are reviewed because both clients display them.

Existing behavior, authorization, API names, source values, and monitoring-only
limits remain the contract. Unit, accessibility, and simulator checks establish
client behavior; they do not replace corporate identity, physical-device,
background push, or manual assistive-technology acceptance.
