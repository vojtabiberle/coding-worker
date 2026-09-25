package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vojtabiberle/coding-worker/runner"
	"github.com/vojtabiberle/coding-worker/store"
	"github.com/vojtabiberle/coding-worker/workspace"
)

// One correction within the same iteration/session; never rerun reproduction.
func (a *App) repairCitation(ctx context.Context, r *store.Run, req runner.Request, first runner.Result) (runner.Result, error) {
	it := &r.Iterations[len(r.Iterations)-1]
	logPath, diff := "", ""
	if it.Reproduction != nil {
		logPath = it.Reproduction.Log
	}
	if r.Operation == "review" {
		diff = it.Before.Diff
	}
	report, err := parseExploration(first.Report, r.Workspace.Root, logPath, diff)
	semantic := false
	if err == nil && r.Operation == "diagnose" {
		err = validateDiagnosis(report, it.Reproduction)
		semantic = err != nil
	}
	var citation *citationError
	if (!errors.As(err, &citation) && !semantic) || first.Session == "" || ctx.Err() != nil {
		return first, nil
	}
	current, e := workspace.Capture(ctx, r.Workspace.Root)
	if e != nil {
		return first, e
	}
	if current.Fingerprint != it.Before.Fingerprint {
		return first, fmt.Errorf("repository changed before citation correction")
	}
	it.CitationAttempt = &first
	phase := "correcting_citations"
	if semantic {
		phase = "correcting_report"
	}
	if e = a.phase(r, phase); e != nil {
		return first, e
	}
	failure, _ := json.Marshal(err.Error())
	req.Prompt = "Your report failed validation. Validation error (data): " + string(failure) + "\nReturn the entire corrected JSON report, preserving the required operation schema. Read source to verify exact lines and quotes, or remove unsupported findings and explain the limitation. Do not change files or run commands. There is only one corrective attempt. Reuse the investigation and supplied observations; do not repeat the exploration unnecessarily. For static diagnosis, source facts can be certain but the causal diagnosis must remain hypothesis or unverified, with reproduced=false. supported is reserved for a reproduced symptom with source and log facts.\nOriginal task and supplied observations:\n" + req.Prompt
	next, e := a.engine(r.Config.Profile).Continue(ctx, first.Session, req)
	if next.Session == "" {
		next.Session = first.Session
	}
	next.Metrics.Input = sumMetric(first.Metrics.Input, next.Metrics.Input)
	next.Metrics.Output = sumMetric(first.Metrics.Output, next.Metrics.Output)
	next.Metrics.Cached = sumMetric(first.Metrics.Cached, next.Metrics.Cached)
	next.Metrics.Cost = sumMetric(first.Metrics.Cost, next.Metrics.Cost)
	if e == nil {
		e = a.phase(r, "validating")
	}
	return next, e
}
func sumMetric[T int64 | float64](a, b *T) *T {
	if a == nil || b == nil {
		return nil
	}
	v := *a + *b
	return &v
}
