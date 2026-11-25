package main

import (
	"cmp"
	"context"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a6n-ai/inscribe"
	"github.com/a6n-ai/inscribe/internal/config"
	"github.com/a6n-ai/inscribe/internal/recall"
	"github.com/a6n-ai/inscribe/internal/stats"
	"github.com/a6n-ai/inscribe/internal/store"
	"github.com/a6n-ai/inscribe/model"
)

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
	}
}

// openStore opens the store for one request and writes the error page when it cannot.
func openStore(w http.ResponseWriter, home string) (*inscribe.Inscribe, bool) {
	h, err := inscribe.Open(inscribe.Options{Home: home, Caller: "inscribe"})
	if err != nil {
		http.Error(w, "the store does not open; run inscribe doctor", http.StatusInternalServerError)
		return nil, false
	}
	return h, true
}

func loadConfig(w http.ResponseWriter, home string) (config.Config, []string, bool) {
	cfg, warns, err := config.Load(home, "")
	if err != nil {
		http.Error(w, "config.toml does not load; run inscribe doctor", http.StatusInternalServerError)
		return cfg, nil, false
	}
	return cfg, warns, true
}

type share struct {
	Analytics            bool
	Next, Button         string
	ShareHost, ShareNote string
}

// shareState shows only the share URL's host: the rest of it may carry a token.
func shareState(cfg config.Config) share {
	s := share{Analytics: cfg.Share.Analytics, Next: "1", Button: "Turn sharing on"}
	switch {
	case cfg.Share.URL == "":
		s.ShareHost = "empty"
	case shareURLOK(cfg.Share.URL):
		u, _ := url.Parse(cfg.Share.URL)
		s.ShareHost = u.Hostname()
	default:
		s.ShareHost = "not usable"
	}
	if cfg.Share.Analytics {
		s.Next, s.Button = "0", "Turn sharing off"
		if !shareURLOK(cfg.Share.URL) {
			s.ShareNote = "Sharing is on, but the share URL is not an absolute http or https URL. Nothing is uploaded."
		}
	}
	return s
}

type weightRow struct {
	Name  string
	Value float64
}

type rateRow struct {
	Source       string
	Hits, Misses int
	Reasons      string
}

type countRow struct {
	Name string
	N    int
}

type topRow struct {
	ID, Text, Scope string
	N               int
}

type overviewPage struct {
	Days                int
	M                   stats.Measured
	Rates               []rateRow
	Status, Type        []countRow
	Hooks               []store.HookRating
	Top                 []topRow
	Weights             []weightRow
	Seen, Hits, Misses  int
	LabelRule, StepRule string
	Share               share
}

func sortedCounts(m map[string]int) []countRow {
	out := make([]countRow, 0, len(m))
	for k, n := range m {
		out = append(out, countRow{k, n})
	}
	slices.SortFunc(out, func(a, b countRow) int { return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.Name, b.Name)) })
	return out
}

