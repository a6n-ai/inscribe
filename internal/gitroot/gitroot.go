// Package gitroot finds repository roots from the filesystem without running git.
package gitroot

import (
	"os"
	"path/filepath"
	"strings"
)

// Checkout returns the nearest directory at or above dir that holds a .git
// entry: a repository or a linked worktree root.
func Checkout(dir string) (string, bool) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		if parent := filepath.Dir(d); parent == d {
			return "", false
		}
	}
}

// Main maps a linked worktree root (its .git is a "gitdir:" file) to the main
// repository's root. Any other root, including a submodule (a .git file with
// no commondir) or a bare repo's worktree, is returned unchanged.
func Main(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git")) //nolint:gosec // G304: reads the .git file of a checkout found by walking up from a directory
	if err != nil {
		return root // a .git directory, or unreadable
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return root
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir")) //nolint:gosec // G304: gitdir is the path that .git file names
	if err != nil {
		return root
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	if filepath.Base(common) != ".git" {
		return root
	}
	main := filepath.Dir(common)
	if r, err := filepath.EvalSymlinks(main); err == nil {
		return r
	}
	return root
}
