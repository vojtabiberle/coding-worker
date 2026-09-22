package cli

import (
	"bytes"
	"coding-worker/internal/testutil"
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/worker"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeEngine struct{}

func (fakeEngine) Start(context.Context, runner.Request) (runner.Result, error) {
	return runner.Result{Session: "ses_cli", Report: "done"}, nil
}
func (fakeEngine) Continue(context.Context, string, runner.Request) (runner.Result, error) {
	return runner.Result{Session: "ses_cli", Report: "continued"}, nil
}
func TestCLI(t *testing.T) {
	root := testutil.Repo(t)
	t.Chdir(root)
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := filepath.Join(t.TempDir(), "config.toml")
	testutil.Write(t, c, "default_profile='a'\n[profiles.a]\nengine='opencode'\nmodel='test/a'\nagent='build'\nmax_steps=3\n[profiles.b]\nengine='opencode'\nmodel='test/b'\nagent='build'\nmax_steps=4\n")
	a := &worker.App{Store: s, ConfigPath: c, Engine: fakeEngine{}}
	bin := t.TempDir()
	testutil.Write(t, filepath.Join(bin, "opencode"), "#!/bin/sh\necho 1.18.31\n")
	os.Chmod(filepath.Join(bin, "opencode"), 0700)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if e := Execute(context.Background(), a, args, &out); e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		return out.String()
	}
	run("profiles")
	run("profile", "use", "b")
	if !strings.Contains(run("status"), "test/b") {
		t.Fatal("wrong selected model")
	}
	run("profile", "use", "a", "--local")
	run("doctor")
	run("experiment", "start", "compare", "--profiles", "a,b")
	run("experiment", "status")
	run("run", "--profile", "b", "task")
	sessions, _ := s.List()
	id := sessions[0].ID
	run("sessions")
	run("session", "show", id)
	run("continue", id, "fix")
	run("review", id, "accepted", "--tests", "passed")
	run("experiment", "report", "compare")
	run("experiment", "stop")
}
