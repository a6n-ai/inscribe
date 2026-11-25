package extract_test

import (
	"strings"
	"testing"
	"time"

	"github.com/a6n-ai/inscribe/internal/extract"
	"github.com/a6n-ai/inscribe/model"
)

func TestSessionSummary(t *testing.T) {
	day := time.Date(2026, 11, 20, 9, 0, 0, 0, time.Local).UnixMilli()
	ev := func(i int, e model.Event) model.Event {
		e.TS, e.Session, e.Project = day+int64(i), "s1", "/p"
		return e
	}
	edit := func(path string) model.Event {
		return model.Event{Kind: model.PostTool, Tool: "edit", Payload: map[string]any{"path": path}}
	}
	cmd := func(c string, code int) model.Event {
		return model.Event{Kind: model.CommandResult, Payload: map[string]any{"command": c, "exit_code": code}}
	}
	evs := []model.Event{
		ev(0, model.Event{Kind: model.Prompt, PromptText: "move the billing   webhooks onto the new queue"}),
		ev(1, edit("/p/billing/webhooks.go")),
		ev(2, cmd("go test ./billing/... 2>&1 | tail", 1)),
		ev(3, edit("/p/billing/webhooks.go")),
		ev(4, edit("/p/billing/queue.go")),
		ev(5, cmd("go test ./billing/...", 0)),
		ev(6, model.Event{Kind: model.Prompt, PromptText: "add a retry test"}),
		ev(7, model.Event{Kind: model.Prompt, PromptText: "[Image #1] now push it"}),
		ev(8, model.Event{Kind: model.Prompt, PromptText: "<task-notification> <task-id>b1</task-id> done </task-notification>"}),
	}
	m, ok := extract.SessionSummary(evs)
	if !ok {
		t.Fatal("no summary")
	}
	want := `2026-11-20 session: "move the billing webhooks onto the new queue" … last: "now push it"; changed webhooks.go, queue.go; fixed: go test ./billing/...`
	if m.Text != want {
		t.Fatalf("text =\n%s\nwant\n%s", m.Text, want)
	}
	if m.Type != model.Session || m.Key != "session|s1" || m.Scope != "/p" || m.ScopeKind != model.ProjectScope {
		t.Fatalf("memory = %+v", m)
	}
	if search := model.SearchText(*m); !strings.Contains(search, "add a retry test") || !strings.Contains(search, "billing/queue.go") {
		t.Fatalf("search text misses the middle prompt or a file: %q", search)
	}
	twin, _ := extract.SessionSummary([]model.Event{ev(0, model.Event{Kind: model.Prompt, PromptText: "fix downloads"}),
		ev(1, edit("/p/engine/download.rs")), ev(2, edit("/p/tests/download.rs"))})
	if !strings.Contains(twin.Text, "changed engine/download.rs, tests/download.rs") {
		t.Fatalf("same-name files = %q", twin.Text)
	}
	if _, ok := extract.SessionSummary([]model.Event{ev(0, edit("/p/x.go"))}); ok {
		t.Fatal("a session with no prompt was summarized")
	}
}
