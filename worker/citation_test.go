package worker

import (
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/workspace"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCitationCorrectionBounded(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "corrected", false: "still_invalid"}[valid], func(t *testing.T) {
			a, root := setup(t)
			calls := 0
			a.Engine = fake{func(req runner.Request) (runner.Result, error) {
				calls++
				quote := "wrong"
				if calls == 2 {
					if !strings.Contains(req.Prompt, "finding 0 evidence 0 (existing.txt:1-1)") {
						t.Fatal("missing citation location", req.Prompt)
					}
					runs, e := a.Store.List()
					if e != nil || runs[0].Phase != "correcting_citations" {
						t.Fatal(runs, e)
					}
					if valid {
						quote = "original"
					}
				}
				cost := 0.25
				return runner.Result{Session: "ses_test", Metrics: runner.Metrics{Cost: &cost}, Report: `{"answer":"Inspection.","findings":[{"kind":"fact","text":"Source.","evidence":[{"file":"existing.txt","line":1,"quote":"` + quote + `"}]}]}`}, nil
			}}
			v, e := a.Explore(context.Background(), ExploreRequest{CWD: root, Question: "Inspect"})
			if (e == nil) != valid || calls != 2 {
				t.Fatal(v, e, calls)
			}
			r, _ := a.Store.Get(v.RunID)
			it := r.Iterations[0]
			if it.CitationAttempt == nil || it.Result.Metrics.Cost == nil || *it.Result.Metrics.Cost != 0.5 {
				t.Fatal(it)
			}
		})
	}
}
func TestErrorBudgetAndFullRetrieval(t *testing.T) {
	_, root := setup(t)
	w, e := workspace.Resolve(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	message := strings.Repeat("Missing executable with a long path ", 40)
	// A persisted error is also representative of runs created before this fix.
	r := store.Run{ID: "failure", State: "failed", Workspace: w, Iterations: []store.Iteration{{Error: message}}}
	for _, op := range []string{"verify", "explore"} {
		r.Operation = op
		var short, long string
		var truncated bool
		if op == "verify" {
			v, e := verifyView(context.Background(), r, 512)
			if e != nil {
				t.Fatal(e)
			}
			short = v.Error
			truncated = v.Truncated
			b, _ := json.Marshal(v)
			if len(b) > 512 {
				t.Fatal(len(b))
			}
			v, e = verifyView(context.Background(), r, 4096)
			if e != nil {
				t.Fatal(e)
			}
			long = v.Error
			if v.Truncated {
				t.Fatal("full result marked truncated")
			}
		} else {
			v, e := exploreView(context.Background(), r, ResultRequest{MaxOutputBytes: 512})
			if e != nil {
				t.Fatal(e)
			}
			short = v.Error
			truncated = v.Truncated
			b, _ := json.Marshal(v)
			if len(b) > 512 {
				t.Fatal(len(b))
			}
			v, e = exploreView(context.Background(), r, ResultRequest{MaxOutputBytes: 4096})
			if e != nil {
				t.Fatal(e)
			}
			long = v.Error
			if v.Truncated {
				t.Fatal("full result marked truncated")
			}
		}
		if !truncated || short == message || long != message {
			t.Fatal(op, truncated, len(short), len(long))
		}
	}
}
