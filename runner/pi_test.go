package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vojtabiberle/coding-worker/workspace"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojtabiberle/coding-worker/config"
)

const piEvents = `{"type":"session","id":"session"}
{"type":"worker_ready"}
{"type":"agent_start"}
{"type":"message_end","message":{"role":"user","content":"hello"}}
{"type":"message_update","usage":{"input":9999}}
{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"OK"}],"usage":{"input":10,"output":5,"cacheRead":2,"cost":{"total":0.01}}}}
{"type":"agent_end"}
`

func TestPiParse(t *testing.T) {
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "sessions"), 0700)
	os.WriteFile(filepath.Join(d, "sessions", "s.jsonl"), []byte(`{"id":"session"}`), 0600)
	var out Result
	if e := parsePi(strings.NewReader(piEvents), &out, nil, d, ""); e != nil || out.Report != "OK" || out.Metrics.Input == nil || *out.Metrics.Input != 10 || out.Tests != nil {
		t.Fatal(out, e)
	}
	for _, input := range []string{"garbage", strings.ReplaceAll(piEvents, `{"type":"worker_ready"}`+"\n", ""), strings.ReplaceAll(piEvents, `{"type":"agent_end"}`, ""), strings.ReplaceAll(piEvents, `"stopReason":"stop"`, `"stopReason":"error","errorMessage":"bad credentials"`), `{"type":"worker_error","message":"step limit"}`} {
		if e := parsePi(strings.NewReader(input), &Result{}, nil, d, ""); e == nil {
			t.Fatal("accepted incomplete/failed output", input)
		}
	}
	var old Result
	if e := json.Unmarshal([]byte(`{"opencode_session_id":"old"}`), &old); e != nil || old.Session != "old" {
		t.Fatal(old, e)
	}
}

// Uses the real pinned Pi executable with a local HTTP provider; never spends model credits.
func TestPiLocalProvider(t *testing.T) {
	if os.Getenv("CODING_WORKER_PI_TEST") != "1" {
		t.Skip("set CODING_WORKER_PI_TEST=1; requires Pi " + PiVersion + " and bubblewrap, no external provider")
	}
	for _, scenario := range []string{"continue", "paths", "steps", "write", "provider-error", "project-settings", "grep-links"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			private := t.TempDir()
			source := t.TempDir()
			os.WriteFile(filepath.Join(root, "source.txt"), []byte("source evidence\n"), 0600)
			secret := filepath.Join(t.TempDir(), "secret.txt")
			os.WriteFile(secret, []byte("OUTSIDE_SECRET_NEVER_RETURN"), 0600)
			os.Symlink(secret, filepath.Join(root, "link"))
			rgConfig := filepath.Join(t.TempDir(), "ripgrep.conf")
			os.WriteFile(rgConfig, []byte("--follow\n"), 0600)
			t.Setenv("RIPGREP_CONFIG_PATH", rgConfig)
			var count atomic.Int32
			var history atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(count.Add(1))
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-only" {
					t.Error("wrong routing/auth")
				}
				var body struct {
					Messages []map[string]any `json:"messages"`
					Tools    []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
				}
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Error(e)
					return
				}
				b, _ := json.Marshal(body.Messages)
				if strings.Contains(string(b), "OUTSIDE_SECRET_NEVER_RETURN") {
					t.Error("outside content leaked")
				}
				if scenario == "continue" {
					if n == 1 {
						history.Store(int32(len(body.Messages)))
					} else if len(body.Messages) <= int(history.Load()) {
						t.Error("history lost")
					}
				}
				if scenario != "write" {
					for _, tool := range body.Tools {
						if !strings.Contains(",read,grep,find,ls,", ","+tool.Function.Name+",") {
							t.Error("unexpected tool", tool.Function.Name)
						}
					}
				}
				if scenario == "provider-error" {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":{"message":"mock provider failure","type":"invalid_request_error"}}`)
					return
				}
				delta := map[string]any{"role": "assistant", "content": `{"answer":"OK","findings":[]}`}
				stop := "stop"
				tool, path := "", ""
				switch scenario {
				case "paths":
					if n == 1 {
						tool, path = "read", secret
					}
					if n == 2 {
						tool, path = "read", filepath.Join(root, "link")
					}
					if n == 3 {
						tool, path = "read", "source.txt"
					}
					if n == 2 || n == 3 {
						if !strings.Contains(string(b), "Path outside the worktree") {
							t.Error("outside read not blocked", string(b))
						}
					}
				case "grep-links":
					if n == 2 && !strings.Contains(string(b), "source evidence") {
						t.Error("grep did not read the source", string(b))
					}
					if n == 1 {
						tool, path = "grep", "."
					}
				case "steps":
					tool, path = "read", "source.txt"
				case "write":
					if n == 1 {
						tool, path = "write", "created.txt"
					}
				}
				if tool != "" {
					args := map[string]any{"path": path}
					if tool == "grep" {
						args["pattern"] = "."
					}
					if tool == "write" {
						args["content"] = "created"
					}
					a, _ := json.Marshal(args)
					delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("call", n), "type": "function", "function": map[string]any{"name": tool, "arguments": string(a)}}}}
					stop = "tool_calls"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range []map[string]any{{"id": fmt.Sprint(n), "object": "chat.completion.chunk", "created": 1, "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}, {"id": fmt.Sprint(n), "object": "chat.completion.chunk", "created": 1, "model": "mock", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": stop}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}} {
					b, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", b)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			models := fmt.Sprintf(`{"providers":{"worker-probe":{"baseUrl":%q,"api":"openai-completions","apiKey":"$WORKER_PROBE_KEY","models":[{"id":"mock","contextWindow":128000,"maxTokens":100}]}}}`, server.URL+"/v1")
			os.WriteFile(filepath.Join(source, "models.json"), []byte(models), 0600)
			t.Setenv("PI_CODING_AGENT_DIR", source)
			t.Setenv("WORKER_PROBE_KEY", "test-only")
			steps := 10
			if scenario == "steps" {
				steps = 1
			}
			req := Request{CWD: root, RuntimeDir: private, ReadOnly: scenario != "write", Prompt: "Inspect this repository", Profile: config.Profile{Engine: "pi", Model: "worker-probe/mock", MaxSteps: steps}}
			if scenario == "project-settings" {
				os.Mkdir(filepath.Join(root, ".pi"), 0700)
				os.WriteFile(filepath.Join(root, ".pi", "settings.json"), []byte(`{"retry":{"enabled":true}}`), 0600)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out, e := (Pi{}).Start(ctx, req)
			if scenario == "project-settings" {
				if e == nil || count.Load() != 0 {
					t.Fatal("project override accepted", e)
				}
				return
			}
			if scenario == "steps" {
				if e == nil || !strings.Contains(e.Error(), "max_steps") || count.Load() != 1 || out.Session == "" {
					t.Fatal(out, e, count.Load())
				}
				return
			}
			if scenario == "provider-error" {
				if e == nil || !strings.Contains(e.Error(), "mock provider failure") {
					t.Fatal(out, e)
				}
				return
			}
			if e != nil || out.Report != `{"answer":"OK","findings":[]}` || out.BackendVersion != PiVersion || out.Session == "" {
				t.Fatal(out, e)
			}
			if scenario == "write" {
				b, e := os.ReadFile(filepath.Join(root, "created.txt"))
				if e != nil || string(b) != "created" {
					t.Fatal(string(b), e)
				}
			}
			if scenario == "continue" {
				req.Prompt = "Follow up"
				again, e := (Pi{}).Continue(ctx, out.Session, req)
				if e != nil || again.Session != out.Session || count.Load() != 2 {
					t.Fatal(again, e)
				}
			}
		})
	}
}

