package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReproduce(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "keep"), []byte("original"), 0600)
	script := filepath.Join(root, "repro.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf 'reported bug\\n'\nif echo changed > keep; then exit 99; fi\nexit 7\n"), 0700)
	req := Request{CWD: root, ReproductionDir: filepath.Join(t.TempDir(), "repro"), Reproduction: &ReproduceRequest{Command: []string{"./repro.sh"}}}
	r, e := Reproduce(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "finished" || r.Exit == nil || *r.Exit != 7 || !strings.Contains(r.Excerpt, "reported bug") {
		t.Fatalf("%+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(root, "keep"))
	if string(b) != "original" {
		t.Fatal("repository changed")
	}
	if _, e = os.Stat(filepath.Join(req.ReproductionDir, "data/opencode/auth.json")); !os.IsNotExist(e) {
		t.Fatal("credentials copied into command runtime")
	}
	log, e := os.ReadFile(r.Log)
	if e != nil || !strings.HasPrefix(string(log), "reported bug\n") {
		t.Fatal(e)
	}
	text, line, e := ReadLog(r.Log, 14, 100)
	if e != nil || line != 2 || len(text) == 0 {
		t.Fatal(text, line, e)
	}
}
func TestReproduceTimeout(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, e := Reproduce(ctx, Request{CWD: t.TempDir(), ReproductionDir: filepath.Join(t.TempDir(), "repro"), Reproduction: &ReproduceRequest{Command: []string{"/bin/sh", "-c", "sleep 30"}}})
	if e != nil || r.Status != "cancelled" || time.Since(start) > 3*time.Second {
		t.Fatal(r, e)
	}
}
func TestReproductionLogLimit(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "log")
	if e != nil {
		t.Fatal(e)
	}
	w := &logWriter{file: f}
	chunk := []byte(strings.Repeat("x", 1<<20))
	for i := 0; i < 10; i++ {
		if _, e = w.Write(chunk); e != nil {
			t.Fatal(e)
		}
	}
	f.Close()
	s, _ := os.Stat(f.Name())
	if s.Size() != 8<<20 || !w.truncated {
		t.Fatal(s.Size(), w.truncated)
	}
}
func TestReproduceValidation(t *testing.T) {
	for _, r := range []ReproduceRequest{{Command: []string{""}}, {Command: []string{"x\x00y"}}, {TimeoutSeconds: 121}} {
		if r.Validate() == nil {
			t.Fatal("invalid command accepted")
		}
	}
}

func TestDiagnosisAgentKeepsPrivateAuthentication(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bubblewrap unavailable")
	}
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "opencode"), 0700)
	os.WriteFile(filepath.Join(data, "opencode/auth.json"), []byte(`{"fake":{"type":"api","key":"test-only"}}`), 0600)
	t.Setenv("XDG_DATA_HOME", data)
	req := Request{CWD: t.TempDir(), RuntimeDir: filepath.Join(t.TempDir(), "agent"), Reproduction: &ReproduceRequest{}}
	_, _, _, e := exploreCommand("true", nil, req, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(req.RuntimeDir, "data/opencode/auth.json")); e != nil {
		t.Fatal("diagnostic agent lost auth", e)
	}
	req.RuntimeDir = filepath.Join(t.TempDir(), "command")
	req.CommandSandbox = true
	_, _, _, e = exploreCommand("true", nil, req, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(req.RuntimeDir, "data/opencode/auth.json")); !os.IsNotExist(e) {
		t.Fatal("reproduction copied auth")
	}
}

func TestReproductionContextOnlyAdvertisesExistingLog(t *testing.T) {
	for _, status := range []string{"not_run", "start_failed"} {
		got := ReproductionContext(Reproduction{Status: status})
		if strings.Contains(got, "log_path") || strings.Contains(got, "reproduction.log") {
			t.Fatal(got)
		}
	}
	got := ReproductionContext(Reproduction{Status: "finished", Log: "/private/run/1.log"})
	if !strings.Contains(got, `"log_path":"reproduction.log"`) || strings.Contains(got, "/private/") {
		t.Fatal(got)
	}
}
