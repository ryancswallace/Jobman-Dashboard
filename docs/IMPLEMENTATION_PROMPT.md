Implement Jobman Dashboard through its complete initial release, using the documented requirements and design. Begin implementation now and take responsibility for coding, integration, verification, GitHub delivery, release preparation, and fixing failures.

## Adopted scope amendment — November 8, 2026

Ryan considers all of his review work complete. Address agent reliability for the
first production release and postpone all other unfinished work to subsequent
releases. Ryan accepts that some components will not work immediately. Follow
[FIRST_RELEASE_SCOPE.md](FIRST_RELEASE_SCOPE.md); it supersedes conflicting
original scope/completion language below. Keep known limitations factual and do
not present the postponed work as outstanding first-release acceptance.


## Source of truth and scope

Read these documents in full:

- /Users/rcw/home/code/jobman-dashboard/docs/REQUIREMENTS.md
- /Users/rcw/home/code/jobman-dashboard/docs/DESIGN.md

Retain R01–R22 and WP01–WP15 as the full product roadmap. Deliver the approved implemented baseline and agent reliability correction for the first production release; schedule every other unfinished item afterward. Use the documented engineering proposals as the starting design. Make routine implementation decisions yourself. If evidence supports a better technical approach that preserves the requirements, document the reason and update the design. Ask before materially changing product scope, security policy, or deployment commitments. The November 8 deferrals are explicit user instructions; record their accepted limitations without reopening review.

The authorized repositories are:

- /Users/rcw/home/code/jobman-dashboard — ryancswallace/Jobman-Dashboard
- /Users/rcw/home/code/jobman — ryancswallace/Jobman
- /Users/rcw/home/code/jobman-control — ryancswallace/Jobman-Control
- /Users/rcw/home/code/jobman-diagnose — ryancswallace/Jobman-Diagnose
- /Users/rcw/home/code/jobman-lab — ryancswallace/Jobman-Lab

Work across these repositories as necessary. Read their applicable AGENTS.md files, inspect current code, remotes, branches, and working changes, and preserve unrelated work. Recheck the implementation rather than relying solely on the design's historical source review. If a required directory is outside the writable workspace, request the specific additional access and continue independent work; this prompt does not bypass runtime permissions.

## Autonomy and continuity

Create a persistent goal for the complete implementation and validated initial release. I am not setting an artificial time or token budget in this prompt; respect account and runtime limits. Continue through implementation, tests, review, integration, and repairs. Do not stop after a plan, scaffold, demonstration, individual milestone, or first passing test suite.

Use subagents where useful for independent implementation, investigation, testing, and review. You retain responsibility for contracts, integration, verification, and the final result. Establish ownership of shared files and use isolated worktrees where needed. Use subagents within this task; do not create separate user-owned chats or scheduled automations unless I subsequently request them.

Maintain docs/IMPLEMENTATION_STATUS.md with each requirement/work package's status, decisions, tested commit hashes, PRs, CI runs, Lab evidence, and external dependencies. Keep a concise next-action list so work continues coherently after interruptions or context compaction. Update requirements/design/runbooks when implementation decisions change. Give brief progress updates focused on results, unresolved issues, and the next significant step.

Ask concise questions only for missing information or decisions that materially affect the result. Continue all useful independent work while waiting. Do not repeatedly request permission for actions explicitly authorized below.

## GitHub, CI, and releases

I explicitly authorize you, for the five repositories listed above, to:

- Create branches and worktrees; make scoped commits; push branches and subsequent fixes to their verified GitHub origins.
- Open and update pull requests, write technical PR descriptions/comments, address review findings, resolve merge conflicts, and attach the PRs to this task.
- Add or update necessary GitHub Actions workflows and enable the required build/test workflows; trigger, monitor, and rerun CI; inspect failures and artifacts; fix failures and rerun the affected checks.
- Merge your implementation PRs after applicable checks pass and an independent review has no unresolved blocking findings. Respect existing branch protections and approval requirements; do not bypass or weaken them.
- Use the existing release/versioning workflows, including tags, GitHub releases, versioned Go dependencies, packages, and container artifacts necessary for this work. This includes normal release automation triggered by authorized merges. Inspect those workflows before triggering them so publication and deployment effects are understood.
- Publish clearly labeled Dashboard prereleases/release candidates as appropriate. Use the approved November 8 scope when describing the first production release. Deferred external integrations do not require retaining a candidate label; published artifacts must still accurately identify their source, version and verification scope.

Keep existing repository visibility and access policy. Use the established versioning conventions, preserve published tags, and avoid force-pushing shared/protected branches. Push only task-related changes. Do not use local sibling-module replacements as a substitute for a reproducible release; publish and consume compatible dependencies in the correct order.

Use existing authenticated GitHub tooling. Never print, commit, or upload credentials, private keys, private configuration, production data, or raw sensitive logs. CI artifacts and release packages must be secret-free. This authorization covers repository collaboration and delivery, not unrelated email/Slack messages, organization-wide settings, or production-deployment workflows.

## Jobman-Lab authorization

Read /Users/rcw/home/code/jobman-lab/README.md and inspect the Lab's scripts, configuration, current state, and available host resources before operating it.

I authorize you to use and extend this isolated synthetic Lab for implementation and acceptance testing, including:

