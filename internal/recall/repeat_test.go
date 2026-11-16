package recall

import (
	"context"
	"testing"

	"github.com/a6n-ai/inscribe/model"
)

func TestPromptRepeatNeedsBetterMatch(t *testing.T) {
	st := newStore(t)
	put(t, st, model.Memory{Type: model.Decision, Key: "relay", Text: "relay mailbox worker deploys with wrangler from apps relay"})
	ask := func(text string) int {
		t.Helper()
		lines, err := Recall(context.Background(), st, nil, cfg(), Query{Project: "/p", Session: "s1", Text: text, Mode: Prompt}, now)
		if err != nil {
			t.Fatal(err)
		}
		return len(lines)
	}
	if ask("deploy the relay worker") != 1 {
		t.Fatal("first prompt should show the line")
	}
	if n := ask("deploy the relay worker again"); n != 0 {
		t.Errorf("same-strength prompt reshowed the line (%d lines)", n)
	}
	if ask("how does the relay mailbox worker deploy with wrangler") != 1 {
		t.Error("a clearly better-matching prompt should show the line again")
	}
	lines, _ := Recall(context.Background(), st, nil, cfg(), Query{Project: "/p", Session: "s2", Text: "deploy the relay worker", Mode: Prompt}, now)
	if len(lines) != 1 {
		t.Error("a new session should show the line")
	}
}
