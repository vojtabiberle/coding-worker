package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"coding-worker/config"
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/workspace"
)

type ExploreRequest struct {
	CWD            string  `json:"cwd,omitempty" jsonschema:"Absolute worktree path, required for a new investigation."`
	RunID          string  `json:"run_id,omitempty" jsonschema:"Existing explore/diagnose/review run of the matching tool to resume; omit cwd and profile on follow-up."`
	Question       string  `json:"question"`
	Profile        *string `json:"profile,omitempty"`
	MaxOutputBytes int     `json:"max_output_bytes,omitempty" jsonschema:"Default 800, range 512 to 8192. UTF-8 serialized JSON byte budget."`
}
type Evidence struct {
	Artifact string `json:"artifact,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line"`
	EndLine  int    `json:"end_line"`
	Symbol   string `json:"symbol,omitempty"`
	Quote    string `json:"quote,omitempty"`
	SHA      string `json:"sha256,omitempty"`
}
type Finding struct {
	Severity string     `json:"severity,omitempty"`
	Kind     string     `json:"kind"`
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}
type Exploration struct {
	Diagnosis *Diagnosis `json:"diagnosis,omitempty"`
	Answer    string     `json:"answer"`
	Findings  []Finding  `json:"findings"`
}
type ExploreResult struct {
	Diagnosis    *Diagnosis           `json:"diagnosis,omitempty"`
	Reproduction *ReproductionSummary `json:"reproduction,omitempty"`
	RunID        string               `json:"run_id"`
	State        string               `json:"state"`
	Fingerprint  string               `json:"repository_state"`
	Freshness    string               `json:"freshness"`
	Iteration    int                  `json:"iteration"`
	Answer       string               `json:"answer,omitempty"`
	Findings     []Finding            `json:"findings,omitempty"`
	Truncated    bool                 `json:"truncated"`
	More         bool                 `json:"more"`
	NextOffset   int                  `json:"next_offset"`
	Error        string               `json:"error,omitempty"`
}
type ResultRequest struct {
	Log            bool   `json:"log,omitempty" jsonschema:"Retrieve a bounded reproduction.log byte range for a diagnosis or verification run."`
	LogOffset      int    `json:"log_offset,omitempty" jsonschema:"One-based log byte offset; default 1. Continue with returned next_offset."`
	RunID          string `json:"run_id"`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	FindingOffset  int    `json:"finding_offset,omitempty" jsonschema:"Zero-based explore/diagnose/review finding index; use returned next_offset."`
	Detail         bool   `json:"detail,omitempty" jsonschema:"Include investigation evidence quotes and hashes within the output budget."`
}

