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
	for _, name := range []string{"worker_status", "worker_result"} {
		sdk.AddTool(s, &sdk.Tool{Name: name, Description: "Get current run, latest iteration, metrics and reviews without raw logs.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in RunID) (*sdk.CallToolResult, any, error) {
			v, e := a.Result(in.RunID)
			return response(v, e)
		})
	}
	sdk.AddTool(s, &sdk.Tool{Name: "worker_record_review", Description: "Attach an external reviewer verdict and optional test/E2E/human outcomes to latest completed iteration."}, func(ctx context.Context, _ *sdk.CallToolRequest, in store.Review) (*sdk.CallToolResult, any, error) {
		e := a.Review(in)
		return response(map[string]bool{"recorded": e == nil}, e)
	})
	return s
}
func Serve(ctx context.Context, a *worker.App) error { return New(a).Run(ctx, &sdk.StdioTransport{}) }
