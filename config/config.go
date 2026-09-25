package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Profile struct {
	Engine   string `toml:"engine" json:"engine"`
	Model    string `toml:"model" json:"model"`
	Agent    string `toml:"agent" json:"agent"`
	MaxSteps int    `toml:"max_steps" json:"max_steps"`
}
type Config struct {
	Default  string             `toml:"default_profile" json:"default_profile"`
	Profiles map[string]Profile `toml:"profiles" json:"profiles"`
}
type Resolved struct {
	Name    string  `json:"profile"`
	Source  string  `json:"source"`
	Profile Profile `json:"settings"`
}

func Paths() (string, string) {
	h, _ := os.UserHomeDir()
	c := os.Getenv("XDG_CONFIG_HOME")
	if c == "" {
		c = filepath.Join(h, ".config")
	}
	d := os.Getenv("XDG_DATA_HOME")
	if d == "" {
		d = filepath.Join(h, ".local", "share")
	}
	if v := os.Getenv("CODING_WORKER_CONFIG"); v != "" {
		c = v
	} else {
		c = filepath.Join(c, "coding-worker", "config.toml")
	}
	if v := os.Getenv("CODING_WORKER_DATA"); v != "" {
		d = v
	} else {
		d = filepath.Join(d, "coding-worker")
	}
	return c, d
}
func Load(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, fmt.Errorf("global config %s: %w", path, e)
	}
	e = toml.Unmarshal(b, &c)
	if e != nil {
		return c, fmt.Errorf("global config: %w", e)
	}
	return c, nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (p Profile) Validate() error {
	if p.Engine != "opencode" && p.Engine != "pi" {
		return fmt.Errorf("unsupported engine %q (expected opencode or pi)", p.Engine)
	}
	parts := strings.SplitN(p.Model, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(p.Model, " \t\r\n\x00") || strings.HasPrefix(p.Model, "-") {
		return fmt.Errorf("model must be provider/model")
	}
	if p.Engine == "pi" && p.Agent != "" {
		return fmt.Errorf("agent is OpenCode-only; omit it for pi")
	}
	if p.Engine == "opencode" && !nameRE.MatchString(p.Agent) {
		return fmt.Errorf("agent must contain only letters, digits, underscores or hyphens")
	}
	if p.MaxSteps < 1 {
		return fmt.Errorf("max_steps must be positive")
	}
	return nil
}
func (c Config) Resolve(root, override string) (Resolved, error) {
	r := Resolved{Name: c.Default, Source: "global default_profile"}
	for _, f := range []string{".coding-worker.toml", ".coding-worker.local.toml"} {
		b, e := os.ReadFile(filepath.Join(root, f))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return r, e
		}
		var p struct {
			Profile string `toml:"profile"`
		}
		if e = toml.Unmarshal(b, &p); e != nil {
			return r, fmt.Errorf("%s: %w", f, e)
		}
		if p.Profile != "" {
			r.Name = p.Profile
			r.Source = f
		}
	}
	if override != "" {
		r.Name = override
		r.Source = "explicit invocation"
	}
	p, ok := c.Profiles[r.Name]
	if !ok {
		return r, fmt.Errorf("unknown profile %q; add it to global config", r.Name)
	}
	r.Profile = p
	return r, p.Validate()
}

// SetProfile changes only the root profile key. Refuse unusual multiline forms rather than corrupt TOML.
func SetProfile(root, name string, local bool) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid profile name")
	}
	f := ".coding-worker.toml"
	if local {
		f = ".coding-worker.local.toml"
	}
	path := filepath.Join(root, f)
	if s, e := os.Lstat(path); e == nil && s.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink config")
	}
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	var m map[string]any
	if e = toml.Unmarshal(b, &m); e != nil {
		return e
	}
	lines := strings.Split(string(b), "\n")
	found := false
	re := regexp.MustCompile(`^(\s*(?:profile|"profile"|'profile')\s*=\s*)("[^"\r\n]*"|'[^'\r\n]*')(\s*(?:#.*)?)$`)
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			break
		}
		if a := re.FindStringSubmatch(l); a != nil {
			lines[i] = a[1] + strconv.Quote(name) + a[3]
			found = true
			break
		}
	}
	if !found {
		if _, ok := m["profile"]; ok {
			return fmt.Errorf("unsupported profile key syntax; edit %s manually", path)
		}
		lines = append([]string{"profile = " + strconv.Quote(name)}, lines...)
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	var check map[string]any
	if e = toml.Unmarshal([]byte(out), &check); e != nil {
		return e
	}
	t, e := os.CreateTemp(root, ".coding-worker-*")
	if e != nil {
		return e
	}
	defer os.Remove(t.Name())
	if _, e = t.WriteString(out); e != nil {
		t.Close()
		return e
	}
	if e = t.Close(); e != nil {
		return e
	}
	return os.Rename(t.Name(), path)
}
