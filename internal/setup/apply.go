package setup

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/a6n-ai/inscribe"
	"github.com/a6n-ai/inscribe/agent"
	"github.com/a6n-ai/inscribe/internal/importer"
)

// Runner runs an external command and returns its output.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

type Failure struct {
	Action Action
	Err    error
}

type Report struct {
	Done   []Action
	Failed []Failure
}

// Apply runs each action in order. A failed action is recorded and the rest
// still run. With env.Out set, each action reports as it starts and ends.
func Apply(ctx context.Context, env Env, actions []Action, run Runner) Report {
	out := env.Out
	if out == nil {
		out = io.Discard
	}
	var rep Report
	for i, a := range actions {
		_, _ = fmt.Fprintf(out, "[%d/%d] %s ...\n", i+1, len(actions), a.Desc)
		start := time.Now()
		if err := applyOne(ctx, env, a, run); err != nil {
			_, _ = fmt.Fprintf(out, "failed: %s: %v\n", a.Desc, err)
			rep.Failed = append(rep.Failed, Failure{Action: a, Err: err})
			continue
		}
		_, _ = fmt.Fprintf(out, "done: %s (%s)\n", a.Desc, time.Since(start).Round(100*time.Millisecond))
		rep.Done = append(rep.Done, a)
	}
	return rep
}

func applyOne(ctx context.Context, env Env, a Action, run Runner) error {
	switch a.Kind {
	case "install-hooks":
		ad, err := agent.Get(a.Agent)
		if err != nil {
			return err
		}
		return ad.Install(a.Files[0], env.InscribeBin)
	case "disable-plugin":
		_, err := run(ctx, a.Command[0], a.Command[1:]...)
		return err
	case "import":
		return importHistory(ctx, a.Since, time.Now(), importProgress(env))
	case "write-config":
		return SetConfigKeys(a.Files[0], a.Keys)
	case "mcp-add":
		return addMCP(ctx, a.Command, run)
	}
	return fmt.Errorf("unknown action %q", a.Kind)
}

// addMCP adds the Inscribe MCP server at user scope unless `claude mcp get inscribe` finds it.
func addMCP(ctx context.Context, add []string, run Runner) error {
	if _, err := run(ctx, "claude", "mcp", "get", "inscribe"); err == nil {
		return nil
	}
	_, err := run(ctx, add[0], add[1:]...)
	return err
}

// importProgress reports transcripts read: redrawn in place on a terminal,
// otherwise a line at each quarter.
func importProgress(env Env) func(done, total int) {
	if env.Out == nil {
		return nil
	}
	last := -1
	return func(done, total int) {
		pct := done * 100 / max(total, 1)
		switch {
		case env.Live:
			_, _ = fmt.Fprintf(env.Out, "\r    %d/%d transcripts (%d%%)", done, total, pct)
			if done == total {
				_, _ = fmt.Fprintln(env.Out)
			}
		case pct/25 > last || done == total:
			last = pct / 25
			_, _ = fmt.Fprintf(env.Out, "    %d/%d transcripts (%d%%)\n", done, total, pct)
		}
	}
}

func importHistory(ctx context.Context, since string, now time.Time, progress func(done, total int)) error {
	from, err := sinceDate(since, now)
	if err != nil {
		return err
	}
	h, err := inscribe.Open(inscribe.Options{Caller: "inscribe"})
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()
	_, err = h.ImportClaudeCode(ctx, importer.Options{Since: from, Progress: progress})
	return err
}

// sinceDate maps an import choice ("30d", "90d", "all") to the earliest date imported.
func sinceDate(since string, now time.Time) (time.Time, error) {
	if since == "all" {
		return time.Time{}, nil
	}
	days, err := strconv.Atoi(strings.TrimSuffix(since, "d"))
	if err != nil {
		return time.Time{}, fmt.Errorf("import since %q: want 30d, 90d, or all", since)
	}
	return now.AddDate(0, 0, -days), nil
}
