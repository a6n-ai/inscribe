package recall

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/a6n-ai/inscribe/model"
)

func TestWorkspaceRecallSeesChildRepos(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(ws, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	put(t, st, model.Memory{Type: model.Decision, Key: "child", ScopeKind: model.ProjectScope, Scope: repo, Text: "deploy the relay worker with wrangler"})
	put(t, st, model.Memory{Type: model.Decision, Key: "other", ScopeKind: model.ProjectScope, Scope: ws + "-sibling/x", Text: "deploy the sibling worker with wrangler"})

	for _, src := range []Source{nil, IndexSource{Idx: indexOf(t, st), Store: st}} {
		for _, q := range []Query{{Project: ws, Mode: SessionStart}, {Project: ws, Text: "deploy wrangler worker", Mode: Prompt}} {
			lines, err := Recall(context.Background(), st, src, cfg(), q, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 || lines[0].Text != "deploy the relay worker with wrangler" {
				t.Errorf("mode %v src %T: lines = %+v, want only the child repo memory", q.Mode, src, lines)
			} else if got := Render(lines); got != "[decision] (repo) deploy the relay worker with wrangler" {
				t.Errorf("render = %q, want the repo named", got)
			}
		}
	}

	// From inside the repo, the workspace's other repos stay invisible.
	lines, err := Recall(context.Background(), st, nil, cfg(), Query{Project: filepath.Join(ws, "other-repo-with-git"), Mode: SessionStart}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Errorf("unrelated project saw %d lines", len(lines))
	}
}
