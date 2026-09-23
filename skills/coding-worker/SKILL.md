---
name: coding-worker
description: Delegate bounded implementation tasks through the coding-worker MCP server, review resulting changes independently, and manage correction rounds and external acceptance. Use when the user requests coding-worker or OpenCode-backed implementation delegation; not for configuring OpenCode or for unrelated coding tasks.
---

# Coding worker orchestration

Use coding-worker for implementation. Keep architecture, scope decisions, review, and acceptance with the calling orchestrator. A completed worker run is not an accepted solution.

## Prepare the task

- Discover the available coding-worker MCP tools and read their current schemas. Tool prefixes depend on the client. If unavailable, report the missing connection; do not pretend delegation occurred. Use `workerctl` as a fallback only when available and suitable for the user's request.
- Resolve the requested repository/worktree to an absolute path. MCP requests require explicit `cwd`; the server's working directory is irrelevant.
- Inspect repository instructions and initial Git status. Preserve pre-existing user changes and note them for later attribution.
- Identify relevant test, build, type-check, and lint commands from repository scripts or CI. Choose checks appropriate to the change; for TypeScript changes, passing tests alone does not establish that the build/type check passes.
- Bound the objective and acceptance criteria. Include relevant files, interface constraints, known failures, and review concerns without copying the entire repository into the request.
- Omit `profile` unless the user selected one or the task explicitly requires an override. Let repository/global configuration or an active experiment choose the model.

## Delegate and track

Call `worker_implement` with `cwd`, `objective`, and applicable `constraints`, `acceptance_criteria`, and `relevant_context`. Include the selected validation commands in the criteria and request their actual results, including skipped checks.

Retain the returned `run_id` for status, correction, and review. Use `worker_status` or `worker_result` to inspect progress/results without fetching raw logs by default.

- A physical worktree allows one writer. On `busy`, inspect known active work and retry only after it finishes. Do not delete locks or start a competing local implementation.
- Different existing worktrees can execute independently when parallel delegation is in scope. Do not create extra branches/worktrees merely because the tools permit it.
- While a worker writes, avoid your own edits and validation commands that mutate that same worktree.
- Timeout is not proof of failure, cancellation, or success. If a run ID is known, retrieve status/result before resubmitting. If no ID was returned, use `workerctl sessions` when available and correlate workspace, time, and state; do not guess among ambiguous runs. Do not blindly repeat a potentially active implementation.
- A cancelled or failed run may leave useful partial edits. Inspect its state and changes before deciding whether to resume or start another run. The current API has no dedicated cancellation tool; do not invent one.

## Independently review and validate

After execution finishes, inspect the actual diff and relevant surrounding code against the objective and acceptance criteria. Snapshots include the entire dirty worktree against HEAD, so not every changed line belongs to this worker.

Run the relevant validation independently using the final code. Report exact commands and outcomes; keep test, build, and type-check results distinct. Explain unavailable or skipped checks rather than treating them as passed. Revalidate affected checks after corrections; old passing results do not validate newer edits.

For changes involving daemons, subprocesses, or concurrency, specifically examine applicable lifecycle risks: startup/readiness, shutdown and cancellation, process ownership, overlapping calls, lock lifetime, stream completion, cleanup, and error paths. Require evidence for relevant behavior; do not expand an unrelated task into a general concurrency audit.

Worker reports and successful exit codes are evidence, not authoritative acceptance. Distinguish implementation failures, pre-existing failures, and environment limitations. Do not infer test success from an arbitrary successful shell command or invent token/cost measurements.

## Corrections and acceptance

Record `changes_requested` when actionable findings remain, then call `worker_continue` with the same `run_id`, concrete feedback, and any additional acceptance criteria. Describe observed versus expected behavior and relevant locations; preserve the original scope. Continuation retains the original profile and OpenCode context.

Continuation rejects repository drift. Review without editing where possible; validation can also modify generated or untracked files. If external changes prevent resumption, inspect the drift and start a new run with current context when appropriate. Never reset, delete, or revert user changes merely to satisfy the drift check.

Keep correction rounds proportional to the task. If a specific failure persists after two targeted correction attempts, reassess the cause, task boundaries, or implementation approach before another worker call. Do not repeat identical feedback indefinitely; surface a concrete blocker when progress requires user input.

Use `worker_record_review` for the latest finished iteration with:

- `run_id` and `verdict`: exactly `accepted`, `changes_requested`, or `rejected`; **not `approved`**.
- `reviewer`: identify the external orchestrator or human accurately.
- `blocker`, `major`, `minor`: counts of actual findings, and `notes` describing the evidence and remaining limitations.
- Optional `tests_passed`, `hidden_e2e_success`, and `human_intervention`: supply only observed outcomes; omit unknown values. Inspect the current schema for any other required fields.

Record `accepted` only after independently verifying the applicable acceptance criteria; reserve `rejected` for work deemed unacceptable rather than an ordinary correction request. If validation is blocked, explain the limitation and do not fabricate acceptance or a rejection finding.

Finish with the implementation outcome, independent checks, remaining issues, and run ID. Commit, push, or deploy only within the user's authorized scope; this skill does not authorize those actions.