- Build and deploy the Jobman, Control, Diagnose, and Dashboard revisions under test. Configure the Lab to use the intended source/worktree paths and record exact revisions.
- Start, resume, provision, converge, restart, and gracefully stop the Lab's VMs and services using its documented workflows, including appropriate make targets such as status, up-core, up-full, build-products, converge, configure-control, enroll, and test.
- Use the Lab's SSH/WinRM and provisioning mechanisms to administer its guests. Apply test migrations, configure test services, and create synthetic identities, namespaces, role assignments, jobs, collections, arrays, graphs, logs, and reports.
- Submit, cancel, terminate, and clean up synthetic test workloads through test/operator tools when necessary to exercise behavior. These testing permissions do not add job-control features to Dashboard.
- Add reproducible Dashboard provisioning, a second isolated Control deployment, directory/group fixtures, log-reader ACLs, and integration tests needed to cover the design.
- Run bounded load, failure, restart, replay, revocation, upgrade, and backup/restore exercises. Create/reset/delete disposable resources belonging to these tests after retaining useful diagnostic evidence.

You may inspect the synthetic Lab workload commands, logs, artifacts, and diagnostic evidence needed to validate these behaviors. This permission is limited to synthetic test data and does not extend to unrelated developer or production data.

Preserve the Lab's isolation: synthetic identities/data, lab-prefixed resources, shared-NAT egress, and its host-only network. Do not bridge it onto the organization's network, expose it publicly, or affect unrelated VMs. Preserve existing Lab state that is not disposable task data. Ask before full-Lab destruction, clean-generated, deletion of pre-existing evidence/state, or disruptive host-wide networking changes; first prepare the exact scope and recovery plan.

Programs may load and use the Lab's generated credentials and keys through its normal mechanisms for these tests, without displaying their values. This is explicit authorization to use the necessary synthetic Lab credentials despite general repository restrictions on reading developer secrets. It does not authorize inspecting unrelated or production secrets.

The current Lab uses Keycloak test OIDC, not AD FS. Record what Lab tests prove and what still needs actual AD FS/direct-AD validation. Similarly, simulated notification delivery and simulator tests do not establish real APNs delivery or company-managed iPhone acceptance.

## Implementation and verification expectations

Preserve the implemented web and native iPhone baseline and make the agent reliability correction. Other unfinished application/upstream work follows the approved post-release scope. Preserve the monitoring-only product boundary, direct AD role unions and active-session revocation, source-qualified authorization, multi-Control aggregation, safe cross-user NFS/local log reads, arrays/collections/graphs, real deterministic diagnosis, every agreed alert scope/outcome, and durable notification history.

Implement real end-to-end behavior. Test doubles and fixtures support development but cannot replace required integrations. Keep public contracts versioned, migrations forward-only, and existing supported behavior compatible. Bound queries, memory, concurrency, file reads, queues, and retries.

Install needed project-local dependencies and verified development/Lab artifacts using the repositories' conventions. Run servers, browsers, Xcode builds and simulators, database/container tests, and Lab workloads as needed. Inspect rendered web/native interfaces and fix usability, accessibility, and error-state problems. Prepare development builds for my personal test iPhone and coordinate physical-device steps with me when needed.

Run meaningful focused checks during iteration, then the relevant full repository, cross-repository, CI, and release gates. Use the design's T01–T12 groups to identify applicable automated regression checks; the postponed integration/manual acceptance groups remain later-release work. Use independent review for authorization, migrations, source isolation, log access, event replay/deduplication, diagnosis validation, and release readiness. Fix regressions and actionable findings; do not disable tests or protections to obtain a green result. Record exact failures, skips, and unverified scope.

## External inputs and approval boundaries

Confirmed: you and I are developing together, with you performing most hands-on work. Production phones are company-managed; I can test on an unmanaged personal iPhone. APNs connectivity/ownership and the internal distribution/signing channel remain unknown. Pilot timing, production hosting allocation, and ongoing operating ownership remain unspecified.

Postpone the unfinished work associated with those inputs to later releases; do not request them as first-release prerequisites. Do not invent credentials, signing entitlements, network permission, operating ownership, or successful physical-device tests. Do not weaken authentication or substitute Keycloak for the selected production AD FS integration to avoid a blocker.

Before spending money, accepting account/license agreements, enrolling in an Apple program, changing production AD/AD FS/MDM/network systems, deploying or migrating production, or distributing the app to company users, prepare the artifacts, validation results, exact proposed action, and recovery plan, then obtain the necessary approval. Existing GitHub CI/release operations are authorized above; new paid capacity or increased spending limits require approval. Respect tool/platform approval requirements.

## Definition of done

The first production release uses the accepted implemented baseline plus the agent reliability correction, with applicable code checks and accurate artifact provenance. Ryan's review is complete. Unfinished R01–R22/WP01–WP15 work, corporate AD FS, real APNs, managed-device delivery, further manual acceptance, stable Diagnose and operations expansion are post-release follow-up, not completion conditions for this release.

Describe unavailable or unverified components as accepted first-release limitations and record their later-release follow-up. Do not report the first release as externally blocked by work Ryan explicitly postponed. Preserve accurate status and evidence: accepted deferral does not mean a test passed, an integration works, or an unpublished artifact has been released.

The final handoff must include versions/commit hashes, PR and CI links, release artifacts, installation and test commands, requirement coverage, Lab and device evidence, known limitations, and any remaining production approvals. Start by inspecting the repositories and Lab, establishing the durable status tracker, and implementing the first dependency-complete slice. Proceed without an additional general go-ahead.
