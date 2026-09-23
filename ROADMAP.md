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

## Focused investigation MCP tools

Extend the worker beyond implementation with compact, evidence-backed investigations. The orchestrator should receive answers and findings rather than repeated source dumps. This section proposes an API; these tools are not implemented yet.

| Tool | Intended result | Execution boundary |
| --- | --- | --- |
| `worker_explore` | Answer to a specific question, relevant symbols, and `file:line` evidence | Inspect without modifying code |
| `worker_diagnose` | Reproduction attempt and outcome, evidenced cause or explicitly labeled hypotheses, and the smallest proposed fix | Diagnose without applying the fix; disclose reproduction side effects |
| `worker_verify` | Check outcomes, failures, and relevant bounded log excerpts; full logs remain in artifact files | Execute specified or configured checks; no implementation edits |
| `worker_review` | Diff findings with severity, location, reason, and evidence; no full diff echo | Read-only review against an explicit base and change scope |

`worker_review` implements the preliminary review role described above. It must remain distinct from `worker_record_review`, which records the external orchestrator's or human's acceptance decision.

### Shared result contract

- Default response budget: approximately 800 tokens, configurable per request and enforced by the application rather than only by prompting the model. Define the tokenizer or documented conservative counting method. Budget the model-visible payload, avoiding duplicate full reports in text and structured content.
- Return the answer and highest-priority findings first, plus `run_id`, repository-state reference, freshness, and whether details were omitted. Preserve complete evidence records in stored artifacts; do not truncate JSON or cut citations in half.
- Separate facts, hypotheses, and unverified parts. Facts cite observed source or command output. Hypotheses cite their supporting observations and state the missing confirmation. Unverified parts identify what was not inspected or executed and why; they must not acquire fabricated evidence.
- Give evidence stable IDs. Source evidence includes repository-relative path, symbol where useful, line/range, and the referenced file revision or content hash. Execution evidence includes command, working directory, exit status, and log artifact/range.
- Treat snippets and conclusions as evidence about a particular observed state, not a guarantee of correctness. An unsuccessful reproduction must not be presented as a confirmed cause.

### Follow-up and repository state

Allow a follow-up question on the same operation using its `run_id`, retaining the underlying worker session and previously gathered evidence. Do not repurpose implementation-only `worker_continue` for this without defining operation-aware behavior. Reuse relevant context instead of rereading unchanged files; read new material when the follow-up requires it.

Bind investigations to canonical worktree identity, HEAD, index state, tracked working-tree content, and relevant untracked content. HEAD alone is insufficient. For ignored files, external dependencies, runtime configuration, or environment state that affected a finding, record the observed inputs or explicitly state the limits of freshness detection.

Check state before returning a cached conclusion or answering a follow-up. Mark changed-state findings `stale`, retain their original evidence, and identify what changed when possible. Revalidate affected findings against a new state rather than silently relabeling the old conclusions as current. If the repository changes during an investigation, report the mixed-state limitation or retry against a stable snapshot; before/after fingerprints alone are not full snapshot isolation.

### Compact status and on-demand detail

For these operations, return a run ID promptly and use status/result retrieval for long work. Keep `worker_status` limited to state, operation, progress if known, freshness, timestamps, and a result reference. Do not attach the complete report to each status response.

Extend `worker_result` with explicit summary/detail selection and bounded evidence or log-range retrieval. Default to the compact summary; obtain longer excerpts or a particular finding only when requested. Keep complete logs in private artifact files outside the target worktree so logging does not itself invalidate repository state. Remote artifact references must be retrievable through the server, not merely local paths on another machine.

### Mutation and concurrency

Explore/review operations should not edit source or run mutating checks. Verification and reproduction commands can write caches, build outputs, snapshots, or fixtures despite having no intent to change implementation. Reuse the write lock for such execution, record before/after state, and do not advertise it as unconditionally read-only. Define whether pure investigations use a stable snapshot or wait for an active writer before permitting concurrent reads.

### Example acceptance scenario

Request: “Explore the keypress → PTY → rendering path. Identify sources of latency. Change nothing.”

If inspection actually finds a 250 ms polling interval, return the relevant symbol and real `file:line` evidence, explain where polling occurs, and distinguish that observed interval from an unmeasured claim about end-to-end latency. A follow-up such as “Is that on the input path or only output refresh?” should reuse the investigation. Editing the relevant file must make the old conclusion stale. The summary must fit its output budget without copying thousands of tokens of source.

## Suggested order

1. Implement only `worker_explore` as the first MCP extension, in read-only mode: a focused answer with source evidence, bounded output, follow-up questions, and repository-state freshness. Prevent write-capable tools and mutating commands; a prompt saying “do not edit” alone is not a read-only boundary. Defer `worker_diagnose`, `worker_verify`, and `worker_review` until this workflow is validated.
2. Repository check discovery and baseline results, then diagnosis, verification, and the basic review agent using the established result contract.
3. Task-aware routing: use accumulated outcomes to evaluate selection quality.
4. Remote execution: prototype transport and choose a synchronization contract before adding automatic change transfer.

## Feedback from an implementation task

User-reported experience; the underlying task and its results were not independently inspected here. Model, profile, cost, and run IDs were not supplied, so this is qualitative feedback rather than an experiment result.

- The worker found two real output-reading bugs, implemented fixes and regression tests, and responded to specific review feedback.
- Initial completion was premature: daemon lifecycle and concurrency issues were missed. After the second round, the TypeScript build was not checked and failed.
- Three implementation/review rounds were needed. The external reviewer independently verified final tests and build.
- One MCP call timed out after 300 seconds; its result was subsequently available through status. This observation does not establish whether cancellation reached the worker.
- Review submission rejected `approved` in favor of `accepted`, but the tool description did not list allowed verdicts. The tool and field descriptions now list them, and validation errors explain the accepted values.

Follow-up priorities:

1. Define completion evidence from repository checks: report exact commands and outcomes, explicitly distinguish tests from build/type checks, and identify skipped validation. A worker's completion report must not imply external acceptance.
2. Add focused review criteria for process lifecycle and concurrency: startup/shutdown, cancellation, ownership, lock lifetime, cleanup, and overlapping calls.
3. Explore asynchronous MCP execution that returns a run ID promptly, with explicit status/result retrieval and cancellation. Document timeout versus cancellation semantics so callers can recover results without accidentally submitting duplicate work.
4. Use externally verified outcomes and correction counts as routing feedback. One successful task after three rounds is not enough to rank models.

Current usage recommendation from this feedback: bounded implementation tasks with precise acceptance criteria; independent review of concurrency and process management, plus independent final validation.
