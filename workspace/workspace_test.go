package workspace

import (
	"coding-worker/internal/testutil"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveWorktrees(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0700)
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(sub, link)
	w, e := Resolve(ctx, link)
	if e != nil || w.Root != root {
		t.Fatalf("%+v %v", w, e)
	}
	other := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, root, "worktree", "add", "-b", "other", other)
	v, e := Resolve(ctx, other)
	if e != nil || v.Root == w.Root || v.Repository != w.Repository {
		t.Fatalf("%+v %v", v, e)
	}
	d := t.TempDir()
	a, e := Lock(d, w.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if b, e := Lock(d, w.Root); !errors.Is(e, ErrBusy) {
		if b != nil {
			b.Close()
		}
		t.Fatalf("same worktree unlocked: %v", e)
	}
	b, e := Lock(d, v.Root)
	if e != nil {
		t.Fatal(e)
	}
	b.Close()
	for _, p := range []string{".", "/missing-test-worktree", t.TempDir()} {
		if _, e := Resolve(ctx, p); e == nil {
			t.Fatalf("unsafe cwd accepted %s", p)
		}
	}
}
func TestLockAcrossProcesses(t *testing.T) {
	if os.Getenv("CW_LOCK_CHILD") == "1" {
		f, e := Lock(os.Getenv("CW_LOCK_DIR"), "/same")
		if errors.Is(e, ErrBusy) {
			os.Exit(0)
		}
		if f != nil {
			f.Close()
		}
		os.Exit(7)
	}
	d := t.TempDir()
	f, e := Lock(d, "/same")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	c := exec.Command(os.Args[0], "-test.run=^TestLockAcrossProcesses$")
	c.Env = append(os.Environ(), "CW_LOCK_CHILD=1", "CW_LOCK_DIR="+d)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("child acquired lock: %v %s", e, b)
	}
}
func TestSnapshotDrift(t *testing.T) {
	root := testutil.Repo(t)
	ctx := context.Background()
	a, e := Capture(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	testutil.Write(t, filepath.Join(root, "new.txt"), "one")
	b, e := Capture(ctx, root)
	if e != nil || a.Fingerprint == b.Fingerprint {
		t.Fatal("untracked file missed", e)
	}
	testutil.Write(t, filepath.Join(root, "new.txt"), "two")
	c, e := Capture(ctx, root)
	if e != nil || b.Fingerprint == c.Fingerprint {
		t.Fatal("untracked content drift missed", e)
	}
}
