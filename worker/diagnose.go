package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"coding-worker/runner"
)

type DiagnoseRequest struct {
	ExploreRequest
	ReproductionCommand []string `json:"reproduction_command,omitempty" jsonschema:"Explicit argv to execute once under a read-only filesystem and disabled network. No shell interpolation unless you explicitly invoke a shell. Omit for static diagnosis or to reuse previous observations on follow-up."`
	TimeoutSeconds      int      `json:"timeout_seconds,omitempty" jsonschema:"Reproduction timeout: default 30 seconds, maximum 120."`
}
type Diagnosis struct {
	CauseStatus            string `json:"cause_status"`
	Cause                  string `json:"cause"`
	MinimalFix             string `json:"minimal_fix"`
	ReproductionAssessment string `json:"reproduction_assessment"`
	Reproduced             bool   `json:"reproduced"`
}
type ReproductionSummary struct {
	Status       string `json:"status"`
	Exit         *int   `json:"exit_status"`
	LogTruncated bool   `json:"log_truncated"`
}

func investigation(operation string) bool {
	return operation == "explore" || operation == "diagnose" || operation == "review"
}
func (a *App) Diagnose(ctx context.Context, in DiagnoseRequest) (ExploreResult, error) {
	spec := runner.ReproduceRequest{Command: in.ReproductionCommand, TimeoutSeconds: in.TimeoutSeconds}
	if e := spec.Validate(); e != nil {
		return ExploreResult{}, e
	}
	return a.investigate(ctx, in.ExploreRequest, "diagnose", &spec)
}
func validateDiagnosis(v Exploration, r *runner.Reproduction) error {
	d := v.Diagnosis
	if d == nil {
		return fmt.Errorf("diagnosis report missing")
	}
	if d.CauseStatus != "supported" && d.CauseStatus != "hypothesis" && d.CauseStatus != "unverified" {
		return fmt.Errorf("invalid cause_status")
	}
	for _, s := range []string{d.Cause, d.MinimalFix, d.ReproductionAssessment} {
		if strings.TrimSpace(s) == "" || len(s) > 2000 {
			return fmt.Errorf("diagnosis cause, minimal_fix and reproduction_assessment must contain 1 to 2000 bytes")
		}
	}
	sourceFact, logFact := false, false
	for _, f := range v.Findings {
		if f.Kind != "fact" {
			continue
		}
		for _, ev := range f.Evidence {
			sourceFact = sourceFact || ev.File != ""
			logFact = logFact || ev.Artifact != ""
		}
	}
	if d.Reproduced && (r == nil || r.Status != "finished" || r.Exit == nil || !logFact) {
		return fmt.Errorf("claimed reproduction requires a finished command and validated log evidence")
	}
	if d.CauseStatus == "supported" && (!d.Reproduced || !sourceFact || !logFact) {
		return fmt.Errorf("supported cause requires reproduction and source/log facts; otherwise use hypothesis or unverified")
	}
	return nil
}

// Only the stored log of this run/iteration is retrievable; callers cannot supply paths.
func (a *App) reproductionLog(ctx context.Context, in ResultRequest) (any, error) {
	budget, e := outputBudget(in.MaxOutputTokens)
	if e != nil {
		return nil, e
	}
	r, e := a.Store.Get(in.RunID)
	if e != nil {
		return nil, e
	}
	if (r.Operation != "diagnose" && r.Operation != "verify") || len(r.Iterations) == 0 {
		return nil, fmt.Errorf("run has no command log")
	}
	it := r.Iterations[len(r.Iterations)-1]
	if it.Reproduction == nil || it.Reproduction.Log == "" {
		return nil, fmt.Errorf("no reproduction was executed")
	}
	offset := in.LogOffset
	if offset == 0 {
		offset = 1
	}
	text, line, e := runner.ReadLog(it.Reproduction.Log, offset, budget)
	if e != nil {
		return nil, e
	}
	info, e := os.Stat(it.Reproduction.Log)
	if e != nil {
		return nil, e
	}
	view := struct {
		RunID      string `json:"run_id"`
		Artifact   string `json:"artifact"`
		Offset     int    `json:"offset"`
		NextOffset int    `json:"next_offset"`
		Line       int    `json:"line"`
		Text       string `json:"text"`
		More       bool   `json:"more"`
		Truncated  bool   `json:"log_truncated"`
		Freshness  string `json:"freshness"`
	}{r.ID, "reproduction.log", offset, offset + len(text), line, text, int64(offset-1+len(text)) < info.Size(), it.Reproduction.LogTruncated, exploreFreshness(ctx, r)}
	for {
		b, _ := json.Marshal(view)
		if len(b) <= budget {
			break
		}
		if len(view.Text) == 0 {
			return nil, fmt.Errorf("log metadata exceeds output budget")
		}
		view.Text = view.Text[:len(view.Text)/2]
		view.NextOffset = offset + len(view.Text)
		view.More = int64(view.NextOffset-1) < info.Size()
	}
	return view, nil
}
