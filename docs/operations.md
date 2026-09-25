# Running and troubleshooting

[Overview](../README.md) · [Setup](setup.md) · [Tools](tools.md) · [Operations](operations.md) · [Experiments](experiments.md) · [Development](development.md)

## CLI

Run from any subdirectory of an existing Git worktree:

```sh
workerctl profile use glm
workerctl profile use deepseek --local
workerctl status
workerctl profiles
workerctl doctor
workerctl run "Implement the requested fix and run relevant tests"
workerctl run --profile glm "Implement another fix"
workerctl sessions
workerctl session show RUN_ID
workerctl continue RUN_ID "Fix the uncovered edge case"
workerctl review RUN_ID accepted --reviewer astra --tests passed
workerctl review RUN_ID changes_requested --major 2 --notes "Handle cancellation"
```

`doctor` checks TOML, selected profile, Git worktree, executable/version, model/agent syntax, and writable persistence. It does not claim credentials or a named remote model are valid. Historical sessions can be inspected outside a repository. Session listing is capped at the latest 1,000 runs.

## MCP execution lifecycle and migration

Reconnect MCP clients after installing the updated binary so `tools/list` and parameter schemas refresh. There are ten tools. Update requests from `max_output_tokens` to `max_output_bytes`; the old name is no longer advertised or supported. Read final reports with `worker_result` after `worker_wait` returns `done:true`; the initial response is only an acknowledgement.

Example MCP sequence (tool names identify separate calls):

```text
worker_explore {"cwd":"/absolute/repo","question":"Where is input polling scheduled?"}
→ {"run_id":"RUN_ID","state":"running", ...}

worker_wait {"run_id":"RUN_ID","timeout_seconds":45}
→ {"iteration":1,"done":false,"timed_out":true,"state":"running", ...}

worker_wait {"run_id":"RUN_ID","iteration":1,"timeout_seconds":45}
→ {"iteration":1,"done":true,"timed_out":false,"state":"completed", ...}

# After state becomes completed, failed, cancelled or interrupted:
worker_result {"run_id":"RUN_ID","max_output_bytes":4096}
```

Repeat bounded waits with the returned iteration instead of resubmitting the execution request. Use status for immediate snapshots. Wait timeout/cancellation does not stop the job. A failed run can still have useful error details. If the error is marked `truncated:true`, retry **result retrieval**, for example with `max_output_bytes:8192`; this does not rerun the model or command.

Jobs belong to their stdio server process, not to a persistent daemon. Graceful disconnect/shutdown cancels and joins active jobs before closing SQLite. Abrupt process death leaves runs recoverable as interrupted once their worktree lock is released. Other clients sharing the data directory can inspect stored state. Request cancellation alone does not cancel an accepted job. There is no dedicated cancel tool yet.

OpenCode failure responses prefer the actual provider error message, then bounded stderr when available. For example, `stream_inactivity_timeout` can mean the upstream model sent no data for 300 seconds; increasing the MCP timeout alone does not fix that provider failure. Stored historical failure events are also summarized by `worker_result`.

## Worktrees, continuation, and safety

Workspace resolution requires absolute paths, follows symlinks, and uses Git's `--show-toplevel` and `--git-common-dir`; a `.git` file in a linked worktree works normally. Repositories need an initial commit. Root (`/`) and home-directory worktrees are refused. Inherited `GIT_*` environment overrides are stripped to prevent accidental operations on another repository.

A canonical physical worktree has one nonblocking OS write lock. Different roots execute concurrently, including linked worktrees sharing a repository. Locks work across MCP and CLI processes. They are held through final persistence; lock files are intentionally never removed. Each backend inherits the lock descriptor so an abrupt wrapper exit does not immediately release its lock. Normal cancellation kills its process group. Detached processes, tools closing inherited descriptors, and writers outside coding-worker are beyond this cooperative lock protocol.

Each iteration records initial/final HEAD, branch, porcelain status, diff, tracked line counts, and changed/untracked paths. Initial snapshots distinguish pre-existing dirty state from the final dirty state. Snapshots describe the **whole worktree against HEAD**, not an assertion that the worker authored every line. Untracked file contents contribute to drift detection, but are not included in tracked diff line counts. Binary line counts are not estimated.

