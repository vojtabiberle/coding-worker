package worker

import (
	"coding-worker/internal/testutil"
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/workspace"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fake struct {
	run func(runner.Request) (runner.Result, error)
}

func (f fake) Start(_ context.Context, r runner.Request) (runner.Result, error) { return f.run(r) }
func (f fake) Continue(_ context.Context, id string, r runner.Request) (runner.Result, error) {
	if id != "ses_test" {
		return runner.Result{}, errors.New("wrong session")
	}
	return f.run(r)
}
func setup(t *testing.T) (*App, string) {
	t.Helper()
	root := testutil.Repo(t)
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	p := filepath.Join(t.TempDir(), "config.toml")
	testutil.Write(t, p, "default_profile='a'\n[profiles.a]\nengine='opencode'\nmodel='test/a'\nagent='worker'\nmax_steps=5\n[profiles.b]\nengine='opencode'\nmodel='test/b'\nagent='worker'\nmax_steps=6\n")
	a := &App{ConfigPath: p, Store: s}
	a.Engine = fake{func(r runner.Request) (runner.Result, error) {
		return runner.Result{Session: "ses_test", Report: "done"}, nil
	}}
	return a, root
}
func TestLifecycle(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	testutil.Write(t, filepath.Join(root, "existing.txt"), "user changes\n")
	a.Engine = fake{func(r runner.Request) (runner.Result, error) {
		testutil.Write(t, filepath.Join(root, "worker.txt"), "implementation\n")
		return runner.Result{Session: "ses_test", Report: "done"}, nil
	}}
	v, e := a.Implement(ctx, ImplementRequest{CWD: root, Objective: "implement"})
	if e != nil {
		t.Fatal(e)
	}
	if v.Iterations != 1 || v.State != "completed" || v.Latest.Before.Status == "" {
		t.Fatalf("%+v", v)
	}
	r, e := a.Store.Get(v.RunID)
	if e != nil || len(r.Iterations) != 1 {
		t.Fatal(e)
	}
	testutil.Write(t, filepath.Join(root, ".coding-worker.toml"), "profile='b'\n")
	if _, e = a.Continue(ctx, ContinueRequest{RunID: v.RunID, Feedback: "fix"}); e == nil {
		t.Fatal("drift accepted")
	}
	os.Remove(filepath.Join(root, ".coding-worker.toml"))
	v, e = a.Continue(ctx, ContinueRequest{RunID: v.RunID, Feedback: "fix"})
	if e != nil || v.Iterations != 2 || v.Config.Name != "a" {
		t.Fatalf("%+v %v", v, e)
	}
	if e = a.Review(store.Review{RunID: v.RunID, Verdict: "accepted", Reviewer: "test"}); e != nil {
		t.Fatal(e)
	}
	v, e = a.Result(v.RunID)
	if e != nil || len(v.Reviews) != 1 || v.Reviews[0].Iteration != 2 {
		t.Fatal(v, e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "existing.txt"))
	if string(b) != "user changes\n" {
		t.Fatal("user edits lost")
	}
	moved := root + "-moved"
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(moved, root)
	if _, e = a.Continue(ctx, ContinueRequest{RunID: v.RunID, Feedback: "fix"}); e == nil {
		t.Fatal("missing worktree accepted")
	}
}
func TestConcurrentClients(t *testing.T) {
	a, root := setup(t)
	other := testutil.Repo(t)
	s, e := store.Open(a.Store.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b := &App{ConfigPath: a.ConfigPath, Store: s}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	f := fake{func(r runner.Request) (runner.Result, error) {
		entered <- struct{}{}
		<-release
		return runner.Result{Session: "ses_test"}, nil
	}}
	a.Engine = f
	b.Engine = f
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, e := a.Implement(context.Background(), ImplementRequest{CWD: root, Objective: "one"})
		errs <- e
	}()
	<-entered
	v, e := b.Implement(context.Background(), ImplementRequest{CWD: root, Objective: "two"})
	if !errors.Is(e, workspace.ErrBusy) || v.State != "busy" {
		t.Fatal(v, e)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, e := b.Implement(context.Background(), ImplementRequest{CWD: other, Objective: "other"})
		errs <- e
	}()
	<-entered
	close(release)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
}
func TestFailureAndRecovery(t *testing.T) {
	a, root := setup(t)
	a.Engine = fake{func(r runner.Request) (runner.Result, error) {
		return runner.Result{Session: "ses_test", Exit: 3}, errors.New("failed")
	}}
	v, e := a.Implement(context.Background(), ImplementRequest{CWD: root, Objective: "fail"})
	if e == nil || v.State != "failed" {
		t.Fatal(v, e)
	}
	r, _ := a.Store.Get(v.RunID)
	r.State = "running"
	r.Finished = nil
	a.Store.Save(&r)
	v, e = a.Result(v.RunID)
	if e != nil || v.State != "interrupted" {
		t.Fatal(v, e)
	}
}
