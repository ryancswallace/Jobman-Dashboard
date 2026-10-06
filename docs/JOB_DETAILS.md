# Job details and submitted commands

The web job Details tab and native iPhone job screen show the shared Jobman
status facts: source and namespace, job ID and name, phase and terminal outcome,
desired state, revision and timestamps, observation confidence and its update
time, workload digest, target name and ID, partition, target generation, backend,
and scheduler observations. Missing observations stay unavailable.

The execution specification adds the full submitted executable, ordered
arguments, and working directory. It is the immutable specification recorded by
Control, not proof that the job started or that a shell expanded its arguments.
The submitted working directory may be a logical path such as `workspace:/work`;
it is not presented as the resolved operating-system directory.
A shell wrapper and its script remain literal arguments when that is how the job
was submitted. Environment values are excluded.

The web screen provides a complete JSON argument vector and an explicit copy
button. The native command screen provides the full indexed arguments and an
explicit JSON-vector copy action. Empty arguments, spaces, line breaks, quoting,
and Unicode retain their argument boundaries. This representation is not a
universal shell command; Dashboard never executes it. Copying is a user action,
not an automatic export. Commands may contain sensitive values supplied by the
submitter, so namespace job-read access also controls their visibility.

Selecting a historical run shows that run's reported facts separately from the
current job. The submitted specification belongs to the immutable job and does
not change when selecting a run. Run creation/update timestamps are not inferred
execution start/completion times. Standalone local process details and exit facts
not reported by Control are not fabricated.

## Source compatibility and bounds

Control advertises `job-execution-detail` and adds `spec.execution` only to its
authorized single-job GET. Dashboard exposes that projection only as `execution`
on its single-job response. Job lists, group children, durable pagination
snapshots, events, notifications, and diagnostics contain no command projection.
Both services recheck the existing namespace-scoped `jobs.read` authority.

Older Control services remain usable and show that command details are
unsupported. New sources report missing, unsupported, or oversized specifications
explicitly; none is shown as an empty command. The projection is bounded to 2 MiB
of encoded JSON, 4,096 arguments, and 65,536 UTF-8 bytes per executable, argument,
or working directory. Oversized specifications are not silently truncated.
No migration or agent/CLI update is required. Upgrade Control before expecting
command detail from a source, then upgrade Dashboard and its clients.

API responses remain `no-store`; command data is not added to persistent client
storage, URLs, application logs, or push payloads. Text rendering does not execute
markup or follow command contents as links. Existing source-qualified identity,
revocation, and account-change clearing rules also apply to these details.
