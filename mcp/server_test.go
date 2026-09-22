package mcp

import (
	"coding-worker/store"
	"coding-worker/worker"
	"context"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
	if e != nil || len(list.Tools) != 5 {
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
