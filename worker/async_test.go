package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"coding-worker/runner"
	"coding-worker/workspace"
)

type blockingEngine struct {
	started chan struct{}
	release chan struct{}
}

func (e blockingEngine) Start(ctx context.Context, _ runner.Request) (runner.Result, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		return runner.Result{}, ctx.Err()
	case <-e.release:
		return runner.Result{Session: "ses_test", Report: `{"answer":"Inspected; no findings.","findings":[]}`}, nil
	}
}
func (e blockingEngine) Continue(ctx context.Context, _ string, r runner.Request) (runner.Result, error) {
	return e.Start(ctx, r)
}
func TestAsyncOwnsLockAndIgnoresRequestCancellation(t *testing.T) {
	a, root := setup(t)
	defer a.StopJobs()
	engine := blockingEngine{make(chan struct{}), make(chan struct{})}
	a.Engine = engine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, e := a.Explore(Async(ctx), ExploreRequest{CWD: root, Question: "Inspect"})
	if e != nil || v.State != "running" || v.RunID == "" {
		t.Fatal(v, e)
	}
	<-engine.started
	cancel()
	lock, e := a.lock(root)
	if !errors.Is(e, workspace.ErrBusy) {
		if lock != nil {
			lock.Close()
		}
		t.Fatal("lock released early", e)
	}
	if _, e = a.Status(context.Background(), v.RunID); e != nil {
		t.Fatal(e)
	}
	r, e := a.Store.Get(v.RunID)
	if e != nil || r.State != "running" {
		t.Fatal(r.State, e)
	}
	close(engine.release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, e = a.Store.Get(v.RunID)
		if e != nil {
			t.Fatal(e)
		}
		if r.State != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.State != "completed" {
		t.Fatal(r.State)
	}
}
func TestStopJobsPersistsCancellation(t *testing.T) {
	a, root := setup(t)
	engine := blockingEngine{make(chan struct{}), make(chan struct{})}
	a.Engine = engine
	v, e := a.Explore(Async(context.Background()), ExploreRequest{CWD: root, Question: "Inspect"})
	if e != nil {
		t.Fatal(e)
	}
	<-engine.started
	a.StopJobs()
	r, e := a.Store.Get(v.RunID)
	if e != nil || r.State != "cancelled" || r.Finished == nil {
		t.Fatal(r.State, e)
	}
	lock, e := a.lock(root)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
}
