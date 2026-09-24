# coding-worker

[![CI](https://github.com/vojtabiberle/coding-worker/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/vojtabiberle/coding-worker/actions/workflows/ci.yml)

A shared local MCP worker for coding tasks. Use it from Codex, Claude Code, or another MCP client to implement changes, investigate code, diagnose bugs, run checks, and review diffs. Your agent keeps control of scope and acceptance.

| Operation | Purpose |
| --- | --- |
| `worker_implement` / `worker_continue` | Implement a change and address feedback |
| `worker_explore` | Answer code questions with source citations |
| `worker_diagnose` | Investigate a bug and propose a minimal fix |
| `worker_verify` | Run an explicit check without a model |
| `worker_review` | Review worktree changes against HEAD |

Runs return an ID promptly. Wait for completion with `worker_wait`, retrieve evidence with `worker_result`, and record external acceptance with `worker_record_review`. Investigation results are compact, cite sources, and detect repository changes.

## Install

Requires Go 1.26+, a C compiler, Git, and OpenCode on `PATH` for model-backed operations. Implementation supports Linux/macOS; exploration, diagnosis, verification and review require **Linux with Bubblewrap and working user namespaces**. Windows users need WSL.

```sh
git clone https://github.com/vojtabiberle/coding-worker.git
cd coding-worker
make install
export PATH="$HOME/.local/bin:$PATH"

opencode auth login
opencode models
mkdir -p ~/.config/coding-worker
cp examples/config.toml ~/.config/coding-worker/config.toml
```

Edit the copied configuration to select a model available from your provider. Run `workerctl doctor` inside a Git repository with an initial commit to check setup.

`make install` installs both binaries and the Codex skill. For another agent:

```sh
make install-skill SKILLS_DIR="$HOME/.claude/skills"
# Or any directory your agent scans:
make install-skill SKILLS_DIR="$HOME/.agents/skills"
```

The skill is optional; it guides operation selection and result handling. See [installation options and profiles](docs/setup.md) for custom paths, `CODEX_HOME`, binary-only installation and configuration precedence.

## Connect your agent

Use an absolute binary path. For Codex, add to `~/.codex/config.toml`:

```toml
[mcp_servers.coding-worker]
command = "/home/YOU/.local/bin/coding-worker"
```

For Claude Code:

```sh
claude mcp add --transport stdio --scope user coding-worker -- /home/YOU/.local/bin/coding-worker
```

All clients must share the same data directory (default `~/.local/share/coding-worker`). Repositories can live anywhere; they do not need a common parent. Reconnect MCP clients after updating the server. See [MCP setup](docs/setup.md#mcp-setup) for additional configuration.

## Use it

Ask your connected agent, for example: “Explore the input handling path, identify possible latency sources, and change nothing.” The [skill](skills/coding-worker/SKILL.md) helps select the appropriate operation.

Direct MCP calls follow this sequence:

```text
worker_explore {"cwd":"/absolute/repo","question":"Where is input polling scheduled?"}
→ run_id
worker_wait {"run_id":"RUN_ID","timeout_seconds":45}
→ done, iteration, state and phase
worker_result {"run_id":"RUN_ID","max_output_bytes":4096}
→ report once the run finishes
```

If `timed_out:true`, repeat `worker_wait` with its returned `iteration`; retrieve the report when `done:true`. Use `worker_status` for an immediate progress snapshot.

Explore, diagnose and review accept follow-up questions with the same `run_id`. Results default to 800 UTF-8 JSON bytes; request details or a larger budget when needed. Implementation results are not subject to this limit.

For CLI implementation:

```sh
workerctl run "Fix the timeout handling and run relevant tests"
```

One run holds the worktree lock at a time. Implementation writes code and requires independent review; it does not commit or push. Investigation tools use a read-only filesystem sandbox, while check/reproduction commands have private temporary storage and no network. Accepted jobs continue after request cancellation, but stopping their MCP server cancels them.

## Documentation

- [Setup and configuration](docs/setup.md) — installation, profiles, client connections.
- [MCP tool reference](docs/tools.md) — inputs, evidence, pagination, diagnosis and review rules.
- [Operations and troubleshooting](docs/operations.md) — CLI, run lifecycle, locking, storage and errors.
- [Profile experiments](docs/experiments.md) — compare model profiles using recorded outcomes.
- [Development](docs/development.md) and [contributing](CONTRIBUTING.md) — architecture, tests and PR requirements.
- [Roadmap](ROADMAP.md) — planned work.

Licensed under [Apache License 2.0](LICENSE).
