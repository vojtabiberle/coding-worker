# Ideas for future versions

These are proposed directions, not implementation commitments. Keep each feature optional and preserve explicit profile selection.

## Task-aware worker routing

Use a small, fast model to select the implementation profile best suited to a task. Consider the objective, relevant repository context, task complexity, available agents/models, and cost or latency preferences.

- Begin with recommendations or opt-in routing. Explicit profile overrides always win.
- Record the selected profile, routing rationale, router version, and routing cost separately from implementation cost.
- Feed external review verdicts, correction iterations, test outcomes, cost, and user overrides back into future routing decisions.
- Build repository-specific history so routing improves for the user's codebase and conventions. Start with stored examples and outcome statistics; model fine-tuning is not required.
- Compare routing against fixed-profile baselines through experiments. Avoid declaring a winner from a few easy tasks; retain exploration and account for task difficulty.

Open questions: which task features are useful, how much context the router needs, how to handle new repositories, and whether feedback should remain repository-local or transfer across projects.

## Remote execution

Allow the local orchestrator to delegate implementation to another machine. Support identical directory layouts or explicit local-to-remote path mappings.

- Explore an SSH-launched stdio MCP server first; consider authenticated network MCP transport if needed later.
- Treat path mapping and file synchronization as separate concerns. A matching path does not imply matching code or dependencies.
- Identify the repository, worktree, base commit, and initial dirty state before execution. Lock the remote worktree and coordinate local changes while work is in flight.
- Return results, metrics, and a reviewable change set with provenance. Detect conflicts before applying results locally; never silently overwrite local edits.
- Define behavior for disconnects, retries, cancellation, and recovering a run without executing it twice.

Synchronization remains undecided. Options to evaluate:

| Approach | Questions to resolve |
| --- | --- |
| Shared filesystem | Lock reliability, latency, and concurrent local edits |
| Remote Git checkout plus patches | Transferring staged/unstaged changes, untracked files, binaries, and returning changes without commits |
| Explicit workspace snapshots or file sync | Exclusions, deletion semantics, transfer size, secrets, and conflict detection |

Also decide how dependencies, toolchains, credentials, and ignored files become available remotely. A directory mapping alone cannot provide those prerequisites.

## Basic review agent

Add an optional reviewer for an initial pass over worker changes before handing them back to the orchestrator.

- Use a separate review context and configurable review profile.
- Review the task, acceptance criteria, relevant code, worker changes, and check results. Distinguish pre-existing changes from worker edits.
- Report actionable findings with file locations, severity, evidence, and suggested corrections.
- Keep review read-only by default. Any automatic correction loop should be opt-in and bounded by iteration and cost limits.
- Store reviewer identity, findings, runtime, and cost separately from implementation metrics.

The built-in reviewer provides preliminary feedback. The external orchestrator or human remains authoritative for acceptance. Its own review verdicts should not be treated as independent proof that its routing or implementation was successful.

## Repository check setup

Discover and propose a basic validation setup for each repository, then run the configured checks during implementation.

- Inspect existing documentation, manifests, scripts, and CI configuration before proposing new commands.
- Reuse the repository's formatter, linter, type checker, build, and test commands where available.
- Provide a small repository-level check configuration with working directories, timeouts, prerequisites, and optional focused versus full check sets.
- Run a baseline to distinguish existing failures from regressions introduced by the worker.
- Record commands, exit codes, duration, and bounded diagnostic output. Mark unavailable or skipped checks as unknown, not passed.
- Require explicit authorization for dependency installation, environment changes, or checks that need external services or credentials. Discovery should not automatically execute arbitrary repository scripts.

Open questions: how much command detection can be reliable, when checks should run, and whether initial setup should only propose configuration or also write it after approval.

## Suggested order

1. Repository check discovery and baseline results: improve the quality of feedback first.
2. Optional basic review agent: add another source of actionable feedback.
3. Task-aware routing: use accumulated outcomes to evaluate selection quality.
4. Remote execution: prototype transport and choose a synchronization contract before adding automatic change transfer.
