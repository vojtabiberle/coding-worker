package mcp

import (
	"context"
	"encoding/json"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vojtabiberle/coding-worker/store"
	"github.com/vojtabiberle/coding-worker/worker"
)

func response(v any, e error) (*sdk.CallToolResult, any, error) {
	if e != nil {
		b, _ := json.Marshal(struct {
			Error  string `json:"error"`
			Result any    `json:"result"`
		}{e.Error(), v})
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}, StructuredContent: map[string]any{"error": e.Error(), "result": v}}, nil, nil
	}
	return nil, v, nil
}

type RunID struct {
	RunID string `json:"run_id"`
}

func New(a *worker.App) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "coding-worker", Version: "0.1.0"}, &sdk.ServerOptions{Instructions: "Shared coding worker: implement writes code; explore answers source questions; diagnose proposes a fix with optional reproduction; verify executes an explicit check without a model; review returns preliminary findings over worktree changes against HEAD. Profiles select OpenCode or Pi; continuation and report correction retain the saved backend/profile. New runs require absolute cwd. Explore/diagnose/review follow-ups use run_id and question, omitting cwd/profile; worker_continue is implementation-only. Execution calls return a persisted run_id and running state after preflight; call worker_wait until done, then fetch result; repeat waits with the returned iteration. Accepted jobs survive request cancellation but are cancelled/joined when their stdio server shuts down. Recover known runs after timeouts before retrying. Use max_output_bytes (512-8192), replacing max_output_tokens. Status includes compact phase/last_progress; result retrieves stored evidence. Invalid citations and diagnosis claims share at most one same-session correction without rerunning reproduction. Failed reports can be repaired through the same investigation tool/run_id if repository state is unchanged; current freshness does not mean the report is valid. Errors respect max_output_bytes and flag truncation; raise the budget for longer text. Investigation/verification payloads default to 800 UTF-8 JSON bytes, configurable 512-8192, without duplicate structured content. These operations require Linux/bubblewrap and share the worktree lock; different worktrees can run concurrently. A busy result means wait for active work. Completion is not acceptance: review independently and record accepted, changes_requested, or rejected with worker_record_review. No commit or push is performed."})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_implement", Description: "Returns run_id/running acknowledgement; final payload is retrieved with worker_result after worker_wait returns done=true. Implement an objective in an explicit Git worktree. Writes code and runs tests; does not commit or push. Returns run/session IDs, state, workspace/profile, latest iteration with change summary and reported checks, available metrics, and external reviews. Completion requires independent validation."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ImplementRequest) (*sdk.CallToolResult, any, error) {
		if in.Origin == "" {
			in.Origin = "mcp"
		}
		v, e := a.Implement(worker.Async(ctx), in)
		return response(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_continue", Description: "Returns run_id/running acknowledgement; final payload is retrieved with worker_result after worker_wait returns done=true. Resume an implementation run's saved backend session with feedback; returns updated implementation result. Rejects repository drift and non-implementation runs."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ContinueRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Continue(worker.Async(ctx), in)
		return response(v, e)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_explore", Description: "Returns run_id/running acknowledgement; final payload is retrieved with worker_result after worker_wait returns done=true. Read-only investigation with source evidence. Requires absolute cwd and question for a new run; use run_id and question for follow-up. Linux/bubblewrap required. Returns answer, fact/hypothesis/unverified findings, source citations, run_id, repository_state, freshness, truncated/more/next_offset. Default response budget 800 UTF-8 JSON bytes. No implementation or shell commands.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ExploreRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Explore(worker.Async(ctx), in)
		return exploreResponse(v, e, in.MaxOutputBytes)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_diagnose", Description: "Returns run_id/running acknowledgement; final payload is retrieved with worker_result after worker_wait returns done=true. Diagnose a bug without applying a fix. Same cwd/question or run_id/question inputs as exploration. Optional reproduction_command is explicit argv executed once by the harness, with a read-only filesystem, no network, and timeout <=120 seconds. Model cannot execute commands. Returns diagnosis cause_status, cause, minimal_fix, reproduction_assessment and reproduced; command status/exit and evidence findings, with run_id and freshness. Default 800 UTF-8 JSON bytes. Omitting a command on follow-up reuses the previous observation. Use worker_result log=true for bounded reproduction output."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.DiagnoseRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Diagnose(worker.Async(ctx), in)
		return exploreResponse(v, e, in.MaxOutputBytes)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_verify", Description: "Returns run_id/running acknowledgement; final payload is retrieved with worker_result after worker_wait returns done=true. Execute one explicit check command without a model. Requires absolute cwd and command argv. Linux/bubblewrap, read-only filesystem, private temporary storage, no network; timeout <=120 seconds. Returns run_id, execution state, outcome (passed/failed/timed_out/cancelled/start_failed), exit_status, duration_seconds, repository_state/freshness and log excerpt/line with truncation flags. Passed means exit 0 only. Default budget 800 UTF-8 JSON bytes. Each call starts a new run. Use worker_result log=true for log pages."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.VerifyRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Verify(worker.Async(ctx), in)
		return verifyResponse(v, e, in.MaxOutputBytes)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_review", Description: "Returns run_id/running acknowledgement; final payload is retrieved with worker_result after worker_wait returns done=true. Read-only review of current worktree changes against HEAD, including untracked files. Supply cwd and question; follow up with run_id and question. Returns answer and fact/hypothesis/unverified findings with severity high/medium/low, reason and validated source/diff citations, plus run_id, repository_state/freshness and pagination; no edits or test execution. Requires Linux/bubblewrap. Default budget 800 UTF-8 JSON bytes, max 8192; context above 128 KiB is rejected. Use worker_result detail=true and finding_offset for more findings. This is not an external acceptance verdict.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ExploreRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.ReviewCode(worker.Async(ctx), in)
		return exploreResponse(v, e, in.MaxOutputBytes)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_status", Description: "Returns run_id, state, operation, iteration_count, started_at, finished_at, phase, last_progress (phase transition time, not heartbeat) and applicable freshness (current/stale/unknown). No report, logs or re-execution.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in RunID) (*sdk.CallToolResult, any, error) {
		v, e := a.Status(ctx, in.RunID)
		return compactResponse(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_wait", Description: "Wait for one run iteration to finish, default 45 seconds, maximum 60; choose less than the client tool timeout. Omit iteration to bind to the latest at entry; repeat with the returned iteration. Returns run_id, iteration, latest_iteration, state, done, timed_out, phase and available last_progress. Timeout or request cancellation stops waiting only. No report, logs, model calls or freshness checks. Historical legacy state may be unknown with done=true. worker_result retrieves the latest iteration, which may have advanced.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.WaitRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Wait(ctx, in)
		return compactResponse(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_result", Description: "Retrieve stored implementation result, explore/diagnose/review findings, or verification summary; never reruns work. For findings use detail=true for quotes/hashes and finding_offset to page; increase max_output_bytes if a finding cannot fit. For diagnosis or verification logs use log=true and log_offset (one-based byte offset). Investigation/verification default 800 UTF-8 JSON bytes, maximum 8192; implementation results are not budgeted. Log pages return text, line, next_offset, more and log_truncated.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ResultRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.QueryResult(ctx, in)
		if vr, ok := v.(worker.VerifyResult); ok {
			return verifyResponse(vr, e, in.MaxOutputBytes)
		}
		if ex, ok := v.(worker.ExploreResult); ok {
			return exploreResponse(ex, e, in.MaxOutputBytes)
		}
		return compactResponse(v, e)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_record_review", Description: "Attach an external reviewer verdict and optional test/E2E/human outcomes to latest completed iteration; returns recorded=true on success. verdict must be accepted, changes_requested, or rejected. Use accepted for approval; approved is not supported."}, func(ctx context.Context, _ *sdk.CallToolRequest, in store.Review) (*sdk.CallToolResult, any, error) {
		e := a.Review(in)
		return response(map[string]bool{"recorded": e == nil}, e)
	})
	return s
}
func Serve(ctx context.Context, a *worker.App) error {
	defer a.StopJobs()
	return New(a).Run(ctx, &sdk.StdioTransport{})
}

