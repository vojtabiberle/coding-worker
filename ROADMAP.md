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

## Review integration

- Optionally run a reviewer after implementation using a separately configured review profile.
- Attribute changes to the worker versus pre-existing dirty state; incorporate task acceptance criteria and check outcomes.
- Add branch/base-ref selection beyond the current worktree-versus-HEAD scope.
- Keep automatic correction loops opt-in and bounded by iteration and cost limits.
- Keep automated findings distinct from external acceptance and independent routing feedback.

## Repository check setup

Discover and propose a basic validation setup for each repository, then run the configured checks during implementation.

- Inspect existing documentation, manifests, scripts, and CI configuration before proposing new commands.
- Reuse the repository's formatter, linter, type checker, build, and test commands where available.
- Provide a small repository-level check configuration with working directories, timeouts, prerequisites, and optional focused versus full check sets.
- Run a baseline to distinguish existing failures from regressions introduced by the worker.
- Aggregate configured checks into separate test/build/type-check outcomes, including unavailable or skipped checks.
- Require explicit authorization for dependency installation, environment changes, or checks that need external services or credentials. Discovery should not automatically execute arbitrary repository scripts.

Open questions: how much command detection can be reliable, when checks should run, and whether initial setup should only propose configuration or also write it after approval.

## Investigation extensions

- Return run IDs before long work completes; add explicit cancellation and document timeout/recovery semantics to prevent duplicate work.
- Add stable evidence IDs and targeted artifact retrieval, including remotely retrievable artifacts.
- Identify which inputs changed when conclusions become stale, and selectively revalidate affected findings.
- Track relevant ignored dependencies and runtime environment inputs beyond cited source hashes.
- Investigate stable snapshots to avoid mixed-state reads during transient external edits; before/after fingerprints do not provide snapshot isolation.
- Support checks that need writable build output through isolated worktree copies, with explicit dependency/environment handling.
- Prioritize findings by severity before budgeted pagination.

## Suggested order

1. Repository check discovery/configuration and separate completion evidence for tests, builds, and type checks.
2. Review integration, change attribution, and branch/base-ref scope.
3. Asynchronous execution, cancellation, and recovery.
4. Task-aware routing evaluated against externally verified outcomes and correction counts.
5. Remote transport and synchronization contract before automatic change transfer.
