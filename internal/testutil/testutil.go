package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func Git(t testing.TB, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %s: %v", args, b, e)
	}
	return string(b)
}
func Repo(t testing.TB) string {
	t.Helper()
	d := t.TempDir()
	Git(t, d, "init", "-b", "main")
	Write(t, filepath.Join(d, "existing.txt"), "original\n")
	Git(t, d, "add", "existing.txt")
	Git(t, d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	return d
}
func Write(t testing.TB, path, body string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