func serveOverview(w http.ResponseWriter, r *http.Request, home string) {
	cfg, _, ok := loadConfig(w, home)
	if !ok {
		return
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	days := 7
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && slices.Contains([]int{7, 14, 30, 90}, d) {
		days = d
	}
	rep, err := h.StatsReport(r.Context(), inscribe.StatsOptions{Days: days, All: true})
	if err != nil {
		http.Error(w, "the stats do not load; run inscribe doctor", http.StatusInternalServerError)
		return
	}
	hooks, err := h.HookRatings(days)
	if err != nil {
		http.Error(w, "the hook ratings do not load", http.StatusInternalServerError)
		return
	}
	wt, err := h.ScoreWeights()
	if err != nil {
		http.Error(w, "the score model does not load", http.StatusInternalServerError)
		return
	}
	m := rep.Measured
	page := overviewPage{
		Days: days, M: m,
		Hooks: hooks, Status: sortedCounts(m.ByStatus), Type: sortedCounts(m.ByType),
		Weights: []weightRow{{"text (pinned)", wt.Text}, {"key", wt.Key}, {"body", wt.Body}, {"type", wt.Type},
			{"scope", wt.Scope}, {"agent", wt.Agent}, {"command", wt.Command}, {"trajectory", wt.Trajectory}},
		Seen: wt.Seen, Hits: wt.Hits, Misses: wt.Misses,
		LabelRule: recall.LabelRule, StepRule: recall.StepRule,
		Share: shareState(cfg),
	}
	for _, src := range []string{"agent", "review", "rule"} {
		rt := m.Rates[src]
		var reasons []string
		for _, c := range sortedCounts(rt.MissReasons) {
			reasons = append(reasons, fmt.Sprintf("%s %d", c.Name, c.N))
		}
		page.Rates = append(page.Rates, rateRow{src, rt.Hits, rt.Misses, strings.Join(reasons, ", ")})
	}
	for _, c := range m.TopInjected {
		row := topRow{ID: c.MemoryID, Text: c.MemoryID, N: c.N}
		if mem, err := h.GetMemory(c.MemoryID); err == nil {
			row.Text, row.Scope = mem.Text, cmp.Or(mem.Scope, string(mem.ScopeKind))
		}
		page.Top = append(page.Top, row)
	}
	render(w, "overview", page)
}

type recallPage struct {
	Q, Project string
	Ran, Off   bool
	Lines      []inscribe.Line
}

func serveRecall(w http.ResponseWriter, r *http.Request, home string) {
	cfg, _, ok := loadConfig(w, home)
	if !ok {
		return
	}
	page := recallPage{Q: strings.TrimSpace(r.URL.Query().Get("q")), Project: strings.TrimSpace(r.URL.Query().Get("project")), Off: !cfg.Recall.Inject}
	if page.Project != "" {
		page.Project = filepath.Clean(page.Project)
	}
	if page.Q != "" && !page.Off {
		h, ok := openStore(w, home)
		if !ok {
			return
		}
		defer func() { _ = h.Close() }()
		// Holdout keeps a preview from marking lines used or writing outcome rows.
		lines, err := h.Recall(r.Context(), inscribe.Query{Project: page.Project, Text: page.Q, Mode: recall.Prompt, Holdout: true})
		if err != nil {
			http.Error(w, "recall failed; run inscribe doctor", http.StatusInternalServerError)
			return
		}
		page.Ran, page.Lines = true, lines
	}
	render(w, "recall", page)
}

const memoryListMax = 300

type memoriesPage struct {
	Q, Status, Type, Scope string
	Statuses, Types        []string
	Total                  int
	Rows                   []model.Memory
}

func serveMemories(w http.ResponseWriter, r *http.Request, home string) {
	q := r.URL.Query()
	page := memoriesPage{Q: strings.TrimSpace(q.Get("q")), Status: cmp.Or(q.Get("status"), "active"), Type: q.Get("type"), Scope: q.Get("scope"),
		Statuses: []string{"active", "candidate", "stale", "superseded", "forgotten", "all"},
		Types:    []string{"project", "command", "fix", "cochange", "preference", "decision", "skill_use", "skill_flow", "note", "session"}}
	var f inscribe.ListFilter
	if page.Status != "all" {
		f.Statuses = []model.Status{model.Status(page.Status)}
	}
	if page.Type != "" {
		f.Types = []model.MemoryType{model.MemoryType(page.Type)}
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	ms, err := h.List(r.Context(), f)
	if err != nil {
		http.Error(w, "the memories do not load", http.StatusInternalServerError)
		return
	}
	needle := strings.ToLower(page.Q)
	for _, m := range ms {
		if page.Scope != "" && string(m.ScopeKind) != page.Scope {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(m.Key+"\n"+m.Text+"\n"+m.Scope), needle) {
			continue
		}
		page.Total++
		if len(page.Rows) < memoryListMax {
			page.Rows = append(page.Rows, m)
		}
	}
	render(w, "memories", page)
}

type memoryPage struct {
	M                         *model.Memory
	Events                    []model.Event
	Outcomes                  []store.MemoryOutcome
	Shown, Helpful, Unhelpful int
}

func serveMemory(w http.ResponseWriter, r *http.Request, home string) {
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	m, err := h.GetMemory(r.PathValue("id"))
	if err != nil {
		http.Error(w, "no such memory", http.StatusNotFound)
		return
	}
	page := memoryPage{M: m}
	if page.Events, err = h.Why(r.Context(), m.ID); err != nil {
		http.Error(w, "the evidence does not load", http.StatusInternalServerError)
		return
	}
	if page.Outcomes, page.Shown, page.Helpful, page.Unhelpful, err = h.MemoryOutcomes(m.ID, 20); err != nil {
		http.Error(w, "the ratings do not load", http.StatusInternalServerError)
		return
	}
	render(w, "memory", page)
}

const sessionListMax = 100

// sessionIdle is how long an open session can go quiet before the page calls it idle.
const sessionIdle = 30 * time.Minute

type sessionItem struct {
	store.SessionRow
	State string
}

// sessionState is "closed" once the session ended, "open" while it had an event
// in the last sessionIdle, and "idle" when it went quiet without a SessionEnd.
func sessionState(r store.SessionRow, now time.Time) string {
	switch {
	case r.Ended != 0:
		return "closed"
	case now.Sub(time.UnixMilli(r.LastEvent)) < sessionIdle:
		return "open"
	default:
		return "idle"
	}
}

type sessionsPage struct {
	Rows             []sessionItem
	Open, Idle, Done int
}

func serveSessions(w http.ResponseWriter, _ *http.Request, home string) {
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	rows, err := h.SessionRows(sessionListMax)
	if err != nil {
		http.Error(w, "the sessions do not load", http.StatusInternalServerError)
		return
	}
	var page sessionsPage
	now := time.Now()
	for _, r := range rows {
		it := sessionItem{r, sessionState(r, now)}
		switch it.State {
		case "open":
			page.Open++
		case "idle":
			page.Idle++
		default:
			page.Done++
		}
		page.Rows = append(page.Rows, it)
	}
	render(w, "sessions", page)
}

type sessionPage struct {
	S               sessionItem
	Captured, Shown []*model.Memory
}

func serveSession(w http.ResponseWriter, r *http.Request, home string) {
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	row, err := h.SessionByID(r.PathValue("id"))
	if err != nil {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	page := sessionPage{S: sessionItem{row, sessionState(row, time.Now())}}
	captured, err := h.SessionCaptured(row.ID)
	if err != nil {
		http.Error(w, "the captured memories do not load", http.StatusInternalServerError)
		return
	}
	shown, err := h.SessionInjectedIDs(row.ID)
	if err != nil {
		http.Error(w, "the shown memories do not load", http.StatusInternalServerError)
		return
	}
	// A purged memory has no row left; it drops out of both lists.
	for _, id := range captured {
		if m, err := h.GetMemory(id); err == nil {
			page.Captured = append(page.Captured, m)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(shown)) {
		if m, err := h.GetMemory(id); err == nil {
			page.Shown = append(page.Shown, m)
		}
	}
	render(w, "session", page)
}

type discoverRow struct {
	store.ShownTally
	Key, Text, Type, Scope string
}

type discoverPage struct {
	Days, Min int
	Replayed  string
	Rows      []discoverRow
}

func serveDiscover(w http.ResponseWriter, r *http.Request, home string) {
	q := r.URL.Query()
	page := discoverPage{Days: 30, Min: 10}
	if n, err := strconv.Atoi(q.Get("replayed")); err == nil && n >= 0 {
		page.Replayed = strconv.Itoa(n)
	}
	if d, err := strconv.Atoi(q.Get("days")); err == nil && slices.Contains([]int{7, 14, 30, 90}, d) {
		page.Days = d
	}
	if n, err := strconv.Atoi(q.Get("min")); err == nil && n >= 1 {
		page.Min = n
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	ts, err := h.UnhelpfulShown(page.Days, page.Min, memoryListMax)
	if err != nil {
		http.Error(w, "the shown counts do not load", http.StatusInternalServerError)
		return
	}
	for _, t := range ts {
		row := discoverRow{ShownTally: t, Key: t.MemoryID}
		if m, err := h.GetMemory(t.MemoryID); err == nil {
			row.Key, row.Text, row.Type, row.Scope = m.Key, m.Text, string(m.Type), cmp.Or(m.Scope, string(m.ScopeKind))
		}
		page.Rows = append(page.Rows, row)
	}
	render(w, "discover", page)
}

// backTo is the page a change returns to: the discover view the form came from
// (rebuilt from its numbers, never echoed), or def.
func backTo(r *http.Request, def string) string {
	days, err1 := strconv.Atoi(r.PostFormValue("days"))
	min, err2 := strconv.Atoi(r.PostFormValue("min"))
	if err1 != nil || err2 != nil || days < 1 || min < 1 {
		return def
	}
	return fmt.Sprintf("/discover?days=%d&min=%d", days, min)
}

func serveRate(w http.ResponseWriter, r *http.Request, home string) {
	if !sameOrigin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	answer := r.PostFormValue("answer")
	if answer != "yes" && answer != "no" && answer != "wrong" {
		http.Error(w, `answer must be "yes", "no" or "wrong"`, http.StatusBadRequest)
		return
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	id := r.PathValue("id")
	if _, err := h.GetMemory(id); err != nil {
		http.Error(w, "no such memory", http.StatusNotFound)
		return
	}
	if err := h.Rate(context.WithoutCancel(r.Context()), id, answer); err != nil {
		http.Error(w, "the rating could not be saved", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, backTo(r, "/memories/"+url.PathEscape(id)), http.StatusSeeOther)
}

func serveReplay(w http.ResponseWriter, r *http.Request, home string) {
	if !sameOrigin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	n, err := h.ReplayRatings(context.WithoutCancel(r.Context()), 90)
	if err != nil {
		http.Error(w, "the replay failed; run inscribe doctor", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/discover?days=30&min=10&replayed="+strconv.Itoa(n), http.StatusSeeOther)
}

// serveForgetMany forgets every checked memory on the discover page.
func serveForgetMany(w http.ResponseWriter, r *http.Request, home string) {
	if !sameOrigin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	ids := r.PostForm["id"]
	if len(ids) > memoryListMax {
		http.Error(w, "too many memories in one request", http.StatusBadRequest)
		return
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	for _, id := range ids {
		if _, err := h.GetMemory(id); err != nil {
			http.Error(w, "no such memory: "+id, http.StatusNotFound)
			return
		}
	}
	for _, id := range ids {
		if err := h.Forget(context.WithoutCancel(r.Context()), id, false); err != nil {
			http.Error(w, "the memories could not all be forgotten", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, backTo(r, "/discover"), http.StatusSeeOther)
}

func serveForget(w http.ResponseWriter, r *http.Request, home string) {
	if !sameOrigin(w, r) {
		return
	}
	h, ok := openStore(w, home)
	if !ok {
		return
	}
	defer func() { _ = h.Close() }()
	id := r.PathValue("id")
	if _, err := h.GetMemory(id); err != nil {
		http.Error(w, "no such memory", http.StatusNotFound)
		return
	}
	if err := h.Forget(context.WithoutCancel(r.Context()), id, false); err != nil {
		http.Error(w, "the memory could not be forgotten", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/memories/"+url.PathEscape(id), http.StatusSeeOther)
}

// setting is one scalar key the settings page edits. Lists, maps and share.url stay
// hand-edited: share.url may carry a token, so the page never prints it.
type setting struct {
	Table, Key, Kind, Help string
	Choices                []string
	get                    func(config.Config) string
}

func (s setting) Name() string {
	if s.Table == "" {
		return s.Key
	}
	return s.Table + "." + s.Key
}

func (s setting) parse(v string) (any, error) {
	switch s.Kind {
	case "bool":
		return strconv.ParseBool(v)
	case "int":
		n, err := strconv.Atoi(v)
		if err == nil && n < 0 {
			err = fmt.Errorf("must not be negative")
		}
		return int64(n), err
	case "float":
		f, err := strconv.ParseFloat(v, 64)
		if err == nil && f < 0 {
			err = fmt.Errorf("must not be negative")
		}
		return f, err
	default:
		if !slices.Contains(s.Choices, v) {
			return nil, fmt.Errorf("allowed values are %s", strings.Join(s.Choices, ", "))
		}
		return v, nil
	}
}

func fb(b bool) string    { return strconv.FormatBool(b) }
func fi(n int) string     { return strconv.Itoa(n) }
func ff(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

var settings = []setting{
	{"recall", "inject", "bool", "Add recalled lines to sessions. Off stops all injection.", nil, func(c config.Config) string { return fb(c.Recall.Inject) }},
	{"recall", "session_budget", "int", "Token budget for the lines added at session start.", nil, func(c config.Config) string { return fi(c.Recall.SessionBudget) }},
	{"recall", "subagent_budget", "int", "Token budget for a subagent start; 0 is off. Lines the session already showed are never repeated.", nil, func(c config.Config) string { return fi(c.Recall.SubagentBudget) }},
	{"recall", "cross_project", "float", "Score scale for notes, decisions, fixes and project facts from your other projects, 0 to 1; 0 keeps recall inside the current project. A repository cannot change it.", nil, func(c config.Config) string { return ff(c.Recall.CrossProject) }},
	{"recall", "prompt_max_lines", "int", "Most lines added to one prompt.", nil, func(c config.Config) string { return fi(c.Recall.PromptMaxLines) }},
	{"recall", "prompt_threshold", "float", "Lowest score a line needs to be added to a prompt.", nil, func(c config.Config) string { return ff(c.Recall.PromptThreshold) }},
	{"recall", "half_life_days", "float", "Days for a memory's recency weight to halve.", nil, func(c config.Config) string { return ff(c.Recall.HalfLifeDays) }},
	{"consolidate", "auto", "bool", "Review finished sessions with a model to add and rate memories.", nil, func(c config.Config) string { return fb(c.Consolidate.Auto) }},
	{"consolidate", "min_events", "int", "Fewest events a session needs before it is reviewed.", nil, func(c config.Config) string { return fi(c.Consolidate.MinEvents) }},
	{"consolidate", "input_budget", "int", "Token budget for one review's input.", nil, func(c config.Config) string { return fi(c.Consolidate.InputBudget) }},
	{"retention", "mode", "choice", "How long stored data is kept.", []string{"rolling", "storage_time", "keep_all"}, func(c config.Config) string { return c.Retention.Mode }},
	{"retention", "prompt_text_days", "int", "Days prompt text is kept.", nil, func(c config.Config) string { return fi(c.Retention.PromptTextDays) }},
	{"retention", "prompt_clock", "choice", "Whether prompt age counts from when it was written or stored.", []string{"original", "stored"}, func(c config.Config) string { return c.Retention.PromptClock }},
	{"retention", "event_days", "int", "Days events are kept.", nil, func(c config.Config) string { return fi(c.Retention.EventDays) }},
	{"skills", "enabled", "choice", "Learn which skills are used and in what order.", []string{"auto", "true", "false"}, func(c config.Config) string { return string(c.Skills.Enabled) }},
	{"stats", "session_line", "bool", "Show the one-line summary at session start.", nil, func(c config.Config) string { return fb(c.Stats.SessionLine) }},
	{"stats", "holdout", "float", "Fraction of prompts held out from injection, 0 to 0.5; 0 is off.", nil, func(c config.Config) string { return ff(c.Stats.Holdout) }},
	{"stats", "holdout_min_sample", "int", "Held-out prompts needed before a verdict.", nil, func(c config.Config) string { return fi(c.Stats.HoldoutMinSample) }},
	{"stats", "idle_minutes", "int", "Gap that ends active time in a session.", nil, func(c config.Config) string { return fi(c.Stats.IdleMinutes) }},
	{"", "debug", "bool", "Show extra diagnostics, such as what each hook event saved.", nil, func(c config.Config) string { return fb(c.Debug) }},
}

type settingRow struct {
	setting
	Value string
}

type settingsPage struct {
	Rows   []settingRow
	Warns  []string
	Saved  bool
	Share  share
	Config string
}

func serveSettings(w http.ResponseWriter, r *http.Request, home string) {
	cfg, warns, ok := loadConfig(w, home)
	if !ok {
		return
	}
	page := settingsPage{Warns: warns, Saved: r.URL.Query().Get("saved") == "1", Share: shareState(cfg), Config: filepath.Join(home, "config.toml")}
	for _, s := range settings {
		page.Rows = append(page.Rows, settingRow{s, s.get(cfg)})
	}
	render(w, "settings", page)
}

func serveSaveSettings(w http.ResponseWriter, r *http.Request, home string) {
	if !sameOrigin(w, r) {
		return
	}
	cfg, _, err := config.Load(home, "")
	if err != nil {
		http.Error(w, "config.toml does not load; fix it by hand", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	// Only changed keys are written, so a default left alone keeps following the shipped one.
	set := map[string]map[string]any{}
	for _, s := range settings {
		v := strings.TrimSpace(r.PostFormValue(s.Name()))
		if s.Kind == "bool" {
			v = fb(v == "on")
		}
		if v == s.get(cfg) {
			continue
		}
		val, err := s.parse(v)
		if err != nil {
			http.Error(w, s.Name()+": "+err.Error(), http.StatusBadRequest)
			return
		}
		if set[s.Table] == nil {
			set[s.Table] = map[string]any{}
		}
		set[s.Table][s.Key] = val
	}
	if len(set) > 0 {
		if msg, err := checkConfig(home, set); err != nil {
			http.Error(w, "the change was not saved: "+msg, http.StatusBadRequest)
			return
		}
		if err := setConfigValues(home, set); err != nil {
			http.Error(w, "the config change failed; run inscribe doctor", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// checkConfig applies set to a copy of config.toml and loads it, so a value the
// config rejects never reaches the real file.
func checkConfig(home string, set map[string]map[string]any) (string, error) {
	tmp, err := os.MkdirTemp("", "inscribe-settings-")
	if err != nil {
		return "the check could not run", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	raw, err := os.ReadFile(filepath.Join(home, "config.toml")) //nolint:gosec // G304: the config path is under the user's INSCRIBE_HOME
	if err == nil {
		err = os.WriteFile(filepath.Join(tmp, "config.toml"), raw, 0o600) //nolint:gosec // G703: tmp comes from os.MkdirTemp, not from the request
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return "the check could not run", err
	}
	if err := setConfigValues(tmp, set); err != nil {
		return "the check could not run", err
	}
	if _, _, err := config.Load(tmp, ""); err != nil {
		return strings.ReplaceAll(err.Error(), tmp+string(filepath.Separator), ""), err
	}
	return "", nil
}

func when(ms int64) string {
	if ms == 0 {
		return "never"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

func pct(hits, misses int) string {
	if hits+misses == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(hits)/float64(hits+misses))
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func mb(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/1e6) }

// eventText is the line an event shows as evidence: its prompt, command or tool.
func eventText(e model.Event) string {
	if e.PromptText != "" {
		return e.PromptText
	}
	for _, k := range []string{"command", "text", "error", "file_path"} {
		if v, ok := e.Payload[k].(string); ok && v != "" {
			return v
		}
	}
	return e.Tool
}

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"when": when, "pct": pct, "add": func(a, b int) int { return a + b }, "div": func(a, b int) int { return a / b }, "list": func(xs ...string) []string { return xs }, "short": short, "base": filepath.Base, "mb": mb, "eventText": eventText,
}).Parse(pageTemplates))
