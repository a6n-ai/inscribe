package store

import (
	"context"
	"testing"

	"github.com/a6n-ai/inscribe/model"
)

func seedRated(t *testing.T, st *Store) (a, b string) {
	t.Helper()
	ctx := context.Background()
	err := st.Write(ctx, func(tx *Tx) error {
		for _, k := range []string{"a", "b"} {
			m, err := tx.UpsertMemory(&model.Memory{Type: model.Command, ScopeKind: model.ProjectScope, Scope: "p", Key: k, Text: k,
				Confidence: 0.5, Status: model.Active, FirstSeen: 1, LastSeen: 1}, nil, 0)
			if err != nil {
				return err
			}
			if k == "a" {
				a = m.ID
			} else {
				b = m.ID
			}
		}
		for _, in := range []InjectionInput{
			{TS: 10, Session: "s", Project: "p", Hook: "UserPromptSubmit", EstTokens: 30, MemoryIDs: []string{a, b}, PromptDigest: "d1"},
			{TS: 11, Session: "s", Project: "p", Hook: "SubagentStart", EstTokens: 50, MemoryIDs: []string{a, b}},
			{TS: 12, Session: "s", Project: "p", Hook: "SubagentStart", EstTokens: 50, MemoryIDs: []string{b}},
			{TS: 13, Session: "s", Project: "p", Hook: "UserPromptSubmit", EstTokens: 99, MemoryIDs: []string{a}, PromptDigest: "d2", Holdout: true},
		} {
			if err := tx.InsertInjection(in); err != nil {
				return err
			}
		}
		for _, o := range []OutcomeInput{
			{MemoryID: a, Session: "s", TS: 10, Features: "[]", PromptDigest: "d1"},
			{MemoryID: b, Session: "s", TS: 10, Features: "[]", PromptDigest: "d1"},
		} {
			if err := tx.InsertScoreOutcome(o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = st.Write(ctx, func(tx *Tx) error {
		for id, label := range map[string]int{a: 1, b: 0} {
			if _, err := tx.tx.Exec(`UPDATE score_outcomes SET label = ?, reason = 'agent', resolved = 20 WHERE memory_id = ?`, label, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestHookRatings(t *testing.T) {
	st, _ := openTemp(t)
	seedRated(t, st)
	got, err := st.HookRatings(Window{From: 0, To: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]HookRating{
		"SubagentStart":    {Hook: "SubagentStart", Tokens: 100},
		"UserPromptSubmit": {Hook: "UserPromptSubmit", Tokens: 30, Helpful: 1, Rated: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, h := range got {
		if h != want[h.Hook] {
			t.Fatalf("%s = %+v, want %+v", h.Hook, h, want[h.Hook])
		}
	}
	if got[0].Hook != "SubagentStart" {
		t.Fatalf("not sorted by tokens: %+v", got)
	}
}

func TestUnhelpfulShown(t *testing.T) {
	st, _ := openTemp(t)
	a, b := seedRated(t, st)
	got, err := st.UnhelpfulShown(Window{From: 0, To: 100}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	// a was rated helpful once, so only b (shown 3 times, rated once, never helpful) is listed.
	if len(got) != 1 || got[0] != (ShownTally{MemoryID: b, Shown: 3, Rated: 1}) {
		t.Fatalf("got %+v, want only %s", got, b)
	}
	if got, _ := st.UnhelpfulShown(Window{From: 0, To: 100}, 4, 10); len(got) != 0 {
		t.Fatalf("minShown 4 = %+v", got)
	}
	if err := st.Write(context.Background(), func(tx *Tx) error { return tx.SetStatus(b, model.Forgotten) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.UnhelpfulShown(Window{From: 0, To: 100}, 1, 10); len(got) != 0 {
		t.Fatalf("forgotten memory still listed: %+v (a=%s)", got, a)
	}
}
