package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type Workspace struct {
	Root       string `json:"worktree"`
	Repository string `json:"repository"`
}

func Git(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	c.Env = GitEnv()
	b, e := c.Output()
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), e, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), e)
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}
func GitEnv() []string {
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			env = append(env, v)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}
func Resolve(ctx context.Context, cwd string) (Workspace, error) {
	var w Workspace
	if !filepath.IsAbs(cwd) {
		return w, fmt.Errorf("cwd must be an absolute path")
	}
	p, e := filepath.EvalSymlinks(cwd)
	if e != nil {
		return w, e
	}
	s, e := os.Stat(p)
	if e != nil || !s.IsDir() {
		return w, fmt.Errorf("cwd must be an existing directory")
	}
	r, e := Git(ctx, p, "rev-parse", "--show-toplevel")
	if e != nil {
		return w, fmt.Errorf("cannot resolve Git worktree at %q; run from inside an existing repository or worktree: %w", p, e)
	}
	r, e = filepath.EvalSymlinks(r)
	if e != nil {
		return w, e
	}
	h, _ := os.UserHomeDir()
	h, _ = filepath.EvalSymlinks(h)
	if r == "/" || r == h {
		return w, fmt.Errorf("refusing root or home worktree")
	}
	common, e := Git(ctx, r, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil {
		return w, e
	}
	common, e = filepath.EvalSymlinks(common)
	if e != nil {
		return w, e
	}
	repo := common
	if filepath.Base(common) == ".git" {
		repo = filepath.Dir(common)
	}
	return Workspace{r, repo}, nil
}

var ErrBusy = errors.New("busy: worktree already has a writing run")

// Lock files are never removed: unlinking a flock inode permits overlapping locks.
func Lock(dir, root string) (*os.File, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	h := sha256.Sum256([]byte(root))
	f, e := os.OpenFile(filepath.Join(dir, hex.EncodeToString(h[:])+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		if errors.Is(e, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, e
	}
	return f, nil
}

type Snapshot struct {
	SHA         string   `json:"sha"`
	Branch      string   `json:"branch"`
	Status      string   `json:"status"`
	Diff        string   `json:"diff"`
	Summary     string   `json:"summary"`
	Files       []string `json:"files"`
	Added       int      `json:"lines_added"`
	Removed     int      `json:"lines_removed"`
	Fingerprint string   `json:"fingerprint"`
}

func Capture(ctx context.Context, root string) (Snapshot, error) {
	var s Snapshot
	var e error
	s.SHA, e = Git(ctx, root, "rev-parse", "--verify", "HEAD")
	if e != nil {
		return s, fmt.Errorf("repository needs an initial commit: %w", e)
	}
	s.Branch, e = Git(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if e != nil {
		return s, e
	}
	s.Status, e = Git(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if e != nil {
		return s, e
	}
	s.Diff, e = Git(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--binary", "HEAD", "--")
	if e != nil {
		return s, e
	}
	s.Summary, e = Git(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--stat", "HEAD", "--")
	if e != nil {
		return s, e
	}
	nums, e := Git(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "HEAD", "--")
	if e != nil {
		return s, e
	}
	for _, line := range strings.Split(nums, "\n") {
		var a, b int
		if _, e := fmt.Sscanf(line, "%d\t%d\t", &a, &b); e == nil {
			s.Added += a
			s.Removed += b
		}
	}
	indexDiff, e := Git(ctx, root, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--binary", "HEAD", "--")
	if e != nil {
		return s, e
	}
	hash := sha256.New()
	hash.Write([]byte(indexDiff + "\x00"))
	hash.Write([]byte(s.SHA + "\x00" + s.Branch + "\x00" + s.Status + "\x00" + s.Diff))
	entries := strings.Split(s.Status, "\x00")
	for i := 0; i < len(entries); i++ {
		v := entries[i]
		if len(v) < 4 {
			continue
		}
		p := v[3:]
		s.Files = append(s.Files, p)
		if v[0] == 'R' || v[0] == 'C' || v[1] == 'R' || v[1] == 'C' {
			i++
		}
		if strings.HasPrefix(v, "?? ") {
			path := filepath.Join(root, p)
			info, e := os.Lstat(path)
			if e != nil {
				return s, e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, e := os.Readlink(path)
				if e != nil {
					return s, e
				}
				hash.Write([]byte(target))
			} else if info.Mode().IsRegular() {
				f, e := os.Open(path)
				if e != nil {
					return s, e
				}
				fileHash := sha256.New()
				_, e = io.Copy(fileHash, f)
				hash.Write(fileHash.Sum(nil))
				f.Close()
				if e != nil {
					return s, e
				}
			}
		}
	}
	s.Fingerprint = hex.EncodeToString(hash.Sum(nil))
	return s, nil
}
