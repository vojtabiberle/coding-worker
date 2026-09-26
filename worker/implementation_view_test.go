package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojtabiberle/coding-worker/internal/testutil"
	"github.com/vojtabiberle/coding-worker/runner"
)

func TestImplementationSummaryAndDetail(t *testing.T) {
	a, root := setup(t)
	exit := 1
	a.Engine = fake{func(r runner.Request) (runner.Result, error) {
		testutil.Write(t, filepath.Join(root, "existing.txt"), "changed\n")
		testutil.Write(t, filepath.Join(root, "new.txt"), "a\nb\n")
		return runner.Result{Session: "ses_test", Report: strings.Repeat("é", 1500), Commands: []runner.Command{{Command: "go test", Status: "completed"}, {Command: "make lint", Status: "completed", Exit: &exit}}}, nil
	}}
	ctx := context.Background()
	v, e := a.Implement(ctx, ImplementRequest{CWD: root, Objective: "implement"})
	if e != nil {
		t.Fatal(e)
	}
	got, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID})
	s, ok := got.(ImplementationSummary)
	if e != nil || !ok {
		t.Fatalf("%T %v", got, e)
	}
	if s.Iteration != 1 || s.Commands != 2 || len(s.FailedCommands) != 1 || s.FailedCommands[0].Command != "make lint" {
		t.Fatalf("%+v", s)
	}
	if !s.ReportTruncated || len(s.Report) > summaryReportBytes || !strings.HasPrefix(strings.Repeat("é", 1500), s.Report) {
		t.Fatalf("report not clipped on a rune boundary: %d", len(s.Report))
	}
	if len(s.Untracked) != 1 || s.Untracked[0] != "new.txt" || s.Added != 1 || s.Removed != 1 {
		t.Fatalf("%+v", s)
	}
	full, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID, Detail: true})
	if r, ok := full.(Result); e != nil || !ok || r.Latest == nil || len(r.Latest.Result.Commands) != 2 {
		t.Fatalf("%T %v", full, e)
	}
}

func TestDiffPageUntrackedPathsPagingAndDrift(t *testing.T) {
	a, root := setup(t)
	a.Engine = fake{func(r runner.Request) (runner.Result, error) {
		testutil.Write(t, filepath.Join(root, "existing.txt"), "changed\n")
		os.MkdirAll(filepath.Join(root, "tests"), 0700)
		testutil.Write(t, filepath.Join(root, "tests", "new_test.txt"), strings.Repeat("line\n", 400))
		testutil.Write(t, filepath.Join(root, "blob.bin"), "a\x00b")
		return runner.Result{Session: "ses_test", Report: "done"}, nil
	}}
	ctx := context.Background()
	v, e := a.Implement(ctx, ImplementRequest{CWD: root, Objective: "implement"})
	if e != nil {
		t.Fatal(e)
	}
	p, e := a.diffPage(ctx, ResultRequest{RunID: v.RunID, Diff: true})
	if e != nil || p.MatchesWorker == nil || !*p.MatchesWorker || p.More {
		t.Fatalf("%+v %v", p, e)
	}
	if !strings.Contains(p.Text, "-original") || !strings.Contains(p.Text, "+++ b/tests/new_test.txt") || !strings.Contains(p.Text, "binary file: 3 bytes") {
		t.Fatal(p.Text)
	}
	if len(p.Files) != 3 {
		t.Fatalf("%+v", p.Files)
	}
	only, e := a.diffPage(ctx, ResultRequest{RunID: v.RunID, Diff: true, Paths: []string{":!tests/"}})
	if e != nil || strings.Contains(only.Text, "new_test.txt") || !strings.Contains(only.Text, "existing.txt") {
		t.Fatalf("pathspec ignored: %+v %v", only, e)
	}
	var text strings.Builder
	offset := 1
	for {
		page, e := a.diffPage(ctx, ResultRequest{RunID: v.RunID, Diff: true, MaxOutputBytes: 512, DiffOffset: offset})
		if e != nil || len(page.Text) > 512 {
			t.Fatal(page, e)
		}
		text.WriteString(page.Text)
		if !page.More {
			break
		}
		offset = page.NextOffset
	}
	if text.String() != p.Text {
		t.Fatal("pages do not reassemble the diff")
	}
	testutil.Write(t, filepath.Join(root, "existing.txt"), "edited by someone else\n")
	p, e = a.diffPage(ctx, ResultRequest{RunID: v.RunID, Diff: true})
	if e != nil || p.MatchesWorker == nil || *p.MatchesWorker {
		t.Fatalf("drift not reported: %+v %v", p, e)
	}
	for _, in := range []ResultRequest{{RunID: v.RunID, Diff: true, MaxOutputBytes: 100}, {RunID: v.RunID, Diff: true, DiffOffset: -1}, {RunID: v.RunID, Diff: true, DiffOffset: 1 << 30}} {
		if _, e := a.diffPage(ctx, in); e == nil {
			t.Fatal("accepted", in)
		}
	}
}
