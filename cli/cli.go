package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"coding-worker/config"
	"coding-worker/runner"
	"coding-worker/store"
	"coding-worker/worker"
	"coding-worker/workspace"
)

const Usage = `workerctl status | profiles | profile use NAME [--local]
workerctl sessions | session show RUN_ID
workerctl run [--profile NAME] "TASK"
workerctl continue RUN_ID "FEEDBACK"
workerctl review RUN_ID accepted|changes_requested|rejected [--reviewer NAME] [--notes TEXT]
workerctl doctor
workerctl experiment start NAME --profiles A,B | status | stop | report NAME
Environment: CODING_WORKER_CONFIG, CODING_WORKER_DATA, CODING_WORKER_DEBUG=1
`

func printJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func Execute(ctx context.Context, a *worker.App, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, e := fmt.Fprint(out, Usage)
		return e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	switch args[0] {
	case "profiles":
		c, e := config.Load(a.ConfigPath)
		if e != nil {
			return e
		}
		return printJSON(out, c)
	case "status", "doctor":
		return status(ctx, a, cwd, out, args[0] == "doctor")
	case "profile":
		if len(args) < 3 || args[1] != "use" || len(args) > 4 {
			return fmt.Errorf("usage: workerctl profile use NAME [--local]")
		}
		local := len(args) == 4
		if local && args[3] != "--local" {
			return fmt.Errorf("unknown flag %s", args[3])
		}
		return a.UseProfile(ctx, cwd, args[2], local)
	case "sessions":
		runs, e := a.Store.List()
		if e != nil {
			return e
		}
		for _, r := range runs {
			v, e := a.Result(r.ID)
			if e != nil {
				return e
			}
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%d\n", v.RunID, v.State, v.Config.Name, v.Workspace.Root, v.Iterations)
		}
		return nil
	case "session":
		if len(args) != 3 || args[1] != "show" {
			return fmt.Errorf("usage: workerctl session show RUN_ID")
		}
		v, e := a.Result(args[2])
		if e != nil {
			return e
		}
		return printJSON(out, v)
	case "run":
		f := flag.NewFlagSet("run", flag.ContinueOnError)
		f.SetOutput(out)
		p := f.String("profile", "", "profile override")
		if e = f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 1 {
			return fmt.Errorf("usage: workerctl run [--profile NAME] \"TASK\"")
		}
		v, e := a.Implement(ctx, worker.ImplementRequest{CWD: cwd, Objective: f.Arg(0), Profile: p, Origin: "workerctl"})
		return errors.Join(e, printJSON(out, v))
	case "continue":
		if len(args) != 3 {
			return fmt.Errorf("usage: workerctl continue RUN_ID \"FEEDBACK\"")
		}
		v, e := a.Continue(ctx, worker.ContinueRequest{RunID: args[1], Feedback: args[2]})
		return errors.Join(e, printJSON(out, v))
	case "review":
		if len(args) < 3 {
			return fmt.Errorf("usage: workerctl review RUN_ID VERDICT [--reviewer NAME] [--notes TEXT]")
		}
		f := flag.NewFlagSet("review", flag.ContinueOnError)
		f.SetOutput(out)
		who := f.String("reviewer", "human", "reviewer")
		notes := f.String("notes", "", "notes")
		blocker := f.Int("blocker", 0, "blockers")
		major := f.Int("major", 0, "major findings")
		minor := f.Int("minor", 0, "minor findings")
		tests := f.String("tests", "unknown", "passed, failed, unknown")
		if e = f.Parse(args[3:]); e != nil {
			return e
		}
		var passed *bool
		switch *tests {
		case "passed":
			v := true
			passed = &v
		case "failed":
			v := false
			passed = &v
		case "unknown":
		default:
			return fmt.Errorf("invalid --tests")
		}
		return a.Review(store.Review{RunID: args[1], Verdict: args[2], Reviewer: *who, Notes: *notes, Blocker: *blocker, Major: *major, Minor: *minor, TestsPassed: passed})
	case "experiment":
		return experiment(ctx, a, cwd, args[1:], out)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], Usage)
	}
}
func status(ctx context.Context, a *worker.App, cwd string, out io.Writer, doctor bool) error {
	w, e := workspace.Resolve(ctx, cwd)
	if e != nil {
		return e
	}
	c, e := config.Load(a.ConfigPath)
	if e != nil {
		return e
	}
	p, e := c.Resolve(w.Root, "")
	if e != nil {
		return e
	}
	binary, e := exec.LookPath("opencode")
	availability := "unavailable"
	if e == nil {
		availability = binary
	}
	runs, e := a.Store.List()
	if e != nil {
		return e
	}
	active := 0
	for _, r := range runs {
		if r.Workspace.Root == w.Root {
			v, e := a.Result(r.ID)
			if e != nil {
				return e
			}
			if v.State == "running" {
				active++
			}
		}
	}
	fmt.Fprintf(out, "Repository:  %s\nWorktree:    %s\nProfile:     %s\nSource:      %s\nEngine:      %s\nModel:       %s\nAgent:       %s\nMax steps:   %d\nSettings:    %s [profiles.%s]\nOpenCode:    %s\nActive runs: %d\n", w.Repository, w.Root, p.Name, p.Source, p.Profile.Engine, p.Profile.Model, p.Profile.Agent, p.Profile.MaxSteps, a.ConfigPath, p.Name, availability, active)
	experiments, e := a.Store.Experiments(w.Repository)
	if e != nil {
		return e
	}
	for _, x := range experiments {
		if x.Active {
			fmt.Fprintf(out, "Experiment:  %s (overrides configured profile unless --profile is supplied)\n", x.Name)
		}
	}
	if doctor {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		v, e := (runner.OpenCode{}).Version(ctx)
		if e != nil {
			return fmt.Errorf("install OpenCode and ensure it is on PATH: %w", e)
		}
		f, e := os.CreateTemp(a.Store.Dir, "doctor-*")
		if e != nil {
			return fmt.Errorf("data directory is not writable: %w", e)
		}
		f.Close()
		os.Remove(f.Name())
		fmt.Fprintf(out, "Version:     %s\nDatabase:    %s (writable)\nDoctor:      OK; provider credentials/model availability require an actual run\n", v, a.Store.Dir)
	}
	return nil
}
func experiment(ctx context.Context, a *worker.App, cwd string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("experiment requires start/status/stop/report")
	}
	w, e := workspace.Resolve(ctx, cwd)
	if e != nil {
		return e
	}
	switch args[0] {
	case "start":
		if len(args) < 2 {
			return fmt.Errorf("experiment start NAME --profiles A,B")
		}
		f := flag.NewFlagSet("experiment", flag.ContinueOnError)
		f.SetOutput(out)
		profiles := f.String("profiles", "", "comma-separated profiles")
		if e = f.Parse(args[2:]); e != nil {
			return e
		}
		names := strings.Split(*profiles, ",")
		c, e := config.Load(a.ConfigPath)
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, n := range names {
			if seen[n] {
				return fmt.Errorf("duplicate profile %q", n)
			}
			seen[n] = true
			p, ok := c.Profiles[n]
			if !ok {
				return fmt.Errorf("unknown profile %q", n)
			}
			if e = p.Validate(); e != nil {
				return e
			}
		}
		return a.Store.StartExperiment(w.Repository, args[1], names)
	case "stop":
		return a.Store.StopExperiment(w.Repository)
	case "status":
		v, e := a.Store.Experiments(w.Repository)
		if e != nil {
			return e
		}
		return printJSON(out, v)
	case "report":
		if len(args) != 2 {
			return fmt.Errorf("experiment report NAME")
		}
		v, e := a.Store.Report(w.Repository, args[1])
		if e != nil {
			return e
		}
		return printJSON(out, v)
	default:
		return fmt.Errorf("unknown experiment command")
	}
}
