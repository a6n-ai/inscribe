package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/a6n-ai/inscribe"
	"github.com/a6n-ai/inscribe/internal/config"
	"github.com/a6n-ai/inscribe/model"
)

func seedMemory(t *testing.T, home, text string) string {
	t.Helper()
	h, err := inscribe.Open(inscribe.Options{Home: home, Caller: "inscribe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	id, err := h.Remember(context.Background(), text, model.Note, model.Global, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestServePagesRender(t *testing.T) {
	home := t.TempDir()
	id := seedMemory(t, home, "quokka builds need the arm64 toolchain")
	h := newServeHandler(home, "127.0.0.1", 7331)
	for target, want := range map[string]string{
		"/":                      "How inscribe is doing",
		"/?days=30":              "last 30 days",
		"/how":                   "How inscribe works",
		"/settings":              "recall.prompt_threshold",
		"/memories":              "quokka builds",
		"/memories?q=QUOKKA":     "quokka builds",
		"/memories?q=zebra":      "no memories match",
		"/memories?status=all":   "quokka builds",
		"/memories/" + id:        "Forget this memory",
		"/recall":                "Try recall",
		"/recall?q=quokka+build": "score",
	} {
		rec := serveReq(h, "GET", target, "", testHost, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200: %s", target, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("GET %s does not contain %q", target, want)
		}
	}
	if rec := serveReq(h, "GET", "/memories/m_nope", "", testHost, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown memory status = %d, want 404", rec.Code)
	}
}

func TestServeRecallPreviewWritesNothing(t *testing.T) {
	home := t.TempDir()
	id := seedMemory(t, home, "quokka builds need the arm64 toolchain")
	h := newServeHandler(home, "127.0.0.1", 7331)
	if rec := serveReq(h, "GET", "/recall?q=quokka+builds+toolchain", "", testHost, nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	ins, err := inscribe.Open(inscribe.Options{Home: home, Caller: "inscribe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ins.Close() }()
	m, err := ins.GetMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.LastUsed != 0 {
		t.Fatalf("preview marked the memory used at %d", m.LastUsed)
	}
	if _, shown, _, _, err := ins.MemoryOutcomes(id, 1); err != nil || shown != 0 {
		t.Fatalf("preview wrote %d outcome rows (err %v)", shown, err)
	}
}

func memoryStatus(t *testing.T, home, id string) model.Status {
	t.Helper()
	h, err := inscribe.Open(inscribe.Options{Home: home, Caller: "inscribe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	m, err := h.GetMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Status
}

func TestServeForget(t *testing.T) {
	home := t.TempDir()
	id := seedMemory(t, home, "quokka builds need the arm64 toolchain")
	h := newServeHandler(home, "127.0.0.1", 7331)
	path := "/memories/" + id + "/forget"
	if rec := serveReq(h, "POST", path, "", testHost, map[string]string{"Origin": "https://evil.test"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", rec.Code)
	}
	if got := memoryStatus(t, home, id); got != model.Active {
		t.Fatalf("status after refused forget = %q", got)
	}
	if rec := serveReq(h, "GET", path, "", testHost, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET forget status = %d, want 405", rec.Code)
	}
	if rec := serveReq(h, "POST", path, "", testHost, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("forget status = %d, want 303", rec.Code)
	}
	if got := memoryStatus(t, home, id); got != model.Forgotten {
		t.Fatalf("status = %q, want forgotten", got)
	}
	if rec := serveReq(h, "POST", "/memories/m_nope/forget", "", testHost, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown forget status = %d, want 404", rec.Code)
	}
}

// settingsForm is the form the settings page submits for cfg, with edits applied.
func settingsForm(cfg config.Config, edits map[string]string) string {
	v := url.Values{}
	for _, s := range settings {
		val := s.get(cfg)
		if e, ok := edits[s.Name()]; ok {
			val = e
		}
		if s.Kind == "bool" {
			if val == "true" {
				v.Set(s.Name(), "on")
			}
			continue
		}
		v.Set(s.Name(), val)
	}
	return v.Encode()
}

func TestServeSettingsWritesOnlyChanged(t *testing.T) {
	home := t.TempDir()
	path := writeConfig(t, home, "debug = true\n\n[recall]\nsession_budget = 7\n")
	cfg, _, err := config.Load(home, "")
	if err != nil {
		t.Fatal(err)
	}
	h := newServeHandler(home, "127.0.0.1", 7331)
	form := settingsForm(cfg, map[string]string{"recall.prompt_max_lines": "5", "debug": "false", "skills.enabled": "false"})
	if rec := serveReq(h, "POST", "/settings", form, testHost, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body)
	}
	var doc map[string]any
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		t.Fatal(err)
	}
	recall, _ := doc["recall"].(map[string]any)
	skills, _ := doc["skills"].(map[string]any)
	if recall["session_budget"] != int64(7) || recall["prompt_max_lines"] != int64(5) || len(recall) != 2 {
		t.Fatalf("recall = %v", recall)
	}
	if doc["debug"] != false || skills["enabled"] != "false" {
		t.Fatalf("debug = %v, skills = %v", doc["debug"], skills)
	}
	if _, ok := doc["stats"]; ok {
		t.Fatalf("unchanged stats defaults were written: %v", doc)
	}
	if _, _, err := config.Load(home, ""); err != nil {
		t.Fatalf("saved config does not load: %v", err)
	}
}

func TestServeSettingsRejectsInvalid(t *testing.T) {
	home := t.TempDir()
	orig := "[recall]\nsession_budget = 7\n"
	path := writeConfig(t, home, orig)
	cfg, _, err := config.Load(home, "")
	if err != nil {
		t.Fatal(err)
	}
	h := newServeHandler(home, "127.0.0.1", 7331)
	for _, edit := range []map[string]string{
		{"stats.holdout": "0.9"},
		{"recall.prompt_max_lines": "many"},
		{"recall.session_budget": "-1"},
		{"retention.mode": "forever"},
	} {
		if rec := serveReq(h, "POST", "/settings", settingsForm(cfg, edit), testHost, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%v status = %d, want 400", edit, rec.Code)
		}
	}
	if rec := serveReq(h, "POST", "/settings", settingsForm(cfg, nil), testHost, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", rec.Code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != orig {
		t.Fatalf("config.toml changed to %q", got)
	}
	if entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "inscribe-settings-*")); len(entries) != 0 {
		t.Fatalf("check left temp dirs: %v", entries)
	}
}

func TestServeDiscoverAndForgetMany(t *testing.T) {
	home := t.TempDir()
	a := seedMemory(t, home, "quokka builds need the arm64 toolchain")
	b := seedMemory(t, home, "wombat deploys run on fridays")
	h := newServeHandler(home, "127.0.0.1", 7331)
	for _, target := range []string{"/discover", "/discover?days=7&min=1", "/discover?days=bogus&min=-3"} {
		if rec := serveReq(h, "GET", target, "", testHost, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Shown often, never helpful") {
			t.Fatalf("GET %s = %d", target, rec.Code)
		}
	}
	form := url.Values{"id": {a, b}, "days": {"7"}, "min": {"1"}}.Encode()
	if rec := serveReq(h, "POST", "/memories/forget", form, testHost, map[string]string{"Origin": "https://evil.test"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin = %d", rec.Code)
	}
	bad := url.Values{"id": {a, "m_nope"}}.Encode()
	if rec := serveReq(h, "POST", "/memories/forget", bad, testHost, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", rec.Code)
	}
	if got := memoryStatus(t, home, a); got != model.Active {
		t.Fatalf("a forgotten although the request named an unknown id: %q", got)
	}
	rec := serveReq(h, "POST", "/memories/forget", form, testHost, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/discover?days=7&min=1" {
		t.Fatalf("forget = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, id := range []string{a, b} {
		if got := memoryStatus(t, home, id); got != model.Forgotten {
			t.Fatalf("%s = %q, want forgotten", id, got)
		}
	}
	bad = url.Values{"id": {a}, "days": {"//evil.test"}, "min": {"1"}}.Encode()
	if rec := serveReq(h, "POST", "/memories/forget", bad, testHost, nil); rec.Header().Get("Location") != "/discover" {
		t.Fatalf("redirect to %q", rec.Header().Get("Location"))
	}
}

func TestServeRate(t *testing.T) {
	home := t.TempDir()
	id := seedMemory(t, home, "quokka builds need the arm64 toolchain")
	h := newServeHandler(home, "127.0.0.1", 7331)
	path := "/memories/" + id + "/rate"
	if rec := serveReq(h, "POST", path, "answer=yes", testHost, map[string]string{"Origin": "https://evil.test"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin = %d", rec.Code)
	}
	if rec := serveReq(h, "POST", path, "answer=maybe", testHost, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad answer = %d", rec.Code)
	}
	if rec := serveReq(h, "POST", "/memories/m_nope/rate", "answer=yes", testHost, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown memory = %d", rec.Code)
	}
	for _, a := range []string{"yes", "no", "wrong"} {
		rec := serveReq(h, "POST", path, "answer="+a, testHost, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/memories/"+id {
			t.Fatalf("rate %s = %d to %q", a, rec.Code, rec.Header().Get("Location"))
		}
	}
	ins, err := inscribe.Open(inscribe.Options{Home: home, Caller: "inscribe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ins.Close() }()
	m, err := ins.GetMemory(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Penalty <= 0 {
		t.Fatalf("wrong left penalty %v", m.Penalty)
	}
	if _, shown, helpful, unhelpful, err := ins.MemoryOutcomes(id, 5); err != nil || shown != 3 || helpful != 1 || unhelpful != 2 {
		t.Fatalf("ratings = %d/%d/%d, %v", shown, helpful, unhelpful, err)
	}
	if rec := serveReq(h, "GET", "/memories/"+id, "", testHost, nil); !strings.Contains(rec.Body.String(), "user") {
		t.Fatalf("memory page does not list the hand ratings")
	}
}

func TestServeReplay(t *testing.T) {
	home := t.TempDir()
	h := newServeHandler(home, "127.0.0.1", 7331)
	if rec := serveReq(h, "POST", "/replay", "", testHost, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site = %d", rec.Code)
	}
	rec := serveReq(h, "POST", "/replay", "", testHost, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/discover?") {
		t.Fatalf("replay = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := serveReq(h, "GET", rec.Header().Get("Location"), "", testHost, nil); !strings.Contains(rec.Body.String(), "Rated 0 shown command lines") {
		t.Fatalf("discover does not report the replay")
	}
}
