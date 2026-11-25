package extract

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/a6n-ai/inscribe/model"
)

const (
	summaryPrompts    = 8   // prompts kept in the body
	summaryPromptRune = 200 // a body prompt is cut to this many runes
	summaryFiles      = 20  // changed files kept in the body
	summaryLineFiles  = 4   // changed files named in the line
)

// SessionKey is the key a session's summary memory is stored under.
func SessionKey(session string) string { return "session|" + session }

// SessionSummary condenses one session's events into a memory: what was asked,
// which files changed, and which commands passed, so a later session in the same
// project can recall the work without reading old transcripts. ok is false for a
// session with no prompt.
func SessionSummary(evs []model.Event) (*model.Memory, bool) {
	var prompts, files, passed []string
	edits := map[string]int{}
	failed := map[string]bool{}
	var fixed []string
	var first, last *model.Event
	for i := range evs {
		e := &evs[i]
		if first == nil {
			first = e
		}
		last = e
		switch e.Kind {
		case model.Prompt:
			p := oneLine(userText(e.PromptText), summaryPromptRune)
			if p != "" && !slices.Contains(prompts, p) {
				prompts = append(prompts, p)
			}
		case model.CommandResult:
			core := CoreCommand(str(e.Payload, "command"), e.Project)
			if core == "" {
				continue
			}
			if code, ok := intVal(e.Payload, "exit_code"); ok && code == 0 {
				if failed[core] && !slices.Contains(fixed, core) {
					fixed = append(fixed, core)
				}
				if !slices.Contains(passed, core) {
					passed = append(passed, core)
				}
			} else {
				failed[core] = true
			}
		default:
			if path, ok := isEdit(e); ok {
				rel := path
				if r, err := filepath.Rel(e.Project, path); err == nil && !strings.HasPrefix(r, "..") {
					rel = r
				}
				if edits[rel] == 0 {
					files = append(files, rel)
				}
				edits[rel]++
			}
		}
	}
	if len(prompts) == 0 {
		return nil, false
	}
	// Most-edited files first; ties keep the order they were first touched.
	slices.SortStableFunc(files, func(a, b string) int { return edits[b] - edits[a] })

	var line strings.Builder
	fmt.Fprintf(&line, "%s session: %q", time.UnixMilli(first.TS).Format("2006-01-02"), oneLine(prompts[0], 120))
	if len(prompts) > 1 {
		fmt.Fprintf(&line, " … last: %q", oneLine(prompts[len(prompts)-1], 100))
	}
	if len(files) > 0 {
		names := make([]string, 0, summaryLineFiles)
		for _, f := range files[:min(len(files), summaryLineFiles)] {
			// Two files of one name (engine/src/download.rs, tests/download.rs) keep their paths.
			name := filepath.Base(f)
			for _, g := range files[:min(len(files), summaryLineFiles)] {
				if g != f && filepath.Base(g) == name {
					name = f
				}
			}
			names = append(names, name)
		}
		fmt.Fprintf(&line, "; changed %s", strings.Join(names, ", "))
		if n := len(files) - summaryLineFiles; n > 0 {
			fmt.Fprintf(&line, " (+%d more)", n)
		}
	}
	if len(fixed) > 0 {
		fmt.Fprintf(&line, "; fixed: %s", strings.Join(fixed[:min(len(fixed), 2)], "; "))
	}
	body := map[string]any{
		"session": first.Session,
		"prompts": prompts[max(0, len(prompts)-summaryPrompts):],
		"files":   files[:min(len(files), summaryFiles)],
		"passed":  passed,
	}
	m := projectMemory(first, model.Session, SessionKey(first.Session), line.String(), body, model.Active)
	m.LastSeen = last.TS
	// A record of what happened, not a guess, so it starts near the top.
	m.Confidence = 0.9
	return m, true
}

var imageRef = regexp.MustCompile(`\[Image #\d+\]`)

// userText is what the user typed in a prompt, without image markers. A prompt
// that opens with a tag, such as <task-notification> or <command-name>, came
// from the agent's harness, not the user, and counts as empty.
func userText(s string) string {
	s = strings.TrimSpace(imageRef.ReplaceAllString(s, " "))
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

func oneLine(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
