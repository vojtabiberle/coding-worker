package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

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

func New(a *worker.App) (*sdk.Server, error) {
	waitSeconds := 45
	if raw, set := os.LookupEnv("CODING_WORKER_WAIT_SECONDS"); set {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 600 {
			return nil, fmt.Errorf("CODING_WORKER_WAIT_SECONDS must be an integer between 1 and 600")
		}
		waitSeconds = n
	}
	waitDescription := fmt.Sprintf("Wait duration in seconds: default %d (omit or use 0), range 1 to 600. Usually omit; override for a deliberately shorter wait. Timeout stops waiting, not the job.", waitSeconds)
	waitSchema := map[string]any{
		"type": "object", "required": []string{"run_id"}, "additionalProperties": false,
		"properties": map[string]any{
			"run_id":          map[string]any{"type": "string"},
			"iteration":       map[string]any{"type": "integer", "description": "One-based iteration to wait for. Omit to bind to the latest iteration at call entry. Reuse the returned iteration on subsequent waits."},
			"timeout_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 600, "default": waitSeconds, "description": waitDescription},
		},
	}

	s := sdk.NewServer(&sdk.Implementation{Name: "coding-worker", Version: "0.1.0"}, &sdk.ServerOptions{Instructions: "Local coding worker. Execution tools return a run_id at once: worker_wait until done=true (repeat with the returned iteration), then worker_result. New runs need an absolute cwd; explore/diagnose/review follow-ups use run_id and question. Profiles select the backend (Pi by default, or OpenCode); follow-ups keep the saved profile. All operations share a per-worktree lock (busy = wait); different worktrees run concurrently. Investigation tools need Linux/bubblewrap. A timeout stops waiting, not the job: recover known runs before retrying. Completion is not acceptance: review independently and record accepted, changes_requested or rejected with worker_record_review. Never commits or pushes."})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_implement", Description: "Returns run_id immediately; call worker_wait until done=true, then worker_result. Implement an objective in a Git worktree; writes code and runs checks, never commits. Put known edge cases in acceptance_criteria."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ImplementRequest) (*sdk.CallToolResult, any, error) {
		if in.Origin == "" {
			in.Origin = "mcp"
		}
		v, e := a.Implement(worker.Async(ctx), in)
		return response(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_continue", Description: "Returns run_id immediately; call worker_wait until done=true, then worker_result. Resume an implementation session with feedback. Rejects repository drift."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ContinueRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Continue(worker.Async(ctx), in)
		return response(v, e)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_explore", Description: "Returns run_id immediately; call worker_wait until done=true, then worker_result. Read-only question about the repository; returns answer and cited findings (fact/hypothesis/unverified). Default 800-byte budget (max_output_bytes 512-8192).", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ExploreRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Explore(worker.Async(ctx), in)
		return exploreResponse(v, e, in.MaxOutputBytes)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_diagnose", Description: "Returns run_id immediately; call worker_wait until done=true, then worker_result. Explain a bug and propose a minimal fix without applying it. Optional reproduction_command argv runs once (read-only FS, no network, <=120 s). Default 800-byte budget."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.DiagnoseRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Diagnose(worker.Async(ctx), in)
		return exploreResponse(v, e, in.MaxOutputBytes)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_verify", Description: "Returns run_id immediately; call worker_wait until done=true, then worker_result. Run one explicit check command without a model (read-only FS, private tmp, no network, <=120 s). Returns outcome, exit_status, duration and log excerpt; passed means exit 0 only."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.VerifyRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Verify(worker.Async(ctx), in)
		return verifyResponse(v, e, in.MaxOutputBytes)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_review", Description: "Returns run_id immediately; call worker_wait until done=true, then worker_result. Cheap preliminary review of worktree changes against HEAD, untracked files included; findings carry severity and validated citations. Not an acceptance verdict. Default 800-byte budget.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ExploreRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.ReviewCode(worker.Async(ctx), in)
		return exploreResponse(v, e, in.MaxOutputBytes)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_status", Description: "Immediate snapshot: state, operation, iteration count, timestamps, phase and freshness. No report.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in RunID) (*sdk.CallToolResult, any, error) {
		v, e := a.Status(ctx, in.RunID)
		return compactResponse(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_wait", Description: fmt.Sprintf("Block until the iteration finishes or timeout_seconds passes (default %d, max 600). Usually omit timeout_seconds; override for a deliberately shorter wait. Returns state, done and timed_out; no report.", waitSeconds), InputSchema: waitSchema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.WaitRequest) (*sdk.CallToolResult, any, error) {
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = waitSeconds
		}
		v, e := a.Wait(ctx, in)
		return compactResponse(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_result", Description: "Stored result; never reruns work. Implementation: compact summary (files, line counts, clipped report, failed commands, tests); detail=true for the full iteration. diff=true returns the current worktree diff vs HEAD incl. untracked files, filtered by Git pathspecs in paths and paged by diff_offset (max_output_bytes 512-65536, default 16384); matches_worker_snapshot=false means the tree changed after the worker. Findings: detail/finding_offset; logs: log/log_offset.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ResultRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.QueryResult(ctx, in)
		if vr, ok := v.(worker.VerifyResult); ok {
			return verifyResponse(vr, e, in.MaxOutputBytes)
		}
		if ex, ok := v.(worker.ExploreResult); ok {
			return exploreResponse(ex, e, in.MaxOutputBytes)
		}
		return compactResponse(v, e)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_record_review", Description: "Record an external verdict (accepted, changes_requested or rejected; not approved) with blocker/major/minor counts on the latest finished iteration."}, func(ctx context.Context, _ *sdk.CallToolRequest, in store.Review) (*sdk.CallToolResult, any, error) {
		e := a.Review(in)
		return response(map[string]bool{"recorded": e == nil}, e)
	})
	return s, nil
}
func Serve(ctx context.Context, a *worker.App) error {
	defer a.StopJobs()
	s, err := New(a)
	if err != nil {
		return err
	}
	return s.Run(ctx, &sdk.StdioTransport{})
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
