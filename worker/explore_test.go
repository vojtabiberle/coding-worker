package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojtabiberle/coding-worker/internal/testutil"
	"github.com/vojtabiberle/coding-worker/runner"
	"github.com/vojtabiberle/coding-worker/workspace"
)

func explorationFixture() string {
	return `{"answer":"The interval is 250 ms; end-to-end latency was not measured.","findings":[{"kind":"fact","text":"Polling uses 250 ms.","evidence":[{"file":"poll.ts","line":1,"quote":"setInterval(poll, 250)"}]},{"kind":"hypothesis","text":"Polling could delay refresh; runtime measurement is needed.","evidence":[{"file":"poll.ts","line":1,"quote":"250"}]},{"kind":"unverified","text":"Latency has not been measured.","evidence":[]}]}`
}
func TestExploreFollowupAndFreshness(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	testutil.Write(t, filepath.Join(root, "poll.ts"), "setInterval(poll, 250)\n")
	calls := 0
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		calls++
		if !req.ReadOnly || !strings.HasPrefix(req.Profile.Agent, runner.ExploreAgent+"-") {
			t.Fatal("not read-only", req)
		}
		return runner.Result{Session: "ses_test", Report: explorationFixture()}, nil
	}}
	v, e := a.Explore(ctx, ExploreRequest{CWD: root, Question: "Find latency"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(v)
	if len(b) > 800 || v.Freshness != "current" || len(v.Findings) == 0 {
		t.Fatalf("%s", b)
	}
	got, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID, Detail: true, MaxOutputBytes: 8192})
	if e != nil {
		t.Fatal(e)
	}
	detail := got.(ExploreResult)
	if len(detail.Findings) != 3 || detail.Findings[0].Evidence[0].SHA == "" {
		t.Fatal(detail)
	}
	status, e := a.Status(ctx, v.RunID)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(status)
	if strings.Contains(string(b), "findings") || strings.Contains(string(b), "Polling") {
		t.Fatal("status contains report")
	}
	v, e = a.Explore(ctx, ExploreRequest{RunID: v.RunID, Question: "Input or refresh?"})
	if e != nil || v.Iteration != 2 || calls != 2 {
		t.Fatal(v, e, calls)
	}
	if _, e = a.Continue(ctx, ContinueRequest{RunID: v.RunID, Feedback: "edit"}); e == nil {
		t.Fatal("exploration continued as implementation")
	}
	testutil.Write(t, filepath.Join(root, "poll.ts"), "setInterval(poll, 100)\n")
	got, e = a.QueryResult(ctx, ResultRequest{RunID: v.RunID})
	if e != nil || got.(ExploreResult).Freshness != "stale" {
		t.Fatal(got, e)
	}
	_, e = a.Explore(ctx, ExploreRequest{RunID: v.RunID, Question: "again"})
	if e == nil || calls != 2 {
		t.Fatal("stale context reused")
	}
}
func TestExploreEvidenceValidation(t *testing.T) {
	root := t.TempDir()
	testutil.Write(t, filepath.Join(root, "poll.ts"), "setInterval(poll, 250)\n")
	for _, raw := range []string{"not json", strings.Replace(explorationFixture(), "setInterval(poll, 250)", "fabricated", 1), strings.Replace(explorationFixture(), `"line":1`, `"line":999`, 1), strings.Replace(explorationFixture(), `"poll.ts"`, `"../outside"`, 1), `{"answer":"claim","findings":[{"kind":"fact","text":"claim","evidence":[]}]}`} {
		if _, e := parseExploration(raw, root); e == nil {
			t.Fatal("invalid report accepted", raw)
		}
	}
	outside := filepath.Join(t.TempDir(), "external")
	testutil.Write(t, outside, "setInterval(poll, 250)\n")
	os.Remove(filepath.Join(root, "poll.ts"))
	os.Symlink(outside, filepath.Join(root, "poll.ts"))
	if _, e := parseExploration(explorationFixture(), root); e == nil {
		t.Fatal("external evidence accepted")
	}
}
func TestExploreBusyAndMutation(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	lock, e := workspace.Lock(filepath.Join(a.Store.Dir, "locks"), root)
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.Explore(ctx, ExploreRequest{CWD: root, Question: "test"})
	if e != workspace.ErrBusy {
		t.Fatal(e)
	}
	lock.Close()
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		testutil.Write(t, filepath.Join(root, "poll.ts"), "setInterval(poll, 250)\n")
		return runner.Result{Session: "ses_test", Report: explorationFixture()}, nil
	}}
	v, e := a.Explore(ctx, ExploreRequest{CWD: root, Question: "test"})
	if e == nil || v.State != "failed" || v.Freshness != "stale" {
		t.Fatal(v, e)
	}
}
func TestExploreIgnoredEvidenceFreshness(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	testutil.Write(t, filepath.Join(root, ".gitignore"), "poll.ts\n")
	testutil.Write(t, filepath.Join(root, "poll.ts"), "setInterval(poll, 250)\n")
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		return runner.Result{Session: "ses_test", Report: explorationFixture()}, nil
	}}
	v, e := a.Explore(ctx, ExploreRequest{CWD: root, Question: "test"})
	if e != nil {
		t.Fatal(e)
	}
	testutil.Write(t, filepath.Join(root, "poll.ts"), "changed\n")
	got, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID})
	if e != nil || got.(ExploreResult).Freshness != "stale" {
		t.Fatal(got, e)
	}
}
