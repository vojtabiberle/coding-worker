# Setup and configuration

[Overview](../README.md) · [Setup](setup.md) · [Tools](tools.md) · [Operations](operations.md) · [Experiments](experiments.md) · [Development](development.md)

## Install

Requires Linux or macOS, Go 1.26+, a C compiler for SQLite, Git, and the selected backend on PATH. Windows is not supported; use WSL. Build and install both executables:

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

Build/install first. Replace `/home/YOU` with your actual home path; use an absolute executable path because desktop clients may have a different PATH. Ensure the selected backend (`opencode` or `pi`) is on the server's PATH too.

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

## Pi backend

Supported Pi version: **0.78.1**, deliberately pinned because event and extension contracts affect isolation and completion detection. Other versions fail explicitly; no fallback to another backend occurs. The npm package requires Node.js 22.19.0 or newer:

```sh
npm install -g --ignore-scripts @earendil-works/pi-coding-agent@0.78.1
pi --version
pi --list-models
```

Configure authentication using Pi's normal login/environment mechanisms, then add a worker profile:

```toml
[profiles.pi]
engine = "pi"
model = "anthropic/REPLACE_WITH_EXACT_MODEL_ID"
max_steps = 30
```

Install `rg` (ripgrep) and `fd` or `fdfind` on the MCP server's PATH for Pi search tools (Debian/Ubuntu packages: `ripgrep fd-find`). Automatic tool downloads are disabled; missing search binaries produce tool errors.

Choose it through the existing MCP `profile` parameter, CLI `--profile pi`, or repository profile file. Omit `agent`: it is OpenCode-only and rejected for Pi. The selected provider/model must resolve exactly, not through Pi's fuzzy model fallback. Run `workerctl doctor` after selecting the profile.

Each run has private Pi state outside the worktree. The adapter snapshots `auth.json` and `models.json` from `PI_CODING_AGENT_DIR` (default `~/.pi/agent`) on the first invocation; it preserves them on continuation. Custom endpoints and environment-key references in Pi's `models.json` therefore work without saving a key in Pi auth. Global settings, discovered extensions, skills, themes and prompt templates are not copied or loaded. Repository instruction-file discovery is disabled for investigations and retained for implementation. The bundled worker policy is the only explicit extension.

`max_steps` caps provider requests per invocation, including any extra requests, before the next request is sent. Exhaustion fails the iteration and retains any persisted session/partial work; it does not reserve a final summary request. Automatic compaction and retry are disabled in private settings. Pi 0.78.1 still reads project `.pi/settings.json`: the worker rejects malformed settings or explicit enablement of retry/compaction before calling the provider. Investigations replace the system prompt; implementation appends worker instructions. These semantics differ from OpenCode's final-summary step behavior. Environment variables are resolved again in each process; avoid changing credentials mid-run. Profile-level provider overrides and Git-config profile selection remain roadmap items.

Pi uses one JSON-mode process per invocation, with prompts on stdin, an exact session file for continuation and process-group cancellation. The adapter validates final assistant output and error events rather than trusting exit status alone. Exploration, diagnosis and review retain Linux/Bubblewrap requirements and allow only read/grep/find/ls inside the worktree, with resolved-path checks. Harness-supplied reproduction observations remain in the prompt. Implementation is not an OS sandbox; its shell tools have your user permissions.

Private run directories contain credentials and sessions: treat the shared worker data directory as sensitive. New results use `session_id` and record Pi's `backend_version`; old `opencode_session_id` records remain readable, and OpenCode implementation responses retain that legacy top-level alias. Pi session IDs are exact file paths and are never passed to OpenCode.
