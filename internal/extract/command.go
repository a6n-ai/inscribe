package extract

import (
	"cmp"
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/a6n-ai/inscribe/internal/config"
	"github.com/a6n-ai/inscribe/internal/gitroot"
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
	segmentSep = regexp.MustCompile(`&&|\|\||[;|&\n]`)
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

// wrappers run the command after them. Each lists its flags that take a value.
var wrappers = map[string]map[string]bool{
	"env":     {"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true},
	"timeout": {"-s": true, "--signal": true, "-k": true, "--kill-after": true},
	"nice":    {"-n": true, "--adjustment": true},
	"nohup":   {}, "time": {}, "command": {}, "exec": {},
	"xargs": {"-n": true, "-I": true, "-P": true, "-L": true, "-s": true, "-d": true, "-E": true},
}

// consumers take a program name as data (which go, pgrep vitest, brew install
// go), so a class program after them is never run.
var consumers = map[string]bool{
	"which": true, "whereis": true, "type": true, "man": true, "help": true, "tldr": true,
	"pgrep": true, "pkill": true, "killall": true, "kill": true, "echo": true, "printf": true,
	"ls": true, "rg": true, "grep": true, "fd": true, "find": true, "cat": true, "head": true, "tail": true,
	"du": true, "brew": true, "git": true, "apt": true, "apt-get": true, "port": true,
	"for": true, "case": true, "select": true, // for p in go cargo: names in a list
}

var (
	programName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	funcHead    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*\(\)\{?$`)
)

// filler words run nothing themselves: shell keywords, a function head such as
// T() {, and the placeholder the scrubber leaves for a secret-looking word.
func filler(w string) bool {
	switch w {
	case "do", "then", "else", "if", "while", "until", "!", "{":
		return true
	}
	return funcHead.MatchString(w) || strings.HasPrefix(w, "[REDACTED:")
}

// outputFilters only shape what a command prints, so the remembered command
// leaves them out: `rtk cargo test` is remembered as `cargo test`.
var outputFilters = map[string]bool{"rtk": true}

// wrappedAt reads an unknown program as a wrapper (rtk, sudo, caffeinate, mise
// exec --) when a class program follows it within a few words, so a plugin
// that runs commands is understood without being listed. It returns the class
// program's index, or i when f[i] is not such a wrapper.
func wrappedAt(f []string, i int) int {
	name := filepath.Base(f[i])
	if classPrograms[name] || consumers[name] || !programName.MatchString(f[i]) {
		return i
	}
	for j, bare := i+1, 0; j < len(f); j++ {
		switch {
		case classPrograms[f[j]]: // a bare name; rm bin/golangci-lint names a file
			return j
		case strings.HasPrefix(f[j], "-"):
		case bare < 2: // a subcommand or its value: rtk bypass, mise exec, op run
			bare++
		default:
			return i
		}
	}
	return i
}

var heredocTag = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// stripHeredocs drops heredoc bodies, so their lines are never read as commands
// once a shell line is split at newlines.
func stripHeredocs(cmd string) string {
	lines := strings.Split(cmd, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		for _, m := range heredocTag.FindAllStringSubmatch(lines[i], -1) {
			for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != m[1] {
				i++
			}
			i++ // the closing tag
		}
	}
	return strings.Join(out, "\n")
}

// segmentFields splits one shell segment into words, without the parens or
// braces of a subshell or group around it.
func segmentFields(seg string) []string {
	return strings.Fields(strings.Trim(seg, "(){} \t\n"))
}

// programAt returns the index of the program a segment runs, past variable
// assignments and wrappers such as env and timeout, or len(f) when there is none.
func programAt(f []string) int {
	i := 0
	for i < len(f) {
		if envAssign.MatchString(f[i]) || filler(f[i]) {
			i++
			continue
		}
		name := filepath.Base(f[i])
		valued, ok := wrappers[name]
		if !ok {
			if j := wrappedAt(f, i); j > i {
				i = j
				continue
			}
			return i
		}
		for i++; i < len(f) && strings.HasPrefix(f[i], "-"); i++ {
			if flag, _, _ := strings.Cut(f[i], "="); valued[flag] && flag == f[i] {
				i++
			}
		}
		if name == "timeout" && i < len(f) {
			i++ // the duration
		}
	}
	return i
}

// commandHeads returns the first words of each segment of a shell line that
// runs a class program, with quoted text removed, so a class word inside an
// argument or a commit message never counts.
func commandHeads(cmd string) []string {
	var heads []string
	for _, seg := range segmentSep.Split(quoted.ReplaceAllString(stripHeredocs(cmd), "Q"), -1) {
		f := segmentFields(seg)
		f = f[programAt(f):]
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
	masked := quoted.ReplaceAllStringFunc(stripHeredocs(cmd), func(q string) string {
		quotes = append(quotes, q)
		return "\x00" + strconv.Itoa(len(quotes)-1) + "\x00"
	})
	dir := ""
	var keep []string
	for _, seg := range segmentSep.Split(redirect.ReplaceAllString(masked, ""), -1) {
		f := segmentFields(seg)
		i := programAt(f)
		if i == len(f) {
			continue
		}
		if f[i] == "cd" && i+1 < len(f) && project != "" {
			if rel := projectRel(project, f[i+1]); rel != "" {
				dir = rel
			}
			continue
		}
		if !classPrograms[filepath.Base(f[i])] {
			continue
		}
		f, i = dropNoise(f, i)
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

// projectRel returns target relative to the checkout it sits in, when that
// checkout is project or one of project's linked worktrees, so a cd into
// <worktree>/apps/web is remembered as `cd apps/web`. It returns "" otherwise.
func projectRel(project, target string) string {
	if !filepath.IsAbs(target) {
		return ""
	}
	base := project
	if root, ok := gitroot.Checkout(target); ok && root != project && gitroot.Main(root) == project {
		base = root
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
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

// dropNoise removes the words before the program at i that the remembered
// command should not keep: filler, and output filters with their subcommand
// (rtk bypass). It returns the words and the program's new index.
func dropNoise(f []string, i int) ([]string, int) {
	for k := 0; k < i; k++ {
		n := 0
		switch {
		case filler(f[k]):
			n = 1
		case outputFilters[filepath.Base(f[k])]:
			n = 1
			for n < i-k && !strings.HasPrefix(f[k+n], "-") && !envAssign.MatchString(f[k+n]) && f[k+n] != f[i] {
				n++ // its subcommand words, up to the program or a flag
			}
		}
		if n > 0 {
			f, i, k = slices.Delete(slices.Clone(f), k, k+n), i-n, k-1
		}
	}
	return f, i
}
