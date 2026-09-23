package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"coding-worker/internal/testutil"
	"coding-worker/runner"
)

func TestCodeReviewLifecycle(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	testutil.Write(t, filepath.Join(root, "existing.txt"), "broken\n")
	calls := 0
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		calls++
		if !req.ReadOnly || !req.Review || !strings.Contains(req.Prompt, "-original") || !strings.Contains(req.Prompt, "+broken") {
			t.Fatal("missing review scope/sandbox", req)
		}
		return runner.Result{Session: "ses_test", Report: `{"answer":"Reviewed changes; tests not run.","findings":[{"kind":"fact","severity":"high","text":"The changed value breaks callers expecting original.","evidence":[{"file":"existing.txt","line":1,"quote":"broken"}]}]}`}, nil
	}}
	v, e := a.ReviewCode(ctx, ExploreRequest{CWD: root, Question: "Find regressions"})
	if e != nil || v.State != "completed" || v.Freshness != "current" || len(v.Findings) != 1 || v.Findings[0].Severity != "high" {
		t.Fatal(v, e)
	}
	b, _ := json.Marshal(v)
	if len(b) > 800 {
		t.Fatal(string(b))
	}
	v, e = a.ReviewCode(ctx, ExploreRequest{RunID: v.RunID, Question: "Explain trigger"})
	if e != nil || calls != 2 || v.Iteration != 2 {
		t.Fatal(v, e)
	}
	if _, e = a.Explore(ctx, ExploreRequest{RunID: v.RunID, Question: "wrong tool"}); e == nil {
		t.Fatal("mixed operation accepted")
	}
	got, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID, Detail: true, MaxOutputTokens: 8192})
	if e != nil || got.(ExploreResult).Findings[0].Evidence[0].SHA == "" {
		t.Fatal(got, e)
	}
	testutil.Write(t, filepath.Join(root, "existing.txt"), "changed again\n")
	v, e = a.ReviewCode(ctx, ExploreRequest{RunID: v.RunID, Question: "Again"})
	if e == nil || v.Freshness != "stale" {
		t.Fatal(v, e)
	}
}
func TestCodeReviewDeletionAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, report string
		valid        bool
	}{
		{"deletion", `{"answer":"Deletion reviewed, tests not run.","findings":[{"kind":"hypothesis","severity":"medium","text":"Removing the fixture may break consumers; confirm usage.","evidence":[{"artifact":"review.diff","line":1,"end_line":20,"quote":"-original"}]}]}`, true},
		{"no findings", `{"answer":"No actionable issues found; tests not run.","findings":[]}`, true},
		{"missing severity", `{"answer":"Review.","findings":[{"kind":"unverified","text":"Concern","evidence":[]}]}`, false},
		{"invented diff", `{"answer":"Review.","findings":[{"kind":"fact","severity":"high","text":"Concern","evidence":[{"artifact":"review.diff","line":1,"quote":"not in diff"}]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, root := setup(t)
			if e := os.Remove(filepath.Join(root, "existing.txt")); e != nil {
				t.Fatal(e)
			}
			a.Engine = fake{func(req runner.Request) (runner.Result, error) {
				// Set range to actual diff line count, including deletion-only diffs.
				report := strings.ReplaceAll(tc.report, `"end_line":20`, `"end_line":7`)
				return runner.Result{Session: "ses_test", Report: report}, nil
			}}
			v, e := a.ReviewCode(context.Background(), ExploreRequest{CWD: root, Question: "Review deletion"})
			if (e == nil) != tc.valid {
				t.Fatal(v, e)
			}
		})
	}
}
func TestCodeReviewCleanAndUntracked(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	if _, e := a.ReviewCode(ctx, ExploreRequest{CWD: root, Question: "Review"}); e == nil {
		t.Fatal("clean review accepted")
	}
	testutil.Write(t, filepath.Join(root, "new.txt"), "unsafe\n")
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		if !strings.Contains(req.Prompt, "new.txt") {
			t.Fatal("untracked file missing")
		}
		return runner.Result{Session: "ses_test", Report: `{"answer":"Review.","findings":[{"kind":"fact","severity":"low","text":"Concern","evidence":[{"file":"existing.txt","line":1,"quote":"original"}]}]}`}, nil
	}}
	if _, e := a.ReviewCode(ctx, ExploreRequest{CWD: root, Question: "Review"}); e == nil {
		t.Fatal("unrelated existing issue accepted")
	}
}