func TestPiVersionAndCancellation(t *testing.T) {
	root := t.TempDir()
	private := t.TempDir()
	source := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", source)
	bin := filepath.Join(t.TempDir(), "pi")
	os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.78.1 >&2; exit; fi\nprintf '%s\\n' '{\"type\":\"session\",\"id\":\"s\"}' '{\"type\":\"worker_ready\"}' '{\"type\":\"agent_start\"}'\nsleep 30 &\nwait\n"), 0700)
	req := Request{CWD: root, RuntimeDir: private, Prompt: "test", Profile: config.Profile{Engine: "pi", Model: "fake/model", MaxSteps: 2}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := (Pi{bin}).Start(ctx, req)
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatal("process group cancellation failed", e)
	}
	os.WriteFile(bin, []byte("#!/bin/sh\necho 0.99.0\n"), 0700)
	if _, e := (Pi{bin}).Version(context.Background()); e == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestPiInheritsWorktreeLock(t *testing.T) {
	root := t.TempDir()
	private := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	bin := filepath.Join(t.TempDir(), "pi")
	os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.78.1; exit; fi\ntest -e /proc/self/fd/3 || exit 1\nprintf '%s\\n' '{\"type\":\"session\",\"id\":\"s\"}' '{\"type\":\"worker_ready\"}' '{\"type\":\"agent_start\"}'\nsleep 30 &\nwait\n"), 0700)
	locks := t.TempDir()
	lock, e := workspace.Lock(locks, root)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	req := Request{CWD: root, RuntimeDir: private, Lock: lock, Profile: config.Profile{Engine: "pi", Model: "fake/model", MaxSteps: 2}, Event: func(b json.RawMessage) error {
		if strings.Contains(string(b), "agent_start") {
			started <- struct{}{}
		}
		return nil
	}}
	go func() { _, e := (Pi{bin}).Start(ctx, req); done <- e }()
	select {
	case <-started:
	case e := <-done:
		t.Fatal("failed before start", e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lock.Close()
	second, e := workspace.Lock(locks, root)
	if second != nil {
		second.Close()
	}
	if !errors.Is(e, workspace.ErrBusy) {
		t.Fatal("child lost inherited lock", e)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	second, e = workspace.Lock(locks, root)
	if e != nil {
		t.Fatal("lock not released", e)
	}
	second.Close()
}
