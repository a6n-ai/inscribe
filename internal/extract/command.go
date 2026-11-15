package extract

import (
	"cmp"
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/a6n-ai/inscribe/internal/config"
	"github.com/a6n-ai/inscribe/internal/store"
	"github.com/a6n-ai/inscribe/model"
)

// classRules is checked in order; the first match wins.
var classRules = []struct {
	class string
	re    *regexp.Regexp
}{
	{"test", regexp.MustCompile(`\b(?:test|tests|pytest|jest|vitest|rspec)\b`)},
	{"lint", regexp.MustCompile(`\b(?:lint|vet|ruff|eslint|golangci-lint|clippy|tsc\s+--noEmit)\b`)},
	{"build", regexp.MustCompile(`\b(?:build|tsc|compile|bundle)\b|^make\b$`)},
	// Entrypoints only; everyday shell (ls, cat, git status) is never stored.
	{"run", regexp.MustCompile(`^(?:make\s+\S+|(?:npm|pnpm|yarn|bun)\s+run\s+\S+|go\s+run\b|cargo\s+run\b|docker\s+compose\s+up\b)`)},
}

var classLabel = map[string]string{"test": "tests", "lint": "lint", "build": "build", "run": "run"}

// NormalizeCommand collapses whitespace so equivalent commands share a key.
func NormalizeCommand(cmd string) string { return strings.Join(strings.Fields(cmd), " ") }

