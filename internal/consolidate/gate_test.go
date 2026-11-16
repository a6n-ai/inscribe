package consolidate_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/a6n-ai/inscribe/internal/consolidate"
	"github.com/a6n-ai/inscribe/internal/store"
	"github.com/a6n-ai/inscribe/model"
)

func (e *env) typed(t model.MemoryType, key string, status model.Status, evidence int64) string {
	e.t.Helper()
	var id string
	err := e.st.Write(context.Background(), func(tx *store.Tx) error {
		m, err := tx.UpsertMemory(&model.Memory{Type: t, ScopeKind: model.ProjectScope, Scope: "/p", Key: key,
			Text: string(t) + " " + key, Confidence: 0.5, Status: status, FirstSeen: 1, LastSeen: 1}, nil, evidence)
		if err != nil {
			return err
		}
		id = m.ID
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestAddedDecisionWaitsForPromote(t *testing.T) {
	e := newEnv(t)
	ids := e.events(2)
	add := fmt.Sprintf(`{"ops":[{"op":"add","type":"decision","scope_kind":"project","scope":"/p","key":"queue-retries","text":"Queue retries stay at 3 because the mail relay rate-limits.","evidence":[%d]}]}`, ids[0])
	if rep := e.run(&fakeRunner{out: add}, false); len(rep.Valid) != 1 {
		t.Fatalf("add rejected: %v", rep.Rejected)
	}
	added, err := e.st.ListMemories(store.ListFilter{Key: "queue-retries"})
	if err != nil || len(added) != 1 || added[0].Status != model.Candidate {
		t.Fatalf("added = %+v (%v), want one candidate", added, err)
	}
	in, err := consolidate.BuildInput(e.st, session, 0, 100000, func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, m := range in.Memories {
		seen = seen || m.ID == added[0].ID
	}
	if !seen {
		t.Fatalf("a later review does not see the candidate to promote it")
	}
}

func TestAddKeepsActiveStatusOnUpdate(t *testing.T) {
	e := newEnv(t)
	ids := e.events(1)
	id := e.typed(model.Decision, "queue-retries", model.Active, ids[0])
	add := fmt.Sprintf(`{"ops":[{"op":"add","type":"decision","scope_kind":"project","scope":"/p","key":"queue-retries","text":"Queue retries stay at 3.","evidence":[%d]}]}`, ids[0])
	if rep := e.run(&fakeRunner{out: add}, false); len(rep.Valid) != 1 {
		t.Fatalf("add rejected: %v", rep.Rejected)
	}
	if got := e.get(id); got.Status != model.Active {
		t.Errorf("updated memory status = %s, want it to stay active", got.Status)
	}
}

func TestReviewDoesNotRateCommandsOrCochanges(t *testing.T) {
	e := newEnv(t)
	ids := e.events(1)
	cmd := e.typed(model.Command, "go test", model.Active, ids[0])
	co := e.typed(model.Cochange, "a.go|b.go", model.Active, ids[0])
	dec := e.typed(model.Decision, "queue-retries", model.Active, ids[0])
	for _, id := range []string{cmd, co, dec} {
		e.injected(id)
	}
	r := &fakeRunner{out: fmt.Sprintf(`{"ops":[{"op":"rate","id":%q,"used":"yes","helpful":"yes"},{"op":"rate","id":%q,"used":"no","helpful":"no"}]}`, cmd, dec)}
	rep := e.run(r, false)
	if len(rep.Valid) != 1 || rep.Valid[0].ID != dec || len(rep.Rejected) != 1 {
		t.Fatalf("valid = %+v rejected = %+v, want only the decision rate", rep.Valid, rep.Rejected)
	}
	unrated := r.prompts[0][strings.LastIndex(r.prompts[0], `"unrated":[`):]
	if strings.Contains(unrated, cmd) || strings.Contains(unrated, co) {
		t.Errorf("command or co-change offered for rating: %s", unrated)
	}
	for _, id := range []string{cmd, co} {
		for _, o := range e.outcomes(id) {
			if o.Label != nil {
				t.Errorf("%s row labelled by review: %+v", id, o)
			}
		}
	}
}
