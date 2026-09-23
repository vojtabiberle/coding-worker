package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vojtabiberle/coding-worker/config"
	"github.com/vojtabiberle/coding-worker/workspace"
)

func TestExploreSandbox(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	root := t.TempDir()
	private := filepath.Join(t.TempDir(), "runtime")
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("safe"), 0600)
	os.Symlink(outside, filepath.Join(root, "link"))
	script := filepath.Join(t.TempDir(), "fake-opencode")
	body := `#!/bin/sh
cat >/dev/null
if echo broken > protected 2>/dev/null; then exit 10; fi
if echo broken > link 2>/dev/null; then exit 11; fi
if echo broken > "$OUTSIDE" 2>/dev/null; then exit 12; fi
printf '%s' "$OPENCODE_CONFIG_CONTENT" > "$XDG_STATE_HOME/config"
printf '%s\n' "$@" > "$XDG_STATE_HOME/args"
echo '{"type":"text","sessionID":"ses_safe","part":{"text":"read-only"}}'
`
	os.WriteFile(script, []byte(body), 0700)
	t.Setenv("OUTSIDE", outside)
	lock, e := workspace.Lock(t.TempDir(), root)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	req := Request{CWD: root, Prompt: "read", ReadOnly: true, RuntimeDir: private, Lock: lock, Profile: config.Profile{Engine: "opencode", Model: "test/model", Agent: ExploreAgent + "-test", MaxSteps: 5}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, e := (OpenCode{script}).Start(ctx, req)
	if e != nil {
		t.Fatalf("sandbox execution: %v %+v", e, r)
	}
	if _, e = os.Stat(filepath.Join(root, "protected")); !os.IsNotExist(e) {
		t.Fatal("worktree write succeeded")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "safe" {
		t.Fatal("external/symlink write succeeded")
	}
	raw, e := os.ReadFile(filepath.Join(private, "state/config"))
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	a := m["agent"].(map[string]any)[req.Profile.Agent].(map[string]any)
	perms := a["permission"].(map[string]any)
	if perms["*"] != "deny" || perms["read"] != "allow" || perms["external_directory"] != "deny" {
		t.Fatal(perms)
	}
	args, _ := os.ReadFile(filepath.Join(private, "state/args"))
	if !strings.Contains(string(args), "--pure") {
		t.Fatal(string(args))
	}
}
func TestExploreRuntimeCannotOverlapWorktree(t *testing.T) {
	r := Request{CWD: t.TempDir(), ReadOnly: true}
	r.RuntimeDir = filepath.Join(r.CWD, "state")
	_, _, _, e := exploreCommand("true", nil, r, nil)
	if e == nil {
		t.Fatal("writable runtime inside worktree accepted")
	}
}

// Opt-in checks installed OpenCode's config schema without an inference request.
func TestInstalledExploreConfig(t *testing.T) {
	if os.Getenv("CODING_WORKER_CONFIG_TEST") != "1" {
		t.Skip("set CODING_WORKER_CONFIG_TEST=1 for installed OpenCode config probe")
	}
	req := Request{CWD: t.TempDir(), ReadOnly: true, RuntimeDir: filepath.Join(t.TempDir(), "runtime"), Profile: config.Profile{Engine: "opencode", Model: "test/model", Agent: ExploreAgent + "-probe", MaxSteps: 2}}
	env, e := runtimeEnv(req.Profile)
	if e != nil {
		t.Fatal(e)
	}
	env, e = exploreEnv(env, req)
	if e != nil {
		t.Fatal(e)
	}
	binary, args, env, e := exploreCommand("opencode", []string{"debug", "config", "--pure"}, req, env)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary, args...)
	c.Env = env
	c.Dir = req.CWD
	b, e := c.Output()
	if e != nil {
		t.Fatalf("sandboxed OpenCode config probe failed: %v", e)
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal("OpenCode config output is not JSON")
	}
	agent := m["agent"].(map[string]any)[req.Profile.Agent].(map[string]any)
	perms := agent["permission"].(map[string]any)
	if perms["*"] != "deny" || perms["read"] != "allow" || m["lsp"] != false {
		t.Fatal("effective read-only config mismatch")
	}
}

func TestExploreCancellationReleasesChildLock(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	root := t.TempDir()
	locks := t.TempDir()
	f, e := workspace.Lock(locks, root)
	if e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(t.TempDir(), "sleeping-opencode")
	os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nsleep 30\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e = (OpenCode{script}).Start(ctx, Request{CWD: root, ReadOnly: true, RuntimeDir: filepath.Join(t.TempDir(), "runtime"), Lock: f, Profile: config.Profile{Agent: ExploreAgent + "-cancel", Model: "test/model", MaxSteps: 1}})
	f.Close()
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("sandbox cancellation failed: %v", e)
	}
	lock, e := workspace.Lock(locks, root)
	if e != nil {
		t.Fatalf("sandbox kept inherited lock: %v", e)
	}
	lock.Close()
}