Continuation uses the saved backend session and immutable profile snapshot. It rejects missing/replaced worktree identity, branch/HEAD changes, or tracked/untracked content drift since the previous result. Start a new run after external edits or configuration changes. No automatic checkout/reset/stash/commit/push/cleanup occurs. Abandoned running records become `interrupted` when their worktree lock can be acquired by a status check or next writer.

**Trust boundary:** implementation backends run as your user. Pi shell tools are unrestricted by the adapter; its bundled policy enforces the request budget. The following permission overlay details apply to OpenCode. The wrapper's own Git operations are read-only. Stable instructions prohibit commits, pushes, destructive Git actions, external edits, and loss of user changes. Runtime permission denials block external-directory tools and subagent delegation; Git prohibitions are worker instructions, and existing shell permissions remain untouched; no `--auto` permission bypass is used. These are not an OS sandbox: shell aliases, scripts, plugins, custom tools, symlinks, or a model ignoring instructions can bypass worker instructions/tool permissions. Use trusted repositories/providers and an OS sandbox/container if hard filesystem or command isolation is required. Existing OpenCode restrictions should remain in place; inspect your configured agent permissions before use.

`max_steps` is sent as `agent.<name>.steps` through an in-memory `OPENCODE_CONFIG_CONTENT` overlay, plus explicit model/agent/dir CLI arguments. Existing inline JSON is merged; on-disk OpenCode configuration is never rewritten. Non-conflicting project/provider/plugin settings remain active. Sharing is disabled for worker invocations. OpenCode's step limit ends tool use with a final summary; it is not a precise token/cost ceiling.

## Persistence and metrics

SQLite: `~/.local/share/coding-worker/coding-worker.db`, WAL mode, busy timeout, transactional run/iteration writes and experiment allocation. Tables: `runs`, `iterations`, `events`, `experiments`, `experiment_assignments`. Files are created private to the current user. Back up SQLite with its backup API, or stop all clients before copying the database and any WAL files.

Runs retain provider/model/profile, origin if supplied, session ID, timestamps, state, and iterations. Iterations retain exit status, worker report (capped at 32 KiB), commands when emitted, failures, unresolved tool errors, Git snapshots, and nullable token/cost fields. Reports from the model are not independently verified. Pi sessions and credentials are stored privately per run; see [Pi setup](setup.md#pi-backend). OpenCode session details remain inspectable with `opencode export SESSION_ID`.

Input, cache-read, output tokens and cost are summed from unique `step_finish` events. Missing metrics stay null; reported zero cost is retained as OpenCode reported it and may not reflect actual billing. Test success stays unknown until an external review supplies it: a successful shell command alone does not prove a test suite passed. Reviews store hidden/E2E results and human intervention without inventing either.

Set `CODING_WORKER_DEBUG=1` to persist raw OpenCode JSON events. Default lifecycle logs contain run/worktree/profile/model/session/duration/exit status, not prompts or source. The database still contains Git diffs, command strings and reports and may contain sensitive project content. Raw events can be large; V1 has no automatic retention policy.

## Troubleshooting

- `unknown profile`: check the source shown by `status`; local overrides take precedence. Define the named global profile.
- `busy`: wait for the active writing run. Do not delete lock files; inspect sessions/processes. Status recovers abandoned records only after the OS lock is free.
- `repository drift`: start a new run with current context after external changes. Continuation never silently switches branches.
- `no session ID` / invalid JSON: verify OpenCode version, installed plugins, and JSON CLI behavior. Run `doctor`, inspect the referenced session, or enable debug for a new run.
- Permission-rejected commands: OpenCode noninteractive runs reject permission questions. Configure narrow appropriate permissions in your normal OpenCode agent configuration.
- MCP startup fails: use absolute executable path, ensure OpenCode is in the inherited PATH, and make global config readable/data directory writable.
- Tool timeout: inspect the known run before retrying. Accepted MCP jobs continue independently of request cancellation; shutting down their server cancels them. Partial implementation edits are preserved.
- SQLite busy/I/O errors: all clients must share local durable storage; avoid network filesystems. Disk/persistence errors fail the request visibly.

Documentation checked before implementation: [OpenCode CLI](https://opencode.ai/docs/cli/), [runtime config](https://opencode.ai/docs/config/), [agent steps](https://opencode.ai/docs/agents/), [Codex MCP](https://developers.openai.com/codex/mcp), [Claude Code MCP](https://code.claude.com/docs/en/mcp). OpenCode's CLI source was also checked for stdin prompts, session IDs, and JSON event shapes.
