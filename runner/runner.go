package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/vojtabiberle/coding-worker/config"
	"github.com/vojtabiberle/coding-worker/workspace"
)

type Request struct {
	BackendVersion  string
	Review          bool
	CWD, Prompt     string
	ReadOnly        bool
	RuntimeDir      string
	Reproduction    *ReproduceRequest
	ReproductionDir string
	CommandSandbox  bool
	Profile         config.Profile
	Lock            *os.File
	Event           func(json.RawMessage) error
}
type Metrics struct {
	Input  *int64   `json:"input_tokens"`
	Cached *int64   `json:"cached_input_tokens"`
	Output *int64   `json:"output_tokens"`
	Cost   *float64 `json:"cost"`
}
type Command struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Exit    *int   `json:"exit_status"`
}
type Result struct {
	BackendVersion string    `json:"backend_version,omitempty"`
	Session        string    `json:"session_id"`
	Report         string    `json:"worker_report"`
	Commands       []Command `json:"commands"`
	Failures       []string  `json:"failures"`
	Unresolved     []string  `json:"unresolved_issues"`
	Metrics        Metrics   `json:"metrics"`
	Exit           int       `json:"exit_status"`
	Tests          *bool     `json:"tests_passed"`
}
type Engine interface {
	Start(context.Context, Request) (Result, error)
	Continue(context.Context, string, Request) (Result, error)
}
type OpenCode struct{ Executable string }

func (o OpenCode) Start(ctx context.Context, r Request) (Result, error) { return o.run(ctx, "", r) }
func (o OpenCode) Continue(ctx context.Context, id string, r Request) (Result, error) {
	if id == "" {
		return Result{}, fmt.Errorf("missing OpenCode session")
	}
	return o.run(ctx, id, r)
}
func (o OpenCode) Binary() string {
	if o.Executable != "" {
		return o.Executable
	}
	return "opencode"
}
func (o OpenCode) Version(ctx context.Context) (string, error) {
	b, e := exec.CommandContext(ctx, o.Binary(), "--version").Output()
	return strings.TrimSpace(string(b)), e
}

const Baseline = `You are an implementation worker controlled by an external orchestrator.
Implement exactly the requested objective. Inspect the repository as needed.
Follow repository-local instructions and conventions. Modify implementation code and tests where necessary.
Run the most relevant tests reasonably available.
Do not redefine the overall architecture unless the requested implementation is impossible.
Preserve all pre-existing user changes. Modify files only inside the target worktree.
Do not commit, push, reset Git state, delete untracked files, or checkout another branch.
At completion report files changed, implementation summary, tests/commands executed, failures, and unresolved issues.
`

func Prompt(objective string, constraints, criteria []string, extra string) string {
	b, _ := json.MarshalIndent(struct {
		Objective   string   `json:"objective"`
		Constraints []string `json:"constraints"`
		Criteria    []string `json:"acceptance_criteria"`
		Context     string   `json:"relevant_context"`
	}{objective, constraints, criteria, extra}, "", "  ")
	return Baseline + "\nTask specification:\n" + string(b)
}

