# Contributing

Use a feature branch and open a pull request against `master`. Direct pushes to `master` are disabled, including for administrators. Before merge, the branch must be up to date and the required `tests` check must pass. No additional reviewer approval is required by default.

## Local checks

Use the Go version in `go.mod`, a C compiler (SQLite uses CGO), Git, and Linux with Bubblewrap for sandbox tests:

```sh
gofmt -w $(git ls-files '*.go')
go mod verify
go vet ./...
go test -race -count=1 ./...
make build
```

CI installs Bubblewrap and checks its namespace support before tests, so missing sandbox support cannot silently skip sandbox coverage. Tests use fake engines/executables; no OpenCode account or API credentials are required. Live model tests are opt-in and may incur provider charges; they are not part of CI.

Keep README, tool descriptions and `skills/coding-worker/SKILL.md` consistent with API changes. Do not commit provider credentials, local configuration overrides, run databases or logs.

Branch protection is configured on GitHub, not by the workflow file alone. Required check: `tests` from GitHub Actions, with up-to-date branches and pull requests required. Protection applies to administrators; force pushes and deletion are disabled.

Contributions are provided under the project's Apache-2.0 license.
