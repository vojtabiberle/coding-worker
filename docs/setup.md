# Setup and configuration

[Overview](../README.md) · [Setup](setup.md) · [Tools](tools.md) · [Operations](operations.md) · [Experiments](experiments.md) · [Development](development.md)

## Install

Requires Linux or macOS, Go 1.26+, a C compiler for SQLite, Git, and OpenCode on PATH. Windows is not supported; use WSL. Build and install both executables:

```sh
git clone https://github.com/vojtabiberle/coding-worker.git
cd coding-worker
make test
make install                         # binaries + Codex skill
# or: make install PREFIX=/usr/local
export PATH="$HOME/.local/bin:$PATH"
```

Skill installation defaults to `$CODEX_HOME/skills/coding-worker` when `CODEX_HOME` is set, otherwise `~/.codex/skills/coding-worker`. Repeated installation replaces the bundled `SKILL.md`; make persistent edits in this repository's source, not the installed copy. Unrelated destination files are preserved.

The same installer works with another client's skill directory:

```sh
make install-skill                         # Codex, no build required
make install-skill SKILLS_DIR="$HOME/.claude/skills"  # Claude Code
make install-skill SKILLS_DIR="$HOME/.agents/skills"  # clients supporting this shared directory
make install-skill SKILLS_DIR="/path/to/agent/skills" # any other agent
make install-bin                           # binaries only
```

`SKILLS_DIR` is the parent skills directory: the installer creates or updates `coding-worker/SKILL.md` inside it. Choose a directory your agent actually scans; `~/.agents/skills` is not automatically discovered by every client. The skill uses the same coding-worker MCP tools regardless of its installation directory; configure the MCP connection in each agent separately.

`PREFIX` controls binaries; `SKILLS_DIR` controls skills independently. It also works with `make install SKILLS_DIR="/path/to/agent/skills"` to install binaries and the skill together. The installed Codex skill is available on the next turn. Updating the skill does not refresh an existing MCP connection: reconnect after updating the server binary to load its current tools and schemas.

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

Or register with `codex mcp add coding-worker -- /home/YOU/.local/bin/coding-worker`, then set the longer tool timeout in TOML. Execution runs asynchronously; the client timeout covers preflight and acknowledgement, not the full model run. `worker_wait` defaults to 45 seconds (maximum 60); keep its duration below the client tool timeout. Repeated wait timeouts do not stop background work.

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

MCP execution tools return after validation, repository capture, persistence and scheduling, before model/command completion. Request cancellation after acceptance does not cancel the background job. Stopping the stdio server cancels its active process groups and waits for final persistence. Each client launches a small stdio server; these processes share the same database and locks. No daemon or listening port is needed. MCP stdout contains protocol messages only; structured lifecycle logs go to stderr.