// Runtime overlay preserves on-disk OpenCode configuration. Permission rules are
// defense in depth, not a shell/filesystem sandbox; see README's trust boundary.
func runtimeEnv(p config.Profile) ([]string, error) {
	m := map[string]any{}
	if raw := os.Getenv("OPENCODE_CONFIG_CONTENT"); raw != "" {
		if e := json.Unmarshal([]byte(raw), &m); e != nil {
			return nil, fmt.Errorf("OPENCODE_CONFIG_CONTENT must be JSON: %w", e)
		}
	}
	if m == nil {
		return nil, fmt.Errorf("OPENCODE_CONFIG_CONTENT must be a JSON object")
	}
	agents, _ := m["agent"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
	}
	a, _ := agents[p.Agent].(map[string]any)
	if a == nil {
		a = map[string]any{}
	}
	a["steps"] = p.MaxSteps
	a["model"] = p.Model
	permission, _ := a["permission"].(map[string]any)
	if permission == nil {
		permission = map[string]any{}
		if old, ok := a["permission"].(string); ok {
			permission["*"] = old
		}
	}
	permission["external_directory"] = "deny"
	permission["task"] = "deny"

	a["permission"] = permission
	agents[p.Agent] = a
	m["agent"] = agents
	m["share"] = "disabled"
	b, e := json.Marshal(m)
	if e != nil {
		return nil, e
	}
	var env []string
	for _, v := range workspace.GitEnv() {
		if !strings.HasPrefix(v, "OPENCODE_CONFIG_CONTENT=") {
			env = append(env, v)
		}
	}
	return append(env, "OPENCODE_CONFIG_CONTENT="+string(b)), nil
}