// Text-only JSON avoids duplicating reports in both MCP content channels.
func compactResponse(v any, e error) (*sdk.CallToolResult, any, error) {
	if e != nil {
		v = map[string]any{"error": clipError(e.Error()), "result": v}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	return &sdk.CallToolResult{IsError: e != nil, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
}
func clipError(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func exploreResponse(v worker.ExploreResult, e error, budget int) (*sdk.CallToolResult, any, error) {
	if budget < 512 || budget > 8192 {
		budget = 800
	}
	if e != nil {
		v.Error = e.Error()
	}
	b, _ := json.Marshal(v)
	if len(b) > budget {
		v.Answer = ""
		v.Diagnosis = nil
		v.Findings = nil
		v.More = true
		v.Truncated = true
		v.NextOffset = 0
		if len(v.Error) > 80 {
			v.Error = v.Error[:80]
		}
		b, _ = json.Marshal(v)
	}
	return &sdk.CallToolResult{IsError: e != nil, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
}

func verifyResponse(v worker.VerifyResult, e error, budget int) (*sdk.CallToolResult, any, error) {
	if budget < 512 || budget > 8192 {
		budget = 800
	}
	if e != nil {
		v.Error = e.Error()
	}
	b, _ := json.Marshal(v)
	if len(b) > budget {
		v.Excerpt = ""
		v.Truncated = true
		if len(v.Error) > 80 {
			v.Error = v.Error[:80]
		}
		b, _ = json.Marshal(v)
	}
	return &sdk.CallToolResult{IsError: e != nil, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
}