var (
	quoted     = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	segmentSep = regexp.MustCompile(`&&|\|\||[;|&]`)
	envAssign  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// classPrograms are the programs whose own words name a class. Any other
// program (rg, git, sqlite3, echo) only mentions "test" as data.
var classPrograms = map[string]bool{
	"go": true, "cargo": true, "npm": true, "pnpm": true, "yarn": true, "bun": true, "bunx": true, "npx": true,
	"deno": true, "make": true, "mvn": true, "mvnw": true, "gradle": true, "gradlew": true, "dotnet": true,
	"uv": true, "poetry": true, "python": true, "python3": true, "swift": true, "mix": true, "zig": true,
	"docker": true, "turbo": true, "nx": true,
	"pytest": true, "jest": true, "vitest": true, "rspec": true, "eslint": true, "ruff": true,
	"golangci-lint": true, "tsc": true,
}

// commandHeads returns the first words of each segment of a shell line that
// runs a class program, with quoted text removed, so a class word inside an
// argument or a commit message never counts.
func commandHeads(cmd string) []string {
	var heads []string
	for _, seg := range segmentSep.Split(quoted.ReplaceAllString(cmd, "Q"), -1) {
		f := strings.Fields(seg)
		for len(f) > 0 && envAssign.MatchString(f[0]) {
			f = f[1:]
		}
		if len(f) == 0 {
			continue
		}
		f[0] = filepath.Base(f[0])
		if !classPrograms[f[0]] {
			continue
		}
		heads = append(heads, strings.Join(f[:min(len(f), 4)], " "))
	}
	return heads
}

// ClassifyCommand returns the command's class, or "" when it is not worth
// remembering.
func ClassifyCommand(cmd string) string {
	class, _ := classify(cmd)
	return class
}

// CommandKey returns the dedupe key a command memory for cmd is stored under,
// or "" when cmd is not worth remembering.
func CommandKey(cmd string) string {
	_, key := classify(NormalizeCommand(cmd))
	return key
}

var (
	redirect   = regexp.MustCompile(`\d*>&\d+|&?\d*>>?\s*\S+|<\s*\S+`)
	quoteToken = regexp.MustCompile("\x00(\\d+)\x00")
	// filterFlags pick one test by name; the remembered command runs them all.
	filterFlags = map[string]bool{"-run": true, "-k": true, "-t": true, "--testNamePattern": true, "--grep": true}
)

// CoreCommand reduces a shell line to the segments that run a class program,
// without redirections or test-name filters, so a one-off
// `cd <project> && go test ./x -run Foo 2>&1 | tail -5` is remembered as
// `go test ./x`. A cd into a folder of project is kept as `cd <rel> && `.
// It returns "" when no segment runs a class program.
func CoreCommand(cmd, project string) string {
	var quotes []string
	masked := quoted.ReplaceAllStringFunc(cmd, func(q string) string {
		quotes = append(quotes, q)
		return "\x00" + strconv.Itoa(len(quotes)-1) + "\x00"
	})
	dir := ""
	var keep []string
	for _, seg := range segmentSep.Split(redirect.ReplaceAllString(masked, ""), -1) {
		f := strings.Fields(seg)
		i := 0
		for i < len(f) && envAssign.MatchString(f[i]) {
			i++
		}
		if i == len(f) {
			continue
		}
		if f[i] == "cd" && i+1 < len(f) && project != "" {
			if rel, err := filepath.Rel(project, f[i+1]); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && filepath.IsAbs(f[i+1]) {
				dir = rel
			}
			continue
		}
		if !classPrograms[filepath.Base(f[i])] {
			continue
		}
		var args []string
		for j := 0; j < len(f); j++ {
			name, _, _ := strings.Cut(f[j], "=")
			if j > i && filterFlags[name] {
				if name == f[j] {
					j++
				}
				continue
			}
			args = append(args, f[j])
		}
		keep = append(keep, strings.Join(args, " "))
	}
	if len(keep) == 0 {
		return ""
	}
	out := strings.Join(keep, " && ")
	if dir != "" {
		out = "cd " + dir + " && " + out
	}
	return quoteToken.ReplaceAllStringFunc(out, func(tok string) string {
		n, _ := strconv.Atoi(quoteToken.FindStringSubmatch(tok)[1])
		return quotes[n]
	})
}

// classify returns the command's class and its dedupe key: the program plus the
// words that matched, so "go test ./a" and "go vet ./b && go test ./c" share "go test".
func classify(cmd string) (class, key string) {
	heads := commandHeads(cmd)
	for _, r := range classRules {
		for _, h := range heads {
			if match := r.re.FindString(h); match != "" {
				prog, _, _ := strings.Cut(h, " ")
				if prog == match || strings.HasPrefix(match, prog+" ") {
					return r.class, match
				}
				return r.class, prog + " " + match
			}
		}
	}
	return "", ""
}

// failureLine matches output lines that only a failed test or build prints.
var failureLine = regexp.MustCompile(`(?m)^\s*(?:--- FAIL\b|FAIL\b|ERR_PNPM_\S+|npm ERR!).*$` +
	`|(?m)^.*\b[1-9]\d* failed\b.*$` +
	`|(?m)^.*\berror TS\d+:.*$` +
	`|(?m)^\S+\.go:\d+:\d+: .*$`)

// PipedFailure returns the first failure line in output when cmd is a test or
// build run whose exit code came from a later pipe or ; segment (`go test ./... |
// tail` exits 0 when tests fail). It returns "" otherwise.
func PipedFailure(cmd, output string) string {
	if c := ClassifyCommand(NormalizeCommand(cmd)); c != "test" && c != "build" {
		return ""
	}
	if !strings.ContainsAny(quoted.ReplaceAllString(cmd, "Q"), "|;") {
		return ""
	}
	return strings.TrimSpace(failureLine.FindString(output))
}

// maxCommandLen drops pasted scripts: a real entrypoint command is short.
const maxCommandLen = 200

type commandX struct{}

// NewCommand remembers the commands that work in a project.
func NewCommand() Extractor { return commandX{} }

func (commandX) Kinds() []model.EventKind { return []model.EventKind{model.CommandResult} }

func (commandX) Extract(_ context.Context, e *model.Event, tx *store.Tx, _ config.Config) error {
	if code, ok := intVal(e.Payload, "exit_code"); !ok || code != 0 {
		return nil
	}
	raw := str(e.Payload, "command")
	cmd := NormalizeCommand(raw)
	if strings.ContainsAny(raw, "\n\r") || strings.Contains(raw, "<<") || len(cmd) > maxCommandLen {
		return nil
	}
	class, key := classify(cmd)
	if class == "" {
		return nil
	}
	m := projectMemory(e, model.Command, key, classLabel[class]+": "+cmp.Or(CoreCommand(cmd, e.Project), cmd), map[string]any{"class": class}, model.Active)
	_, err := tx.UpsertMemory(m, nil, e.ID)
	return err
}
