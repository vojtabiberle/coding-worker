# coding-worker

Shared local implementation worker for Codex, Claude Code, and other MCP clients. OpenCode implements code; the calling orchestrator owns architecture, review, and acceptance. Switching worker models requires no MCP client changes.

See [future version ideas](ROADMAP.md) for task-aware routing, remote execution, basic reviews, and repository check setup.

The optional [coding-worker skill](skills/coding-worker/SKILL.md) guides delegation, independent validation, correction rounds, and external acceptance. Install its folder into `~/.codex/skills/coding-worker/` and invoke it as `$coding-worker`. The MCP server remains usable without the skill.

## Install

Requires Linux or macOS, Go 1.26+, a C compiler for SQLite, Git, and OpenCode on PATH. Windows is not supported; use WSL. Build and install both executables:

```sh
make test
make install                         # ~/.local/bin
# or: make install PREFIX=/usr/local
export PATH="$HOME/.local/bin:$PATH"
```

Configure OpenCode's provider credentials separately (`opencode auth login`). Check available models with `opencode models`, and agents with `opencode agent list`. This implementation was checked against local OpenCode **1.18.31** and current official CLI documentation. No live model call is part of normal tests.

```sh
mkdir -p ~/.config/coding-worker
cp examples/config.toml ~/.config/coding-worker/config.toml
```

Edit the example model IDs to match your configured provider. `greenpt/*` identifiers are user-supplied examples, not verified provider offerings. The example uses OpenCode's built-in `build` agent. Set `agent = "worker"` to use your custom worker agent; existing project agent configuration still applies.

## Configuration

Global profiles live in `~/.config/coding-worker/config.toml`:

```toml
default_profile = "glm"

[profiles.glm]
engine = "opencode"
model = "greenpt/glm-5.3"
agent = "build"
max_steps = 50

[profiles.deepseek]
engine = "opencode"
model = "greenpt/deepseek-v4.1-flash"
agent = "build"
max_steps = 60
```

Each physical worktree may have `.coding-worker.toml` containing `profile = "glm"`. `.coding-worker.local.toml` overrides that file; **add it to your project's `.gitignore`**. `workerctl profile use` preserves unrelated TOML fields and comments, and refuses unusual profile syntax rather than rewriting it. It rejects symlink config destinations.

Precedence: explicit invocation profile > local file > project file > global default. All config files are parsed even with an explicit override, so malformed configuration fails visibly. Profile fields come from the selected global profile; project files select a profile rather than redefining fields.

An active experiment deliberately overrides automatic profile selection. An explicit `--profile` or MCP `profile` bypasses experiment assignment and is excluded from its report. `status` shows an active experiment.

