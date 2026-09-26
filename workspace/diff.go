package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileChange is one changed path in a worktree diff against HEAD.
type FileChange struct {
	Path      string `json:"path"`
	Added     int    `json:"lines_added"`
	Removed   int    `json:"lines_removed"`
	Untracked bool   `json:"untracked,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

// Untracked files larger than this are listed without content.
const maxUntrackedDiffBytes = 1 << 20

// WorktreeDiff returns the unified diff of the worktree against HEAD, including
// untracked files as new-file hunks, limited to Git pathspecs when supplied.
func WorktreeDiff(ctx context.Context, root string, pathspecs []string) (string, []FileChange, error) {
	spec := append([]string{"--"}, pathspecs...)
	diff, e := Git(ctx, root, append([]string{"diff", "--no-ext-diff", "--no-textconv", "HEAD"}, spec...)...)
	if e != nil {
		return "", nil, e
	}
	nums, e := Git(ctx, root, append([]string{"diff", "--no-ext-diff", "--no-textconv", "--numstat", "HEAD"}, spec...)...)
	if e != nil {
		return "", nil, e
	}
	var files []FileChange
	for _, line := range strings.Split(nums, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		f := FileChange{Path: parts[2]}
		if parts[0] == "-" {
			f.Binary = true
		} else {
			f.Added, _ = strconv.Atoi(parts[0])
			f.Removed, _ = strconv.Atoi(parts[1])
		}
		files = append(files, f)
	}
	var b strings.Builder
	if diff != "" {
		b.WriteString(diff)
		b.WriteString("\n")
	}
	others, e := Git(ctx, root, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, spec...)...)
	if e != nil {
		return "", nil, e
	}
	for _, p := range strings.Split(others, "\x00") {
		if p == "" {
			continue
		}
		f, hunk, e := untrackedHunk(root, p)
		if e != nil {
			return "", nil, e
		}
		files = append(files, f)
		b.WriteString(hunk)
	}
	return b.String(), files, nil
}

func untrackedHunk(root, p string) (FileChange, string, error) {
	f := FileChange{Path: p, Untracked: true}
	header := fmt.Sprintf("diff --git a/%s b/%s\nnew file (untracked)\n", p, p)
	path := filepath.Join(root, p)
	info, e := os.Lstat(path)
	if e != nil {
		return f, "", e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, e := os.Readlink(path)
		if e != nil {
			return f, "", e
		}
		return f, header + "symlink -> " + target + "\n", nil
	}
	if !info.Mode().IsRegular() {
		return f, header + "not a regular file\n", nil
	}
	if info.Size() > maxUntrackedDiffBytes {
		f.Binary = true
		return f, header + fmt.Sprintf("content omitted: %d bytes\n", info.Size()), nil
	}
	data, e := os.ReadFile(path)
	if e != nil {
		return f, "", e
	}
	if bytes.IndexByte(data, 0) >= 0 {
		f.Binary = true
		return f, header + fmt.Sprintf("binary file: %d bytes\n", len(data)), nil
	}
	text := strings.TrimSuffix(string(data), "\n")
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	f.Added = len(lines)
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", p, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return f, b.String(), nil
}
