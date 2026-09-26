---
name: coding-worker
description: Use coding-worker MCP for bounded implementation, repository exploration, diagnosis, verification, and code review, with independent validation and external acceptance. Use for repository tasks; not for developing or configuring the worker itself.
---

# Coding worker orchestration

Delegate repository work to the coding-worker MCP tools; keep scope, architecture, independent review and acceptance yourself. A finished worker run is not an accepted solution.

## When to delegate

Delegation pays when the worker absorbs more of your work than it costs you to brief it and review the result. Your cost per task is roughly the context you load (tool results, files, diffs) times the turns that re-read it, plus what you write.

- Delegate multi-file changes and work that needs much reading, trial and error, or test iterations.
- Do small, well-understood edits yourself: the brief, the waiting and the review can cost more than the edit.
- If your own tokens cost about the same as the worker's, delegation mostly adds latency; prefer doing the work yourself unless you need parallelism.
- Judge by cost per accepted change, not per call: a delegation that needs correction rounds is rarely cheaper.

## Prepare

- Resolve the absolute worktree path (`cwd`). Note pre-existing changes: worker results include the whole dirty tree against HEAD.
- Pick the checks that establish acceptance (tests, build, type check, lint) from repository scripts or CI.
- Omit `profile` unless the user chose one; configuration selects the backend and model.
- Write the edge cases you will check into `acceptance_criteria` up front (empty and malformed input, repeated markers, missing files, conflicting rules). Criteria found only in review cost a correction round.

## Operations

| Need | Tool |
| --- | --- |
| Implement a bounded change | `worker_implement`, then `worker_continue` with feedback |
| Answer a repository question | `worker_explore` |
| Explain a bug, propose a minimal fix | `worker_diagnose` (optional `reproduction_command` argv) |
| Run a known check without a model | `worker_verify` (`command` argv) |
| Preliminary review of current changes | `worker_review` |

Every execution tool returns a `run_id` at once. Call `worker_wait` with the longest `timeout_seconds` below your tool timeout (max 600) and repeat with the returned `iteration` while `timed_out` is true; then `worker_result`. `done:true` includes failure and cancellation. A wait timeout or request cancellation does not stop the job: never resubmit a run whose ID you know; inspect it with `worker_status`/`worker_result`. All operations share a worktree lock (`busy` means wait); do not edit or run mutating commands in a worktree while the worker writes.

Investigation and verification sandboxes are read-only with no network; checks that write into the repository fail there. Results default to 800 bytes (`max_output_bytes` up to 8192); page findings with `finding_offset`, logs with `log`/`log_offset`. A failed or cancelled run may leave useful partial edits: inspect them before starting another.

## Review economically

1. `worker_result` for an implementation returns a compact summary: files, line counts, untracked files, clipped report, tests, failures and failed commands only. Use `detail=true` only when you need the full iteration.
2. Run `worker_review` for a cheap preliminary pass. Treat its findings as leads with citations, not as a verdict.
3. Read the change with `worker_result` `diff=true` rather than repeated `git diff` and file reads. Narrow it with `paths` (Git pathspecs, e.g. `[":!tests/"]`) and page with `diff_offset`. Read surrounding source only where the diff or a review finding needs it. `matches_worker_snapshot:false` means the tree changed after the worker.
4. Run the acceptance checks yourself, or through `worker_verify` when they work read-only. Report test, build and type-check results separately; a skipped check is not a pass.

For daemons, subprocesses or concurrency, also check startup, shutdown and cancellation, ownership, overlapping calls, lock lifetime and error paths.

## Corrections and acceptance

- Record `changes_requested` with `worker_record_review`, then `worker_continue` with the same `run_id`: observed vs expected behaviour, locations, any new acceptance criteria. Continuation keeps the backend session and rejects repository drift; never reset or revert user changes to satisfy it.
- If the same failure survives two targeted corrections, reassess the approach or the task boundary instead of repeating the feedback.
- Finish with `worker_record_review`: `verdict` exactly `accepted`, `changes_requested` or `rejected` (not `approved`), an accurate `reviewer`, counts of `blocker`/`major`/`minor` findings, and `notes` with the evidence. Add `tests_passed`, `hidden_e2e_success` or `human_intervention` only when observed.
- Record `accepted` only after you verified the acceptance criteria; if validation is blocked, say so instead of accepting or inventing a finding.

Report the outcome, your own checks, remaining issues and the run ID. The worker never commits or pushes; do so only within the user's authorization. Never invent token or cost figures.
