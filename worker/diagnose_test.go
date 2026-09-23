package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojtabiberle/coding-worker/internal/testutil"
	"github.com/vojtabiberle/coding-worker/runner"
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
	details, e := a.QueryResult(context.Background(), ResultRequest{RunID: v.RunID, Detail: true, MaxOutputBytes: 8192})
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

func TestDiagnosisCitationCorrectionDoesNotRepeatCommand(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	a, root := setup(t)
	calls := 0
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		calls++
		report := diagnosisFixture()
		if calls == 1 {
			report = strings.Replace(report, `"quote":"original"`, `"quote":"inaccurate"`, 1)
		}
		return runner.Result{Session: "ses_test", Report: report}, nil
	}}
	v, e := a.Diagnose(context.Background(), DiagnoseRequest{ExploreRequest: ExploreRequest{CWD: root, Question: "Diagnose"}, ReproductionCommand: []string{"/bin/sh", "-c", `printf 'BUG: reproduced\n'`}})
	if e != nil || v.State != "completed" || calls != 2 {
		t.Fatal(v, e, calls)
	}
	r, e := a.Store.Get(v.RunID)
	if e != nil {
		t.Fatal(e)
	}
	logs, e := filepath.Glob(filepath.Join(a.Store.Dir, "reproductions", r.ID, "*.log"))
	if e != nil || len(logs) != 1 || len(r.Iterations) != 1 || r.Iterations[0].CitationAttempt == nil {
		t.Fatal(logs, e)
	}
}

func TestDiagnosisArtifactCorrection(t *testing.T) {
	for _, artifact := range []string{"reproduction.log", "invented.log", "review.diff"} {
		t.Run(artifact, func(t *testing.T) {
			a, root := setup(t)
			calls := 0
			a.Engine = fake{func(req runner.Request) (runner.Result, error) {
				calls++
				if calls == 1 && strings.Contains(req.Prompt, `"log_path":"reproduction.log"`) {
					t.Fatal("nonexistent log advertised")
				}
				if calls == 2 && (!strings.Contains(req.Prompt, artifact) || !strings.Contains(req.Prompt, "finding 0 evidence 0")) {
					t.Fatal("missing artifact identity in corrective prompt")
				}
				report := Exploration{Answer: "No reproduction was executed.", Diagnosis: &Diagnosis{CauseStatus: "unverified", Cause: "Unconfirmed.", MinimalFix: "Need reproduction.", ReproductionAssessment: "No command supplied."}, Findings: []Finding{{Kind: "unverified", Text: "Runtime evidence missing.", Evidence: []Evidence{}}}}
				if calls == 1 {
					report.Findings[0] = Finding{Kind: "fact", Text: "Incorrect metadata citation.", Evidence: []Evidence{{Artifact: artifact, Line: 1, Quote: `{"status":"not_run"}`}}}
				}
				b, _ := json.Marshal(report)
				return runner.Result{Session: "ses_test", Report: string(b)}, nil
			}}
			v, e := a.Diagnose(context.Background(), DiagnoseRequest{ExploreRequest: ExploreRequest{CWD: root, Question: "Static diagnosis"}})
			if e != nil || v.State != "completed" || calls != 2 || v.Diagnosis.Reproduced {
				t.Fatal(v, e, calls)
			}
			r, e := a.Store.Get(v.RunID)
			if e != nil || r.Iterations[0].Reproduction.Log != "" || r.Iterations[0].CitationAttempt == nil {
				t.Fatal(r, e)
			}
		})
	}
}
func TestUnknownArtifactNamesTheCitation(t *testing.T) {
	_, root := setup(t)
	_, e := parseExploration(`{"answer":"Claim.","findings":[{"kind":"fact","text":"Claim.","evidence":[{"artifact":"invented.log","line":1,"quote":"data"}]}]}`, root)
	if e == nil || !strings.Contains(e.Error(), `unknown evidence artifact "invented.log"`) || !strings.Contains(e.Error(), "finding 0 evidence 0") {
		t.Fatal(e)
	}
}

func TestRejectedStaticDiagnosisCanBeRepairedInSession(t *testing.T) {
	a, root := setup(t)
	ctx := context.Background()
	calls := 0
	a.Engine = fake{func(req runner.Request) (runner.Result, error) {
		calls++
		if !strings.Contains(req.Prompt, "cause_status must be hypothesis or unverified") {
			t.Fatal("static constraints missing")
		}
		if calls == 2 && !strings.Contains(req.Prompt, "supported cause requires reproduction") {
			t.Fatal("validation feedback missing")
		}
		if calls == 3 && !strings.Contains(req.Prompt, "Repair the previous rejected report in this same session") {
			t.Fatal("manual repair context missing")
		}
		status := "supported"
		if calls >= 3 {
			status = "hypothesis"
		}
		report := Exploration{Answer: "Source suggests a guard, but no reproduction.", Diagnosis: &Diagnosis{CauseStatus: status, Cause: "Source guard.", MinimalFix: "No confirmed repair.", ReproductionAssessment: "Not run."}, Findings: []Finding{{Kind: "fact", Text: "Source content.", Evidence: []Evidence{{File: "existing.txt", Line: 1, Quote: "original"}}}}}
		b, _ := json.Marshal(report)
		return runner.Result{Session: "ses_test", Report: string(b)}, nil
	}}
	v, e := a.Diagnose(ctx, DiagnoseRequest{ExploreRequest: ExploreRequest{CWD: root, Question: "Diagnose statically"}})
	if e == nil || v.State != "failed" || calls != 2 || v.Freshness != "current" {
		t.Fatal(v, e, calls)
	}
	r, e := a.Store.Get(v.RunID)
	if e != nil {
		t.Fatal(e)
	}
	var normalized Exploration
	if e = json.Unmarshal([]byte(r.Iterations[0].Result.Report), &normalized); e != nil {
		t.Fatal(e)
	}
	if normalized.Findings[0].Evidence[0].SHA == "" {
		t.Fatal("validated hashes discarded with semantic failure")
	}
	// Reproduce the historical run format: valid citations but hashes not persisted.
	normalized.Findings[0].Evidence[0].SHA = ""
	b, _ := json.Marshal(normalized)
	r.Iterations[0].Result.Report = string(b)
	if e = a.Store.Save(&r); e != nil {
		t.Fatal(e)
	}
	v, e = a.Diagnose(ctx, DiagnoseRequest{ExploreRequest: ExploreRequest{RunID: r.ID, Question: "Correct the cause status without another investigation"}})
	if e != nil || v.State != "completed" || v.Iteration != 2 || calls != 3 {
		t.Fatal(v, e, calls)
	}
	testutil.Write(t, filepath.Join(root, "existing.txt"), "changed\n")
	v, e = a.Diagnose(ctx, DiagnoseRequest{ExploreRequest: ExploreRequest{RunID: r.ID, Question: "Again"}})
	if e == nil || v.Freshness != "stale" || calls != 3 {
		t.Fatal(v, e, calls)
	}
}
