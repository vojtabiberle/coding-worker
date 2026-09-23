package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"coding-worker/runner"
	"coding-worker/workspace"
)

// ReviewCode is distinct from recording an external acceptance verdict.
func (a *App) ReviewCode(ctx context.Context, in ExploreRequest) (ExploreResult, error) {
	return a.investigate(ctx, in, "review", nil)
}
func reviewContext(s workspace.Snapshot) (string, error) {
	if len(s.Files) == 0 {
		return "", fmt.Errorf("no worktree changes to review against HEAD")
	}
	data, e := json.Marshal(struct {
		Base  string   `json:"base_sha"`
		Diff  string   `json:"review.diff"`
		Files []string `json:"changed_files"`
	}{s.SHA, s.Diff, s.Files})
	if e != nil {
		return "", e
	}
	if len(data) > 128<<10 {
		return "", fmt.Errorf("review context exceeds 128 KiB; reduce the change set")
	}
	return "\n" + runner.ReviewPrompt + "\nReview data (diff line numbers start at 1):\n" + string(data), nil
}
func validateCodeReview(v Exploration, s workspace.Snapshot) error {
	changed := map[string]bool{}
	for _, p := range s.Files {
		changed[p] = true
	}
	for _, f := range v.Findings {
		if f.Severity != "high" && f.Severity != "medium" && f.Severity != "low" {
			return fmt.Errorf("review finding requires severity high, medium or low")
		}
		relevant := false
		for _, ev := range f.Evidence {
			relevant = relevant || changed[ev.File] || ev.Artifact == "review.diff"
		}
		if !relevant {
			return fmt.Errorf("review finding must cite a changed file or review.diff; put unverified scope in answer")
		}
	}
	return nil
}