type limitedBuffer struct{ b []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remain := 32768 - len(b.b)
	if remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		b.b = append(b.b, p...)
	}
	return n, nil
}
func (o OpenCode) run(ctx context.Context, session string, r Request) (Result, error) {
	out := Result{Session: session, Exit: -1}
	args := []string{"run", "--model", r.Profile.Model, "--agent", r.Profile.Agent, "--dir", r.CWD, "--format", "json"}
	if session != "" {
		args = append(args, "--session", session)
	}
	env, e := runtimeEnv(r.Profile)
	if e != nil {
		return out, e
	}
	binary := o.Binary()
	if r.ReadOnly {
		args = append(args, "--pure")
		env, e = exploreEnv(env, r)
		if e != nil {
			return out, e
		}
		binary, args, env, e = exploreCommand(binary, args, r, env)
		if e != nil {
			return out, e
		}
	}
	// Send prompts on stdin rather than exposing them in argv.
	c := exec.CommandContext(ctx, binary, args...)
	c.Dir = r.CWD
	c.Stdin = strings.NewReader(r.Prompt)
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if r.Lock != nil {
		c.ExtraFiles = []*os.File{r.Lock}
	}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 5 * time.Second
	pipe, e := c.StdoutPipe()
	if e != nil {
		return out, e
	}
	var stderr limitedBuffer
	c.Stderr = &stderr
	if e = c.Start(); e != nil {
		return out, e
	}
	parseErr := Parse(pipe, &out, r.Event)
	if parseErr != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	waitErr := c.Wait()
	out.Exit = c.ProcessState.ExitCode()
	detail := ""
	if len(out.Failures) > 0 {
		detail = ErrorDetail(out.Failures[0])
	} else {
		detail = ErrorDetail(string(stderr.b))
	}
	if detail != "" && (waitErr != nil || parseErr != nil) && len(out.Failures) == 0 {
		out.Failures = append(out.Failures, detail)
	}
	if parseErr != nil {
		if detail != "" {
			return out, fmt.Errorf("%s: %w", detail, parseErr)
		}
		if r.ReadOnly && out.Session == "" {
			return out, fmt.Errorf("read-only OpenCode sandbox failed; check bubblewrap/user namespaces and OpenCode --pure support: %w", parseErr)
		}
		return out, parseErr
	}
	if waitErr != nil {
		if detail != "" {
			return out, fmt.Errorf("%s (OpenCode exit %d, session %s): %w", detail, out.Exit, out.Session, waitErr)
		}
		return out, fmt.Errorf("OpenCode exit %d (inspect session %s): %w", out.Exit, out.Session, waitErr)
	}
	if len(out.Failures) > 0 {
		return out, fmt.Errorf("OpenCode: %s", detail)
	}
	if out.Session == "" {
		return out, fmt.Errorf("OpenCode returned no session ID; verify JSON CLI compatibility")
	}
	return out, nil
}
func add[T int64 | float64](dst **T, v *T) {
	if v == nil {
		return
	}
	if *dst == nil {
		*dst = new(T)
	}
	**dst += *v
}
func Parse(reader io.Reader, out *Result, event func(json.RawMessage) error) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 65536), 8<<20)
	seen := map[string]bool{}
	var missingCost, missingInput, missingOutput, missingCache bool
	sawOutput := false
	for s.Scan() {
		line := append([]byte(nil), s.Bytes()...)
		if len(line) == 0 {
			continue
		}
		var e struct {
			Type    string          `json:"type"`
			Session string          `json:"sessionID"`
			Error   json.RawMessage `json:"error"`
			Part    struct {
				ID      string   `json:"id"`
				Session string   `json:"sessionID"`
				Text    string   `json:"text"`
				Tool    string   `json:"tool"`
				Cost    *float64 `json:"cost"`
				Tokens  *struct {
					Input  *int64 `json:"input"`
					Output *int64 `json:"output"`
					Cache  struct {
						Read *int64 `json:"read"`
					} `json:"cache"`
				} `json:"tokens"`
				State struct {
					Status string `json:"status"`
					Error  string `json:"error"`
					Input  struct {
						Command string `json:"command"`
					} `json:"input"`
					Metadata struct {
						Exit *int `json:"exit"`
					} `json:"metadata"`
				} `json:"state"`
			} `json:"part"`
		}
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("invalid OpenCode JSON event: %w", err)
		}
		if event != nil {
			if err := event(line); err != nil {
				return err
			}
		}
		if e.Session != "" {
			out.Session = e.Session
		} else if e.Part.Session != "" {
			out.Session = e.Part.Session
		}
		key := e.Type + ":" + e.Part.ID
		if e.Part.ID != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		switch e.Type {
		case "text":
			sawOutput = true
			if len(out.Report) < 32768 {
				out.Report += e.Part.Text + "\n"
				if len(out.Report) > 32768 {
					out.Report = out.Report[:32768]
				}
			}
		case "step_finish":
			sawOutput = true
			missingCost = missingCost || e.Part.Cost == nil
			if e.Part.Tokens == nil {
				missingInput = true
				missingOutput = true
				missingCache = true
			} else {
				missingInput = missingInput || e.Part.Tokens.Input == nil
				missingOutput = missingOutput || e.Part.Tokens.Output == nil
				missingCache = missingCache || e.Part.Tokens.Cache.Read == nil
			}
			add(&out.Metrics.Cost, e.Part.Cost)
			if t := e.Part.Tokens; t != nil {
				add(&out.Metrics.Input, t.Input)
				add(&out.Metrics.Output, t.Output)
				add(&out.Metrics.Cached, t.Cache.Read)
			}
		case "tool_use":
			if e.Part.Tool == "bash" {
				out.Commands = append(out.Commands, Command{e.Part.State.Input.Command, e.Part.State.Status, e.Part.State.Metadata.Exit})
			}
			if e.Part.State.Status == "error" {
				out.Unresolved = append(out.Unresolved, e.Part.State.Error)
			}
		case "error":
			out.Failures = append(out.Failures, string(e.Error))
		}
	}
	if missingCost {
		out.Metrics.Cost = nil
	}
	if missingInput {
		out.Metrics.Input = nil
	}
	if missingOutput {
		out.Metrics.Output = nil
	}
	if missingCache {
		out.Metrics.Cached = nil
	}
	if err := s.Err(); err != nil {
		return err
	}
	if !sawOutput && len(out.Failures) == 0 {
		return fmt.Errorf("OpenCode returned no completed output events")
	}
	return nil
}

// UnmarshalJSON keeps runs written before backend-neutral session references readable.
func (r *Result) UnmarshalJSON(b []byte) error {
	type plain Result
	var v struct {
		*plain
		Legacy string `json:"opencode_session_id"`
	}
	v.plain = (*plain)(r)
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if r.Session == "" {
		r.Session = v.Legacy
	}
	return nil
}
