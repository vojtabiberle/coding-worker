package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coding-worker/config"
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/workspace"
)

type App struct {
	ConfigPath string
	Store      *store.Store
	Engine     runner.Engine
	Debug      bool
}

func Open() (*App, error) {
	c, d := config.Paths()
	s, e := store.Open(d)
	if e != nil {
		return nil, e
	}
	return &App{ConfigPath: c, Store: s, Engine: runner.OpenCode{}, Debug: os.Getenv("CODING_WORKER_DEBUG") == "1"}, nil
}

type ImplementRequest struct {
	CWD         string   `json:"cwd"`
	Objective   string   `json:"objective"`
	Constraints []string `json:"constraints,omitempty"`
	Acceptance  []string `json:"acceptance_criteria,omitempty"`
	Context     string   `json:"relevant_context,omitempty"`
	Profile     *string  `json:"profile,omitempty"`
	Origin      string   `json:"origin,omitempty"`
}
type ContinueRequest struct {
	RunID      string   `json:"run_id"`
	Feedback   string   `json:"feedback"`
	Acceptance []string `json:"acceptance_criteria,omitempty"`
}
type Result struct {
	RunID      string              `json:"run_id"`
	Operation  string              `json:"operation,omitempty"`
	State      string              `json:"state"`
	Workspace  workspace.Workspace `json:"workspace"`
	Config     config.Resolved     `json:"config"`
	Provider   string              `json:"provider"`
	Session    string              `json:"opencode_session_id"`
	Iterations int                 `json:"iteration_count"`
	Started    time.Time           `json:"started_at"`
	Finished   *time.Time          `json:"finished_at"`
	Latest     *store.Iteration    `json:"latest,omitempty"`
	Reviews    []store.Review      `json:"reviews"`
	Error      string              `json:"error,omitempty"`
}

