package mcp

import (
	"context"
	"encoding/json"

	"coding-worker/store"
	"coding-worker/worker"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
	s := sdk.NewServer(&sdk.Implementation{Name: "coding-worker", Version: "0.1.0"}, &sdk.ServerOptions{Instructions: "Shared implementation worker. Always supply absolute cwd. Review returned work externally; record review with worker_record_review. Different worktrees run concurrently; a busy result means retry after the current writer finishes."})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_implement", Description: "Implement an objective in an explicit Git worktree. Writes code and runs tests; does not commit or push."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ImplementRequest) (*sdk.CallToolResult, any, error) {
		if in.Origin == "" {
			in.Origin = "mcp"
		}
		v, e := a.Implement(ctx, in)
		return response(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_continue", Description: "Resume a run's OpenCode session with review feedback. Rejects repository drift."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ContinueRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Continue(ctx, in)
		return response(v, e)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_explore", Description: "Read-only investigation with source evidence. Requires absolute cwd and question for a new run; use run_id and question for follow-up. Linux/bubblewrap required. Default response budget 800 conservative tokens (UTF-8 bytes). No implementation or shell commands.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ExploreRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Explore(ctx, in)
		return exploreResponse(v, e, in.MaxOutputTokens)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_diagnose", Description: "Diagnose a bug without applying a fix. Same cwd/question or run_id/question inputs as exploration. Optional reproduction_command is explicit argv executed once by the harness, with a read-only filesystem, no network, and timeout <=120 seconds. Model cannot execute commands. Returns evidence-backed cause or hypothesis and minimal proposed repair. Use worker_result log=true for bounded reproduction output."}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.DiagnoseRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.Diagnose(ctx, in)
		return exploreResponse(v, e, in.MaxOutputTokens)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_status", Description: "Compact execution status; does not return the report. Exploration freshness is current, stale, or unknown.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in RunID) (*sdk.CallToolResult, any, error) {
		v, e := a.Status(ctx, in.RunID)
		return compactResponse(v, e)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "worker_result", Description: "Retrieve implementation result or budgeted exploration findings. For exploration use detail=true for quotes/hashes and finding_offset to page; increase max_output_tokens if a finding cannot fit. For diagnosis logs use log=true and log_offset (one-based byte offset). Default 800, maximum 8192.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in worker.ResultRequest) (*sdk.CallToolResult, any, error) {
		v, e := a.QueryResult(ctx, in)
		if ex, ok := v.(worker.ExploreResult); ok {
			return exploreResponse(ex, e, in.MaxOutputTokens)
		}
		return compactResponse(v, e)
	})

	sdk.AddTool(s, &sdk.Tool{Name: "worker_record_review", Description: "Attach an external reviewer verdict and optional test/E2E/human outcomes to latest completed iteration. verdict must be accepted, changes_requested, or rejected. Use accepted for approval; approved is not supported."}, func(ctx context.Context, _ *sdk.CallToolRequest, in store.Review) (*sdk.CallToolResult, any, error) {
		e := a.Review(in)
		return response(map[string]bool{"recorded": e == nil}, e)
	})
	return s
}
func Serve(ctx context.Context, a *worker.App) error { return New(a).Run(ctx, &sdk.StdioTransport{}) }

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
		v.Error = clipError(e.Error())
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
