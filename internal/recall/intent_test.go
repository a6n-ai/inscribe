package recall

import (
	"context"
	"fmt"
	"testing"

	"github.com/a6n-ai/inscribe/model"
)

func TestIntent(t *testing.T) {
	for text, want := range map[string]bool{
		"did we do any ui changes as well?":            true,
		"also start on previous things we tought of":   true,
		"check old logs for the deploy":                true,
		"recall what we decided about auth":            true,
		"do it like before":                            true,
		"what did we try last time":                    true,
		"lets push this, check cpu now before pushing": false,
		"run the tests":                                false,
		"add a remembered-device toggle":               false,
	} {
		if got := Intent(text); got != want {
			t.Errorf("Intent(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestIntentPromptIncludesCandidates(t *testing.T) {
	st := newStore(t)
	put(t, st, model.Memory{Type: model.Note, Key: "a", Text: "deploy with make", Status: model.Candidate})
	put(t, st, model.Memory{Type: model.Note, Key: "b", Text: "deploy with ship", Status: model.Forgotten})
	lines, err := Recall(context.Background(), st, nil, cfg(), Query{Project: "/p", Text: "how did we deploy with make last time", Mode: Prompt}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Key != "a" || !lines[0].Unconfirmed {
		t.Fatalf("lines = %+v, want only the candidate, marked unconfirmed", lines)
	}
	if got := Render(lines); got != "[note] (unconfirmed) deploy with make" {
		t.Errorf("render = %q", got)
	}
}

func TestIntentPromptRaisesLineCap(t *testing.T) {
	st := newStore(t)
	for i := 0; i < 10; i++ {
		put(t, st, model.Memory{Type: model.Note, Key: fmt.Sprint(i), Text: fmt.Sprintf("deploy step %d", i)})
	}
	lines, _ := Recall(context.Background(), st, nil, cfg(), Query{Project: "/p", Text: "how did we deploy earlier", Mode: Prompt}, now)
	if len(lines) != intentMaxLines {
		t.Fatalf("lines = %d, want %d", len(lines), intentMaxLines)
	}
}

func TestIntentPromptSkipsCochangeCandidates(t *testing.T) {
	st := newStore(t)
	put(t, st, model.Memory{Type: model.Cochange, Key: "c", Text: "ui.tsx changes with ui.css", Status: model.Candidate})
	put(t, st, model.Memory{Type: model.Cochange, Key: "d", Text: "nav.tsx changes with nav.css"})
	lines, _ := Recall(context.Background(), st, nil, cfg(), Query{Project: "/p", Text: "did we make ui changes", Mode: Prompt}, now)
	if len(lines) != 1 || lines[0].Key != "d" {
		t.Fatalf("lines = %+v, want only the active cochange", lines)
	}
}
