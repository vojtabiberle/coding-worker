package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vojtabiberle/coding-worker/store"
	"github.com/vojtabiberle/coding-worker/workspace"
)

type WaitRequest struct {
	RunID          string `json:"run_id"`
	Iteration      int    `json:"iteration,omitempty" jsonschema:"One-based iteration to wait for. Omit to bind to the latest iteration at call entry. Reuse the returned iteration on subsequent waits."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"Wait duration: default 45 seconds, range 1 to 600. Use the longest value below the MCP client's tool timeout; each extra wait costs the caller a model turn. Timeout stops waiting, not the job."`
}
type WaitResult struct {
	RunID           string     `json:"run_id"`
	Iteration       int        `json:"iteration"`
	State           string     `json:"state"`
	Done            bool       `json:"done"`
	TimedOut        bool       `json:"timed_out"`
	Phase           string     `json:"phase,omitempty"`
	LastProgress    *time.Time `json:"last_progress,omitempty"`
	LatestIteration int        `json:"latest_iteration"`
}

const maxWaitSeconds = 600

func (a *App) Wait(ctx context.Context, in WaitRequest) (WaitResult, error) {
	return a.wait(ctx, in, time.Second)
}
func (a *App) wait(ctx context.Context, in WaitRequest, poll time.Duration) (WaitResult, error) {
	if strings.TrimSpace(in.RunID) == "" || len(in.RunID) > 128 {
		return WaitResult{}, fmt.Errorf("run_id must contain 1 to 128 bytes")
	}
	if in.Iteration < 0 {
		return WaitResult{}, fmt.Errorf("iteration must be positive when supplied")
	}
	timeout := in.TimeoutSeconds
	if timeout == 0 {
		timeout = 45
	}
	if timeout < 1 || timeout > maxWaitSeconds {
		return WaitResult{}, fmt.Errorf("timeout_seconds must be 1 to %d (0 defaults to 45)", maxWaitSeconds)
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	target := in.Iteration
	v := WaitResult{RunID: in.RunID}
	loaded := false
	for {
		if e := ctx.Err(); e != nil {
			return v, e
		}
		p, e := a.Store.Progress(waitCtx, in.RunID, target)
		if e != nil {
			if ctx.Err() != nil {
				return v, ctx.Err()
			}
			if loaded && waitCtx.Err() != nil {
				v.TimedOut = true
				return v, nil
			}
			return v, e
		}
		if target == 0 {
			target = max(1, p.Iterations)
		}
		if target > max(1, p.Iterations) {
			return v, fmt.Errorf("iteration %d does not exist", target)
		}
		loaded = true
		v = waitView(in.RunID, target, p)
		if v.Done {
			return v, nil
		}
		// Recover only after acquiring the same nonblocking writer lock. Never hold
		// the lock, a transaction or a DB connection while waiting between polls.
		lock, le := a.lock(p.Root)
		if le == nil {
			e = a.Store.RecoverContext(waitCtx, p.Root)
			lock.Close()
			if e != nil {
				if ctx.Err() != nil {
					return v, ctx.Err()
				}
				if waitCtx.Err() != nil {
					v.TimedOut = true
					return v, nil
				}
				return v, e
			}
			p, e = a.Store.Progress(waitCtx, in.RunID, target)
			if e != nil {
				if ctx.Err() != nil {
					return v, ctx.Err()
				}
				if waitCtx.Err() != nil {
					v.TimedOut = true
					return v, nil
				}
				return v, e
			}
			v = waitView(in.RunID, target, p)
			if v.Done {
				return v, nil
			}
		} else if !errors.Is(le, workspace.ErrBusy) {
			return v, le
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return v, ctx.Err()
			}
			v.TimedOut = true
			return v, nil
		case <-ticker.C:
		}
	}
}
func waitView(id string, target int, p store.Progress) WaitResult {
	v := WaitResult{RunID: id, Iteration: target, LatestIteration: p.Iterations}
	if target >= p.Iterations {
		v.State = p.State
		v.Phase = p.Phase
		v.LastProgress = p.LastProgress
		v.Done = p.State != "running"
	} else {
		// A continuation cannot begin until the prior iteration finishes. Legacy
		// errored iterations have no saved state: don't guess failed vs cancelled.
		v.State = p.IterationState
		v.Done = true
		if v.State == "" || v.State == "running" {
			v.State = "unknown"
			if p.IterationFinished != nil && !p.IterationError {
				v.State = "completed"
			}
		}
	}
	if v.Done {
		v.Phase = v.State
	}
	return v
}
