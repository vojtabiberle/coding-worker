# Architecture and testing

[Overview](../README.md) · [Setup](setup.md) · [Tools](tools.md) · [Operations](operations.md) · [Experiments](experiments.md) · [Development](development.md)

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

## Contributing and license

See [CONTRIBUTING.md](../CONTRIBUTING.md) for local checks and the pull request workflow. CI runs formatting, dependency verification, vet, race tests with Bubblewrap, and builds. It does not require provider credentials or run paid model calls. The `tests` check is required for merging into `master`; direct pushes, force pushes, deletions and administrator bypass are disabled by repository protection settings.

Licensed under [Apache License 2.0](../LICENSE).
