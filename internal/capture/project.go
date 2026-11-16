package capture

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/a6n-ai/inscribe/internal/gitroot"
)

// ResolveProject returns the git root containing cwd (symlinks resolved), or
// the resolved cwd when it is not inside a repository. A linked worktree
// resolves to its main repository, so both share one project. It never runs git.
func ResolveProject(cwd string) string {
	dir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		dir = filepath.Clean(cwd)
	}
	root, ok := gitroot.Checkout(dir)
	if !ok {
		return dir
	}
	return gitroot.Main(root)
}

// normalizeExcludes expands a leading ~/ and resolves symlinks, so entries
// compare equal to the resolved project paths that ResolveProject returns.
func normalizeExcludes(paths []string) []string {
	home, _ := os.UserHomeDir()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		} else {
			p = filepath.Clean(p)
		}
		out = append(out, p)
	}
	return out
}

// underAny reports whether project is one of paths or inside one of them.
func underAny(project string, paths []string) bool {
	for _, p := range paths {
		p = filepath.Clean(p)
		if project == p || strings.HasPrefix(project, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
