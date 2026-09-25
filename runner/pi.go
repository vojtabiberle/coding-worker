package runner

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Pi's extension/event contract is versioned independently of coding-worker.
const PiVersion = "0.78.1"

//go:embed pi-guard.mjs
var piGuard []byte

type Pi struct{ Executable string }

func (p Pi) Binary() string {
	if p.Executable != "" {
		return p.Executable
	}
	return "pi"
}
func (p Pi) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, p.Binary(), "--version").CombinedOutput()
	v := strings.TrimSpace(string(b))
	if e != nil {
		return v, e
	}
	if v != PiVersion {
		return v, fmt.Errorf("unsupported Pi version %q; install %s", v, PiVersion)
	}
	return v, nil
}
func (p Pi) Start(ctx context.Context, r Request) (Result, error) { return p.run(ctx, "", r) }
func (p Pi) Continue(ctx context.Context, id string, r Request) (Result, error) {
	if id == "" {
		return Result{}, fmt.Errorf("missing Pi session")
	}
	return p.run(ctx, id, r)
}

func (p Pi) run(ctx context.Context, session string, r Request) (out Result, err error) {
	out = Result{Session: session, Exit: -1}
	defer func() {
		if out.Session == "" && filepath.IsAbs(r.RuntimeDir) {
			files, _ := filepath.Glob(filepath.Join(r.RuntimeDir, "sessions", "*.jsonl"))
			if len(files) == 1 {
				out.Session = files[0]
			}
		}
	}()

	version, e := p.Version(ctx)
	if e != nil {
		return out, e
	}
	out.BackendVersion = version
	if r.BackendVersion != "" && r.BackendVersion != version {
		return out, fmt.Errorf("Pi session requires version %s", r.BackendVersion)
	}
	if e = r.Profile.Validate(); e != nil {
		return out, e
	}
	root, private, e := runtimePaths(r)
	if e != nil {
		return out, e
	}
	// Pi 0.78.1 merges project settings over private settings. Reject overrides
	// that would restart model work behind the adapter's completion/error handling.
	if b, err := os.ReadFile(filepath.Join(root, ".pi", "settings.json")); err == nil {
		var settings struct {
			Retry struct {
				Enabled bool `json:"enabled"`
			} `json:"retry"`
			Compaction struct {
				Enabled bool `json:"enabled"`
			} `json:"compaction"`
		}
		if err = json.Unmarshal(b, &settings); err != nil {
			return out, fmt.Errorf("Pi project settings: %w", err)
		}
		if settings.Retry.Enabled || settings.Compaction.Enabled {
			return out, fmt.Errorf("Pi worker requires retry.enabled=false and compaction.enabled=false in project .pi/settings.json when specified")
		}
	} else if !os.IsNotExist(err) {
		return out, err
	}
	agent := filepath.Join(private, "agent")
	for _, dir := range []string{agent, filepath.Join(private, "sessions"), filepath.Join(private, "tmp")} {
		if e = os.MkdirAll(dir, 0700); e != nil {
			return out, e
		}
	}
	if session != "" {
		resolved, err := filepath.EvalSymlinks(session)
		if err != nil || !filepath.IsAbs(session) || !inside(filepath.Join(private, "sessions"), resolved) {
			return out, fmt.Errorf("Pi session unavailable or outside its run directory")
		}
		session = resolved
	} else {
		// Snapshot the user's Pi auth/models once. Never copy settings or executable resources.
		source := os.Getenv("PI_CODING_AGENT_DIR")
		if source == "" {
			h, _ := os.UserHomeDir()
			source = filepath.Join(h, ".pi", "agent")
		}
		for _, name := range []string{"auth.json", "models.json"} {
			b, err := os.ReadFile(filepath.Join(source, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return out, err
			}
			if e = os.WriteFile(filepath.Join(agent, name), b, 0600); e != nil {
				return out, e
			}
		}
	}
	// No retries/compaction outside the bounded request; follow-ups preserve the session.
	if e = os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"retry":{"enabled":false},"compaction":{"enabled":false}}`), 0600); e != nil {
		return out, e
	}
	policy, _ := json.Marshal(map[string]any{"root": root, "readOnly": r.ReadOnly, "maxSteps": r.Profile.MaxSteps, "model": r.Profile.Model})
	for name, b := range map[string][]byte{"guard.mjs": piGuard, "policy.json": policy} {
		if e = os.WriteFile(filepath.Join(private, name), b, 0600); e != nil {
			return out, e
		}
	}
	prompt := Baseline
	allowed := "read,bash,edit,write,grep,find,ls"
	if r.ReadOnly {
		allowed = "read,grep,find,ls"
		prompt = strings.ReplaceAll(ExplorePrompt, "read, glob and grep", "read, find, ls and grep")
		if r.Review {
			prompt += "\n" + ReviewPrompt
		}
		if r.Reproduction != nil {
			prompt += "\n" + DiagnosePrompt
		}
	}
	if e = os.WriteFile(filepath.Join(private, "prompt.txt"), []byte(prompt), 0600); e != nil {
		return out, e
	}
	provider, model, _ := strings.Cut(r.Profile.Model, "/")
	args := []string{"--mode", "json", "--provider", provider, "--model", model, "--session-dir", filepath.Join(private, "sessions"), "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--extension", filepath.Join(private, "guard.mjs"), "--tools", allowed, promptFlag(r.ReadOnly), filepath.Join(private, "prompt.txt")}
	if r.ReadOnly {
		args = append(args, "--no-context-files")
	}
	if session != "" {
		args = append(args, "--session", session)
	}
	env := piEnvironment(agent, private)
	binary := p.Binary()
	if r.ReadOnly {
		binary, args, env, e = sandboxCommand(binary, args, r, env)
		if e != nil {
			return out, e
		}
	}
	c := exec.CommandContext(ctx, binary, args...)
	c.Dir = root
	c.Env = env
	c.Stdin = strings.NewReader(r.Prompt)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if r.Lock != nil {
		c.ExtraFiles = []*os.File{r.Lock}
	}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 5 * time.Second
	stdout, e := c.StdoutPipe()
	if e != nil {
		return out, e
	}
	var stderr limitedBuffer
	c.Stderr = &stderr
	if e = c.Start(); e != nil {
		return out, e
	}
	parseErr := parsePi(stdout, &out, r.Event, private, session)
	if parseErr != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	waitErr := c.Wait()
	out.Exit = c.ProcessState.ExitCode()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err := errors.Join(parseErr, waitErr); err != nil {
		return out, fmt.Errorf("Pi: %w; %s", err, ErrorDetail(string(stderr.b)))
	}
	return out, nil
}
func piEnvironment(agent, private string) []string {
	values := map[string]string{"PI_CODING_AGENT_DIR": agent, "PI_CODING_AGENT_SESSION_DIR": filepath.Join(private, "sessions"), "TMPDIR": filepath.Join(private, "tmp"), "PI_OFFLINE": "1", "PI_TELEMETRY": "0"}
	var env []string
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if _, ok := values[k]; !ok && k != "RIPGREP_CONFIG_PATH" {
			env = append(env, v)
		}
	}
	for k, v := range values {
		env = append(env, k+"="+v)
	}
	return env
}

func parsePi(reader io.Reader, out *Result, event func(json.RawMessage) error, private, previous string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 65536), 8<<20)
	ready, ended, assistant := false, false, false
	finalStop := ""
	sessionID := ""
	commands := map[string]string{}
	missingInput, missingOutput, missingCache, missingCost := false, false, false, false
	for scanner.Scan() {
		line := scanner.Bytes()
		var v struct {
			Type       string          `json:"type"`
			ID         string          `json:"id"`
			Message    json.RawMessage `json:"message"`
			ToolName   string          `json:"toolName"`
			ToolCallID string          `json:"toolCallId"`
			IsError    bool            `json:"isError"`
			Args       struct {
				Command string `json:"command"`
			} `json:"args"`
		}
		if e := json.Unmarshal(line, &v); e != nil {
			return fmt.Errorf("invalid Pi JSON event: %w", e)
		}
		if event != nil {
			if e := event(line); e != nil {
				return e
			}
		}
		switch v.Type {
		case "session":
			sessionID = v.ID
		case "worker_ready":
			ready = true
		case "worker_error":
			var msg string
			_ = json.Unmarshal(v.Message, &msg)
			return errors.New(msg)
		case "agent_start":
			if !ready {
				return fmt.Errorf("Pi worker policy did not initialize")
			}
			ended = false
		case "agent_end":
			ended = true
		case "message_end":
			var role struct {
				Role string `json:"role"`
			}
			if e := json.Unmarshal(v.Message, &role); e != nil {
				return e
			}
			if role.Role != "assistant" {
				continue
			}
			var m struct {
				Role         string `json:"role"`
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
				Content      []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Usage *struct {
					Input  *int64 `json:"input"`
					Output *int64 `json:"output"`
					Cache  *int64 `json:"cacheRead"`
					Cost   *struct {
						Total *float64 `json:"total"`
					} `json:"cost"`
				} `json:"usage"`
			}
			if e := json.Unmarshal(v.Message, &m); e != nil {
				return e
			}
			if m.Role != "assistant" {
				continue
			}
			assistant = true
			finalStop = m.StopReason
			if m.StopReason == "error" || m.StopReason == "aborted" {
				return fmt.Errorf("Pi %s: %s", m.StopReason, m.ErrorMessage)
			}
			if m.StopReason == "length" {
				return fmt.Errorf("Pi response exceeded output limit")
			}
			out.Report = ""
			for _, c := range m.Content {
				if c.Type == "text" {
					out.Report += c.Text
				}
			}
			if len(out.Report) > 32768 {
				return fmt.Errorf("Pi report exceeds 32768 bytes")
			}
			if m.Usage == nil {
				missingInput = true
				missingOutput = true
				missingCache = true
				missingCost = true
			} else {
				u := m.Usage
				missingInput = missingInput || u.Input == nil
				missingOutput = missingOutput || u.Output == nil
				missingCache = missingCache || u.Cache == nil
				missingCost = missingCost || u.Cost == nil || u.Cost.Total == nil
				add(&out.Metrics.Input, u.Input)
				add(&out.Metrics.Output, u.Output)
				add(&out.Metrics.Cached, u.Cache)
				if u.Cost != nil {
					add(&out.Metrics.Cost, u.Cost.Total)
				}
			}
		case "tool_execution_start":
			if v.ToolName == "bash" {
				commands[v.ToolCallID] = v.Args.Command
			}
		case "tool_execution_end":
			if command, ok := commands[v.ToolCallID]; ok {
				status := "completed"
				if v.IsError {
					status = "failed"
				}
				out.Commands = append(out.Commands, Command{Command: command, Status: status})
				delete(commands, v.ToolCallID)
			}
		}
	}
	if e := scanner.Err(); e != nil {
		return e
	}
	// Use an exact session file, never Pi's latest-session search. Persist it even on a future failed continuation.
	files, e := filepath.Glob(filepath.Join(private, "sessions", "*.jsonl"))
	if e != nil {
		return e
	}
	if previous != "" {
		out.Session = previous
	} else if len(files) == 1 {
		out.Session = files[0]
	}
	if !ready || !ended || !assistant || finalStop != "stop" || sessionID == "" || out.Session == "" || strings.TrimSpace(out.Report) == "" {
		return fmt.Errorf("Pi returned incomplete session/output")
	}
	f, e := os.Open(out.Session)
	if e != nil {
		return fmt.Errorf("Pi session persistence: %w", e)
	}
	var header struct {
		ID string `json:"id"`
	}
	e = json.NewDecoder(io.LimitReader(f, 65536)).Decode(&header)
	f.Close()
	if e != nil || header.ID != sessionID {
		return fmt.Errorf("Pi session header does not match emitted session")
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
	if missingCost {
		out.Metrics.Cost = nil
	}
	return nil
}

func promptFlag(readOnly bool) string {
	if readOnly {
		return "--system-prompt"
	}
	return "--append-system-prompt"
}
