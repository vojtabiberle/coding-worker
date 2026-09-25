package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecedence(t *testing.T) {
	d := t.TempDir()
	p := Profile{Engine: "opencode", Model: "test/model", Agent: "worker", MaxSteps: 5}
	c := Config{Default: "a", Profiles: map[string]Profile{"a": p, "b": p, "c": p, "d": p}}
	check := func(override, want string) {
		t.Helper()
		r, e := c.Resolve(d, override)
		if e != nil || r.Name != want {
			t.Fatalf("%+v %v want %s", r, e, want)
		}
	}
	check("", "a")
	os.WriteFile(filepath.Join(d, ".coding-worker.toml"), []byte("profile='b'\n"), 0600)
	check("", "b")
	os.WriteFile(filepath.Join(d, ".coding-worker.local.toml"), []byte("profile='c'\n"), 0600)
	check("", "c")
	check("d", "d")
	if _, e := c.Resolve(d, "missing"); e == nil {
		t.Fatal("missing profile accepted")
	}
	os.WriteFile(filepath.Join(d, ".coding-worker.toml"), []byte("profile=["), 0600)
	if _, e := c.Resolve(d, ""); e == nil {
		t.Fatal("malformed project accepted")
	}
}
func TestMalformedGlobal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("[profiles"), 0600)
	if _, e := Load(p); e == nil {
		t.Fatal("accepted malformed config")
	}
}
func TestSetPreservesFields(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".coding-worker.toml")
	body := "# comment\nprofile = 'old' # keep\nother = 42\n[table]\nprofile = 'nested'\n"
	os.WriteFile(p, []byte(body), 0600)
	if e := SetProfile(d, "new", false); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != strings.Replace(body, "'old'", `"new"`, 1) {
		t.Fatalf("unrelated content changed: %s", b)
	}
	if e := SetProfile(d, "local", true); e != nil {
		t.Fatal(e)
	}
}
func TestValidation(t *testing.T) {
	for _, p := range []Profile{{}, {Engine: "other", Model: "x/y", Agent: "worker", MaxSteps: 5}, {Engine: "opencode", Model: "missing", Agent: "worker", MaxSteps: 5}} {
		if p.Validate() == nil {
			t.Fatal("invalid accepted")
		}
	}
}

func TestPiProfile(t *testing.T) {
	p := Profile{Engine: "pi", Model: "provider/model", MaxSteps: 5}
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	p.Agent = "build"
	if e := p.Validate(); e == nil {
		t.Fatal("OpenCode agent silently accepted for Pi")
	}
}
