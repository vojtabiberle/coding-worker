package mcp

import (
	"coding-worker/store"
	"coding-worker/worker"
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
	if e != nil || len(list.Tools) != 6 {
		t.Fatal(list, e)
	}
	v, e := cs.CallTool(ctx, &sdk.CallToolParams{Name: "worker_implement", Arguments: map[string]any{"cwd": "relative", "objective": "test"}})
	if e != nil || !v.IsError {
		t.Fatal(v, e)
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