Paths honor `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. `CODING_WORKER_CONFIG` selects an alternate global file; `CODING_WORKER_DATA` selects an absolute data directory. **All clients must use the same data directory**, on a local filesystem supporting SQLite and `flock`. Separate data directories create independent lock/history domains.

## MCP setup

Build/install first. Replace `/home/YOU` with your actual home path; use an absolute executable path because desktop clients may have a different PATH. Ensure `opencode` is on the server's PATH too.

Codex CLI and desktop: add to `~/.codex/config.toml`:

```toml
[mcp_servers.coding-worker]
command = "/home/YOU/.local/bin/coding-worker"
tool_timeout_sec = 1800
```

Or register with `codex mcp add coding-worker -- /home/YOU/.local/bin/coding-worker`, then set the longer tool timeout in TOML. The default Codex tool timeout is too short for many implementation tasks.

Claude Code, globally:

```sh
claude mcp add --transport stdio --scope user coding-worker -- /home/YOU/.local/bin/coding-worker
claude mcp get coding-worker
```

Equivalent server entry inside `mcpServers` (user scope is stored in `~/.claude.json`; prefer the CLI to edit it):

```json
{
  "mcpServers": {
    "coding-worker": {
      "type": "stdio",
      "command": "/home/YOU/.local/bin/coding-worker",
      "args": []
    }
  }
}
```

Set client execution timeouts appropriately; cancellation kills the OpenCode process group and finalizes the run as cancelled. Each client launches a small stdio server; these processes share the same database and locks. No daemon or listening port is needed. MCP stdout contains protocol messages only; structured lifecycle logs go to stderr.

### Tools

- `worker_implement`: required absolute `cwd` and `objective`; optional `constraints`, `acceptance_criteria`, `relevant_context`, `profile`, `origin`.
- `worker_continue`: `run_id`, `feedback`, optional additional `acceptance_criteria`.
- `worker_diagnose`: diagnosis with optional caller-specified reproduction, evidence-backed cause/hypothesis, and proposed minimal fix. See diagnosis below.
- `worker_explore`: focused read-only investigation; new requests take absolute `cwd` and `question`, follow-ups take `run_id` and `question`. See read-only exploration below.
- `worker_status`: `run_id`; compact execution metadata without a report.
- `worker_result`: `run_id`; implementation result or bounded exploration findings. Raw diffs and event logs are omitted.
- `worker_record_review`: `run_id`, `verdict` (`accepted`, `changes_requested`, `rejected`), `reviewer`, `blocker`, `major`, `minor`, `notes`; optional nullable `tests_passed`, `hidden_e2e_success`, `human_intervention`.

Example implementation request:

```json
{
  "cwd": "/home/YOU/git/project-worktree",
  "objective": "Fix timeout handling in the HTTP client",
  "constraints": ["Preserve public API"],
  "acceptance_criteria": ["Existing tests pass", "Add timeout regression coverage"],
  "relevant_context": "Review focused on retry cancellation"
}
```

Calls are synchronous; other connections can inspect active runs. Errors include a structured result with `state: "busy"` on lock contention, or a run ID when an executed run fails. Do not equate a completed execution with accepted implementation. External reviewers record acceptance.

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

## Worktrees, continuation, and safety

Workspace resolution requires absolute paths, follows symlinks, and uses Git's `--show-toplevel` and `--git-common-dir`; a `.git` file in a linked worktree works normally. Repositories need an initial commit. Root (`/`) and home-directory worktrees are refused. Inherited `GIT_*` environment overrides are stripped to prevent accidental operations on another repository.

A canonical physical worktree has one nonblocking OS write lock. Different roots execute concurrently, including linked worktrees sharing a repository. Locks work across MCP and CLI processes. They are held through final persistence; lock files are intentionally never removed. OpenCode inherits the lock descriptor so an abrupt wrapper exit does not immediately release its lock. Normal cancellation kills its process group. Detached processes, tools closing inherited descriptors, and writers outside coding-worker are beyond this cooperative lock protocol.

Each iteration records initial/final HEAD, branch, porcelain status, diff, tracked line counts, and changed/untracked paths. Initial snapshots distinguish pre-existing dirty state from the final dirty state. Snapshots describe the **whole worktree against HEAD**, not an assertion that the worker authored every line. Untracked file contents contribute to drift detection, but are not included in tracked diff line counts. Binary line counts are not estimated.

Continuation uses the saved OpenCode session and immutable profile snapshot. It rejects missing/replaced worktree identity, branch/HEAD changes, or tracked/untracked content drift since the previous result. Start a new run after external edits or configuration changes. No automatic checkout/reset/stash/commit/push/cleanup occurs. Abandoned running records become `interrupted` when their worktree lock can be acquired by a status check or next writer.

**Trust boundary:** OpenCode runs as your user. The wrapper's own Git operations are read-only. Stable instructions prohibit commits, pushes, destructive Git actions, external edits, and loss of user changes. Runtime permission denials block external-directory tools and subagent delegation; Git prohibitions are worker instructions, and existing shell permissions remain untouched; no `--auto` permission bypass is used. These are not an OS sandbox: shell aliases, scripts, plugins, custom tools, symlinks, or a model ignoring instructions can bypass worker instructions/tool permissions. Use trusted repositories/providers and an OS sandbox/container if hard filesystem or command isolation is required. Existing OpenCode restrictions should remain in place; inspect your configured agent permissions before use.

`max_steps` is sent as `agent.<name>.steps` through an in-memory `OPENCODE_CONFIG_CONTENT` overlay, plus explicit model/agent/dir CLI arguments. Existing inline JSON is merged; on-disk OpenCode configuration is never rewritten. Non-conflicting project/provider/plugin settings remain active. Sharing is disabled for worker invocations. OpenCode's step limit ends tool use with a final summary; it is not a precise token/cost ceiling.

## Persistence and metrics

SQLite: `~/.local/share/coding-worker/coding-worker.db`, WAL mode, busy timeout, transactional run/iteration writes and experiment allocation. Tables: `runs`, `iterations`, `events`, `experiments`, `experiment_assignments`. Files are created private to the current user. Back up SQLite with its backup API, or stop all clients before copying the database and any WAL files.

Runs retain provider/model/profile, origin if supplied, session ID, timestamps, state, and iterations. Iterations retain exit status, worker report (capped at 32 KiB), commands when emitted, failures, unresolved tool errors, Git snapshots, and nullable token/cost fields. Reports from the model are not independently verified. OpenCode session details remain inspectable with `opencode export SESSION_ID`.

Input, cache-read, output tokens and cost are summed from unique `step_finish` events. Missing metrics stay null; reported zero cost is retained as OpenCode reported it and may not reflect actual billing. Test success stays unknown until an external review supplies it: a successful shell command alone does not prove a test suite passed. Reviews store hidden/E2E results and human intervention without inventing either.

Set `CODING_WORKER_DEBUG=1` to persist raw OpenCode JSON events. Default lifecycle logs contain run/worktree/profile/model/session/duration/exit status, not prompts or source. The database still contains Git diffs, command strings and reports and may contain sensitive project content. Raw events can be large; V1 has no automatic retention policy.

## Experiments

```sh
workerctl experiment start glm-vs-deepseek --profiles glm,deepseek
workerctl experiment status
workerctl run "First task"     # glm
workerctl run "Second task"    # deepseek
workerctl experiment report glm-vs-deepseek
workerctl experiment stop
```

One active experiment per shared repository identity; linked worktrees participate together. Allocation advances transactionally only when a run is created, so busy requests do not consume assignments. Continuations keep their original assignment. Experiment names are unique per repository; use a new name for a new trial.

Reports group tasks by profile and give values **with sample counts** (`n`), using null for unavailable denominators:

- First-pass acceptance among runs with a current external verdict.
- Mean correction iterations to acceptance (`iteration - 1`) among accepted runs.
- Execution failure rate among finished runs; failed/cancelled/interrupted executions count as failures.
- Test pass rate among current externally reviewed test outcomes.
- Mean execution duration where all iteration finish times are known (excludes time awaiting review; interrupted runs have unknown end times).
- Mean cost among finished runs with complete reported costs.
- Total finished worker cost divided by accepted tasks, only if all finished tasks have known cost.

Tiny sample counts are descriptive, not evidence that one model is better. Active runs are excluded from outcome/cost denominators. Changing a profile's model during an experiment mixes treatments; use stable profile names/configuration for a trial. Hidden/E2E and intervention outcomes remain available in recorded reviews, but V1 reports do not aggregate them.

## Architecture and tests

`config/` resolves TOML; `workspace/` canonicalizes Git worktrees and locks them; `runner/` implements the replaceable Engine interface and OpenCode JSON adapter; `store/` owns SQLite; `worker/` owns lifecycle; `mcp/` and `cli/` are transport/presentation layers. `cmd/coding-worker` and `cmd/workerctl` build the executables. Uses the official Go MCP SDK, go-toml, and SQLite; no hand-written MCP framing or daemon framework.

```sh
go test ./...
go test -race ./...
go vet ./...
# Optional, explicitly spends provider credits:
CODING_WORKER_LIVE_TEST=1 CODING_WORKER_LIVE_MODEL=provider/model go test ./runner -run TestLiveOpenCode -v
```

Tests use temporary Git repositories/worktrees, independent SQLite connections, child processes, fake engines, fake OpenCode executables, and an MCP client handshake. They do not require live LLM calls.

## Troubleshooting

- `unknown profile`: check the source shown by `status`; local overrides take precedence. Define the named global profile.
- `busy`: wait for the active writing run. Do not delete lock files; inspect sessions/processes. Status recovers abandoned records only after the OS lock is free.
- `repository drift`: start a new run with current context after external changes. Continuation never silently switches branches.
- `no session ID` / invalid JSON: verify OpenCode version, installed plugins, and JSON CLI behavior. Run `doctor`, inspect the referenced session, or enable debug for a new run.
- Permission-rejected commands: OpenCode noninteractive runs reject permission questions. Configure narrow appropriate permissions in your normal OpenCode agent configuration.
- MCP startup fails: use absolute executable path, ensure OpenCode is in the inherited PATH, and make global config readable/data directory writable.
- Tool timeout: increase the client timeout. Inspect the cancelled run before retrying; partial edits are preserved.
- SQLite busy/I/O errors: all clients must share local durable storage; avoid network filesystems. Disk/persistence errors fail the request visibly.

Documentation checked before implementation: [OpenCode CLI](https://opencode.ai/docs/cli/), [runtime config](https://opencode.ai/docs/config/), [agent steps](https://opencode.ai/docs/agents/), [Codex MCP](https://developers.openai.com/codex/mcp), [Claude Code MCP](https://code.claude.com/docs/en/mcp). OpenCode's CLI source was also checked for stdin prompts, session IDs, and JSON event shapes.

## Read-only exploration

`worker_explore` answers a focused repository question without implementing changes. It is currently synchronous and requires **Linux, Bubblewrap (`bwrap`), working user namespaces, and OpenCode with `--pure` support**. Implementation commands retain their previous platform support. Unsupported exploration environments fail closed; there is no unsandboxed fallback.

```json
{
  "cwd": "/home/YOU/git/project-worktree",
  "question": "Explore the keypress → PTY → rendering path. Find possible latency sources. Change nothing.",
  "max_output_tokens": 800
}
```

The configured profile supplies the model and step limit. Exploration uses a dedicated per-run read-only agent, not the profile's implementation agent. Only `read`, `glob`, and `grep` are allowed; shell, edits, delegation, LSP, and other tools are denied. `--pure` disables external plugins. Bubblewrap mounts the host filesystem read-only, with private device/process mounts and a single writable directory for this investigation's OpenCode data, cache, temporary files, and session. Symlinks cannot turn a read-only host path into a writable path. Provider network access remains enabled; this is filesystem write isolation, not network isolation or protection from a compromised host/kernel.

Private state is stored under `CODING_WORKER_DATA/explore/RUN_ID` (using the normal default data directory when unset). The runtime must be outside the target worktree. Existing OpenCode `auth.json` is copied privately on first use so credentials and sessions work without writing the original auth file. Environment credentials and normal provider configuration remain available. Authentication changes after starting an investigation do not refresh its private copy. Projects requiring plugin behavior or installation into read-only project/config directories may not work in exploration mode.

A worktree lock is held for the whole investigation, excluding other coding-worker writers and explorations on that same tree. Other worktrees remain independent. External editors do not participate in this lock: before/after state checks detect ordinary drift, but are not snapshot isolation against transient external edits.

The response separates `fact`, `hypothesis`, and `unverified` findings. Facts and hypotheses require `file`, `line`, `end_line`, and an exact source quote; the wrapper checks quotes against the cited lines and records file hashes. Matching a quote verifies the citation, **not the model's interpretation of it**. Unverified findings explain missing evidence. Malformed reports and unsupported citations fail visibly rather than becoming accepted facts.

Responses default to an 800-unit conservative budget, enforced on the UTF-8 serialized JSON payload at the MCP boundary. One budget unit allows one byte, conservatively bounding token use without choosing a provider-specific tokenizer. This can be considerably shorter than 800 actual model tokens. Accepted budgets: 512–8192. Only one text JSON payload is emitted, avoiding duplicate text/structured reports. `truncated` identifies shortened content; `more` and `next_offset` paginate findings. If a finding cannot fit, the offset does not advance: request a larger budget or the summary form.

Ask a follow-up using the same run/session:

```json
{"run_id":"RUN_ID","question":"Is the polling on input or only on output refresh?"}
```

Omit `cwd` and `profile` on follow-up. `worker_continue` remains implementation-only. A follow-up retains context and model selection but rejects stale or unverifiable state; start a new exploration after external changes.

`worker_status` now returns compact metadata for all runs, without reports. `worker_result` preserves implementation results and returns bounded exploration findings. To fetch citations and longer text without rerunning the model:

```json
{"run_id":"RUN_ID","detail":true,"finding_offset":0,"max_output_tokens":4096}
```

Full validated exploration reports remain in SQLite iteration records. Freshness is `current`, `stale`, or `unknown`, based on worktree identity, HEAD/branch, index, tracked changes, untracked content, and hashes of cited files (including cited ignored files). It does not cover uncited ignored files, external dependencies, or runtime environment changes. Old source locations always refer to the recorded state. Running or unavailable worktrees return unknown freshness; results changed during exploration are stale.

Explorations are excluded from implementation experiment assignments. A timeout does not prove that a run succeeded or failed: retrieve status by a known run ID, or identify it through `workerctl sessions` before retrying. This version does not yet return a run ID asynchronously before execution.

Additional verification without paid inference:

```sh
# Test the installed OpenCode config schema inside the read-only sandbox:
CODING_WORKER_CONFIG_TEST=1 go test ./runner -run TestInstalledExploreConfig -v
```

Normal tests use a fake engine/executable and exercise source evidence, follow-up, freshness, output budgets, real Bubblewrap write denial, and cancellation. Sandbox tests skip when `bwrap` is absent; failures when it is installed should be investigated rather than treated as a passing sandbox check.

## Diagnosis without applying a fix

`worker_diagnose` adds a reproduction observation, a supported cause or labeled hypothesis, and a proposed minimal repair. It shares exploration's session continuation, repository freshness, compact status, source citation checks, response budget, and Linux/Bubblewrap requirement.

```json
{
  "cwd": "/home/YOU/git/project-worktree",
  "question": "Why does the parser fail on an empty response? Expected an empty result; observed a panic.",
  "reproduction_command": ["go", "test", "./parser", "-run", "TestEmptyResponse", "-count=1"],
  "timeout_seconds": 30,
  "max_output_tokens": 800
}
```

The harness executes the exact supplied argv once; the model cannot choose or run additional commands. Relative executable paths resolve against the worktree. Shell syntax is interpreted only when the caller explicitly supplies a shell, such as `["/bin/sh", "-c", "..."]`. Without a command, diagnosis is static and reproduction is `not_run`.

Reproduction runs with the original filesystem read-only, disabled network, a minimal environment, and private writable cache/temp/state directories outside the repository. It does **not** copy the whole project or give tests a writable project checkout. Commands that require project writes, external services, additional environment variables, or unavailable dependencies may therefore fail for environmental reasons. Those failures are not proof that the reported bug was reproduced. Inference still uses provider networking in its separate sandbox; provider authentication is not copied into the reproduction runtime.

Timeout defaults to 30 seconds, maximum 120. Cancellation terminates the sandbox process group; execution statuses distinguish `not_run`, `finished`, `timed_out`, `cancelled`, and `start_failed`. `finished` includes nonzero exit codes. A sandbox startup failure is detected before treating output as command observations. Filesystem/network namespaces do not make arbitrary untrusted commands safe from every possible host interaction; supply trusted repository commands.

The `diagnosis` response contains `cause_status` (`supported`, `hypothesis`, `unverified`), `cause`, `minimal_fix`, `reproduction_assessment`, and `reproduced`. A claimed reproduction requires a finished command and matching log citations; a supported cause additionally requires source fact evidence. These checks validate the presence and location of evidence, not its semantic interpretation. A failing command alone does not automatically set `reproduced` or confirm a cause. No suggested fix is applied.

Combined stdout/stderr is stored privately under the worker data directory, outside both the target worktree and the command's writable sandbox. Logs are capped at 8 MiB while excess output is drained; `log_truncated` records overflow. The model receives only the last 4 KiB of the retained log, with its starting line number. Source and `reproduction.log` citations are checked against stored content.

Retrieve bounded log ranges through `worker_result`:

```json
{"run_id":"RUN_ID","log":true,"log_offset":1,"max_output_tokens":4096}
```

`log_offset` is a one-based **byte** offset. Follow the returned `next_offset` while `more` is true. There is no arbitrary file-path input. The payload stays within the requested conservative byte/token budget. Logs can contain sensitive command output; they remain private and have no automatic retention policy.

Follow up with `worker_diagnose` using the same `run_id` and a new `question`. Omitting `reproduction_command` reuses the previous observation without rerunning it; supplying a command executes a new attempt. Source drift requires a new run. Log retrieval selects the latest observation (which may have been reused); older attempts remain in SQLite iteration records and private log files. `worker_explore` and `worker_continue` do not resume diagnosis runs. Diagnoses do not participate in implementation experiment assignments.
