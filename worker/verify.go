package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"coding-worker/config"
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/workspace"
)

// One explicit check per run; multiple checks can use the repository's check script.
type VerifyRequest struct {
	CWD            string   `json:"cwd"`
	Command        []string `json:"command" jsonschema:"Required explicit argv for a repository check. No shell interpolation unless a shell is explicitly invoked. Filesystem read-only except private temporary storage; network disabled."`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" jsonschema:"Default 30 seconds; maximum 120."`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty"`
}
type VerifyResult struct {
	RunID        string  `json:"run_id"`
	State        string  `json:"state"`
	Outcome      string  `json:"outcome"`
	Exit         *int    `json:"exit_status"`
	Duration     float64 `json:"duration_seconds"`
	Freshness    string  `json:"freshness"`
	Fingerprint  string  `json:"repository_state"`
	Excerpt      string  `json:"excerpt,omitempty"`
	ExcerptLine  int     `json:"excerpt_line,omitempty"`
	LogTruncated bool    `json:"log_truncated"`
	Truncated    bool    `json:"truncated"`
	Error        string  `json:"error,omitempty"`
}

func (a *App) Verify(ctx context.Context, in VerifyRequest) (VerifyResult, error) {
	spec := runner.ReproduceRequest{Command: in.Command, TimeoutSeconds: in.TimeoutSeconds}
	if len(in.Command) == 0 {
		return VerifyResult{}, fmt.Errorf("command is required")
	}
	if e := spec.Validate(); e != nil {
		return VerifyResult{}, e
	}
	if _, e := outputBudget(in.MaxOutputBytes); e != nil {
		return VerifyResult{}, e
	}
	w, e := workspace.Resolve(ctx, in.CWD)
	if e != nil {
		return VerifyResult{}, e
	}
	lock, e := a.lock(w.Root)
	if e != nil {
		return VerifyResult{State: "busy"}, e
	}
	defer lock.Close()
	if e = a.Store.Recover(w.Root); e != nil {
		return VerifyResult{}, e
	}
	before, e := workspace.Capture(ctx, w.Root)
	if e != nil {
		return VerifyResult{}, e
	}
	now := time.Now().UTC()
	r := store.Run{ID: store.ID(), Operation: "verify", Origin: "mcp", Workspace: w, Base: before.SHA, Started: now, State: "running", Iterations: []store.Iteration{{Number: 1, Started: now, Before: before}}}
	if e = a.Store.Create(&r, config.Config{}, true); e != nil {
		return VerifyResult{}, e
	}
	if ctx.Value(asyncKey{}) == true {
		e = a.launch(lock, func(jobCtx context.Context, held *os.File) {
			_, err := a.finishVerify(jobCtx, &r, spec, held, in.MaxOutputBytes)
			if err != nil {
				slog.Error("background verification failed", "run_id", r.ID, "error", err)
			}
		})
		if e != nil {
			r.State = "failed"
			r.Iterations[0].Error = e.Error()
			return VerifyResult{RunID: r.ID, State: r.State}, errors.Join(e, a.Store.Save(&r))
		}
		return VerifyResult{RunID: r.ID, State: "running", Outcome: "unknown", Freshness: "unknown"}, nil
	}
	return a.finishVerify(ctx, &r, spec, lock, in.MaxOutputBytes)
}
func (a *App) finishVerify(ctx context.Context, r *store.Run, spec runner.ReproduceRequest, lock *os.File, budget int) (VerifyResult, error) {
	if e := a.phase(r, "checking"); e != nil {
		return VerifyResult{}, e
	}
	w := r.Workspace
	before := r.Iterations[0].Before
	var e error
	var now time.Time
	observation, runErr := runner.Reproduce(ctx, runner.Request{CWD: w.Root, Lock: lock, Reproduction: &spec, ReproductionDir: filepath.Join(a.Store.Dir, "reproductions", r.ID, "1")})
	it := &r.Iterations[0]
	it.Reproduction = &observation
	captureCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	it.After, e = workspace.Capture(captureCtx, w.Root)
	runErr = errors.Join(runErr, e)
	if e == nil && before.Fingerprint != it.After.Fingerprint {
		runErr = errors.Join(runErr, fmt.Errorf("repository changed during verification"))
	}
	r.State = "completed"
	if runErr != nil {
		r.State = "failed"
		it.Error = runErr.Error()
	}
	if ctx.Err() != nil {
		r.State = "cancelled"
	}
	now = time.Now().UTC()
	r.Finished = &now
	it.Finished = &now
	e = a.Store.Save(r)
	v, viewErr := verifyView(captureCtx, *r, budget)
	return v, errors.Join(runErr, e, viewErr)
}
func verifyView(ctx context.Context, r store.Run, budget int) (VerifyResult, error) {
	n, e := outputBudget(budget)
	if e != nil {
		return VerifyResult{}, e
	}
	v := VerifyResult{RunID: r.ID, State: r.State, Outcome: "unknown", Freshness: exploreFreshness(ctx, r)}
	if len(r.Iterations) > 0 {
		it := r.Iterations[len(r.Iterations)-1]
		v.Fingerprint = it.After.Fingerprint
		v.Error = it.Error
		if o := it.Reproduction; o != nil {
			v.Outcome = o.Status
			v.Exit = o.Exit
			v.Duration = o.Duration
			v.Excerpt = o.Excerpt
			v.ExcerptLine = o.ExcerptLine
			v.LogTruncated = o.LogTruncated
			if o.Status == "finished" && o.Exit != nil {
				v.Outcome = "failed"
				if *o.Exit == 0 {
					v.Outcome = "passed"
				}
			}
		}
	}
	for {
		b, _ := json.Marshal(v)
		if len(b) <= n {
			return v, nil
		}
		if v.Excerpt == "" {
			if v.Error == "" {
				return v, fmt.Errorf("verification metadata exceeds output budget")
			}
			v.Error = clip(v.Error, len(v.Error)/2)
			if len(v.Error) <= 3 {
				v.Error = ""
			}
			v.Truncated = true
			continue
		}
		v.Excerpt = v.Excerpt[:len(v.Excerpt)/2]
		v.Truncated = true
	}
}
