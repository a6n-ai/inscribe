package consolidate_test

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/a6n-ai/inscribe/internal/consolidate"
	"github.com/a6n-ai/inscribe/internal/store"
)

// injectedFor records that memoryID was shown for the prompt with digest at ts.
func (e *env) injectedFor(memoryID, digest string, ts int64) {
	e.t.Helper()
	err := e.st.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertScoreOutcome(store.OutcomeInput{MemoryID: memoryID, Session: session, Agent: "claude-code",
			TS: ts, Features: "{}", Score: 0.5, PromptDigest: digest, ShownKey: "k", ShownText: "k", LabelRule: "shown"})
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestRatePerPrompt(t *testing.T) {
	e := newEnv(t)
	ids := e.events(1)
	m := e.memory("deploy", ids[0])
	e.injectedFor(m, "dA", 1) // p1
	e.injectedFor(m, "dB", 2) // p2
	e.injectedFor(m, "dB", 3) // p2 again: same prompt, one rating covers both rows
	r := &fakeRunner{out: fmt.Sprintf(`{"ops":[
		{"op":"rate","id":%[1]q,"prompt":"p1","used":"no","helpful":"no"},
		{"op":"rate","id":%[1]q,"prompt":"p2","used":"yes","helpful":"yes"},
		{"op":"rate","id":%[1]q,"prompt":"p2","used":"no","helpful":"no"},
		{"op":"rate","id":%[1]q,"used":"no","helpful":"no"},
		{"op":"rate","id":%[1]q,"prompt":"p9","used":"no","helpful":"no"}]}`, m)}
	rep := e.run(r, false)
	if len(rep.Valid) != 2 || len(rep.Rejected) != 3 {
		t.Fatalf("valid = %d rejected = %v, want the p1 and first p2 rates valid", len(rep.Valid), rep.Rejected)
	}
	if p := r.prompts[0]; !strings.Contains(p, `"shown":["p1","p2"]`) || !strings.Contains(p, `"prompts":{`) {
		t.Errorf("review input lacks the memory's shown prompts:\n%s", p)
	}
	byDigest := map[string][]int{}
	for _, o := range e.outcomes(m) {
		if o.Label == nil {
			t.Fatalf("row %+v left unlabeled", o)
		}
		byDigest[o.PromptDigest] = append(byDigest[o.PromptDigest], *o.Label)
	}
	if fmt.Sprint(byDigest["dA"]) != "[0]" || fmt.Sprint(byDigest["dB"]) != "[1 1]" {
		t.Errorf("labels by prompt = %v, want dA miss and dB hit", byDigest)
	}
}

func TestRateWrongPenalizesOncePerRun(t *testing.T) {
	e := newEnv(t)
	ids := e.events(1)
	m := e.memory("deploy", ids[0])
	e.injectedFor(m, "dA", 1)
	e.injectedFor(m, "dB", 2)
	r := &fakeRunner{out: fmt.Sprintf(`{"ops":[
		{"op":"rate","id":%[1]q,"prompt":"p1","used":"no","helpful":"wrong"},
		{"op":"rate","id":%[1]q,"prompt":"p2","used":"no","helpful":"wrong"}]}`, m)}
	rep := e.run(r, false)
	if len(rep.Valid) != 2 {
		t.Fatalf("valid = %d rejected = %v", len(rep.Valid), rep.Rejected)
	}
	if got := e.get(m).Penalty; math.Abs(got-0.2) > 1e-9 {
		t.Errorf("penalty = %v, want one wrong penalty of 0.2", got)
	}
	undo, err := consolidate.Undo(context.Background(), e.st, rep.RunID)
	if err != nil || undo.Restored != 2 || len(undo.Skipped) != 0 {
		t.Fatalf("undo = %+v (%v), want both rates restored", undo, err)
	}
	if got := e.get(m).Penalty; got != 0 {
		t.Errorf("penalty after undo = %v, want 0", got)
	}
	for _, o := range e.outcomes(m) {
		if o.Label != nil {
			t.Errorf("row %+v still labeled after undo", o)
		}
	}
}
