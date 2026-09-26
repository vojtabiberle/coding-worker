package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vojtabiberle/coding-worker/config"
	"github.com/vojtabiberle/coding-worker/store"
	"github.com/vojtabiberle/coding-worker/worker"
	"strings"
	"testing"
)

func TestProtocol(t *testing.T) {
	db, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := &worker.App{Store: db}
	s := New(a)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx := context.Background()
	ss, e := s.Connect(ctx, serverTransport, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, e := c.Connect(ctx, clientTransport, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	list, e := cs.ListTools(ctx, nil)
	if e != nil || len(list.Tools) != 10 {
		t.Fatal(list, e)
	}
	v, e := cs.CallTool(ctx, &sdk.CallToolParams{Name: "worker_implement", Arguments: map[string]any{"cwd": "relative", "objective": "test"}})
	if e != nil || !v.IsError {
		t.Fatal(v, e)
	}
	if e = db.Create(&store.Run{ID: "finished", State: "completed", Iterations: []store.Iteration{{Number: 1, State: "completed"}}}, config.Config{}, true); e != nil {
		t.Fatal(e)
	}
	w, e := cs.CallTool(ctx, &sdk.CallToolParams{Name: "worker_wait", Arguments: map[string]any{"run_id": "finished"}})
	if e != nil || w.IsError || w.StructuredContent != nil || len(w.Content) != 1 {
		t.Fatal(w, e)
	}
	var waited worker.WaitResult
	if e = json.Unmarshal([]byte(w.Content[0].(*sdk.TextContent).Text), &waited); e != nil || !waited.Done || waited.Iteration != 1 || waited.TimedOut {
		t.Fatal(waited, e)
	}
	for detail, key := range map[bool]string{false: `"commands_run"`, true: `"latest"`} {
		res, e := cs.CallTool(ctx, &sdk.CallToolParams{Name: "worker_result", Arguments: map[string]any{"run_id": "finished", "detail": detail}})
		if e != nil || res.IsError || !strings.Contains(res.Content[0].(*sdk.TextContent).Text, key) {
			t.Fatal(detail, res, e)
		}
	}
	v, e = cs.CallTool(ctx, &sdk.CallToolParams{Name: "worker_status", Arguments: map[string]any{"run_id": "missing"}})
	if e != nil || !v.IsError {
		t.Fatal(v, e)
	}
}

func TestExploreTransportBudget(t *testing.T) {
	for _, budget := range []int{512, 800, 8192} {
		v := worker.ExploreResult{RunID: strings.Repeat("a", 32), State: "failed", Fingerprint: strings.Repeat("b", 64), Answer: strings.Repeat("\\\"界", 10000), Findings: []worker.Finding{{Text: strings.Repeat("x", 10000)}}}
		r, _, e := exploreResponse(v, fmt.Errorf("%s", strings.Repeat("failure", 1000)), budget)
		if e != nil {
			t.Fatal(e)
		}
		if r.StructuredContent != nil || len(r.Content) != 1 {
			t.Fatal("duplicated payload")
		}
		body := r.Content[0].(*sdk.TextContent).Text
		if len(body) > budget || !json.Valid([]byte(body)) {
			t.Fatalf("budget exceeded: %d > %d", len(body), budget)
		}
	}
}

func TestDiagnosisSummaryBudget(t *testing.T) {
	v := worker.ExploreResult{RunID: strings.Repeat("a", 32), State: "completed", Fingerprint: strings.Repeat("b", 64), Freshness: "current", Iteration: 1, Diagnosis: &worker.Diagnosis{CauseStatus: "hypothesis", Cause: "A polling delay could explain the symptom; measurement is still missing.", MinimalFix: "Proposed: replace polling with event delivery.", ReproductionAssessment: "No command supplied; hypothesis only."}, Reproduction: &worker.ReproductionSummary{Status: "not_run"}}
	r, _, e := exploreResponse(v, nil, 800)
	if e != nil {
		t.Fatal(e)
	}
	b := r.Content[0].(*sdk.TextContent).Text
	var got worker.ExploreResult
	json.Unmarshal([]byte(b), &got)
	if len(b) > 800 || got.Diagnosis == nil {
		t.Fatalf("diagnosis lost from default summary: %s", b)
	}
}

func TestVerifyTransportBudget(t *testing.T) {
	for _, budget := range []int{512, 800, 8192} {
		v := worker.VerifyResult{RunID: strings.Repeat("a", 32), State: "failed", Outcome: "start_failed", Fingerprint: strings.Repeat("b", 64), Freshness: "current", Excerpt: strings.Repeat("\\\"界", 10000)}
		r, _, e := verifyResponse(v, fmt.Errorf("%s", strings.Repeat("failure", 1000)), budget)
		if e != nil || !r.IsError || r.StructuredContent != nil {
			t.Fatal(r, e)
		}
		body := r.Content[0].(*sdk.TextContent).Text
		if len(body) > budget || !json.Valid([]byte(body)) {
			t.Fatalf("budget exceeded: %d > %d", len(body), budget)
		}
	}
}
