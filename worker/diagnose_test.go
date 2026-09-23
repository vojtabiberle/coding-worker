package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"coding-worker/internal/testutil"
	"coding-worker/runner"
)

func diagnosisFixture() string {
	return `{"answer":"The test demonstrates the reported failure.","diagnosis":{"cause_status":"supported","cause":"Fixture demonstrates the reported branch.","minimal_fix":"Correct the original branch; no change applied.","reproduction_assessment":"The command emitted the expected bug marker.","reproduced":true},"findings":[{"kind":"fact","text":"Original branch is present.","evidence":[{"file":"existing.txt","line":1,"quote":"original"}]},{"kind":"fact","text":"Reproduction emitted bug marker.","evidence":[{"artifact":"reproduction.log","line":1,"quote":"BUG: reproduced"}]}]}`
}
func TestDiagnoseLifecycle(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	a, root := setup(t)
	ctx := context.Background()
	calls := 0
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		calls++
		if !req.ReadOnly || !strings.Contains(req.Prompt, "BUG: reproduced") || req.Reproduction == nil {
			t.Fatal("missing observation or isolation")
		}
		return runner.Result{Session: "ses_test", Report: diagnosisFixture()}, nil
	}}
	v, e := a.Diagnose(ctx, DiagnoseRequest{ExploreRequest: ExploreRequest{CWD: root, Question: "Diagnose bug"}, ReproductionCommand: []string{"/bin/sh", "-c", "printf 'BUG: reproduced\\n'; exit 3"}})
	if e != nil {
		t.Fatal(e)
	}
	if v.State != "completed" || v.Diagnosis == nil || !v.Diagnosis.Reproduced || v.Reproduction == nil || *v.Reproduction.Exit != 3 {
		t.Fatal(v)
	}
	r, _ := a.Store.Get(v.RunID)
	oldLog := r.Iterations[0].Reproduction.Log
	v, e = a.Diagnose(ctx, DiagnoseRequest{ExploreRequest: ExploreRequest{RunID: v.RunID, Question: "Why?"}})
	if e != nil || v.Iteration != 2 || calls != 2 {
		t.Fatal(v, e, calls)
	}
	r, _ = a.Store.Get(v.RunID)
	if r.Iterations[1].Reproduction.Log != oldLog {
		t.Fatal("follow-up reran command")
	}
	details, e := a.QueryResult(ctx, ResultRequest{RunID: v.RunID, Log: true})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(details)
	if len(b) > 800 || !strings.Contains(string(b), "BUG: reproduced") || strings.Contains(string(b), oldLog) {
		t.Fatal(string(b))
	}
	if _, e = a.Explore(ctx, ExploreRequest{RunID: v.RunID, Question: "wrong tool"}); e == nil {
		t.Fatal("mixed operation follow-up accepted")
	}
	testutil.Write(t, filepath.Join(root, "existing.txt"), "changed")
	v, e = a.Diagnose(ctx, DiagnoseRequest{ExploreRequest: ExploreRequest{RunID: v.RunID, Question: "again"}})
	if e == nil || v.Freshness != "stale" {
		t.Fatal(v, e)
	}
}
func TestDiagnosisCannotInventReproduction(t *testing.T) {
	root := t.TempDir()
	testutil.Write(t, filepath.Join(root, "existing.txt"), "original")
	log := filepath.Join(t.TempDir(), "log")
	testutil.Write(t, log, "BUG: reproduced\n")
	v, e := parseExploration(diagnosisFixture(), root, log)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range []*runner.Reproduction{nil, {Status: "not_run"}, {Status: "timed_out"}} {
		if validateDiagnosis(v, r) == nil {
			t.Fatal("unobserved reproduction accepted")
		}
	}
	raw := strings.Replace(diagnosisFixture(), "BUG: reproduced", "invented", 1)
	if _, e = parseExploration(raw, root, log); e == nil {
		t.Fatal("invented log evidence accepted")
	}
	os.Remove(log)
}
func TestStaticDiagnosis(t *testing.T) {
	a, root := setup(t)
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		return runner.Result{Session: "ses_test", Report: `{"answer":"Cannot confirm the cause without reproduction.","diagnosis":{"cause_status":"unverified","cause":"Insufficient evidence.","minimal_fix":"Inspect the failing branch before proposing a fix.","reproduction_assessment":"No command was supplied.","reproduced":false},"findings":[{"kind":"unverified","text":"Reproduction not run.","evidence":[]}]}`}, nil
	}}
	v, e := a.Diagnose(context.Background(), DiagnoseRequest{ExploreRequest: ExploreRequest{CWD: root, Question: "Why?"}})
	if e != nil || v.Reproduction.Status != "not_run" || v.Diagnosis.Reproduced {
		t.Fatal(v, e)
	}
}

func TestFullDiagnosisDetails(t *testing.T) {
	a, root := setup(t)
	text := strings.Repeat("evidence limitation ", 90)
	report := Exploration{Answer: "Summary", Diagnosis: &Diagnosis{CauseStatus: "unverified", Cause: text, MinimalFix: text, ReproductionAssessment: text}}
	raw, _ := json.Marshal(report)
	a.Engine = fake{func(runner.Request) (runner.Result, error) {
		return runner.Result{Session: "ses_test", Report: string(raw)}, nil
	}}
	v, e := a.Diagnose(context.Background(), DiagnoseRequest{ExploreRequest: ExploreRequest{CWD: root, Question: "Why?"}})
	if e != nil {
		t.Fatal(e)
	}
	details, e := a.QueryResult(context.Background(), ResultRequest{RunID: v.RunID, Detail: true, MaxOutputTokens: 8192})
	if e != nil {
		t.Fatal(e)
	}
	full := details.(ExploreResult)
	if full.Diagnosis.MinimalFix != text || full.Truncated {
		t.Fatal("full diagnosis inaccessible")
	}
	b, _ := json.Marshal(full)
	if len(b) > 8192 {
		t.Fatal("detail exceeds budget")
	}
}