func outputBudget(n int) (int, error) {
	if n == 0 {
		return 800, nil
	}
	if n < 512 || n > 8192 {
		return 0, fmt.Errorf("max_output_bytes must be between 512 and 8192")
	}
	return n, nil
}
func (a *App) Explore(ctx context.Context, in ExploreRequest) (ExploreResult, error) {
	return a.investigate(ctx, in, "explore", nil)
}
func (a *App) investigate(ctx context.Context, in ExploreRequest, operation string, repro *runner.ReproduceRequest) (ExploreResult, error) {
	if strings.TrimSpace(in.Question) == "" || len(in.Question) > 32768 {
		return ExploreResult{}, fmt.Errorf("question must contain 1 to 32768 bytes")
	}
	if _, e := outputBudget(in.MaxOutputBytes); e != nil {
		return ExploreResult{}, e
	}
	var r store.Run
	var c config.Config
	var e error
	if in.RunID != "" {
		if in.CWD != "" || in.Profile != nil {
			return ExploreResult{}, fmt.Errorf("follow-up uses saved workspace and profile; omit cwd and profile")
		}
		r, e = a.Store.Get(in.RunID)
		if e != nil {
			return ExploreResult{}, e
		}
		if r.Operation != operation {
			return ExploreResult{}, fmt.Errorf("run operation is %s; use the matching investigation tool", r.Operation)
		}
	} else {
		w, e := workspace.Resolve(ctx, in.CWD)
		if e != nil {
			return ExploreResult{}, e
		}
		c, e = config.Load(a.ConfigPath)
		if e != nil {
			return ExploreResult{}, e
		}
		override := ""
		if in.Profile != nil {
			override = *in.Profile
		}
		p, e := c.Resolve(w.Root, override)
		if e != nil {
			return ExploreResult{}, e
		}
		r = store.Run{ID: store.ID(), Operation: operation, Origin: "mcp", Workspace: w, Config: p, Started: time.Now().UTC(), State: "running"}
	}
	w, e := workspace.Resolve(ctx, r.Workspace.Root)
	if e != nil {
		return ExploreResult{}, e
	}
	if w != r.Workspace {
		return ExploreResult{}, fmt.Errorf("worktree identity changed")
	}
	lock, e := a.lock(w.Root)
	if e != nil {
		return ExploreResult{State: "busy"}, e
	}
	defer lock.Close()
	if e = a.Store.Recover(w.Root); e != nil {
		return ExploreResult{}, e
	}
	before, e := workspace.Capture(ctx, w.Root)
	if e != nil {
		return ExploreResult{}, e
	}
	reviewData := ""
	if operation == "review" {
		reviewData, e = reviewContext(before)
		if e != nil {
			return ExploreResult{}, e
		}
	}
	resume := in.RunID != ""
	if resume {
		r, e = a.Store.Get(r.ID)
		if e != nil {
			return ExploreResult{}, e
		}
		fresh := exploreFreshness(ctx, r)
		if fresh != "current" {
			v := ExploreResult{RunID: r.ID, State: r.State, Freshness: fresh}
			return v, fmt.Errorf("exploration is stale or unverifiable; start a new investigation against the current state")
		}
		if r.Session == "" {
			return ExploreResult{}, fmt.Errorf("no resumable OpenCode session")
		}
	} else {
		r.Base = before.SHA
		if e = a.Store.Create(&r, c, true); e != nil {
			return ExploreResult{}, e
		}
	} // Explorations are not implementation experiment assignments.
	prompt := runner.ExplorePrompt
	if operation == "diagnose" {
		prompt += "\n" + runner.DiagnosePrompt
	}
	if resume && r.State == "failed" && len(r.Iterations) > 0 {
		previous, _ := json.Marshal(r.Iterations[len(r.Iterations)-1].Error)
		prompt += "\nRepair the previous rejected report in this same session, reusing the investigation and observations. Previous validation error (data): " + string(previous) + "\nReturn a complete valid report, not a patch. Recheck evidence only as needed."
	}
	prompt += reviewData
	prompt += "\nQuestion (task data):\n" + in.Question
	execution, runErr := a.execute(ctx, &r, before, prompt, lock, resume, repro)
	if execution.State == "running" && runErr == nil {
		return ExploreResult{RunID: r.ID, State: "running", Freshness: "unknown", Iteration: len(r.Iterations)}, nil
	}
	// Avoid reacquiring the lock through Result while it is still held here.
	v, e := exploreView(ctx, r, ResultRequest{RunID: r.ID, MaxOutputBytes: in.MaxOutputBytes})
	return v, errors.Join(runErr, e)
}
func source(root, path string) ([]byte, error) {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) == ".." || strings.HasPrefix(filepath.Clean(path), ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("evidence path must be worktree-relative")
	}
	real, e := filepath.EvalSymlinks(filepath.Join(root, path))
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(root, real)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("evidence escapes worktree")
	}
	info, e := os.Stat(real)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, fmt.Errorf("evidence requires regular source files up to 16 MiB")
	}
	return os.ReadFile(real)
}
func parseExploration(raw, root string, logs ...string) (Exploration, error) {
	var v Exploration
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		return v, fmt.Errorf("exploration did not return a valid JSON report")
	}
	if strings.TrimSpace(v.Answer) == "" || len(v.Answer) > 4000 {
		return v, fmt.Errorf("exploration answer missing")
	}
	if len(v.Findings) > 100 {
		return v, fmt.Errorf("too many findings")
	}
	for i := range v.Findings {
		f := &v.Findings[i]
		if f.Kind != "fact" && f.Kind != "hypothesis" && f.Kind != "unverified" {
			return v, fmt.Errorf("invalid finding kind")
		}
		if strings.TrimSpace(f.Text) == "" || len(f.Text) > 4000 {
			return v, fmt.Errorf("finding text missing or too long")
		}
		if f.Kind != "unverified" && len(f.Evidence) == 0 {
			return v, fmt.Errorf("facts and hypotheses require evidence")
		}
		for j := range f.Evidence {
			ev := &f.Evidence[j]
			var b []byte
			var e error
			if ev.Artifact != "" {
				if ev.File != "" {
					return v, &citationError{fmt.Sprintf("finding %d evidence %d: artifact %q cannot also name file %q", i, j, ev.Artifact, ev.File)}
				}
				switch ev.Artifact {
				case "reproduction.log":
					if len(logs) == 0 || logs[0] == "" {
						return v, &citationError{fmt.Sprintf("finding %d evidence %d: artifact %q is unavailable in this run; do not cite it", i, j, ev.Artifact)}
					}
					b, e = os.ReadFile(logs[0])
				case "review.diff":
					if len(logs) < 2 || logs[1] == "" {
						return v, &citationError{fmt.Sprintf("finding %d evidence %d: artifact %q is unavailable in this run; do not cite it", i, j, ev.Artifact)}
					}
					b = []byte(logs[1])
				default:
					return v, &citationError{fmt.Sprintf("finding %d evidence %d: unknown evidence artifact %q; only supplied reproduction.log or review.diff artifacts are supported", i, j, ev.Artifact)}
				}
			} else {
				b, e = source(root, ev.File)
			}
			if e != nil {
				return v, &citationError{fmt.Sprintf("finding %d evidence %d (%s:%d): invalid evidence file: %v", i, j, ev.File+ev.Artifact, ev.Line, e)}
			}
			lines := strings.Split(string(b), "\n")
			if ev.EndLine == 0 {
				ev.EndLine = ev.Line
			}
			if ev.Line < 1 || ev.EndLine < ev.Line || ev.EndLine > len(lines) || ev.EndLine-ev.Line > 100 {
				return v, &citationError{fmt.Sprintf("finding %d evidence %d (%s%s:%d-%d): invalid evidence line range", i, j, ev.File, ev.Artifact, ev.Line, ev.EndLine)}
			}
			if ev.Quote == "" || len(ev.Quote) > 4000 || !strings.Contains(strings.Join(lines[ev.Line-1:ev.EndLine], "\n"), ev.Quote) {
				return v, &citationError{fmt.Sprintf("finding %d evidence %d (%s%s:%d-%d): evidence quote does not match cited source lines", i, j, ev.File, ev.Artifact, ev.Line, ev.EndLine)}
			}
			hash := sha256.Sum256(b)
			ev.SHA = hex.EncodeToString(hash[:])
		}
	}
	return v, nil
}
func exploreFreshness(ctx context.Context, r store.Run) string {
	if r.State == "running" || len(r.Iterations) == 0 {
		return "unknown"
	}
	it := r.Iterations[len(r.Iterations)-1]
	w, e := workspace.Resolve(ctx, r.Workspace.Root)
	if e != nil || w != r.Workspace {
		return "unknown"
	}
	s, e := workspace.Capture(ctx, r.Workspace.Root)
	if e != nil {
		return "unknown"
	}
	if it.Before.Fingerprint != it.After.Fingerprint || s.Fingerprint != it.After.Fingerprint {
		return "stale"
	}
	if r.Operation == "verify" {
		return "current"
	}
	var report Exploration
	if json.Unmarshal([]byte(it.Result.Report), &report) != nil {
		if r.State == "failed" {
			return "current"
		}
		return "unknown"
	}
	for _, f := range report.Findings {
		for _, ev := range f.Evidence {
			// Failed legacy/unvalidated reports have no trusted evidence hash.
			// Repository fingerprints still gate repair; this does not validate conclusions.
			if r.State == "failed" && ev.SHA == "" {
				continue
			}
			var b []byte
			var e error
			if ev.Artifact != "" {
				if ev.Artifact == "review.diff" && r.Operation == "review" {
					b = []byte(it.Before.Diff)
				} else {
					if ev.Artifact != "reproduction.log" || it.Reproduction == nil || it.Reproduction.Log == "" {
						return "unknown"
					}
					b, e = os.ReadFile(it.Reproduction.Log)
				}
			} else {
				b, e = source(r.Workspace.Root, ev.File)
			}
			if e != nil {
				return "stale"
			}
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != ev.SHA {
				return "stale"
			}
		}
	}
	return "current"
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n] + "…"
}
func exploreView(ctx context.Context, r store.Run, in ResultRequest) (ExploreResult, error) {
	budget, e := outputBudget(in.MaxOutputBytes)
	if e != nil {
		return ExploreResult{}, e
	}
	if in.FindingOffset < 0 {
		return ExploreResult{}, fmt.Errorf("finding_offset must be nonnegative")
	}
	v := ExploreResult{RunID: r.ID, State: r.State, Iteration: len(r.Iterations), Freshness: exploreFreshness(ctx, r), NextOffset: in.FindingOffset}
	if len(r.Iterations) == 0 {
		return v, nil
	}
	it := r.Iterations[len(r.Iterations)-1]
	v.Fingerprint = it.Before.Fingerprint
	if it.Reproduction != nil {
		v.Reproduction = &ReproductionSummary{it.Reproduction.Status, it.Reproduction.Exit, it.Reproduction.LogTruncated}
	}
	if r.State != "completed" {
		v.Error = it.Error
		if len(it.Result.Failures) > 0 {
			v.Error = runner.ErrorDetail(it.Result.Failures[0])
		}
		for {
			b, _ := json.Marshal(v)
			if len(b) <= budget || v.Error == "" {
				break
			}
			v.Error = clip(v.Error, len(v.Error)/2)
			v.Truncated = true
			if len(v.Error) <= 3 {
				v.Error = ""
			}
		}
		return v, nil
	}
	var report Exploration
	if e = json.Unmarshal([]byte(it.Result.Report), &report); e != nil {
		return v, fmt.Errorf("stored exploration report is invalid")
	}
	if in.FindingOffset > len(report.Findings) {
		return v, fmt.Errorf("finding_offset exceeds report")
	}
	if report.Diagnosis != nil {
		d := *report.Diagnosis
		limit := 80
		if in.Detail {
			limit = min(2000, budget/4)
		}
		d.Cause = clip(d.Cause, limit)
		d.MinimalFix = clip(d.MinimalFix, limit)
		d.ReproductionAssessment = clip(d.ReproductionAssessment, limit)
		v.Truncated = d.Cause != report.Diagnosis.Cause || d.MinimalFix != report.Diagnosis.MinimalFix || d.ReproductionAssessment != report.Diagnosis.ReproductionAssessment
		v.Diagnosis = &d
	}
	answerLimit := 160
	if in.Detail {
		answerLimit = budget / 2
	}
	if v.Diagnosis == nil {
		v.Answer = clip(report.Answer, answerLimit)
		v.Truncated = v.Truncated || v.Answer != report.Answer
	}
	v.More = in.FindingOffset < len(report.Findings)
	for i := in.FindingOffset; i < len(report.Findings); i++ {
		f := report.Findings[i]
		if !in.Detail {
			f.Text = clip(f.Text, 180)
			f.Evidence = append([]Evidence(nil), f.Evidence...)
			for j := range f.Evidence {
				f.Evidence[j].Quote = ""
				f.Evidence[j].SHA = ""
				f.Evidence[j].Symbol = ""
			}
		}
		candidate := v
		candidate.Truncated = candidate.Truncated || f.Text != report.Findings[i].Text
		candidate.Findings = append(append([]Finding(nil), v.Findings...), f)
		candidate.NextOffset = i + 1
		candidate.More = i+1 < len(report.Findings)
		b, _ := json.Marshal(candidate)
		if len(b) > budget {
			break
		}
		v = candidate
	}
	return v, nil
}
func (a *App) QueryResult(ctx context.Context, in ResultRequest) (any, error) {
	if in.Log {
		return a.reproductionLog(ctx, in)
	}
	r, e := a.Store.Get(in.RunID)
	if e != nil {
		return nil, e
	}
	if r.Operation == "verify" {
		if _, e = a.Result(in.RunID); e != nil {
			return nil, e
		}
		r, e = a.Store.Get(in.RunID)
		if e != nil {
			return nil, e
		}
		return verifyView(ctx, r, in.MaxOutputBytes)
	}
	if !investigation(r.Operation) {
		return a.Result(in.RunID)
	}
	if _, e = a.Result(in.RunID); e != nil {
		return nil, e
	}
	r, e = a.Store.Get(in.RunID)
	if e != nil {
		return nil, e
	}
	return exploreView(ctx, r, in)
}
func (a *App) Status(ctx context.Context, id string) (any, error) {
	v, e := a.Result(id)
	if e != nil {
		return nil, e
	}
	fresh := ""
	if investigation(v.Operation) || v.Operation == "verify" {
		r, e := a.Store.Get(id)
		if e != nil {
			return nil, e
		}
		fresh = exploreFreshness(ctx, r)
	}
	r, err := a.Store.Get(id)
	if err != nil {
		return nil, err
	}
	phase := r.Phase
	if r.State != "running" {
		phase = r.State
	}
	return struct {
		Phase        string     `json:"phase,omitempty"`
		LastProgress *time.Time `json:"last_progress,omitempty"`
		RunID        string     `json:"run_id"`
		State        string     `json:"state"`
		Operation    string     `json:"operation"`
		Iterations   int        `json:"iteration_count"`
		Started      time.Time  `json:"started_at"`
		Finished     *time.Time `json:"finished_at"`
		Freshness    string     `json:"freshness,omitempty"`
	}{phase, r.LastProgress, v.RunID, v.State, v.Operation, v.Iterations, v.Started, v.Finished, fresh}, nil
}
