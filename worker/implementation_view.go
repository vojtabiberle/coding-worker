package worker

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vojtabiberle/coding-worker/runner"
	"github.com/vojtabiberle/coding-worker/store"
	"github.com/vojtabiberle/coding-worker/workspace"
)

const summaryReportBytes = 2000

// ImplementationSummary is the default MCP view of an implementation result: enough to
// decide what to review, without command logs or snapshots (detail=true returns those).
type ImplementationSummary struct {
	RunID           string           `json:"run_id"`
	Operation       string           `json:"operation,omitempty"`
	State           string           `json:"state"`
	Profile         string           `json:"profile"`
	Iteration       int              `json:"iteration"`
	Iterations      int              `json:"iteration_count"`
	Started         time.Time        `json:"started_at"`
	Finished        *time.Time       `json:"finished_at"`
	Files           []string         `json:"files"`
	Added           int              `json:"lines_added"`
	Removed         int              `json:"lines_removed"`
	Untracked       []string         `json:"untracked_files,omitempty"`
	Report          string           `json:"worker_report"`
	ReportTruncated bool             `json:"worker_report_truncated,omitempty"`
	TestsPassed     *bool            `json:"tests_passed"`
	Exit            int              `json:"exit_status"`
	Failures        []string         `json:"failures,omitempty"`
	Unresolved      []string         `json:"unresolved_issues,omitempty"`
	Commands        int              `json:"commands_run"`
	FailedCommands  []runner.Command `json:"failed_commands,omitempty"`
	Metrics         runner.Metrics   `json:"metrics"`
	Error           string           `json:"error,omitempty"`
	LatestReview    *store.Review    `json:"latest_review,omitempty"`
}

func implementationSummary(v Result) ImplementationSummary {
	s := ImplementationSummary{RunID: v.RunID, Operation: v.Operation, State: v.State, Profile: v.Config.Name, Iterations: v.Iterations, Started: v.Started, Finished: v.Finished, Error: v.Error}
	if n := len(v.Reviews); n > 0 {
		s.LatestReview = &v.Reviews[n-1]
	}
	i := v.Latest
	if i == nil {
		return s
	}
	s.Iteration = i.Number
	s.Files, s.Added, s.Removed = i.After.Files, i.After.Added, i.After.Removed
	for _, entry := range strings.Split(i.After.Status, "\x00") {
		if strings.HasPrefix(entry, "?? ") {
			s.Untracked = append(s.Untracked, entry[3:])
		}
	}
	r := i.Result
	s.Report, s.ReportTruncated = clipUTF8(r.Report, summaryReportBytes)
	s.TestsPassed, s.Exit, s.Failures, s.Unresolved, s.Metrics = r.Tests, r.Exit, r.Failures, r.Unresolved, r.Metrics
	s.Commands = len(r.Commands)
	for _, c := range r.Commands {
		if c.Status != "completed" || (c.Exit != nil && *c.Exit != 0) {
			s.FailedCommands = append(s.FailedCommands, c)
		}
	}
	if s.Error == "" {
		s.Error = i.Error
	}
	return s
}

func clipUTF8(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// DiffPage is one page of the run worktree's current diff against HEAD.
type DiffPage struct {
	RunID string `json:"run_id"`
	State string `json:"state"`
	// MatchesWorker is false when the worktree changed after the worker's last iteration.
	MatchesWorker *bool                  `json:"matches_worker_snapshot,omitempty"`
	Files         []workspace.FileChange `json:"files"`
	Text          string                 `json:"text"`
	Offset        int                    `json:"offset"`
	NextOffset    int                    `json:"next_offset,omitempty"`
	More          bool                   `json:"more"`
	TotalBytes    int                    `json:"total_bytes"`
}

func diffBudget(n int) (int, error) {
	if n == 0 {
		return 16384, nil
	}
	if n < 512 || n > 65536 {
		return 0, fmt.Errorf("max_output_bytes for diff must be between 512 and 65536")
	}
	return n, nil
}

func (a *App) diffPage(ctx context.Context, in ResultRequest) (DiffPage, error) {
	budget, e := diffBudget(in.MaxOutputBytes)
	if e != nil {
		return DiffPage{}, e
	}
	if in.DiffOffset < 0 {
		return DiffPage{}, fmt.Errorf("diff_offset must be positive when supplied")
	}
	r, e := a.Store.Get(in.RunID)
	if e != nil {
		return DiffPage{}, e
	}
	text, files, e := workspace.WorktreeDiff(ctx, r.Workspace.Root, in.Paths)
	if e != nil {
		return DiffPage{}, e
	}
	v := DiffPage{RunID: r.ID, State: r.State, Files: files, TotalBytes: len(text)}
	if n := len(r.Iterations); n > 0 && r.Iterations[n-1].After.Fingerprint != "" {
		current, e := workspace.Capture(ctx, r.Workspace.Root)
		if e != nil {
			return DiffPage{}, e
		}
		same := current.Fingerprint == r.Iterations[n-1].After.Fingerprint
		v.MatchesWorker = &same
	}
	start := in.DiffOffset
	if start == 0 {
		start = 1
	}
	if start > len(text)+1 {
		return DiffPage{}, fmt.Errorf("diff_offset %d is past the end of the diff (%d bytes)", start, len(text))
	}
	v.Offset = start
	rest := text[start-1:]
	if len(rest) > budget {
		cut := strings.LastIndexByte(rest[:budget], '\n') + 1
		if cut == 0 {
			rest, _ = clipUTF8(rest, budget)
			cut = len(rest)
		}
		rest = rest[:cut]
		v.More = true
		v.NextOffset = start + cut
	}
	v.Text = rest
	return v, nil
}
