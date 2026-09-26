package worker

import (
	"context"
	"errors"
	"github.com/vojtabiberle/coding-worker/config"
	"sync"
	"testing"
	"time"

	"github.com/vojtabiberle/coding-worker/store"
	"github.com/vojtabiberle/coding-worker/workspace"
)

func TestWaitTerminalAndHistorical(t *testing.T) {
	a, root := setup(t)
	now := time.Now().UTC()
	for _, state := range []string{"completed", "failed", "cancelled", "interrupted"} {
		r := &store.Run{ID: state, State: state, Workspace: workspace.Workspace{Root: root}, Iterations: []store.Iteration{{Number: 1, State: state, Finished: &now}}}
		if e := a.Store.Create(r, config.Config{}, true); e != nil {
			t.Fatal(e)
		}
		v, e := a.Wait(context.Background(), WaitRequest{RunID: r.ID})
		if e != nil || !v.Done || v.TimedOut || v.State != state || v.Iteration != 1 {
			t.Fatal(v, e)
		}
		r.State = "running"
		r.Phase = "model"
		r.LastProgress = &now
		r.Iterations = append(r.Iterations, store.Iteration{Number: 2, State: "running"})
		if e = a.Store.Save(r); e != nil {
			t.Fatal(e)
		}
		v, e = a.Wait(context.Background(), WaitRequest{RunID: r.ID, Iteration: 1})
		if e != nil || !v.Done || v.State != state || v.LatestIteration != 2 || v.LastProgress != nil {
			t.Fatal(v, e)
		}
	}
	p := store.Progress{State: "running", Iterations: 2, IterationError: true, IterationFinished: &now}
	if v := waitView("old", 1, p); !v.Done || v.State != "unknown" {
		t.Fatal(v)
	}
	p.IterationError = false
	if v := waitView("old", 1, p); !v.Done || v.State != "completed" {
		t.Fatal(v)
	}
}

func TestWaitTimeoutCancellationAndSharedStore(t *testing.T) {
	a, root := setup(t)
	defer a.StopJobs()
	engine := blockingEngine{make(chan struct{}), make(chan struct{})}
	a.Engine = engine
	run, e := a.Explore(Async(context.Background()), ExploreRequest{CWD: root, Question: "Inspect"})
	if e != nil {
		t.Fatal(e)
	}
	<-engine.started
	other, e := store.Open(a.Store.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	b := &App{Store: other}
	v, e := b.wait(context.Background(), WaitRequest{RunID: run.RunID, TimeoutSeconds: 1}, 10*time.Millisecond)
	if e != nil || v.Done || !v.TimedOut || v.State != "running" || v.Iteration != 1 {
		t.Fatal(v, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	_, e = b.wait(ctx, WaitRequest{RunID: run.RunID}, 10*time.Millisecond)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	lock, e := b.lock(root)
	if !errors.Is(e, workspace.ErrBusy) {
		if lock != nil {
			lock.Close()
		}
		t.Fatal("wait released job lock", e)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := b.wait(context.Background(), WaitRequest{RunID: run.RunID, Iteration: 1, TimeoutSeconds: 5}, 10*time.Millisecond)
			if e != nil || !v.Done || v.State != "completed" || v.TimedOut {
				t.Error(v, e)
			}
		}()
	}
	close(engine.release)
	wg.Wait()
}

func TestWaitRecoversOrphanAndValidates(t *testing.T) {
	a, root := setup(t)
	r := &store.Run{ID: "orphan", State: "running", Workspace: workspace.Workspace{Root: root}, Iterations: []store.Iteration{{Number: 1, State: "running"}}}
	if e := a.Store.Create(r, config.Config{}, true); e != nil {
		t.Fatal(e)
	}
	v, e := a.Wait(context.Background(), WaitRequest{RunID: r.ID})
	if e != nil || !v.Done || v.State != "interrupted" {
		t.Fatal(v, e)
	}
	saved, e := a.Store.Get(r.ID)
	if e != nil || saved.Finished != nil || saved.Iterations[0].State != "interrupted" {
		t.Fatal(saved, e)
	}
	for _, in := range []WaitRequest{{}, {RunID: "missing"}, {RunID: r.ID, Iteration: -1}, {RunID: r.ID, Iteration: 2}, {RunID: r.ID, TimeoutSeconds: -1}, {RunID: r.ID, TimeoutSeconds: 601}} {
		if _, e := a.Wait(context.Background(), in); e == nil {
			t.Fatal("accepted", in)
		}
	}
}
