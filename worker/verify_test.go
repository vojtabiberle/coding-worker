package worker

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"coding-worker/internal/testutil"
)

func TestVerifyLifecycle(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	a, root := setup(t)
	a.Engine = nil
	a.ConfigPath = "/nonexistent/config"
	ctx := context.Background()
	for _, tc := range []struct {
		script, outcome string
		exit            int
	}{
		{"printf 'checks passed\\n'", "passed", 0},
		{"printf 'assertion failed\\n'; exit 7", "failed", 7},
		{"printf changed > existing.txt", "failed", -1},
	} {
		v, e := a.Verify(ctx, VerifyRequest{CWD: root, Command: []string{"/bin/sh", "-c", tc.script}, MaxOutputBytes: 512})
		if e != nil || v.State != "completed" || v.Outcome != tc.outcome || v.Exit == nil || (tc.exit >= 0 && *v.Exit != tc.exit) || v.Freshness != "current" {
			t.Fatalf("%+v %v", v, e)
		}
		b, _ := json.Marshal(v)
		if len(b) > 512 {
			t.Fatal(string(b))
		}
		log, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID, Log: true})
		if e != nil {
			t.Fatal(e)
		}
		b, _ = json.Marshal(log)
		if len(b) > 800 || !strings.Contains(string(b), "reproduction.log") {
			t.Fatal(string(b))
		}
		if _, e = a.Continue(ctx, ContinueRequest{RunID: v.RunID}); e == nil {
			t.Fatal("verification resumed as implementation")
		}
		testutil.Write(t, filepath.Join(root, "new.txt"), tc.script)
		got, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID})
		if e != nil || got.(VerifyResult).Freshness != "stale" {
			t.Fatal(got, e)
		}
		testutil.Git(t, root, "add", "new.txt")
		testutil.Git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "change")
	}
}
func TestVerifyTimeoutAndValidation(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	a, root := setup(t)
	if _, e := a.Verify(context.Background(), VerifyRequest{CWD: root}); e == nil {
		t.Fatal("empty command accepted")
	}
	v, e := a.Verify(context.Background(), VerifyRequest{CWD: root, Command: []string{"/bin/sleep", "20"}, TimeoutSeconds: 1})
	if e != nil || v.Outcome != "timed_out" {
		t.Fatal(v, e)
	}
	v, e = a.Verify(context.Background(), VerifyRequest{CWD: root, Command: []string{"/does/not/exist"}})
	if e == nil || v.State != "failed" || v.Outcome != "start_failed" {
		t.Fatal(v, e)
	}
}