func (a *App) lock(root string) (*os.File, error) {
	return workspace.Lock(filepath.Join(a.Store.Dir, "locks"), root)
}
func (a *App) Implement(ctx context.Context, in ImplementRequest) (Result, error) {
	if strings.TrimSpace(in.Objective) == "" {
		return Result{}, fmt.Errorf("objective required")
	}
	w, e := workspace.Resolve(ctx, in.CWD)
	if e != nil {
		return Result{}, e
	}
	c, e := config.Load(a.ConfigPath)
	if e != nil {
		return Result{}, e
	}
	profile := ""
	if in.Profile != nil {
		profile = *in.Profile
	}
	p, e := c.Resolve(w.Root, profile)
	if e != nil {
		return Result{}, e
	}
	lock, e := a.lock(w.Root)
	if e != nil {
		return busy(e), e
	}
	defer lock.Close()
	if e = a.Store.Recover(w.Root); e != nil {
		return Result{}, e
	}
	before, e := workspace.Capture(ctx, w.Root)
	if e != nil {
		return Result{}, e
	}
	r := store.Run{ID: store.ID(), Origin: in.Origin, Workspace: w, Base: before.SHA, Config: p, Started: time.Now().UTC(), State: "running"}
	if e = a.Store.Create(&r, c, profile != ""); e != nil {
		return Result{}, e
	}
	return a.execute(ctx, &r, before, runner.Prompt(in.Objective, in.Constraints, in.Acceptance, in.Context), lock, false)
}
func busy(e error) Result {
	if errors.Is(e, workspace.ErrBusy) {
		return Result{State: "busy", Error: e.Error()}
	}
	return Result{Error: e.Error()}
}
func (a *App) Continue(ctx context.Context, in ContinueRequest) (Result, error) {
	if strings.TrimSpace(in.Feedback) == "" {
		return Result{}, fmt.Errorf("feedback required")
	}
	r, e := a.Store.Get(in.RunID)
	if e != nil {
		return Result{}, e
	}
	w, e := workspace.Resolve(ctx, r.Workspace.Root)
	if e != nil {
		return Result{}, fmt.Errorf("original worktree unavailable: %w", e)
	}
	if w != r.Workspace {
		return Result{}, fmt.Errorf("original worktree identity changed")
	}
	lock, e := a.lock(w.Root)
	if e != nil {
		return busy(e), e
	}
	defer lock.Close()
	if e = a.Store.Recover(w.Root); e != nil {
		return Result{}, e
	}
	r, e = a.Store.Get(in.RunID)
	if e != nil {
		return Result{}, e
	}
	if r.Operation == "explore" {
		return Result{}, fmt.Errorf("use worker_explore with run_id for investigation follow-ups")
	}
	if r.Session == "" {
		return Result{}, fmt.Errorf("run has no resumable OpenCode session")
	}
	before, e := workspace.Capture(ctx, w.Root)
	if e != nil {
		return Result{}, e
	}
	if len(r.Iterations) == 0 {
		return Result{}, fmt.Errorf("no previous iteration")
	}
	last := r.Iterations[len(r.Iterations)-1].After
	if last.Fingerprint == "" || before.Fingerprint != last.Fingerprint {
		return Result{}, fmt.Errorf("repository drift: HEAD, branch, tracked changes or untracked files changed; start a new run")
	}
	return a.execute(ctx, &r, before, runner.Prompt(in.Feedback, nil, in.Acceptance, "Continue the existing implementation session."), lock, true)
}
func (a *App) execute(ctx context.Context, r *store.Run, before workspace.Snapshot, prompt string, lock *os.File, resume bool) (Result, error) {
	i := store.Iteration{Number: len(r.Iterations) + 1, Started: time.Now().UTC(), Before: before}
	r.Iterations = append(r.Iterations, i)
	r.State = "running"
	r.Finished = nil
	if e := a.Store.Save(r); e != nil {
		return Result{}, e
	}
	req := runner.Request{CWD: r.Workspace.Root, Prompt: prompt, Profile: r.Config.Profile, Lock: lock}
	if r.Operation == "explore" {
		req.ReadOnly = true
		req.RuntimeDir = filepath.Join(a.Store.Dir, "explore", r.ID)
		req.Profile.Agent = runner.ExploreAgent + "-" + r.ID
	}
	return a.invoke(ctx, r, req, resume)
}
func (a *App) invoke(ctx context.Context, r *store.Run, req runner.Request, resume bool) (Result, error) {
	idx := len(r.Iterations) - 1
	if a.Debug {
		req.Event = func(b json.RawMessage) error { return a.Store.Event(r.ID, idx+1, "opencode", b) }
	}
	slog.Info("worker started", "run_id", r.ID, "worktree", r.Workspace.Root, "profile", r.Config.Name, "model", r.Config.Profile.Model)
	var result runner.Result
	var runErr error
	if resume {
		result, runErr = a.Engine.Continue(ctx, r.Session, req)
	} else {
		result, runErr = a.Engine.Start(ctx, req)
	}
	now := time.Now().UTC()
	r.Finished = &now
	r.Session = result.Session
	r.State = "completed"
	i := &r.Iterations[idx]
	i.Finished = &now
	i.Result = result
	// Finalize even after client cancellation, before releasing the worktree lock.
	captureCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	after, e := workspace.Capture(captureCtx, r.Workspace.Root)
	i.After = after
	if e != nil {
		runErr = errors.Join(runErr, e)
	}
	if r.Operation == "explore" && after.Fingerprint != i.Before.Fingerprint {
		runErr = errors.Join(runErr, fmt.Errorf("repository changed during exploration; conclusions are stale"))
	}
	if r.Operation == "explore" && runErr == nil {
		report, err := parseExploration(result.Report, r.Workspace.Root)
		if err != nil {
			runErr = err
		} else {
			b, _ := json.Marshal(report)
			i.Result.Report = string(b)
		}
	}
	if after.SHA != "" && (after.SHA != i.Before.SHA || after.Branch != i.Before.Branch) {
		runErr = errors.Join(runErr, fmt.Errorf("worker changed HEAD or branch unexpectedly"))
	}
	if runErr != nil {
		r.State = "failed"
		if ctx.Err() != nil {
			r.State = "cancelled"
		}
		i.Error = runErr.Error()
	}
	if e = a.Store.Save(r); e != nil {
		return Result{RunID: r.ID, Operation: r.Operation, State: r.State}, fmt.Errorf("persist final run: %w", e)
	}
	slog.Info("worker finished", "run_id", r.ID, "worktree", r.Workspace.Root, "profile", r.Config.Name, "model", r.Config.Profile.Model, "session", r.Session, "duration", now.Sub(i.Started).String(), "exit_status", result.Exit)
	v, e := a.view(*r)
	return v, errors.Join(runErr, e)
}
func (a *App) view(r store.Run) (Result, error) {
	reviews, e := a.Store.Reviews(r.ID)
	v := Result{RunID: r.ID, Operation: r.Operation, State: r.State, Workspace: r.Workspace, Config: r.Config, Provider: r.Provider, Session: r.Session, Iterations: len(r.Iterations), Started: r.Started, Finished: r.Finished, Reviews: reviews}
	if len(r.Iterations) > 0 {
		i := r.Iterations[len(r.Iterations)-1]
		i.Before.Diff = ""
		i.After.Diff = ""
		v.Latest = &i
	}
	return v, e
}
func (a *App) Result(id string) (Result, error) {
	r, e := a.Store.Get(id)
	if e != nil {
		return Result{}, e
	}
	if r.State == "running" {
		lock, le := a.lock(r.Workspace.Root)
		if le == nil {
			defer lock.Close()
			if e = a.Store.Recover(r.Workspace.Root); e != nil {
				return Result{}, e
			}
			r, e = a.Store.Get(id)
			if e != nil {
				return Result{}, e
			}
		} else if !errors.Is(le, workspace.ErrBusy) {
			return Result{}, le
		}
	}
	return a.view(r)
}
func (a *App) Review(v store.Review) error {
	r, e := a.Store.Get(v.RunID)
	if e != nil {
		return e
	}
	lock, e := a.lock(r.Workspace.Root)
	if e != nil {
		return e
	}
	defer lock.Close()
	return a.Store.Review(v)
}
func (a *App) UseProfile(ctx context.Context, cwd, name string, local bool) error {
	w, e := workspace.Resolve(ctx, cwd)
	if e != nil {
		return e
	}
	c, e := config.Load(a.ConfigPath)
	if e != nil {
		return e
	}
	p, ok := c.Profiles[name]
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	if e = p.Validate(); e != nil {
		return e
	}
	lock, e := a.lock(w.Root)
	if e != nil {
		return e
	}
	defer lock.Close()
	return config.SetProfile(w.Root, name, local)
}
