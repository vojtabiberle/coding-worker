package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vojtabiberle/coding-worker/config"
)

func TestProgressProjectionAndCancellation(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now().UTC()
	r := &Run{ID: "progress", State: "running", Phase: "model", LastProgress: &now, Iterations: []Iteration{{Number: 1, State: "failed", Error: "evidence rejected", Finished: &now}, {Number: 2, State: "running"}}}
	if e = s.Create(r, config.Config{}, true); e != nil {
		t.Fatal(e)
	}
	p, e := s.Progress(context.Background(), r.ID, 0)
	if e != nil || p.Iterations != 2 || p.IterationState != "running" || p.IterationError || p.IterationFinished != nil || !p.LastProgress.Equal(now) {
		t.Fatal(p, e)
	}
	p, e = s.Progress(context.Background(), r.ID, 1)
	if e != nil || p.IterationState != "failed" || !p.IterationError || !p.IterationFinished.Equal(now) {
		t.Fatal(p, e)
	}
	if _, e = s.Progress(context.Background(), "missing", 0); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	// A queued DB operation must respect cancellation even with its sole connection occupied.
	conn, e := s.DB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e = s.Progress(ctx, r.ID, 0); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if e = s.RecoverContext(ctx, ""); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
