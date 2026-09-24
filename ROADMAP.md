# Ideas for future versions

These are proposed directions, not implementation commitments. Keep each feature optional and preserve explicit profile selection.

## Repository profiles with independent provider credentials

Combine provider settings in worker profiles with repository-level profile selection through Git config. A repository could select a company account, another API key, a proxy, or a custom endpoint without changing the user's normal backend configuration or requiring those credentials to be saved in OpenCode.

### Provider settings per profile

Extend the global worker profile with provider identity, optional adapter, custom base URL, model definitions where required, and a reference to the API key. Illustrative syntax only; these fields are not implemented:

```toml
[profiles.company]
engine = "opencode"
model = "company/my-model"
agent = "build"
max_steps = 50

[profiles.company.provider]
id = "company"
adapter = "@ai-sdk/openai-compatible"
base_url = "https://ai.example.com/v1"
api_key_env = "COMPANY_AI_API_KEY"
```

- Resolve credentials from the worker server's environment at execution time. Store references, not secret values, in profile snapshots, SQLite, MCP results and logs. Fail clearly if a referenced variable is missing; do not silently fall back to the user's usual account.
- Apply overrides only to the child process for that run. Do not mutate shared process environment or OpenCode's config/auth files; concurrent repositories may use different accounts.
- For the OpenCode adapter, construct a `provider` overlay in `OPENCODE_CONFIG_CONTENT`. Map the credential reference to `options.apiKey: "{env:COMPANY_AI_API_KEY}"` and URL to `options.baseURL`. Custom provider IDs also need model definitions and the appropriate SDK adapter.
- Preserve profile/session identity on continuation. Define credential rotation and missing-variable behavior explicitly without persisting the resolved key.
- Keep provider-specific translation in the backend adapter. Future backends need not use OpenCode's option names.

### Feasibility evidence

Verified on **2026-09-23 with OpenCode 1.18.32**, using isolated temporary HOME/XDG directories, dummy keys, and a local HTTP server. Configuration was passed through `OPENCODE_CONFIG_CONTENT`; the server intentionally returned HTTP 401 after recording the request. No real credentials or paid model calls were used.

| Probe | Observed result |
| --- | --- |
| Custom provider `worker-probe`, adapter `@ai-sdk/openai-compatible` | Request reached the configured `/custom/v1/chat/completions` endpoint with the selected model |
| Built-in `openai`, adapter `@ai-sdk/openai` | Request reached the configured `/custom/v1/responses` endpoint with the selected model |
| `options.apiKey` referencing an environment variable, while `auth.json` held a different dummy key | Both providers sent the override key in the Authorization header |
| Existing authentication file | Remained unchanged after both probes |

This verifies configuration loading, credential precedence and HTTP routing, not a successful model response or every provider/authentication type. The probes tested overriding an existing API key; add a regression with no saved credential as part of implementation, along with missing-variable handling, concurrent account isolation and secret-free persistence.

