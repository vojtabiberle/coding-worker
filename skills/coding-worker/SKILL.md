---
name: coding-worker
description: Use coding-worker MCP for bounded implementation, repository exploration, diagnosis, verification, and code review, with independent validation and external acceptance. Use when the user requests coding-worker or OpenCode-backed delegation; not for developing or configuring the worker itself.
---

# Coding worker orchestration

Select the coding-worker operation matching the task. Keep architecture, scope decisions, independent review, and acceptance with the calling orchestrator. A completed worker run is not an accepted solution.

## Prepare the task

- Discover the available coding-worker MCP tools and read their current schemas. Tool prefixes depend on the client. If unavailable, report the missing connection; do not pretend delegation occurred. Use `workerctl` as a fallback only when available and suitable for the user's request.
- Resolve the requested repository/worktree to an absolute path. MCP requests require explicit `cwd`; the server's working directory is irrelevant.
- Inspect repository instructions and initial Git status. Preserve pre-existing user changes and note them for later attribution.
- Identify relevant test, build, type-check, and lint commands from repository scripts or CI. Choose checks appropriate to the change; for TypeScript changes, passing tests alone does not establish that the build/type check passes.
- Bound the objective and acceptance criteria. Include relevant files, interface constraints, known failures, and review concerns without copying the entire repository into the request.
- Omit `profile` unless the user selected one or the task explicitly requires an override. Let repository/global configuration or an active experiment choose the model.

## Choose the operation

| Need | Tool | Returned evidence |
| --- | --- | --- |
| Implement a bounded change | `worker_implement` | Change summary, session, reported checks and metrics |
| Answer a repository question without edits | `worker_explore` | Answer and source-backed facts, hypotheses, unverified parts |
| Explain a bug and propose a minimal fix | `worker_diagnose` | Cause status, reproduction assessment, source/log evidence; no fix applied |
| Run a known check | `worker_verify` | Observed outcome, exit status, duration and log excerpt; no model |
| Inspect current changes | `worker_review` | Severity, reason and validated source/diff citations; no edits or tests |

Explore/diagnose/review start with absolute `cwd` and `question`, plus optional `profile`. Follow up through the same tool with `run_id` and `question`, omitting `cwd` and `profile`. `worker_continue` is implementation-only. Investigations use the configured profile outside implementation experiments. Review scope is working tree against HEAD including untracked files; no arbitrary base-ref support. Pre-existing dirty changes are included. `review.diff` citations use diff line numbers, especially for deleted code.

Diagnosis can take `reproduction_command` as explicit argv. Omit it for static diagnosis or to reuse prior observations on follow-up; supplying it runs a new attempt. Verification requires `command` argv and always starts a new run. These commands have a default 30-second timeout, maximum 120; no shell interpolation unless explicitly invoking a shell. A failed command or environment error does not prove the reported bug was reproduced. Without reproduction, diagnosis cause_status must be hypothesis or unverified and reproduced=false, even when individual source facts are certain.

These four tools require Linux/Bubblewrap. Model investigations have read/glob/grep access only. Reproduction/verification commands run with read-only host files, private temporary storage, minimal environment and no network. Checks writing repository build output may fail; use supported temporary output paths or report the limitation. Do not silently bypass the sandbox or install dependencies. Inference provider networking remains enabled.

## Retrieve evidence economically

- Reconnect after server upgrades to refresh tool schemas. Use `max_output_bytes`, replacing the unsupported old name `max_output_tokens`.
- Default `max_output_bytes` is a conservative 800 UTF-8 JSON **byte** budget, allowed 512–8192. Implementation results do not use this budget.
- `worker_status` returns state, operation, iteration count, timestamps, phase/last_progress and applicable freshness, without a report. Progress timestamps indicate phase transitions, not a heartbeat. It never reruns work.
- `worker_result` returns stored findings/summary. Use `detail:true` for quotes/hashes and `finding_offset` from `next_offset` while `more` is true. If an item cannot fit and the offset does not advance, increase the budget or request summary form.
- For diagnosis/verification logs, use `log:true` and one-based `log_offset`; continue with returned `next_offset`. Full retained logs stay in private files, capped at 8 MiB; `log_truncated` means output was lost. A shortened summary is separately marked `truncated`.
- `current` freshness covers recorded Git state and cited source hashes, not all ignored dependencies or runtime inputs. `stale`/`unknown` conclusions cannot support current acceptance; inspect drift and start a new run when appropriate. Do not relabel old evidence as current.
- Invalid source/artifact citations and invalid diagnosis claims share at most one automatic same-session correction; reproduction is not rerun. A second invalid report fails. If the repository is unchanged, send a corrective question through the same investigation tool/run_id to repair the rejected report without starting a new session. A failed run with current freshness still has an invalid report. This may add one model call. Errors name the offending citation and artifact. Diagnosis observation metadata is not log content: cite `reproduction.log` only when observations include `log_path`; otherwise describe the missing execution as unverified.
- Error text respects the response budget; `truncated:true` marks shortening. Increase `max_output_bytes` to retrieve more text, up to 8192; larger errors remain in local run records.
- Facts/hypotheses require checked citations, but quote validation does not validate reasoning. Review severity is high/medium/low. No findings is not proof of correctness. Verification `state` describes execution; `outcome` distinguishes passed/failed/timed_out/cancelled/start_failed, and passed only means exit 0.

## Delegate and track

Call `worker_implement` with `cwd`, `objective`, and applicable `constraints`, `acceptance_criteria`, and `relevant_context`. Include the selected validation commands in the criteria and request their actual results, including skipped checks.

MCP execution tools acknowledge with a persisted `run_id` and `state: running`, before work completes. Poll `worker_status` at reasonable intervals, then fetch `worker_result`; do not treat the acknowledgement as a completed report. Retain the returned `run_id` for status, correction, and review. Use `worker_status` or `worker_result` to inspect progress/results without fetching raw logs by default.

- All operations hold the same worktree lock, including investigations. On `busy`, inspect known active work and retry only after it finishes. Do not delete locks or start a competing local implementation.
- Different existing worktrees can execute independently when parallel delegation is in scope. Do not create extra branches/worktrees merely because the tools permit it.
- While a worker writes, avoid your own edits and validation commands that mutate that same worktree.
- Request cancellation does not cancel an accepted background job. Disconnecting/stopping its stdio server cancels active jobs and waits for final persistence; CLI remains synchronous. Timeout is not proof of failure, cancellation, or success. If a run ID is known, retrieve status/result before resubmitting. If no ID was returned, use `workerctl sessions` when available and correlate workspace, time, and state; do not guess among ambiguous runs. Do not blindly repeat a potentially active implementation.
- A cancelled or failed run may leave useful partial edits. Inspect its state and changes before deciding whether to resume or start another run. The current API has no dedicated cancellation tool; do not invent one.

## Independently review and validate

Use `worker_review` for an optional preliminary pass; it does not replace external acceptance or automatically record a verdict. After execution finishes, inspect the actual diff and relevant surrounding code against the objective and acceptance criteria. Snapshots include the entire dirty worktree against HEAD, so not every changed line belongs to this worker.

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
