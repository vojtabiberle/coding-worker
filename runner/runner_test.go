package runner

import (
	"coding-worker/config"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixture = `{"type":"step_finish","sessionID":"ses_1","part":{"id":"p1","cost":0.1,"tokens":{"input":12,"output":4,"cache":{"read":3}}}}
{"type":"step_finish","sessionID":"ses_1","part":{"id":"p1","cost":0.1,"tokens":{"input":12,"output":4,"cache":{"read":3}}}}
{"type":"text","sessionID":"ses_1","part":{"id":"p2","text":"implemented"}}
{"type":"tool_use","sessionID":"ses_1","part":{"id":"p3","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"metadata":{"exit":0}}}}
`

func TestParse(t *testing.T) {
	var r Result
	if e := Parse(strings.NewReader(fixture), &r, nil); e != nil {
		t.Fatal(e)
	}
	if r.Session != "ses_1" || *r.Metrics.Cost != 0.1 || *r.Metrics.Input != 12 || len(r.Commands) != 1 || r.Tests != nil {
		t.Fatalf("%+v", r)
	}
	var unknown Result
	Parse(strings.NewReader(`{"type":"text","part":{"text":"done"}}`), &unknown, nil)
	if unknown.Metrics.Cost != nil || unknown.Metrics.Input != nil {
		t.Fatal("invented metrics")
	}
	if Parse(strings.NewReader("not json"), &r, nil) == nil {
		t.Fatal("malformed event accepted")
	}
}
func TestInvocation(t *testing.T) {
	d := t.TempDir()
	bin := filepath.Join(d, "opencode")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > args\ncat > prompt\nprintf '%s' \"$OPENCODE_CONFIG_CONTENT\" > config\ncat <<'EVENTS'\n" + fixture + "EVENTS\n"
	os.WriteFile(bin, []byte(script), 0700)
	p := config.Profile{Engine: "opencode", Model: "test/model", Agent: "worker", MaxSteps: 7}
	r, e := (OpenCode{bin}).Continue(context.Background(), "ses_1", Request{CWD: d, Prompt: "do a task", Profile: p})
	if e != nil {
		t.Fatal(e)
	}
	args, _ := os.ReadFile(filepath.Join(d, "args"))
	if !strings.Contains(string(args), "--session\nses_1") || !strings.Contains(string(args), "--model\ntest/model") {
		t.Fatal(string(args))
	}
	prompt, _ := os.ReadFile(filepath.Join(d, "prompt"))
	if string(prompt) != "do a task" {
		t.Fatal(string(prompt))
	}
	raw, _ := os.ReadFile(filepath.Join(d, "config"))
	var m map[string]any
	if e = json.Unmarshal(raw, &m); e != nil {
		t.Fatal(e)
	}
	if r.Exit != 0 {
		t.Fatal(r.Exit)
	}
}
func TestCancellation(t *testing.T) {
	d := t.TempDir()
	bin := filepath.Join(d, "opencode")
	os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := (OpenCode{bin}).Start(ctx, Request{CWD: d, Profile: config.Profile{Agent: "worker"}})
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation failed: %v", e)
	}
}
func TestLiveOpenCode(t *testing.T) {
	if os.Getenv("CODING_WORKER_LIVE_TEST") != "1" {
		t.Skip("explicit opt-in required; uses provider credits")
	}
	model := os.Getenv("CODING_WORKER_LIVE_MODEL")
	if model == "" {
		t.Fatal("CODING_WORKER_LIVE_MODEL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, e := (OpenCode{}).Start(ctx, Request{CWD: t.TempDir(), Prompt: "Reply with OK. Do not call tools or modify files.", Profile: config.Profile{Engine: "opencode", Model: model, Agent: "build", MaxSteps: 1}})
	if e != nil || r.Session == "" {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestPartialMetricsRemainUnknown(t *testing.T) {
	var r Result
	if e := Parse(strings.NewReader(fixture+"{\"type\":\"step_finish\",\"part\":{\"id\":\"missing\"}}\n"), &r, nil); e != nil {
		t.Fatal(e)
	}
	if r.Metrics.Cost != nil || r.Metrics.Input != nil || r.Metrics.Cached != nil || r.Metrics.Output != nil {
		t.Fatalf("partial metrics reported as complete: %+v", r.Metrics)
	}
}
func TestRuntimeOverlayPreservesRestrictions(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"custom":{}},"agent":{"worker":{"permission":{"bash":"deny","edit":"ask"},"temperature":0.1}}}`)
	env, e := runtimeEnv(config.Profile{Agent: "worker", Model: "test/model", MaxSteps: 3})
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	for _, v := range env {
		if strings.HasPrefix(v, "OPENCODE_CONFIG_CONTENT=") {
			json.Unmarshal([]byte(strings.TrimPrefix(v, "OPENCODE_CONFIG_CONTENT=")), &m)
		}
	}
	a := m["agent"].(map[string]any)["worker"].(map[string]any)
	permissions := a["permission"].(map[string]any)
	if permissions["bash"] != "deny" || permissions["edit"] != "ask" || m["provider"] == nil {
		t.Fatal(m)
	}
}

func TestFailureIncludesProviderCauseAndStderr(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"provider", `printf '%s\n' '{"type":"error","sessionID":"ses_failed","error":{"name":"UnknownError","data":{"message":"Upstream model stream stalled: no data received for 300000ms"}}}'; exit 1`, "Upstream model stream stalled"},
		{"stderr", `printf '%s\n' '{"type":"text","sessionID":"ses_failed","part":{"text":""}}'; echo 'provider credentials missing' >&2; exit 1`, "provider credentials missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			bin := filepath.Join(d, "fake")
			if e := os.WriteFile(bin, []byte("#!/bin/sh\n"+tc.script), 0700); e != nil {
				t.Fatal(e)
			}
			r, e := (OpenCode{bin}).Start(context.Background(), Request{CWD: d, Profile: config.Profile{Agent: "worker"}})
			if e == nil || !strings.Contains(e.Error(), tc.want) || len(r.Failures) == 0 {
				t.Fatal(r, e)
			}
		})
	}
}