Official documentation confirms [custom providers, API keys, base URLs and adapters](https://opencode.ai/docs/providers/#custom-provider), [environment substitution](https://opencode.ai/docs/config/#env-vars), and [runtime configuration precedence](https://opencode.ai/docs/config/#precedence-order). Inline configuration overrides ordinary global/project settings; managed administrator settings can still take precedence. OAuth-based authentication may need provider-specific handling beyond these API-key probes.

### Profile selection through Git config

Add a `coding-worker.profile` key as an alternative to repository TOML files. Proposed usage (not yet read by coding-worker):

```sh
git config --local coding-worker.profile company
git config --local --get coding-worker.profile
```

The repository stores only a profile name; its definition stays in the global worker config and its key stays in an environment variable. This permits private per-repository choices without committing a configuration file. All clients still share the same worker data directory.

- Read Git config through Git in the canonical worktree, retaining the existing protection against inherited `GIT_*` overrides. Do not parse `.git/config` directly; linked worktrees can have a `.git` file.
- Define local/worktree/global scopes and conditional includes. Repository-local config is shared by linked worktrees; decide whether to support Git's worktree-specific config when enabled, without enabling it implicitly.
- Proposed precedence: explicit invocation profile > `.coding-worker.local.toml` > effective Git `coding-worker.profile` > `.coding-worker.toml` > global worker default. Finalize this contract before implementation, including how Git global defaults interact with project selection.
- Show the selected profile and configuration source in `workerctl status`/`doctor`; reject unknown profiles rather than silently selecting another account.
- Specify how this selection interacts with experiments and future routing. An explicitly chosen repository account must not silently become another account through automatic profile assignment.

End-to-end acceptance: two repositories select different profiles through Git config, use distinct environment-provided keys and endpoints without saving those keys in OpenCode, run concurrently without credential crossover, and retain correct profile/session behavior on continuation. Existing TOML-only setups must keep working.

## Multiple coding backends

Support interchangeable coding backends behind the existing MCP operations. Initial candidates: **PI** and **OpenCode 2.x**, alongside the current OpenCode 1.x integration. This is a proposal to evaluate and implement adapters, not a claim that these backends already satisfy the worker contract.

- Select the backend through a worker profile, keeping MCP tool names and result formats stable. Repository/Git-config profile selection should choose the backend together with its model and credential configuration.
- Build on the existing Engine interface; extend it only for demonstrated differences. Keep CLI arguments, configuration, authentication, event parsing and session handling inside each adapter.
- Investigate each backend's noninteractive/API mode, structured output, continuation, cancellation, tool permissions and metrics before promising equivalent operation support. Define capabilities explicitly; reject unsupported operations rather than silently falling back or weakening isolation.
- Preserve shared orchestration guarantees: asynchronous run IDs, worktree locking, repository freshness, bounded results, evidence validation, corrective attempts, and external acceptance. `worker_verify` remains independent of model backends.
- Keep backend identity and compatible version information with runs. Continuation must retain the original backend/session; do not silently migrate an in-flight session to a different engine or major version.
- Translate profile provider/credential settings per backend. Do not assume OpenCode configuration keys or SDK adapters apply to PI; retain secret-free persistence and per-run credential isolation.

### OpenCode 2.x compatibility

Evaluate OpenCode 2.x separately from the existing 1.x adapter: verify CLI/API changes, configuration schema, provider overrides, event formats, session storage and read-only execution support. Determine whether version-aware behavior in one adapter is sufficient or separate adapters are required. Document supported versions and a migration path; keep 1.x usable until equivalent behavior is demonstrated.

The OpenCode 1.18.32 provider probes above are evidence for that version only. Repeat key precedence, custom URL, missing-credential and isolation checks against 2.x; do not infer compatibility from the product name.

Acceptance: run the same bounded task through supported backends and retrieve a common result shape; verify same-backend continuation, cancellation, lock lifetime, credential isolation, and explicit rejection of unsupported capabilities. Use adapter fixtures and local mock providers in normal CI, with opt-in live compatibility tests for supported versions.

## Task-aware worker routing

Use a small, fast model to select the implementation profile best suited to a task. Consider the objective, relevant repository context, task complexity, available agents/models, and cost or latency preferences.

- Begin with recommendations or opt-in routing. Explicit profile overrides always win.
- Record the selected profile, routing rationale, router version, and routing cost separately from implementation cost.
- Feed external review verdicts, correction iterations, test outcomes, cost, and user overrides back into future routing decisions.
- Build repository-specific history so routing improves for the user's codebase and conventions. Start with stored examples and outcome statistics; model fine-tuning is not required.
- Compare routing against fixed-profile baselines through experiments. Avoid declaring a winner from a few easy tasks; retain exploration and account for task difficulty.

Open questions: which task features are useful, how much context the router needs, how to handle new repositories, and whether feedback should remain repository-local or transfer across projects.

## Remote execution

Allow the local orchestrator to delegate implementation to another machine. Support identical directory layouts or explicit local-to-remote path mappings.

- Explore an SSH-launched stdio MCP server first; consider authenticated network MCP transport if needed later.
- Treat path mapping and file synchronization as separate concerns. A matching path does not imply matching code or dependencies.
- Identify the repository, worktree, base commit, and initial dirty state before execution. Lock the remote worktree and coordinate local changes while work is in flight.
- Return results, metrics, and a reviewable change set with provenance. Detect conflicts before applying results locally; never silently overwrite local edits.
- Define behavior for disconnects, retries, cancellation, and recovering a run without executing it twice.

Synchronization remains undecided. Options to evaluate:

| Approach | Questions to resolve |
| --- | --- |
| Shared filesystem | Lock reliability, latency, and concurrent local edits |
| Remote Git checkout plus patches | Transferring staged/unstaged changes, untracked files, binaries, and returning changes without commits |
| Explicit workspace snapshots or file sync | Exclusions, deletion semantics, transfer size, secrets, and conflict detection |

Also decide how dependencies, toolchains, credentials, and ignored files become available remotely. A directory mapping alone cannot provide those prerequisites.

## Review integration

- Optionally run a reviewer after implementation using a separately configured review profile.
- Attribute changes to the worker versus pre-existing dirty state; incorporate task acceptance criteria and check outcomes.
- Add branch/base-ref selection beyond the current worktree-versus-HEAD scope.
- Keep automatic correction loops opt-in and bounded by iteration and cost limits.
- Keep automated findings distinct from external acceptance and independent routing feedback.

## Repository check setup

Discover and propose a basic validation setup for each repository, then run the configured checks during implementation.

- Inspect existing documentation, manifests, scripts, and CI configuration before proposing new commands.
- Reuse the repository's formatter, linter, type checker, build, and test commands where available.
- Provide a small repository-level check configuration with working directories, timeouts, prerequisites, and optional focused versus full check sets.
- Run a baseline to distinguish existing failures from regressions introduced by the worker.
- Aggregate configured checks into separate test/build/type-check outcomes, including unavailable or skipped checks.
- Require explicit authorization for dependency installation, environment changes, or checks that need external services or credentials. Discovery should not automatically execute arbitrary repository scripts.

Open questions: how much command detection can be reliable, when checks should run, and whether initial setup should only propose configuration or also write it after approval.

## Investigation extensions

- Add explicit per-run cancellation and optional execution independent of a stdio server lifetime.
- Add stable evidence IDs and targeted artifact retrieval, including remotely retrievable artifacts.
- Identify which inputs changed when conclusions become stale, and selectively revalidate affected findings.
- Track relevant ignored dependencies and runtime environment inputs beyond cited source hashes.
- Investigate stable snapshots to avoid mixed-state reads during transient external edits; before/after fingerprints do not provide snapshot isolation.
- Support checks that need writable build output through isolated worktree copies, with explicit dependency/environment handling.
- Prioritize findings by severity before budgeted pagination.

## Suggested order

1. Provider overrides and Git-config profile selection, with credential isolation and explicit precedence; evaluate PI and OpenCode 2.x adapter requirements alongside this design.
2. Repository check discovery/configuration and separate completion evidence for tests, builds, and type checks.
3. Review integration, change attribution, and branch/base-ref scope.
4. Explicit cancellation and durable execution/recovery.
5. Task-aware routing evaluated against externally verified outcomes and correction counts.
6. Remote transport and synchronization contract before automatic change transfer.
