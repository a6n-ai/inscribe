package recall

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/a6n-ai/inscribe/internal/store"
	"github.com/a6n-ai/inscribe/model"
)

func ran(ts int64, cmd string, code int) model.Event {
	return model.Event{TS: ts, Kind: model.CommandResult, Project: "/p", Payload: map[string]any{"command": cmd, "exit_code": code}}
}

func TestReplayLabel(t *testing.T) {
	m := model.Memory{Type: model.Command, Key: "cargo test", Text: "tests: cargo test -p x"}
	cases := []struct {
		name  string
		evs   []model.Event
		label int
		rated bool
	}{
		{"ran it and passed", []model.Event{ran(20, "cargo test -p x 2>&1 | tail", 0)}, 1, true},
		{"ran it and failed", []model.Event{ran(20, "cargo test -p x", 101)}, 0, true},
		{"ran another cargo test", []model.Event{ran(20, "cargo test -p y", 0)}, 0, true},
		{"ran something else", []model.Event{ran(20, "git status", 0)}, 0, false},
		{"ran it before it was shown", []model.Event{ran(5, "cargo test -p x", 0)}, 0, false},
		{"failed then passed", []model.Event{ran(20, "cargo test -p x", 1), ran(30, "cargo test -p x", 0)}, 1, true},
	}
	for _, c := range cases {
		label, rated := replayLabel(m, 10, sessionRuns(c.evs))
		if label != c.label || rated != c.rated {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", c.name, label, rated, c.label, c.rated)
		}
	}
}

func TestReplayRatesOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "inscribe.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	var cmdID, noteID string
	err = st.Write(ctx, func(tx *store.Tx) error {
		c, err := tx.UpsertMemory(&model.Memory{Type: model.Command, ScopeKind: model.ProjectScope, Scope: "/p", Key: "cargo test",
			Text: "tests: cargo test -p x", Confidence: 0.5, Status: model.Active, FirstSeen: 1, LastSeen: 1}, nil, 0)
		if err != nil {
			return err
		}
		n, err := tx.UpsertMemory(&model.Memory{Type: model.Note, ScopeKind: model.ProjectScope, Scope: "/p", Key: "n",
			Text: "a note", Confidence: 0.5, Status: model.Active, FirstSeen: 1, LastSeen: 1}, nil, 0)
		if err != nil {
			return err
		}
		cmdID, noteID = c.ID, n.ID
		if err := tx.InsertInjection(store.InjectionInput{TS: 10, Session: "s", Project: "/p", Hook: "SubagentStart", EstTokens: 9, MemoryIDs: []string{cmdID, noteID}}); err != nil {
			return err
		}
		e := ran(20, "cargo test -p x", 0)
		e.Session, e.Agent = "s", "claude-code"
		_, err = tx.InsertEvent(&e)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	w := store.Window{From: 0, To: 100}
	if n, err := Replay(ctx, st, w, time.UnixMilli(50)); err != nil || n != 1 {
		t.Fatalf("first replay = %d, %v; want 1", n, err)
	}
	if n, err := Replay(ctx, st, w, time.UnixMilli(60)); err != nil || n != 0 {
		t.Fatalf("second replay = %d, %v; want 0", n, err)
	}
	rows, shown, helpful, _, err := st.MemoryOutcomes(cmdID, 5)
	if err != nil || shown != 1 || helpful != 1 || rows[0].Reason != store.ReasonReplay {
		t.Fatalf("outcomes = %+v shown=%d helpful=%d err=%v", rows, shown, helpful, err)
	}
	if _, shown, _, _, _ := st.MemoryOutcomes(noteID, 5); shown != 0 {
		t.Fatalf("a note was rated %d times", shown)
	}
	hr, err := st.HookRatings(w)
	if err != nil || len(hr) != 1 || hr[0].Helpful != 1 || hr[0].Rated != 1 {
		t.Fatalf("hook ratings = %+v, %v", hr, err)
	}
	groups, err := st.RecentScoreGroups(10)
	if err != nil || len(groups) != 0 {
		t.Fatalf("replay rows reached the score model's groups: %d, %v", len(groups), err)
	}
}
