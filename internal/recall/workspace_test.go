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

	// Cross-project recall off: this test is about the workspace's own repos.
	c := cfg()
	c.CrossProject = 0
	for _, src := range []Source{nil, IndexSource{Idx: indexOf(t, st), Store: st}} {
		for _, q := range []Query{{Project: ws, Mode: SessionStart}, {Project: ws, Text: "deploy wrangler worker", Mode: Prompt}} {
			lines, err := Recall(context.Background(), st, src, c, q, now)
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

func TestPromptRecallReadsOtherProjects(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	here, there := filepath.Join(root, "here"), filepath.Join(root, "billing")
	st := newStore(t)
	put(t, st, model.Memory{Type: model.Decision, Key: "retries", ScopeKind: model.ProjectScope, Scope: there, Text: "billing webhooks retry three times with backoff"})
	put(t, st, model.Memory{Type: model.Command, Key: "go test", ScopeKind: model.ProjectScope, Scope: there, Text: "tests: go test ./billing/webhooks retry"})
	q := Query{Project: here, Text: "how do billing webhooks retry", Mode: Prompt}
	for _, src := range []Source{nil, IndexSource{Idx: indexOf(t, st), Store: st}} {
		lines, err := Recall(context.Background(), st, src, cfg(), q, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 1 || lines[0].Repo != "billing" {
			t.Fatalf("src %T: lines = %+v, want only the billing decision, labeled with its repo", src, lines)
		}
		if got := Render(lines); got != "[decision] (billing) billing webhooks retry three times with backoff" {
			t.Fatalf("render = %q", got)
		}
		local, err := Recall(context.Background(), st, src, cfg(), Query{Project: there, Text: q.Text, Mode: Prompt}, now)
		if err != nil || len(local) == 0 || lines[0].Score >= local[0].Score {
			t.Fatalf("other-project score %v not below its own-project score (%+v, %v)", lines[0].Score, local, err)
		}
		off := cfg()
		off.CrossProject = 0
		if lines, _ := Recall(context.Background(), st, src, off, q, now); len(lines) != 0 {
			t.Fatalf("cross_project 0 still read another project: %+v", lines)
		}
	}
}
