# Architecture and testing

[Overview](../README.md) · [Setup](setup.md) · [Tools](tools.md) · [Operations](operations.md) · [Experiments](experiments.md) · [Development](development.md)

## Architecture and tests

`config/` resolves TOML; `workspace/` canonicalizes Git worktrees and locks them; `runner/` implements the replaceable Engine interface and OpenCode/Pi JSON adapters; `store/` owns SQLite; `worker/` owns lifecycle; `mcp/` and `cli/` are transport/presentation layers. `cmd/coding-worker` and `cmd/workerctl` build the executables. Uses the official Go MCP SDK, go-toml, and SQLite; no hand-written MCP framing or daemon framework.

```sh
go test ./...
go test -race ./...
go vet ./...
# Optional locally; enabled in CI. Requires Pi 0.78.1, uses only a local mock provider:
CODING_WORKER_PI_TEST=1 go test -race ./...
# Optional, explicitly spends provider credits:
CODING_WORKER_LIVE_TEST=1 CODING_WORKER_LIVE_MODEL=provider/model go test ./runner -run TestLiveOpenCode -v
```

Tests use temporary Git repositories/worktrees, independent SQLite connections, child processes, fake engines, fake backend executables, real pinned Pi against a local mock provider, and an MCP client handshake. They do not require live LLM calls.

## Contributing and license

See [CONTRIBUTING.md](../CONTRIBUTING.md) for local checks and the pull request workflow. CI runs formatting, dependency verification, vet, race tests with Bubblewrap, and builds. It does not require provider credentials or run paid model calls. The `tests` check is required for merging into `master`; direct pushes, force pushes, deletions and administrator bypass are disabled by repository protection settings.

Licensed under [Apache License 2.0](../LICENSE).

The Pi adapter pins its CLI/extension contract. Before widening support, run mock-provider tests for continuation, policy initialization, request limits, provider errors, tool restrictions, credentials and cancellation on each supported version. `runner/pi-guard.mjs` is embedded in the Go binary; deployment needs no separate extension installation. It deliberately stops before an over-budget provider request, because exceptions in Pi extension handlers alone do not fail closed.
